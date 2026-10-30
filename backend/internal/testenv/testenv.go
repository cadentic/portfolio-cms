// Package testenv builds the complete backend (API, worker, real Brevo and
// Mailjet adapters) against a throwaway PostgreSQL database and the in-memory
// mock providers, for integration and end-to-end tests.
//
// Tests are skipped unless TEST_DATABASE_URL points at a PostgreSQL server
// where the user may create databases, e.g.
//
//	TEST_DATABASE_URL=postgres://postgres@localhost:5432/postgres?sslmode=disable go test ./...
package testenv

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/cadentic/portfolio-cms/backend/internal/alerts"
	"github.com/cadentic/portfolio-cms/backend/internal/api"
	"github.com/cadentic/portfolio-cms/backend/internal/auth"
	"github.com/cadentic/portfolio-cms/backend/internal/campaigns"
	"github.com/cadentic/portfolio-cms/backend/internal/config"
	"github.com/cadentic/portfolio-cms/backend/internal/content"
	"github.com/cadentic/portfolio-cms/backend/internal/email"
	"github.com/cadentic/portfolio-cms/backend/internal/email/brevo"
	"github.com/cadentic/portfolio-cms/backend/internal/email/mailjet"
	"github.com/cadentic/portfolio-cms/backend/internal/googlecontacts"
	"github.com/cadentic/portfolio-cms/backend/internal/mailer"
	"github.com/cadentic/portfolio-cms/backend/internal/mcp"
	"github.com/cadentic/portfolio-cms/backend/internal/media"
	"github.com/cadentic/portfolio-cms/backend/internal/mockprovider"
	"github.com/cadentic/portfolio-cms/backend/internal/oauth"
	"github.com/cadentic/portfolio-cms/backend/internal/providers"
	"github.com/cadentic/portfolio-cms/backend/internal/queue"
	"github.com/cadentic/portfolio-cms/backend/internal/ratelimit"
	"github.com/cadentic/portfolio-cms/backend/internal/settings"
	"github.com/cadentic/portfolio-cms/backend/internal/sitecontent"
	"github.com/cadentic/portfolio-cms/backend/internal/store"
	"github.com/cadentic/portfolio-cms/backend/internal/subscribers"
	"github.com/cadentic/portfolio-cms/backend/internal/templates"
	"github.com/cadentic/portfolio-cms/backend/internal/tokens"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	ProjectID     = "demo-portfolio"
	AdminEmail    = "owner@cadentic.dev"
	WebhookSecret = "test-webhook-secret-123"
	SiteURL       = "https://cadentic.test"
)

type Env struct {
	T           *testing.T
	Ctx         context.Context
	DB          *pgxpool.Pool
	Mock        *mockprovider.Server
	MockHTTP    *httptest.Server
	API         *httptest.Server
	Server      *api.Server
	Worker      *queue.Worker
	Settings    *settings.Store
	Providers   *providers.Repo
	Subscribers *subscribers.Service
	Campaigns   *campaigns.Service
	Content     *content.Service
	SiteContent *sitecontent.Service
	Signer      tokens.Signer
	Secrets     providers.MapSecrets
	BrevoID     string
	MailjetID   string
}

