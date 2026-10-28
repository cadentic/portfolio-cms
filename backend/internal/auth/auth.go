// Package auth verifies Firebase Authentication ID tokens server-side and
// authorises the portfolio owner. Frontend role claims are never trusted: the
// token signature, audience, issuer and expiry are checked here, and admin
// rights come only from the server-side ADMIN_UIDS / ADMIN_EMAILS allow-list.
package auth

import (
	"context"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

const jwksURL = "https://www.googleapis.com/service_accounts/v1/jwk/securetoken@system.gserviceaccount.com"

var (
	ErrUnauthenticated = errors.New("authentication required")
	ErrForbidden       = errors.New("not authorised")
)

type Identity struct {
	UID           string `json:"uid"`
	Email         string `json:"email"`
	EmailVerified bool   `json:"email_verified"`
	Name          string `json:"name,omitempty"`
}

// Actor is the audit-log identity string.
func (i *Identity) Actor() string {
	if i.Email != "" {
		return i.Email
	}
	return "uid:" + i.UID
}

type Verifier interface {
	Verify(ctx context.Context, idToken string) (*Identity, error)
}

type FirebaseVerifier struct {
	ProjectID string
	Emulator  bool // accept unsigned Auth Emulator tokens (never in production)
	Client    *http.Client
	JWKSURL   string

	mu      sync.Mutex
	keys    map[string]*rsa.PublicKey
	expires time.Time
}

type claims struct {
	jwt.RegisteredClaims
	Email         string `json:"email"`
	EmailVerified bool   `json:"email_verified"`
	Name          string `json:"name"`
	AuthTime      int64  `json:"auth_time"`
}

func (v *FirebaseVerifier) Verify(ctx context.Context, token string) (*Identity, error) {
	iss := "https://securetoken.google.com/" + v.ProjectID
	var c claims
	opts := []jwt.ParserOption{jwt.WithAudience(v.ProjectID), jwt.WithIssuer(iss), jwt.WithExpirationRequired(),
		jwt.WithIssuedAt(), jwt.WithLeeway(30 * time.Second)}
	if v.Emulator {
		// The Auth Emulator issues alg=none tokens.
		p := jwt.NewParser(append(opts, jwt.WithValidMethods([]string{"none", "RS256"}))...)
		if _, _, err := p.ParseUnverified(token, &c); err != nil {
			return nil, ErrUnauthenticated
		}
		if err := jwt.NewValidator(opts...).Validate(c); err != nil {
			return nil, ErrUnauthenticated
		}
	} else {
		_, err := jwt.ParseWithClaims(token, &c, func(t *jwt.Token) (any, error) {
			kid, _ := t.Header["kid"].(string)
			return v.key(ctx, kid)
		}, append(opts, jwt.WithValidMethods([]string{"RS256"}))...)
		if err != nil {
			return nil, ErrUnauthenticated
		}
	}
	if c.Subject == "" || len(c.Subject) > 128 || (c.AuthTime > 0 && c.AuthTime > time.Now().Add(time.Minute).Unix()) {
		return nil, ErrUnauthenticated
	}
	return &Identity{UID: c.Subject, Email: strings.ToLower(c.Email), EmailVerified: c.EmailVerified, Name: c.Name}, nil
}

func (v *FirebaseVerifier) key(ctx context.Context, kid string) (*rsa.PublicKey, error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	if k, ok := v.keys[kid]; ok && time.Now().Before(v.expires) {
		return k, nil
	}
	url := v.JWKSURL
	if url == "" {
		url = jwksURL
	}
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	client := v.Client
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch signing keys: %w", err)
	}
	defer resp.Body.Close()
	var set struct {
		Keys []struct {
			Kid string `json:"kid"`
			N   string `json:"n"`
			E   string `json:"e"`
		} `json:"keys"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&set); err != nil {
		return nil, err
	}
	keys := map[string]*rsa.PublicKey{}
	for _, k := range set.Keys {
		n, err1 := base64.RawURLEncoding.DecodeString(k.N)
		e, err2 := base64.RawURLEncoding.DecodeString(k.E)
		if err1 != nil || err2 != nil {
			continue
		}
		keys[k.Kid] = &rsa.PublicKey{N: new(big.Int).SetBytes(n), E: int(new(big.Int).SetBytes(e).Int64())}
	}
	maxAge := time.Hour
	if cc := resp.Header.Get("Cache-Control"); strings.Contains(cc, "max-age=") {
		var secs int
		if _, err := fmt.Sscanf(cc[strings.Index(cc, "max-age=")+8:], "%d", &secs); err == nil && secs > 0 {
			maxAge = time.Duration(secs) * time.Second
		}
	}
	v.keys, v.expires = keys, time.Now().Add(maxAge)
	k, ok := keys[kid]
	if !ok {
		return nil, errors.New("unknown key id")
	}
	return k, nil
}

// Authorizer decides whether a verified identity is the site owner.
type Authorizer struct {
	UIDs                []string
	Emails              []string
	AllowUnverifiedMail bool // emulator only
}

func (a Authorizer) IsAdmin(id *Identity) bool {
	if id == nil {
		return false
	}
	if slices.Contains(a.UIDs, id.UID) {
		return true
	}
	return id.Email != "" && (id.EmailVerified || a.AllowUnverifiedMail) && slices.Contains(a.Emails, strings.ToLower(id.Email))
}

type ctxKey struct{}

func WithIdentity(ctx context.Context, id *Identity) context.Context {
	return context.WithValue(ctx, ctxKey{}, id)
}

func FromContext(ctx context.Context) *Identity {
	id, _ := ctx.Value(ctxKey{}).(*Identity)
	return id
}

func BearerToken(r *http.Request) string {
	h := r.Header.Get("Authorization")
	if len(h) > 7 && strings.EqualFold(h[:7], "bearer ") {
		return strings.TrimSpace(h[7:])
	}
	return ""
}

// AdminOnly protects a handler: valid Firebase token + server-side allow-list.
func AdminOnly(v Verifier, a Authorizer, onErr func(http.ResponseWriter, int, string), next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tok := BearerToken(r)
		if tok == "" {
			onErr(w, http.StatusUnauthorized, "sign in required")
			return
		}
		id, err := v.Verify(r.Context(), tok)
		if err != nil {
			onErr(w, http.StatusUnauthorized, "invalid or expired session")
			return
		}
		if !a.IsAdmin(id) {
			onErr(w, http.StatusForbidden, "this account is not authorised to manage the site")
			return
		}
		next.ServeHTTP(w, r.WithContext(WithIdentity(r.Context(), id)))
	})
}
