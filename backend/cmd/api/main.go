// Command api serves the public and admin REST API, webhooks, OAuth and the
// MCP endpoint, and (unless RUN_WORKER=false) runs the email queue worker.
package main

import (
	"context"
	"errors"
	"github.com/jackc/pgx/v5/pgxpool"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
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
	"github.com/cadentic/portfolio-cms/backend/internal/email/smtp"
	"github.com/cadentic/portfolio-cms/backend/internal/mailer"
	"github.com/cadentic/portfolio-cms/backend/internal/mcp"
	"github.com/cadentic/portfolio-cms/backend/internal/media"
	"github.com/cadentic/portfolio-cms/backend/internal/oauth"
	"github.com/cadentic/portfolio-cms/backend/internal/providers"
	"github.com/cadentic/portfolio-cms/backend/internal/queue"
	"github.com/cadentic/portfolio-cms/backend/internal/ratelimit"
	"github.com/cadentic/portfolio-cms/backend/internal/retention"
	"github.com/cadentic/portfolio-cms/backend/internal/settings"
	"github.com/cadentic/portfolio-cms/backend/internal/sitecontent"
	"github.com/cadentic/portfolio-cms/backend/internal/store"
	"github.com/cadentic/portfolio-cms/backend/internal/subscribers"
	"github.com/cadentic/portfolio-cms/backend/internal/templates"
	"github.com/cadentic/portfolio-cms/backend/internal/tokens"
)

func main() {
	log := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	if err := run(log); err != nil {
		log.Error("fatal", "err", err)
		os.Exit(1)
	}
}

func run(log *slog.Logger) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	db, err := store.Open(ctx, cfg.DatabaseURL, cfg.DBMaxConns)
	if err != nil {
		return err
	}
	defer db.Close()
	// A read-only database is being copied to another host (tools/cloudmigrate).
	// Keep serving reads, but skip the start-up writes.
	readOnly, err := store.IsReadOnly(ctx, db)
	if err != nil {
		return err
	}
	if readOnly {
		log.Warn("database is read-only; skipping migrations and seeding")
		if err := store.CheckMigrated(ctx, db); err != nil {
			return err
		}
	} else {
		if err := store.Migrate(ctx, db, log); err != nil {
			return err
		}
		if err := templates.Seed(ctx, db); err != nil {
			return err
		}
	}

	httpClient := &http.Client{Timeout: 30 * time.Second}
	registry := email.NewRegistry(brevo.New(httpClient), mailjet.New(httpClient), smtp.New())
	baseURLs := map[string]string{}
	if cfg.BrevoBaseURL != "" {
		baseURLs["brevo"] = cfg.BrevoBaseURL
	}
	if cfg.MailjetBaseURL != "" {
		baseURLs["mailjet"] = cfg.MailjetBaseURL
	}
	provs := &providers.Repo{DB: db, Secrets: providers.EnvSecrets{}, Registry: registry, BaseURLs: baseURLs}
	if !readOnly {
		if err := seedProviders(ctx, db, provs, log); err != nil {
			return err
		}
	}

	sets := settings.NewStore(db, 30*time.Second)
	signer := tokens.Signer{Key: cfg.AppSecret}
	mail := &mailer.Mailer{Settings: sets, SiteURL: cfg.PublicSiteURL}
	subs := &subscribers.Service{DB: db, Settings: sets, Mailer: mail, Signer: signer, SiteURL: cfg.PublicSiteURL}
	camps := &campaigns.Service{DB: db, Settings: sets, Subscribers: subs, SiteURL: cfg.PublicSiteURL}
	cms := &content.Service{DB: db, SiteURL: cfg.PublicSiteURL}
	site := &sitecontent.Service{DB: db, TTL: 5 * time.Second, Log: log}
	alertSvc := &alerts.Service{DB: db, Log: log}
	if cfg.AdminAlertEmail != "" {
		alertSvc.Notify = func(ctx context.Context, id string, sev alerts.Severity, kind, msg string) {
			_, err := mail.Send(ctx, db, mailer.Request{
				Template:       "admin_alert",
				To:             email.Address{Email: cfg.AdminAlertEmail},
				Vars:           map[string]string{"severity": string(sev), "kind": kind, "message": msg, "admin_url": cfg.PublicSiteURL + "/admin/email/monitor"},
				IdempotencyKey: "alert:" + id,
				Kind:           "alert",
				Priority:       1,
			})
			if err != nil {
				log.Error("alert notification enqueue failed", "err", err)
			}
		}
	}

	verifier := &auth.FirebaseVerifier{
		ProjectID: cfg.FirebaseProjectID,
		Emulator:  cfg.FirebaseEmulatorHost != "" && !cfg.IsProduction(),
		Client:    httpClient,
	}
	authz := auth.Authorizer{UIDs: cfg.AdminUIDs, Emails: cfg.AdminEmails, AllowUnverifiedMail: verifier.Emulator}
	if len(cfg.AdminUIDs) == 0 && len(cfg.AdminEmails) == 0 {
		log.Warn("no ADMIN_UIDS or ADMIN_EMAILS configured: the admin area is locked")
	}

	mediaStore, err := media.New(cfg)
	if err != nil {
		return err
	}

	srv := &api.Server{
		Cfg: cfg, DB: db, Log: log, Verifier: verifier, Authorizer: authz, Settings: sets,
		Providers: provs, Registry: registry, Subscribers: subs, Campaigns: camps, Content: cms,
		Mailer: mail, Alerts: alertSvc, Limiter: &ratelimit.Limiter{DB: db}, Signer: signer,
		OAuth: &oauth.Server{DB: db, Issuer: cfg.PublicSiteURL, SiteURL: cfg.PublicSiteURL, Authorizer: authz},
		MCP: &mcp.Server{DB: db, Content: cms, Campaigns: camps, Subscribers: subs, Providers: provs, SiteContent: site, Log: log,
			SiteURL: cfg.PublicSiteURL},
		Media: mediaStore, SiteContent: site,
	}

	var wg sync.WaitGroup
	if cfg.RunWorker {
		w := &queue.Worker{
			DB: db, Providers: provs, Registry: registry, Settings: sets, Alerts: alertSvc, Log: log, ID: cfg.WorkerID,
			Hooks: []queue.Hook{
				camps.Dispatch,
				every(time.Hour, func(ctx context.Context) error { return subs.PurgeStalePending(ctx) }),
				every(6*time.Hour, func(ctx context.Context) error {
					es, err := sets.Email(ctx)
					if err != nil {
						return err
					}
					_, err = queue.PurgeBodies(ctx, db, es.JobBodyRetentionDays)
					return err
				}),
				every(10*time.Minute, func(ctx context.Context) error { return ratelimit.Cleanup(ctx, db) }),
				every(6*time.Hour, func(ctx context.Context) error {
					n, err := retention.Purge(ctx, db)
					log.Info("retention purge", "rows", n)
					return err
				}),
			},
		}
		wg.Add(1)
		go func() { defer wg.Done(); w.Run(ctx) }()
	}

	hs := &http.Server{
		Addr:              cfg.HTTPAddr,
		Handler:           srv.Routes(),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       60 * time.Second,
		WriteTimeout:      120 * time.Second,
		IdleTimeout:       120 * time.Second,
		MaxHeaderBytes:    64 << 10,
	}
	errc := make(chan error, 1)
	go func() {
		log.Info("api listening", "addr", cfg.HTTPAddr, "env", cfg.Env)
		if err := hs.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errc <- err
		}
	}()

	select {
	case err := <-errc:
		return err
	case <-ctx.Done():
	}
	log.Info("shutting down")
	sctx, cancel := context.WithTimeout(context.Background(), cfg.ShutdownTimeout)
	defer cancel()
	_ = hs.Shutdown(sctx)
	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-sctx.Done():
		log.Warn("worker did not stop in time; leases will be recovered")
	}
	return nil
}

