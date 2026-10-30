// Package googlecontacts syncs the contacts that carry one chosen Google
// Contacts label into the newsletter subscribers, on their own list
// (source "google_contacts").
//
// The owner connects a Google account once (OAuth, read-only contacts scope),
// picks a label and attests that everyone with that label agreed to receive
// email. The worker then syncs about every six hours. New contacts are added
// as verified with that attestation as consent evidence and get no email.
// Anyone who unsubscribed, bounced, complained or is otherwise suppressed is
// never added again. Removing a label in Google does not unsubscribe anyone.
package googlecontacts

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/cadentic/portfolio-cms/backend/internal/audit"
	"github.com/cadentic/portfolio-cms/backend/internal/store"
	"github.com/cadentic/portfolio-cms/backend/internal/subscribers"
	"github.com/cadentic/portfolio-cms/backend/internal/tokens"
)

// Source is the subscribers.source value of the Google Contacts list.
const Source = subscribers.GoogleContactsSource

const (
	ScopeContacts = "https://www.googleapis.com/auth/contacts.readonly"
	CallbackPath  = "/api/google/oauth/callback"

	// SyncInterval is how often the worker syncs a connected, unpaused label.
	SyncInterval = 6 * time.Hour
	// syncLease is how long one run may hold the sync before another may start.
	syncLease = 15 * time.Minute
	// maxMembers caps a label; a larger one is refused rather than cut short.
	maxMembers = 10000
	batchSize  = 200 // people:batchGet limit
)

var (
	ErrNotConfigured = userError("Google sign-in is not configured: set GOOGLE_OAUTH_CLIENT_ID and GOOGLE_OAUTH_CLIENT_SECRET")
	ErrNotConnected  = userError("connect a Google account first")
	ErrNoLabel       = userError("choose a Google Contacts label first")
	ErrBusy          = userError("a sync is already running; try again in a minute")
	ErrState         = userError("this Google sign-in link expired or was already used; start again from the admin")
	ErrAttest        = userError("confirm that everyone with this label agreed to receive your emails")
)

type Service struct {
	DB           *pgxpool.Pool
	Client       *http.Client
	ClientID     string
	ClientSecret string
	SiteURL      string
	AppSecret    []byte
	Log          *slog.Logger

	// Google endpoints; empty uses the real ones (tests point them at a fake).
	AuthURL   string
	TokenURL  string
	RevokeURL string
	PeopleURL string
}

func (s *Service) Configured() bool { return s.ClientID != "" && s.ClientSecret != "" }

func (s *Service) RedirectURL() string { return s.SiteURL + CallbackPath }

func or(v, def string) string {
	if v != "" {
		return v
	}
	return def
}

func (s *Service) authURL() string {
	return or(s.AuthURL, "https://accounts.google.com/o/oauth2/v2/auth")
}
func (s *Service) tokenURL() string { return or(s.TokenURL, "https://oauth2.googleapis.com/token") }
func (s *Service) revokeURL() string {
	return or(s.RevokeURL, "https://oauth2.googleapis.com/revoke")
}
func (s *Service) peopleURL() string { return or(s.PeopleURL, "https://people.googleapis.com") }

// --- status -------------------------------------------------------------------

type Status struct {
	Configured   bool           `json:"configured"`
	RedirectURL  string         `json:"redirect_url"`
	Connected    bool           `json:"connected"`
	AccountEmail string         `json:"account_email"`
	ConnectedAt  *time.Time     `json:"connected_at"`
	ConnectedBy  string         `json:"connected_by"`
	Label        string         `json:"label_resource"`
	LabelName    string         `json:"label_name"`
	AttestedBy   string         `json:"attested_by"`
	AttestedAt   *time.Time     `json:"attested_at"`
	Paused       bool           `json:"paused"`
	Running      bool           `json:"running"`
	LastSyncAt   *time.Time     `json:"last_sync_at"`
	NextSyncAt   *time.Time     `json:"next_sync_at"`
	LastResult   map[string]any `json:"last_result"`
	LastError    string         `json:"last_error"`
	ListSize     int            `json:"list_size"`
}