// New creates a fresh database and wires every component. Delivery settings
// are tightened (1s timeouts, small backoffs) so tests run quickly; tests move
// time forward with Advance instead of sleeping.
func New(t *testing.T) *Env {
	t.Helper()
	base := os.Getenv("TEST_DATABASE_URL")
	if base == "" {
		t.Skip("TEST_DATABASE_URL not set; skipping database-backed test")
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	admin, err := pgx.Connect(ctx, base)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	b := make([]byte, 6)
	_, _ = rand.Read(b)
	dbName := "pcms_test_" + hex.EncodeToString(b)
	if _, err := admin.Exec(ctx, "CREATE DATABASE "+dbName); err != nil {
		t.Fatalf("create database: %v", err)
	}
	u, _ := url.Parse(base)
	u.Path = "/" + dbName
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	if testing.Verbose() && os.Getenv("TEST_LOG") != "" {
		log = slog.New(slog.NewTextHandler(os.Stderr, nil))
	}
	db, err := store.Open(ctx, u.String(), 10)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() {
		db.Close()
		_, _ = admin.Exec(context.Background(), "DROP DATABASE IF EXISTS "+dbName+" WITH (FORCE)")
		_ = admin.Close(context.Background())
	})
	if err := store.Migrate(ctx, db, log); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if err := templates.Seed(ctx, db); err != nil {
		t.Fatalf("seed templates: %v", err)
	}

	mock := mockprovider.New("", "")
	mockHTTP := httptest.NewServer(mock.Handler())
	t.Cleanup(mockHTTP.Close)

	client := &http.Client{Timeout: 10 * time.Second}
	registry := email.NewRegistry(brevo.New(client), mailjet.New(client))
	secrets := providers.MapSecrets{
		"EMAIL_BREVO_API_KEY": "brevo-test-key", "EMAIL_BREVO_WEBHOOK_SECRET": WebhookSecret,
		"EMAIL_MAILJET_API_KEY": "mj-key", "EMAIL_MAILJET_SECRET_KEY": "mj-secret", "EMAIL_MAILJET_WEBHOOK_SECRET": WebhookSecret,
	}
	provs := &providers.Repo{DB: db, Secrets: secrets, Registry: registry,
		BaseURLs: map[string]string{"brevo": mockHTTP.URL, "mailjet": mockHTTP.URL}}

	sets := settings.NewStore(db, 0)
	es := settings.DefaultEmail()
	es.SenderEmail = "newsletter@cadentic.test"
	es.PostalAddress = "Kolkata, India"
	es.TestRecipients = []string{AdminEmail}
	if err := sets.SaveEmail(ctx, es, "test"); err != nil {
		t.Fatalf("save email settings: %v", err)
	}
	ds := settings.DefaultDelivery()
	ds.ProviderRetryBaseSeconds, ds.ProviderRetryMaxSeconds = 1, 2
	ds.CycleBackoffBaseSeconds, ds.CycleBackoffMaxSeconds = 5, 10
	ds.SendTimeoutSeconds = 1
	ds.UnknownReconcileSeconds = 30
	ds.LeaseSeconds = 30
	if err := sets.SaveDelivery(ctx, ds, "test"); err != nil {
		t.Fatalf("save delivery settings: %v", err)
	}

	signer := tokens.Signer{Key: []byte("0123456789abcdef0123456789abcdef-test")}
	mail := &mailer.Mailer{Settings: sets, SiteURL: SiteURL}
	subs := &subscribers.Service{DB: db, Settings: sets, Mailer: mail, Signer: signer, SiteURL: SiteURL}
	camps := &campaigns.Service{DB: db, Settings: sets, Subscribers: subs, SiteURL: SiteURL}
	cms := &content.Service{DB: db, SiteURL: SiteURL}
	site := &sitecontent.Service{DB: db, TTL: 0, Log: log}
	alertSvc := &alerts.Service{DB: db, Log: log}

	cfg := &config.Config{Env: "test", PublicSiteURL: SiteURL, CORSOrigins: []string{SiteURL}, AppSecret: signer.Key,
		FirebaseProjectID: ProjectID, FirebaseEmulatorHost: "emulator", AdminEmails: []string{AdminEmail}, BlogEnabled: true,
		GoogleContactsEnabled: true}
	verifier := &auth.FirebaseVerifier{ProjectID: ProjectID, Emulator: true, Client: client}
	authz := auth.Authorizer{Emails: []string{AdminEmail}}
	srv := &api.Server{
		Cfg: cfg, DB: db, Log: log, Verifier: verifier, Authorizer: authz, Settings: sets, Providers: provs,
		Registry: registry, Subscribers: subs, Campaigns: camps, Content: cms, Mailer: mail, Alerts: alertSvc,
		Limiter: &ratelimit.Limiter{DB: db}, Signer: signer, SiteContent: site,
		GoogleContacts: &googlecontacts.Service{DB: db, Client: client, SiteURL: SiteURL, AppSecret: signer.Key, Log: log},
		OAuth:          &oauth.Server{DB: db, Issuer: SiteURL, SiteURL: SiteURL, Authorizer: authz},
		MCP: &mcp.Server{DB: db, Content: cms, Campaigns: camps, Subscribers: subs, Providers: provs, SiteContent: site, Log: log,
			SiteURL: SiteURL},
	}
	cfg.MediaMaxUploadBytes = 1 << 20
	if srv.Media, err = media.NewLocal(t.TempDir()); err != nil {
		t.Fatalf("media storage: %v", err)
	}
	apiHTTP := httptest.NewServer(srv.Routes())
	t.Cleanup(apiHTTP.Close)

	e := &Env{T: t, Ctx: ctx, DB: db, Mock: mock, MockHTTP: mockHTTP, API: apiHTTP, Server: srv, Settings: sets,
		Providers: provs, Subscribers: subs, Campaigns: camps, Content: cms, SiteContent: site, Signer: signer, Secrets: secrets}
	e.Worker = e.NewWorker("worker-1")

	s := func(v string) *string { return &v }
	bp := func(v bool) *bool { return &v }
	ip := func(v int) *int { return &v }
	fast := 1000.0 // the local token bucket is covered separately
	bv, err := provs.Create(ctx, providers.Input{Name: s("Brevo"), Kind: s("brevo"), Enabled: bp(true), Priority: ip(10),
		CredentialRefs: map[string]string{"api_key": "EMAIL_BREVO_API_KEY"}, WebhookSecretRef: s("EMAIL_BREVO_WEBHOOK_SECRET"), MaxAttempts: ip(2), RateLimitPerSecond: &fast})
	if err != nil {
		t.Fatalf("create brevo: %v", err)
	}
	mj, err := provs.Create(ctx, providers.Input{Name: s("Mailjet"), Kind: s("mailjet"), Enabled: bp(true), Priority: ip(20),
		CredentialRefs:   map[string]string{"api_key": "EMAIL_MAILJET_API_KEY", "secret_key": "EMAIL_MAILJET_SECRET_KEY"},
		WebhookSecretRef: s("EMAIL_MAILJET_WEBHOOK_SECRET"), MaxAttempts: ip(2), RateLimitPerSecond: &fast})
	if err != nil {
		t.Fatalf("create mailjet: %v", err)
	}
	e.BrevoID, e.MailjetID = bv.ID, mj.ID
	return e
}

