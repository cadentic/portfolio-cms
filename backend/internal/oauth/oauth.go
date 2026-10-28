// Package oauth is a minimal OAuth 2.1 authorization server (authorization
// code + PKCE S256, dynamic client registration, refresh-token rotation) that
// protects the MCP endpoint, following the MCP authorization specification.
//
// The owner authenticates with Firebase in the Angular admin ("Connect an AI
// assistant" consent screen); every MCP request re-checks the admin allow-list.
package oauth

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/cadentic/portfolio-cms/backend/internal/auth"
	"github.com/cadentic/portfolio-cms/backend/internal/tokens"
)

const (
	codeTTL    = 5 * time.Minute
	accessTTL  = time.Hour
	refreshTTL = 30 * 24 * time.Hour
	Scope      = "cms"
)

type Server struct {
	DB         *pgxpool.Pool
	Issuer     string // public base URL, e.g. https://cadentic.dev
	SiteURL    string // where the consent page lives
	Authorizer auth.Authorizer
}

func (s *Server) ResourceURL() string { return s.Issuer + "/mcp" }

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func oauthErr(w http.ResponseWriter, code int, kind, desc string) {
	writeJSON(w, code, map[string]string{"error": kind, "error_description": desc})
}

// ProtectedResourceMetadata implements RFC 9728.
func (s *Server) ProtectedResourceMetadata(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, map[string]any{
		"resource":                 s.ResourceURL(),
		"authorization_servers":    []string{s.Issuer},
		"scopes_supported":         []string{Scope},
		"bearer_methods_supported": []string{"header"},
		"resource_name":            "Portfolio CMS",
	})
}

// AuthorizationServerMetadata implements RFC 8414.
func (s *Server) AuthorizationServerMetadata(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, map[string]any{
		"issuer":                                s.Issuer,
		"authorization_endpoint":                s.Issuer + "/oauth/authorize",
		"token_endpoint":                        s.Issuer + "/oauth/token",
		"registration_endpoint":                 s.Issuer + "/oauth/register",
		"revocation_endpoint":                   s.Issuer + "/oauth/revoke",
		"response_types_supported":              []string{"code"},
		"grant_types_supported":                 []string{"authorization_code", "refresh_token"},
		"code_challenge_methods_supported":      []string{"S256"},
		"token_endpoint_auth_methods_supported": []string{"none"},
		"scopes_supported":                      []string{Scope},
	})
}

func validRedirect(u string) bool {
	p, err := url.Parse(u)
	if err != nil || p.Fragment != "" || p.Host == "" {
		return false
	}
	if p.Scheme == "https" {
		return true
	}
	host := p.Hostname()
	return p.Scheme == "http" && (host == "localhost" || host == "127.0.0.1" || host == "::1")
}