func (s *Service) Status(ctx context.Context) (*Status, error) {
	st := &Status{Configured: s.Configured(), RedirectURL: s.RedirectURL()}
	var res []byte
	var started *time.Time
	err := s.DB.QueryRow(ctx, `SELECT refresh_token_enc IS NOT NULL, coalesce(account_email,''), connected_at, coalesce(connected_by,''),
		coalesce(label_resource,''), coalesce(label_name,''), coalesce(attested_by,''), attested_at, paused, sync_started_at,
		last_sync_at, last_result, coalesce(last_error,''),
		(SELECT count(*) FROM subscribers WHERE source=$1 AND status='verified')
		FROM google_contacts_sync WHERE id=1`, Source).Scan(&st.Connected, &st.AccountEmail, &st.ConnectedAt, &st.ConnectedBy,
		&st.Label, &st.LabelName, &st.AttestedBy, &st.AttestedAt, &st.Paused, &started, &st.LastSyncAt, &res, &st.LastError, &st.ListSize)
	if err != nil {
		return nil, err
	}
	st.Running = started != nil && time.Since(*started) < syncLease
	_ = json.Unmarshal(res, &st.LastResult)
	if st.Connected && st.Label != "" && !st.Paused {
		next := time.Now()
		if st.LastSyncAt != nil && st.LastSyncAt.Add(SyncInterval).After(next) {
			next = st.LastSyncAt.Add(SyncInterval)
		}
		st.NextSyncAt = &next
	}
	return st, nil
}

// --- connect ------------------------------------------------------------------

// StartConnect stores a single-use state with a PKCE verifier and returns the
// Google consent URL the owner's browser opens.
func (s *Service) StartConnect(ctx context.Context, actor string) (string, error) {
	if !s.Configured() {
		return "", ErrNotConfigured
	}
	state, hash := tokens.NewRandom()
	verifier, _ := tokens.NewRandom()
	if _, err := s.DB.Exec(ctx, `DELETE FROM google_oauth_states WHERE expires_at < now()`); err != nil {
		return "", err
	}
	if _, err := s.DB.Exec(ctx, `INSERT INTO google_oauth_states (state_hash, code_verifier, created_by, expires_at)
		VALUES ($1,$2,$3, now() + interval '10 minutes')`, hash, verifier, actor); err != nil {
		return "", err
	}
	sum := sha256.Sum256([]byte(verifier))
	q := url.Values{
		"client_id":              {s.ClientID},
		"redirect_uri":           {s.RedirectURL()},
		"response_type":          {"code"},
		"scope":                  {"openid email " + ScopeContacts},
		"access_type":            {"offline"},
		"prompt":                 {"consent"}, // always returns a refresh token
		"include_granted_scopes": {"false"},
		"state":                  {state},
		"code_challenge":         {base64.RawURLEncoding.EncodeToString(sum[:])},
		"code_challenge_method":  {"S256"},
	}
	return s.authURL() + "?" + q.Encode(), nil
}

type tokenResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	Scope        string `json:"scope"`
	IDToken      string `json:"id_token"`
	Error        string `json:"error"`
	ErrorDesc    string `json:"error_description"`
}

func (s *Service) token(ctx context.Context, form url.Values) (*tokenResponse, error) {
	form.Set("client_id", s.ClientID)
	form.Set("client_secret", s.ClientSecret)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.tokenURL(), strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := s.Client.Do(req)
	if err != nil {
		return nil, userErrorf("Google token request failed: %w", err)
	}
	defer resp.Body.Close()
	var tr tokenResponse
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&tr); err != nil {
		return nil, userErrorf("Google token response unreadable (HTTP %d)", resp.StatusCode)
	}
	if resp.StatusCode != http.StatusOK || tr.AccessToken == "" {
		if tr.Error == "invalid_grant" {
			return nil, errReconnect
		}
		return nil, userErrorf("Google refused the token request (HTTP %d): %s", resp.StatusCode, firstNonEmpty(tr.Error, "no access token"))
	}
	return &tr, nil
}

var errReconnect = userError("Google access was revoked or expired; disconnect and connect Google Contacts again")

func firstNonEmpty(v ...string) string {
	for _, x := range v {
		if x != "" {
			return x
		}
	}
	return ""
}