// NewWorker returns another worker sharing the database (to model a second
// replica or a restarted process). Jitter is fixed for deterministic backoff.
func (e *Env) NewWorker(id string) *queue.Worker {
	return &queue.Worker{DB: e.DB, Providers: e.Providers, Registry: e.Server.Registry, Settings: e.Settings,
		Alerts: e.Server.Alerts, Log: e.Server.Log, ID: id, Hooks: []queue.Hook{e.Campaigns.Dispatch},
		Jitter: func() float64 { return 0.5 }}
}

// Tick runs one worker iteration and fails the test on error.
func (e *Env) Tick() int {
	e.T.Helper()
	n, err := e.Worker.Tick(e.Ctx)
	if err != nil {
		e.T.Fatalf("worker tick: %v", err)
	}
	return n
}

// Advance makes every scheduled (but not parked) job and open circuit due now,
// standing in for the passage of time.
func (e *Env) Advance() {
	e.T.Helper()
	e.Exec(`UPDATE email_jobs SET next_attempt_at = now() - interval '1 second'
		WHERE next_attempt_at > now() AND next_attempt_at <> 'infinity'`)
	e.Exec(`UPDATE email_providers SET circuit_opened_at = now() - interval '1 day' WHERE circuit_state = 'open'`)
}

// Drain ticks (advancing time between ticks) until nothing is due or the
// limit is reached.
func (e *Env) Drain(maxTicks int) {
	e.T.Helper()
	for i := 0; i < maxTicks; i++ {
		e.Advance()
		if e.Tick() == 0 && e.Count(`SELECT count(*) FROM email_jobs WHERE status IN ('queued','retry_scheduled') AND next_attempt_at <= now()`) == 0 {
			return
		}
	}
}

func (e *Env) Exec(sql string, args ...any) {
	e.T.Helper()
	if _, err := e.DB.Exec(e.Ctx, sql, args...); err != nil {
		e.T.Fatalf("exec %q: %v", sql, err)
	}
}

func (e *Env) Count(sql string, args ...any) int {
	e.T.Helper()
	var n int
	if err := e.DB.QueryRow(e.Ctx, sql, args...).Scan(&n); err != nil {
		e.T.Fatalf("count %q: %v", sql, err)
	}
	return n
}

func (e *Env) Str(sql string, args ...any) string {
	e.T.Helper()
	var s string
	if err := e.DB.QueryRow(e.Ctx, sql, args...).Scan(&s); err != nil {
		e.T.Fatalf("query %q: %v", sql, err)
	}
	return s
}

// JobStatus returns the status of the newest job to the address.
func (e *Env) JobStatus(to string) string {
	return e.Str(`SELECT status FROM email_jobs WHERE to_email=$1 ORDER BY created_at DESC LIMIT 1`, to)
}

func (e *Env) JobID(to string) string {
	return e.Str(`SELECT id::text FROM email_jobs WHERE to_email=$1 AND provider_message_id IS NOT NULL ORDER BY created_at DESC LIMIT 1`, to)
}

// Enqueue puts a transactional message straight on the queue.
func (e *Env) Enqueue(to, key string) string {
	e.T.Helper()
	id, err := e.Server.Mailer.Send(e.Ctx, e.DB, mailer.Request{Template: "contact_ack", To: email.Address{Email: to},
		Vars: map[string]string{"first_name": "Test"}, IdempotencyKey: key, Kind: "contact_ack"})
	if err != nil {
		e.T.Fatalf("enqueue: %v", err)
	}
	return id
}

// --- HTTP helpers ------------------------------------------------------------