// Register implements RFC 7591 dynamic client registration for public clients.
func (s *Server) Register(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ClientName   string   `json:"client_name"`
		RedirectURIs []string `json:"redirect_uris"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10)).Decode(&req); err != nil {
		oauthErr(w, 400, "invalid_client_metadata", "invalid JSON")
		return
	}
	if len(req.RedirectURIs) == 0 || len(req.RedirectURIs) > 5 {
		oauthErr(w, 400, "invalid_redirect_uri", "1-5 redirect_uris required")
		return
	}
	for _, u := range req.RedirectURIs {
		if !validRedirect(u) {
			oauthErr(w, 400, "invalid_redirect_uri", "redirect URIs must be https or loopback http")
			return
		}
	}
	name := strings.TrimSpace(req.ClientName)
	if name == "" {
		name = "MCP client"
	}
	if len(name) > 80 {
		name = name[:80]
	}
	raw, _ := tokens.NewRandom()
	clientID := "mcp_" + raw[:24]
	if _, err := s.DB.Exec(r.Context(), `INSERT INTO oauth_clients (client_id, client_name, redirect_uris) VALUES ($1,$2,$3)`,
		clientID, name, req.RedirectURIs); err != nil {
		oauthErr(w, 500, "server_error", "could not register client")
		return
	}
	writeJSON(w, 201, map[string]any{
		"client_id": clientID, "client_name": name, "redirect_uris": req.RedirectURIs,
		"token_endpoint_auth_method": "none", "grant_types": []string{"authorization_code", "refresh_token"},
		"response_types": []string{"code"}, "client_id_issued_at": time.Now().Unix(),
	})
}

type AuthorizeRequest struct {
	ClientID            string `json:"client_id"`
	RedirectURI         string `json:"redirect_uri"`
	ResponseType        string `json:"response_type"`
	CodeChallenge       string `json:"code_challenge"`
	CodeChallengeMethod string `json:"code_challenge_method"`
	State               string `json:"state"`
	Scope               string `json:"scope"`
	Resource            string `json:"resource"`
}

func fromQuery(q url.Values) AuthorizeRequest {
	return AuthorizeRequest{ClientID: q.Get("client_id"), RedirectURI: q.Get("redirect_uri"), ResponseType: q.Get("response_type"),
		CodeChallenge: q.Get("code_challenge"), CodeChallengeMethod: q.Get("code_challenge_method"), State: q.Get("state"),
		Scope: q.Get("scope"), Resource: q.Get("resource")}
}

func (s *Server) validate(ctx context.Context, a AuthorizeRequest) (string, error) {
	var name string
	var uris []string
	err := s.DB.QueryRow(ctx, `SELECT client_name, redirect_uris FROM oauth_clients WHERE client_id=$1`, a.ClientID).Scan(&name, &uris)
	if err != nil {
		return "", errors.New("unknown client")
	}
	ok := false
	for _, u := range uris {
		if u == a.RedirectURI {
			ok = true
		}
	}
	if !ok {
		return "", errors.New("redirect_uri does not match the registered client")
	}
	if a.ResponseType != "code" || a.CodeChallengeMethod != "S256" || len(a.CodeChallenge) < 43 {
		return "", errors.New("authorization code flow with PKCE (S256) is required")
	}
	if a.Resource != "" && a.Resource != s.ResourceURL() {
		return "", errors.New("unknown resource")
	}
	return name, nil
}

// Authorize validates the request and sends the browser to the CMS consent
// page, where the owner signs in with Firebase.
func (s *Server) Authorize(w http.ResponseWriter, r *http.Request) {
	a := fromQuery(r.URL.Query())
	if _, err := s.validate(r.Context(), a); err != nil {
		http.Error(w, "Invalid authorization request: "+err.Error(), http.StatusBadRequest)
		return
	}
	http.Redirect(w, r, s.SiteURL+"/admin/connect?"+r.URL.RawQuery, http.StatusFound)
}

// ClientInfo lets the consent page show which app is asking.
func (s *Server) ClientInfo(w http.ResponseWriter, r *http.Request) {
	a := fromQuery(r.URL.Query())
	name, err := s.validate(r.Context(), a)
	if err != nil {
		oauthErr(w, 400, "invalid_request", err.Error())
		return
	}
	u, _ := url.Parse(a.RedirectURI)
	writeJSON(w, 200, map[string]string{"client_name": name, "redirect_host": u.Host})
}

// Approve is called by the consent page with the owner's Firebase ID token
// (AdminOnly middleware); it issues a one-time authorization code.
func (s *Server) Approve(w http.ResponseWriter, r *http.Request) {
	id := auth.FromContext(r.Context())
	var a AuthorizeRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10)).Decode(&a); err != nil {
		oauthErr(w, 400, "invalid_request", "invalid JSON")
		return
	}
	if _, err := s.validate(r.Context(), a); err != nil {
		oauthErr(w, 400, "invalid_request", err.Error())
		return
	}
	raw, hash := tokens.NewRandom()
	if _, err := s.DB.Exec(r.Context(), `INSERT INTO oauth_codes (code_hash, client_id, redirect_uri, code_challenge, scope, resource, uid, email, expires_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)`, hash, a.ClientID, a.RedirectURI, a.CodeChallenge, Scope, a.Resource, id.UID, id.Email,
		time.Now().Add(codeTTL)); err != nil {
		oauthErr(w, 500, "server_error", "could not issue code")
		return
	}
	u, _ := url.Parse(a.RedirectURI)
	q := u.Query()
	q.Set("code", raw)
	if a.State != "" {
		q.Set("state", a.State)
	}
	q.Set("iss", s.Issuer)
	u.RawQuery = q.Encode()
	writeJSON(w, 200, map[string]string{"redirect": u.String()})
}

func pkceOK(verifier, challenge string) bool {
	if len(verifier) < 43 || len(verifier) > 128 {
		return false
	}
	sum := sha256.Sum256([]byte(verifier))
	return subtle.ConstantTimeCompare([]byte(base64.RawURLEncoding.EncodeToString(sum[:])), []byte(challenge)) == 1
}

// Token exchanges a code (with PKCE verifier) or a refresh token.
func (s *Server) Token(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 16<<10)
	if err := r.ParseForm(); err != nil {
		oauthErr(w, 400, "invalid_request", "form body required")
		return
	}
	ctx := r.Context()
	clientID := r.PostForm.Get("client_id")
	switch r.PostForm.Get("grant_type") {
	case "authorization_code":
		var cid, redirect, challenge, uid, mail string
		var expires time.Time
		var used *time.Time
		err := s.DB.QueryRow(ctx, `WITH old AS (SELECT code_hash, used_at FROM oauth_codes WHERE code_hash=$1 FOR UPDATE)
			UPDATE oauth_codes c SET used_at=COALESCE(c.used_at, now()) FROM old WHERE c.code_hash=old.code_hash
			RETURNING c.client_id, c.redirect_uri, c.code_challenge, c.uid, c.email, c.expires_at, old.used_at`, tokens.Hash(r.PostForm.Get("code"))).
			Scan(&cid, &redirect, &challenge, &uid, &mail, &expires, &used)
		if err != nil || used != nil || time.Now().After(expires) || cid != clientID ||
			redirect != r.PostForm.Get("redirect_uri") || !pkceOK(r.PostForm.Get("code_verifier"), challenge) {
			oauthErr(w, 400, "invalid_grant", "invalid, expired or already used authorization code")
			return
		}
		s.issue(w, ctx, cid, uid, mail)
	case "refresh_token":
		var cid, uid, mail string
		err := s.DB.QueryRow(ctx, `UPDATE oauth_tokens SET revoked_at=now() WHERE token_hash=$1 AND kind='refresh'
			AND revoked_at IS NULL AND expires_at > now() RETURNING client_id, uid, email`,
			tokens.Hash(r.PostForm.Get("refresh_token"))).Scan(&cid, &uid, &mail)
		if err != nil || cid != clientID {
			oauthErr(w, 400, "invalid_grant", "invalid refresh token")
			return
		}
		s.issue(w, ctx, cid, uid, mail)
	default:
		oauthErr(w, 400, "unsupported_grant_type", "use authorization_code or refresh_token")
	}
}

func (s *Server) issue(w http.ResponseWriter, ctx context.Context, clientID, uid, mail string) {
	access, ah := tokens.NewRandom()
	refresh, rh := tokens.NewRandom()
	_, err := s.DB.Exec(ctx, `INSERT INTO oauth_tokens (token_hash, kind, client_id, uid, email, scope, expires_at)
		VALUES ($1,'access',$3,$4,$5,$6,$7), ($2,'refresh',$3,$4,$5,$6,$8)`,
		ah, rh, clientID, uid, mail, Scope, time.Now().Add(accessTTL), time.Now().Add(refreshTTL))
	if err != nil {
		oauthErr(w, 500, "server_error", "could not issue token")
		return
	}
	writeJSON(w, 200, map[string]any{"access_token": access, "token_type": "Bearer", "expires_in": int(accessTTL.Seconds()),
		"refresh_token": refresh, "scope": Scope})
}

func (s *Server) Revoke(w http.ResponseWriter, r *http.Request) {
	_ = r.ParseForm()
	_, _ = s.DB.Exec(r.Context(), `UPDATE oauth_tokens SET revoked_at=now() WHERE token_hash=$1`, tokens.Hash(r.PostForm.Get("token")))
	w.WriteHeader(200)
}

// RevokeAll disconnects every MCP client (admin action).
func (s *Server) RevokeAll(ctx context.Context) error {
	_, err := s.DB.Exec(ctx, `UPDATE oauth_tokens SET revoked_at=now() WHERE revoked_at IS NULL`)
	return err
}

// Protect authenticates MCP requests with an access token and re-checks that
// the token's owner is still an authorised admin.
func (s *Server) Protect(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tok := auth.BearerToken(r)
		challenge := `Bearer resource_metadata="` + s.Issuer + `/.well-known/oauth-protected-resource"`
		if tok == "" {
			w.Header().Set("WWW-Authenticate", challenge)
			oauthErr(w, 401, "invalid_token", "authorization required")
			return
		}
		var id auth.Identity
		err := s.DB.QueryRow(r.Context(), `UPDATE oauth_tokens SET last_used_at=now() WHERE token_hash=$1 AND kind='access'
			AND revoked_at IS NULL AND expires_at > now() RETURNING uid, email`, tokens.Hash(tok)).Scan(&id.UID, &id.Email)
		if errors.Is(err, pgx.ErrNoRows) || err != nil {
			w.Header().Set("WWW-Authenticate", challenge+`, error="invalid_token"`)
			oauthErr(w, 401, "invalid_token", "token is invalid or expired")
			return
		}
		id.EmailVerified = true // verified at consent time
		if !s.Authorizer.IsAdmin(&id) {
			oauthErr(w, 403, "insufficient_scope", "account is no longer authorised")
			return
		}
		next.ServeHTTP(w, r.WithContext(auth.WithIdentity(r.Context(), &id)))
	})
}

type Connection struct {
	ClientID   string     `json:"client_id"`
	ClientName string     `json:"client_name"`
	Email      string     `json:"email"`
	LastUsedAt *time.Time `json:"last_used_at"`
	CreatedAt  time.Time  `json:"created_at"`
}

func (s *Server) Connections(ctx context.Context) ([]Connection, error) {
	rows, err := s.DB.Query(ctx, `SELECT c.client_id, c.client_name, max(t.email), max(t.last_used_at), min(t.created_at)
		FROM oauth_tokens t JOIN oauth_clients c USING (client_id)
		WHERE t.revoked_at IS NULL AND t.expires_at > now() GROUP BY c.client_id, c.client_name ORDER BY 5 DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Connection{}
	for rows.Next() {
		var c Connection
		if err := rows.Scan(&c.ClientID, &c.ClientName, &c.Email, &c.LastUsedAt, &c.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}