// Callback finishes the sign-in Google redirects back to. It returns the
// admin who started it, for the audit log.
func (s *Service) Callback(ctx context.Context, code, state string) (string, error) {
	if !s.Configured() {
		return "", ErrNotConfigured
	}
	if state == "" || len(state) > 100 || code == "" || len(code) > 2048 {
		return "", ErrState
	}
	var verifier, actor string
	err := s.DB.QueryRow(ctx, `DELETE FROM google_oauth_states WHERE state_hash=$1 AND expires_at > now()
		RETURNING code_verifier, created_by`, tokens.Hash(state)).Scan(&verifier, &actor)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrState
	}
	if err != nil {
		return "", err
	}
	tr, err := s.token(ctx, url.Values{"grant_type": {"authorization_code"}, "code": {code},
		"redirect_uri": {s.RedirectURL()}, "code_verifier": {verifier}})
	if err != nil {
		return actor, err
	}
	if !strings.Contains(" "+tr.Scope+" ", " "+ScopeContacts+" ") {
		return actor, userError("Google did not grant access to contacts; connect again and tick the contacts permission")
	}
	if tr.RefreshToken == "" {
		return actor, userError("Google returned no refresh token; remove the app's access in your Google account and connect again")
	}
	enc, err := s.seal([]byte(tr.RefreshToken))
	if err != nil {
		return actor, err
	}
	acct := idTokenEmail(tr.IDToken)
	// A different Google account has different labels: forget the old choice.
	if _, err := s.DB.Exec(ctx, `UPDATE google_contacts_sync SET refresh_token_enc=$1, account_email=NULLIF($2,''),
		connected_at=now(), connected_by=$3, last_error=NULL, updated_at=now(),
		label_resource=CASE WHEN account_email IS DISTINCT FROM NULLIF($2,'') THEN NULL ELSE label_resource END,
		label_name=CASE WHEN account_email IS DISTINCT FROM NULLIF($2,'') THEN NULL ELSE label_name END,
		attested_by=CASE WHEN account_email IS DISTINCT FROM NULLIF($2,'') THEN NULL ELSE attested_by END,
		attested_at=CASE WHEN account_email IS DISTINCT FROM NULLIF($2,'') THEN NULL ELSE attested_at END
		WHERE id=1`, enc, acct, actor); err != nil {
		return actor, err
	}
	_ = audit.Log(ctx, s.DB, actor, "google_contacts.connect", "google_contacts", "", map[string]any{"account": acct}, "")
	return actor, nil
}

// idTokenEmail reads the email claim. The token came straight from Google's
// token endpoint over TLS, so its signature need not be checked again.
func idTokenEmail(idToken string) string {
	parts := strings.Split(idToken, ".")
	if len(parts) != 3 {
		return ""
	}
	b, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return ""
	}
	var c struct {
		Email string `json:"email"`
	}
	_ = json.Unmarshal(b, &c)
	if a, err := subscribers.NormalizeEmail(c.Email); err == nil {
		return a
	}
	return ""
}

// Disconnect revokes the token at Google (best effort) and forgets it. The
// contacts already on the list stay there.
func (s *Service) Disconnect(ctx context.Context, actor string) error {
	if rt, err := s.refreshToken(ctx); err == nil {
		s.revoke(ctx, rt)
	}
	if _, err := s.DB.Exec(ctx, `UPDATE google_contacts_sync SET refresh_token_enc=NULL, account_email=NULL, connected_at=NULL,
		connected_by=NULL, label_resource=NULL, label_name=NULL, attested_by=NULL, attested_at=NULL, last_error=NULL, updated_at=now()
		WHERE id=1`); err != nil {
		return err
	}
	return audit.Log(ctx, s.DB, actor, "google_contacts.disconnect", "google_contacts", "", nil, "")
}

func (s *Service) revoke(ctx context.Context, token string) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.revokeURL(), strings.NewReader(url.Values{"token": {token}}.Encode()))
	if err != nil {
		return
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if resp, err := s.Client.Do(req); err == nil {
		resp.Body.Close()
	} else if s.Log != nil {
		s.Log.Warn("google token revoke failed", "err", err)
	}
}

// --- token encryption ---------------------------------------------------------