type Response struct {
	Status int
	Body   []byte
	Header http.Header
}

func (r Response) JSON(v any) error { return json.Unmarshal(r.Body, v) }

func (r Response) Map() map[string]any {
	m := map[string]any{}
	_ = json.Unmarshal(r.Body, &m)
	return m
}

func (e *Env) Do(method, path string, body any, headers map[string]string) Response {
	e.T.Helper()
	var rd io.Reader
	switch b := body.(type) {
	case nil:
	case string:
		rd = strings.NewReader(b)
	case []byte:
		rd = bytes.NewReader(b)
	default:
		j, _ := json.Marshal(b)
		rd = bytes.NewReader(j)
	}
	req, _ := http.NewRequestWithContext(e.Ctx, method, e.API.URL+path, rd)
	if rd != nil && req.Header.Get("Content-Type") == "" {
		req.Header.Set("Content-Type", "application/json")
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		e.T.Fatalf("%s %s: %v", method, path, err)
	}
	defer resp.Body.Close()
	out, _ := io.ReadAll(resp.Body)
	return Response{Status: resp.StatusCode, Body: out, Header: resp.Header}
}

// Admin sends a request authenticated as the site owner.
func (e *Env) Admin(method, path string, body any) Response {
	return e.Do(method, path, body, map[string]string{"Authorization": "Bearer " + EmulatorToken("owner-uid", AdminEmail)})
}

// AdminUpload posts a multipart file as the site owner.
func (e *Env) AdminUpload(path, field, filename string, data []byte) Response {
	e.T.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	fw, _ := mw.CreateFormFile(field, filename)
	_, _ = fw.Write(data)
	_ = mw.Close()
	return e.Do("POST", path, buf.Bytes(), map[string]string{"Content-Type": mw.FormDataContentType(),
		"Authorization": "Bearer " + EmulatorToken("owner-uid", AdminEmail)})
}

// EmulatorToken builds an unsigned Firebase Auth Emulator ID token.
func EmulatorToken(uid, mail string) string {
	enc := func(v any) string {
		b, _ := json.Marshal(v)
		return base64.RawURLEncoding.EncodeToString(b)
	}
	now := time.Now().Unix()
	return enc(map[string]any{"alg": "none", "typ": "JWT"}) + "." + enc(map[string]any{
		"iss": "https://securetoken.google.com/" + ProjectID, "aud": ProjectID, "sub": uid, "user_id": uid,
		"email": mail, "email_verified": true, "iat": now, "exp": now + 3600, "auth_time": now,
	}) + "."
}

var confirmRe = regexp.MustCompile(`/subscribe/confirm\?token=([A-Za-z0-9_\-]+)`)

// ConfirmToken delivers the queue and extracts the newest confirmation token
// mailed to the address.
func (e *Env) ConfirmToken(to string) string {
	e.T.Helper()
	e.Drain(5)
	msgs := e.Mock.Messages()
	for i := len(msgs) - 1; i >= 0; i-- {
		if strings.EqualFold(msgs[i].To, to) {
			if m := confirmRe.FindStringSubmatch(msgs[i].HTML); m != nil {
				return m[1]
			}
		}
	}
	e.T.Fatalf("no confirmation email for %s", to)
	return ""
}

// Subscribe runs the public double opt-in flow end to end.
func (e *Env) Subscribe(addr string, topics ...string) string {
	e.T.Helper()
	r := e.Do("POST", "/api/public/subscribe", map[string]any{"email": addr, "name": "Reader", "consent": true, "topics": topics}, nil)
	if r.Status != 202 {
		e.T.Fatalf("subscribe %s: %d %s", addr, r.Status, r.Body)
	}
	tok := e.ConfirmToken(addr)
	if r := e.Do("POST", "/api/public/subscribe/confirm", map[string]string{"token": tok}, nil); r.Status != 200 {
		e.T.Fatalf("confirm %s: %d %s", addr, r.Status, r.Body)
	}
	e.Drain(5) // welcome email
	return e.Str(`SELECT id::text FROM subscribers WHERE email=$1`, addr)
}

// Webhook posts a provider event with the shared-secret basic auth.
func (e *Env) Webhook(providerID string, payload any, secret string) Response {
	b, _ := json.Marshal(payload)
	req, _ := http.NewRequestWithContext(e.Ctx, "POST", e.API.URL+"/api/webhooks/"+providerID, bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	if secret != "" {
		req.SetBasicAuth("webhook", secret)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		e.T.Fatalf("webhook: %v", err)
	}
	defer resp.Body.Close()
	out, _ := io.ReadAll(resp.Body)
	return Response{Status: resp.StatusCode, Body: out}
}

func Sprint(v ...any) string { return fmt.Sprint(v...) }