// every throttles a hook so it runs at most once per interval in this process.
func every(d time.Duration, h queue.Hook) queue.Hook {
	var mu sync.Mutex
	var last time.Time
	return func(ctx context.Context) error {
		mu.Lock()
		if time.Since(last) < d {
			mu.Unlock()
			return nil
		}
		last = time.Now()
		mu.Unlock()
		return h(ctx)
	}
}

// seedProviders creates the default Brevo (primary) and Mailjet (secondary)
// entries on first boot. They reference environment variables by name only;
// a provider is used only once its secrets are present in the environment.
func seedProviders(ctx context.Context, db *pgxpool.Pool, r *providers.Repo, log *slog.Logger) error {
	// API and worker containers can start together; serialise the check-then-insert.
	conn, err := db.Acquire(ctx)
	if err != nil {
		return err
	}
	defer conn.Release()
	if _, err := conn.Exec(ctx, `SELECT pg_advisory_lock($1)`, store.LockProviders); err != nil {
		return err
	}
	defer conn.Exec(context.Background(), `SELECT pg_advisory_unlock($1)`, store.LockProviders) //nolint:errcheck
	existing, err := r.List(ctx)
	if err != nil || len(existing) > 0 {
		return err
	}
	s := func(v string) *string { return &v }
	b := func(v bool) *bool { return &v }
	i := func(v int) *int { return &v }
	defaults := []providers.Input{
		{Name: s("Brevo"), Kind: s("brevo"), Enabled: b(true), Priority: i(10),
			CredentialRefs:   map[string]string{"api_key": "EMAIL_BREVO_API_KEY"},
			WebhookSecretRef: s("EMAIL_BREVO_WEBHOOK_SECRET"), MaxAttempts: i(3)},
		{Name: s("Mailjet"), Kind: s("mailjet"), Enabled: b(true), Priority: i(20),
			CredentialRefs:   map[string]string{"api_key": "EMAIL_MAILJET_API_KEY", "secret_key": "EMAIL_MAILJET_SECRET_KEY"},
			WebhookSecretRef: s("EMAIL_MAILJET_WEBHOOK_SECRET"), MaxAttempts: i(3)},
	}
	for _, in := range defaults {
		if _, err := r.Create(ctx, in); err != nil {
			return err
		}
	}
	log.Info("seeded default email providers (Brevo primary, Mailjet secondary)")
	return nil
}