func (s *Service) aead() (cipher.AEAD, error) {
	if len(s.AppSecret) < 32 {
		return nil, userError("APP_SECRET is too short to encrypt the Google token")
	}
	m := hmac.New(sha256.New, s.AppSecret)
	m.Write([]byte("google-contacts-refresh-token-v1"))
	block, err := aes.NewCipher(m.Sum(nil))
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

func (s *Service) seal(plain []byte) ([]byte, error) {
	a, err := s.aead()
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, a.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	return a.Seal(nonce, nonce, plain, []byte("google_contacts_sync")), nil
}

func (s *Service) open(enc []byte) ([]byte, error) {
	a, err := s.aead()
	if err != nil {
		return nil, err
	}
	if len(enc) < a.NonceSize() {
		return nil, errReconnect
	}
	p, err := a.Open(nil, enc[:a.NonceSize()], enc[a.NonceSize():], []byte("google_contacts_sync"))
	if err != nil {
		return nil, errReconnect // APP_SECRET changed since the token was stored
	}
	return p, nil
}

func (s *Service) refreshToken(ctx context.Context) (string, error) {
	var enc []byte
	if err := s.DB.QueryRow(ctx, `SELECT refresh_token_enc FROM google_contacts_sync WHERE id=1`).Scan(&enc); err != nil {
		return "", err
	}
	if enc == nil {
		return "", ErrNotConnected
	}
	p, err := s.open(enc)
	return string(p), err
}

func (s *Service) accessToken(ctx context.Context) (string, error) {
	if !s.Configured() {
		return "", ErrNotConfigured
	}
	rt, err := s.refreshToken(ctx)
	if err != nil {
		return "", err
	}
	tr, err := s.token(ctx, url.Values{"grant_type": {"refresh_token"}, "refresh_token": {rt}})
	if err != nil {
		return "", err
	}
	return tr.AccessToken, nil
}

// --- People API ---------------------------------------------------------------

func (s *Service) get(ctx context.Context, access, path string, q url.Values, v any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.peopleURL()+path+"?"+q.Encode(), nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+access)
	resp, err := s.Client.Do(req)
	if err != nil {
		return userErrorf("Google Contacts request failed: %w", err)
	}
	defer resp.Body.Close()
	switch {
	case resp.StatusCode == http.StatusNotFound:
		return errLabelGone
	case resp.StatusCode == http.StatusForbidden:
		return userError("Google refused access to contacts (HTTP 403); check that the People API is enabled for the OAuth client's project")
	case resp.StatusCode == http.StatusUnauthorized:
		return errReconnect
	case resp.StatusCode != http.StatusOK:
		return userErrorf("Google Contacts returned HTTP %d", resp.StatusCode)
	}
	return json.NewDecoder(io.LimitReader(resp.Body, 32<<20)).Decode(v)
}

var errLabelGone = userError("the chosen Google Contacts label no longer exists; choose another one")

type Label struct {
	Resource    string `json:"resource"`
	Name        string `json:"name"`
	MemberCount int    `json:"member_count"`
}

// Labels lists the account's own labels (not system groups such as "My Contacts").
func (s *Service) Labels(ctx context.Context) ([]Label, error) {
	access, err := s.accessToken(ctx)
	if err != nil {
		return nil, err
	}
	out := []Label{}
	page := ""
	for i := 0; i < 20; i++ {
		var r struct {
			ContactGroups []struct {
				ResourceName  string `json:"resourceName"`
				FormattedName string `json:"formattedName"`
				Name          string `json:"name"`
				GroupType     string `json:"groupType"`
				MemberCount   int    `json:"memberCount"`
			} `json:"contactGroups"`
			NextPageToken string `json:"nextPageToken"`
		}
		q := url.Values{"pageSize": {"1000"}, "groupFields": {"name,groupType,memberCount"}}
		if page != "" {
			q.Set("pageToken", page)
		}
		if err := s.get(ctx, access, "/v1/contactGroups", q, &r); err != nil {
			return nil, err
		}
		for _, g := range r.ContactGroups {
			if g.GroupType == "USER_CONTACT_GROUP" {
				out = append(out, Label{Resource: g.ResourceName, Name: firstNonEmpty(g.FormattedName, g.Name), MemberCount: g.MemberCount})
			}
		}
		if page = r.NextPageToken; page == "" {
			break
		}
	}
	return out, nil
}

type contact struct {
	Resource string
	Name     string
	Email    string
}

// members returns the label's contacts with their primary (or first) email.
func (s *Service) members(ctx context.Context, access, label string) ([]contact, error) {
	var g struct {
		MemberResourceNames []string `json:"memberResourceNames"`
		MemberCount         int      `json:"memberCount"`
	}
	if err := s.get(ctx, access, "/v1/"+label, url.Values{"maxMembers": {strconv.Itoa(maxMembers)}}, &g); err != nil {
		return nil, err
	}
	if g.MemberCount > maxMembers {
		return nil, userErrorf("the label has %d contacts; the sync handles up to %d", g.MemberCount, maxMembers)
	}
	out := make([]contact, 0, len(g.MemberResourceNames))
	for i := 0; i < len(g.MemberResourceNames); i += batchSize {
		end := min(i+batchSize, len(g.MemberResourceNames))
		q := url.Values{"personFields": {"names,emailAddresses"}, "resourceNames": g.MemberResourceNames[i:end]}
		var r struct {
			Responses []struct {
				Person *struct {
					ResourceName string `json:"resourceName"`
					Names        []struct {
						DisplayName string `json:"displayName"`
						Metadata    struct {
							Primary bool `json:"primary"`
						} `json:"metadata"`
					} `json:"names"`
					EmailAddresses []struct {
						Value    string `json:"value"`
						Metadata struct {
							Primary bool `json:"primary"`
						} `json:"metadata"`
					} `json:"emailAddresses"`
				} `json:"person"`
			} `json:"responses"`
		}
		if err := s.get(ctx, access, "/v1/people:batchGet", q, &r); err != nil {
			return nil, err
		}
		for _, resp := range r.Responses {
			p := resp.Person
			if p == nil {
				continue
			}
			c := contact{Resource: p.ResourceName}
			for j, n := range p.Names {
				if j == 0 || n.Metadata.Primary {
					c.Name = n.DisplayName
				}
				if n.Metadata.Primary {
					break
				}
			}
			for j, e := range p.EmailAddresses {
				if j == 0 || e.Metadata.Primary {
					c.Email = e.Value
				}
				if e.Metadata.Primary {
					break
				}
			}
			out = append(out, c)
		}
	}
	return out, nil
}

// --- settings -----------------------------------------------------------------

type LabelInput struct {
	Label  string `json:"label_resource"`
	Attest bool   `json:"attest"`
}

// SetLabel chooses the label to sync and records the owner's attestation that
// its contacts consented.
func (s *Service) SetLabel(ctx context.Context, in LabelInput, actor string) (*Label, error) {
	if !in.Attest {
		return nil, ErrAttest
	}
	labels, err := s.Labels(ctx)
	if err != nil {
		return nil, err
	}
	for _, l := range labels {
		if l.Resource == in.Label {
			if _, err := s.DB.Exec(ctx, `UPDATE google_contacts_sync SET label_resource=$1, label_name=$2, attested_by=$3,
				attested_at=now(), last_error=NULL, updated_at=now() WHERE id=1`, l.Resource, l.Name, actor); err != nil {
				return nil, err
			}
			_ = audit.Log(ctx, s.DB, actor, "google_contacts.label", "google_contacts", "", map[string]any{"label": l.Name,
				"attestation": attestation(l.Name)}, "")
			return &l, nil
		}
	}
	return nil, userError("that label was not found in the connected Google account")
}

func (s *Service) SetPaused(ctx context.Context, paused bool, actor string) error {
	if _, err := s.DB.Exec(ctx, `UPDATE google_contacts_sync SET paused=$1, updated_at=now() WHERE id=1`, paused); err != nil {
		return err
	}
	action := "google_contacts.resume"
	if paused {
		action = "google_contacts.pause"
	}
	return audit.Log(ctx, s.DB, actor, action, "google_contacts", "", nil, "")
}

func attestation(label string) string {
	return fmt.Sprintf("Google Contacts label %q, consent attested by owner", label)
}

// --- sync ---------------------------------------------------------------------

type Row struct {
	Email  string `json:"email"`
	Name   string `json:"name"`
	Status string `json:"status"` // new | added | on_list | subscribed | opted_out | no_email | invalid | duplicate
}

type Result struct {
	Label   string         `json:"label"`
	Summary map[string]int `json:"summary"`
	Rows    []Row          `json:"rows,omitempty"`
}

type config struct {
	label, labelName, account, attestedBy string
	paused                                bool
}

func (s *Service) config(ctx context.Context) (*config, error) {
	var c config
	var connected bool
	err := s.DB.QueryRow(ctx, `SELECT refresh_token_enc IS NOT NULL, coalesce(label_resource,''), coalesce(label_name,''),
		coalesce(account_email,''), coalesce(attested_by,''), paused FROM google_contacts_sync WHERE id=1`).
		Scan(&connected, &c.label, &c.labelName, &c.account, &c.attestedBy, &c.paused)
	if err != nil {
		return nil, err
	}
	if !connected {
		return nil, ErrNotConnected
	}
	if c.label == "" {
		return nil, ErrNoLabel
	}
	return &c, nil
}

// classify fetches the label and sorts each contact without writing anything.
func (s *Service) classify(ctx context.Context, c *config) ([]Row, error) {
	access, err := s.accessToken(ctx)
	if err != nil {
		return nil, err
	}
	people, err := s.members(ctx, access, c.label)
	if err != nil {
		return nil, err
	}
	rows := make([]Row, 0, len(people))
	seen := map[string]bool{}
	var emails []string
	for _, p := range people {
		r := Row{Name: p.Name, Email: strings.TrimSpace(p.Email)}
		switch addr, err := subscribers.NormalizeEmail(r.Email); {
		case r.Email == "":
			r.Status = "no_email"
		case err != nil:
			r.Status = "invalid"
		case seen[addr]:
			r.Email, r.Status = addr, "duplicate"
		default:
			r.Email, r.Status = addr, "new"
			seen[addr] = true
			emails = append(emails, addr)
		}
		rows = append(rows, r)
	}
	if len(emails) == 0 {
		return rows, nil
	}
	state := map[string]string{}
	q, err := s.DB.Query(ctx, `SELECT lower(email::text), CASE
			WHEN status IN ('unsubscribed','bounced','complained','suppressed') THEN 'opted_out'
			WHEN source = $2 THEN 'on_list' ELSE 'subscribed' END FROM subscribers WHERE email = ANY($1::citext[])
		UNION ALL SELECT lower(email::text), 'opted_out' FROM email_suppressions WHERE lifted_at IS NULL AND email = ANY($1::citext[])`,
		emails, Source)
	if err != nil {
		return nil, err
	}
	for q.Next() {
		var e, st string
		if err := q.Scan(&e, &st); err != nil {
			q.Close()
			return nil, err
		}
		if state[e] != "opted_out" {
			state[e] = st
		}
	}
	q.Close()
	if err := q.Err(); err != nil {
		return nil, err
	}
	for i := range rows {
		if st, ok := state[rows[i].Email]; ok && rows[i].Status == "new" {
			rows[i].Status = st
		}
	}
	return rows, nil
}

func summarize(rows []Row) map[string]int {
	m := map[string]int{"total": len(rows)}
	for _, r := range rows {
		m[r.Status]++
	}
	return m
}

// Preview shows what a sync would do now, without changing anything.
func (s *Service) Preview(ctx context.Context) (*Result, error) {
	c, err := s.config(ctx)
	if err != nil {
		return nil, err
	}
	rows, err := s.classify(ctx, c)
	if err != nil {
		return nil, err
	}
	res := &Result{Label: c.labelName, Summary: summarize(rows), Rows: rows}
	if len(res.Rows) > 500 {
		res.Rows = res.Rows[:500]
	}
	return res, nil
}

// SyncNow runs a sync for the owner, even when paused or not yet due.
func (s *Service) SyncNow(ctx context.Context, actor string) (*Result, error) {
	if _, err := s.config(ctx); err != nil {
		return nil, err
	}
	if !s.claim(ctx, false) {
		return nil, ErrBusy
	}
	return s.run(ctx, actor, "manual")
}

// RunDue is the worker hook: it syncs when connected, unpaused and due.
func (s *Service) RunDue(ctx context.Context) error {
	if !s.Configured() || !s.claim(ctx, true) {
		return nil
	}
	_, err := s.run(ctx, "worker", "scheduled")
	if errors.Is(err, errReconnect) || errors.Is(err, errLabelGone) {
		return nil // recorded in last_error for the owner; retrying every tick will not help
	}
	return err
}

// claim takes the sync lease, so only one worker or button press runs at a time.
func (s *Service) claim(ctx context.Context, onlyDue bool) bool {
	due := ""
	if onlyDue {
		due = ` AND NOT paused AND label_resource IS NOT NULL AND refresh_token_enc IS NOT NULL
			AND (last_sync_at IS NULL OR last_sync_at < now() - make_interval(secs => ` + strconv.Itoa(int(SyncInterval.Seconds())) + `))`
	}
	tag, err := s.DB.Exec(ctx, `UPDATE google_contacts_sync SET sync_started_at=now()
		WHERE id=1 AND (sync_started_at IS NULL OR sync_started_at < now() - make_interval(secs => $1))`+due,
		int(syncLease.Seconds()))
	return err == nil && tag.RowsAffected() == 1
}

func (s *Service) run(ctx context.Context, actor, trigger string) (res *Result, err error) {
	defer func() {
		var msg any
		if err != nil {
			msg = err.Error()
		}
		var b []byte
		if res != nil {
			b, _ = json.Marshal(map[string]any{"label": res.Label, "summary": res.Summary, "trigger": trigger})
		}
		// last_sync_at moves on failure too, so a broken connection is retried
		// on the normal schedule rather than on every worker tick.
		if _, uerr := s.DB.Exec(context.WithoutCancel(ctx), `UPDATE google_contacts_sync SET sync_started_at=NULL, last_sync_at=now(),
			last_result=coalesce($1::jsonb, last_result), last_error=$2, updated_at=now() WHERE id=1`, b, msg); uerr != nil && err == nil {
			err = uerr
		}
		details := map[string]any{"trigger": trigger}
		if res != nil {
			details["label"], details["summary"] = res.Label, res.Summary
		}
		if err != nil {
			details["error"] = err.Error()
		}
		_ = audit.Log(context.WithoutCancel(ctx), s.DB, actor, "google_contacts.sync", "google_contacts", "", details, "")
	}()
	c, err := s.config(ctx)
	if err != nil {
		return nil, err
	}
	rows, err := s.classify(ctx, c)
	if err != nil {
		return nil, err
	}
	runID := strconv.FormatInt(time.Now().UnixNano(), 36)
	ev := map[string]any{"evidence": attestation(c.labelName), "label": c.labelName, "label_resource": c.label,
		"google_account": c.account, "attested_by": c.attestedBy, "sync_run": runID}
	attester := firstNonEmpty(c.attestedBy, actor)
	for i := range rows {
		if rows[i].Status != "new" {
			continue
		}
		added, err := s.add(ctx, rows[i], ev, attester)
		if err != nil {
			return nil, err
		}
		if added {
			rows[i].Status = "added"
		} else {
			rows[i].Status = "opted_out" // suppressed or added elsewhere since classify
		}
	}
	res = &Result{Label: c.labelName, Summary: summarize(rows)}
	return res, nil
}

// add inserts one contact as a verified subscriber on the Google list with the
// owner's attestation as consent. It adds nobody with an active suppression.
func (s *Service) add(ctx context.Context, r Row, ev map[string]any, actor string) (bool, error) {
	added := false
	err := store.InTx(ctx, s.DB, func(tx pgx.Tx) error {
		var id string
		err := tx.QueryRow(ctx, `INSERT INTO subscribers (email, name, status, source, verified_at)
			SELECT $1, NULLIF($2,''), 'verified', $3, now()
			WHERE NOT EXISTS (SELECT 1 FROM email_suppressions WHERE email=$1 AND lifted_at IS NULL)
			ON CONFLICT (email) DO NOTHING RETURNING id`, r.Email, cleanName(r.Name), Source).Scan(&id)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO subscriber_preferences (subscriber_id, topics) VALUES ($1,'{}')`, id); err != nil {
			return err
		}
		for _, action := range []string{"granted", "attested"} {
			if _, err := tx.Exec(ctx, `INSERT INTO subscriber_consents (subscriber_id, action, lawful_basis, source, evidence, actor)
				VALUES ($1,$2,'consent',$3,$4,$5)`, id, action, Source, ev, actor); err != nil {
				return err
			}
		}
		added = true
		return nil
	})
	return added, err
}

func cleanName(s string) string {
	s = strings.TrimSpace(strings.Map(func(r rune) rune {
		if r < 32 || r == '<' || r == '>' {
			return -1
		}
		return r
	}, s))
	if r := []rune(s); len(r) > 100 {
		s = string(r[:100])
	}
	return s
}

// userErr is a failure whose message is meant for the owner (Google's answer,
// a missing setting), unlike database errors.
type userErr struct{ err error }

func (e userErr) Error() string { return e.err.Error() }
func (e userErr) Unwrap() error { return e.err }

func userError(msg string) error { return userErr{errors.New(msg)} }

func userErrorf(format string, a ...any) error { return userErr{fmt.Errorf(format, a...)} }

// IsUserError reports whether err's message can be shown to the owner as is.
func IsUserError(err error) bool {
	var u userErr
	return errors.As(err, &u)
}
