// Package config loads process configuration from environment variables.
// Secrets (provider API keys, webhook secrets, APP_SECRET) are read from the
// environment, which in production is populated from the cloud secret manager.
package config

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	Env              string // "development" | "production" | "test"
	HTTPAddr         string
	DatabaseURL      string
	DBMaxConns       int32
	PublicSiteURL    string // Angular site origin, used in email links
	CORSOrigins      []string
	TrustedProxyHops int
	// TrustCloudflareIP honours CF-Connecting-IP; only safe when the origin is reachable through Cloudflare alone.
	TrustCloudflareIP bool
	// TrustClientIPHeader takes X-Client-IP from the web tier. Only safe when the
	// API is reachable solely through that tier (Cloud Run IAM, internal ingress, compose).
	TrustClientIPHeader bool

	AppSecret []byte // HMAC key for unsubscribe/download tokens

	FirebaseProjectID    string
	FirebaseEmulatorHost string // only honoured outside production
	AdminUIDs            []string
	AdminEmails          []string

	RunWorker bool
	WorkerID  string

	ResumeFilePath  string
	AdminAlertEmail string
	GTMID           string
	GA4ID           string
	BlogEnabled     bool

	// Media storage: "local" (MEDIA_LOCAL_DIR), "gcs" or "azure". Buckets are
	// private; files are served only through GET /media/{key}.
	MediaStorage          string
	MediaLocalDir         string
	MediaMaxUploadBytes   int64
	MediaGCSBucket        string
	MediaGCSCredentials   string // service-account key JSON; empty uses the metadata server
	MediaAzureAccount     string
	MediaAzureContainer   string
	MediaAzureSAS         string // empty uses the managed identity
	AzureIdentityEndpoint string
	AzureIdentityHeader   string
	AzureClientID         string

	// Provider base URL overrides (local mock server / tests only).
	BrevoBaseURL   string
	MailjetBaseURL string

	ShutdownTimeout time.Duration
}

func (c *Config) IsProduction() bool { return c.Env == "production" }

func Load() (*Config, error) {
	c := &Config{
		Env:                  get("APP_ENV", "development"),
		HTTPAddr:             get("HTTP_ADDR", ":8080"),
		DatabaseURL:          os.Getenv("DATABASE_URL"),
		DBMaxConns:           int32(getInt("DB_MAX_CONNS", 10)),
		PublicSiteURL:        strings.TrimRight(get("PUBLIC_SITE_URL", "http://localhost:4200"), "/"),
		CORSOrigins:          list(get("CORS_ALLOWED_ORIGINS", "http://localhost:4200")),
		TrustedProxyHops:     getInt("TRUSTED_PROXY_HOPS", 0),
		TrustCloudflareIP:    getBool("TRUST_CLOUDFLARE_IP", false),
		TrustClientIPHeader:  getBool("TRUST_CLIENT_IP_HEADER", false),
		AppSecret:            []byte(os.Getenv("APP_SECRET")),
		FirebaseProjectID:    os.Getenv("FIREBASE_PROJECT_ID"),
		FirebaseEmulatorHost: os.Getenv("FIREBASE_AUTH_EMULATOR_HOST"),
		AdminUIDs:            list(os.Getenv("ADMIN_UIDS")),
		AdminEmails:          lower(list(os.Getenv("ADMIN_EMAILS"))),
		RunWorker:            getBool("RUN_WORKER", true),
		WorkerID:             get("WORKER_ID", hostname()),
		ResumeFilePath:       os.Getenv("RESUME_FILE_PATH"),
		AdminAlertEmail:      os.Getenv("ADMIN_ALERT_EMAIL"),
		GTMID:                os.Getenv("GTM_ID"),
		GA4ID:                os.Getenv("GA4_MEASUREMENT_ID"),
		BlogEnabled:          getBool("BLOG_ENABLED", true),
		BrevoBaseURL:         os.Getenv("EMAIL_BREVO_BASE_URL"),
		MailjetBaseURL:       os.Getenv("EMAIL_MAILJET_BASE_URL"),
		ShutdownTimeout:      time.Duration(getInt("SHUTDOWN_TIMEOUT_SECONDS", 25)) * time.Second,

		MediaStorage:          strings.ToLower(get("MEDIA_STORAGE", "local")),
		MediaLocalDir:         get("MEDIA_LOCAL_DIR", "/data/media"),
		MediaMaxUploadBytes:   int64(getInt("MEDIA_MAX_UPLOAD_MB", 30)) << 20,
		MediaGCSBucket:        os.Getenv("MEDIA_GCS_BUCKET"),
		MediaGCSCredentials:   os.Getenv("MEDIA_GCS_CREDENTIALS"),
		MediaAzureAccount:     os.Getenv("MEDIA_AZURE_ACCOUNT"),
		MediaAzureContainer:   os.Getenv("MEDIA_AZURE_CONTAINER"),
		MediaAzureSAS:         os.Getenv("MEDIA_AZURE_SAS"),
		AzureIdentityEndpoint: os.Getenv("IDENTITY_ENDPOINT"),
		AzureIdentityHeader:   os.Getenv("IDENTITY_HEADER"),
		AzureClientID:         os.Getenv("AZURE_CLIENT_ID"),
	}
	var errs []error
	if c.DatabaseURL == "" {
		errs = append(errs, errors.New("DATABASE_URL is required"))
	}
	if len(c.AppSecret) < 32 {
		errs = append(errs, errors.New("APP_SECRET must be at least 32 bytes"))
	}
	if c.FirebaseProjectID == "" {
		errs = append(errs, errors.New("FIREBASE_PROJECT_ID is required"))
	}
	if len(c.AdminUIDs) == 0 && len(c.AdminEmails) == 0 {
		errs = append(errs, errors.New("set ADMIN_UIDS and/or ADMIN_EMAILS to authorise the owner"))
	}
	if c.MediaMaxUploadBytes <= 0 {
		errs = append(errs, errors.New("MEDIA_MAX_UPLOAD_MB must be positive"))
	}
	if c.IsProduction() {
		if c.FirebaseEmulatorHost != "" {
			errs = append(errs, errors.New("FIREBASE_AUTH_EMULATOR_HOST must not be set in production"))
		}
		if c.BrevoBaseURL != "" || c.MailjetBaseURL != "" {
			errs = append(errs, errors.New("provider base URL overrides are not allowed in production"))
		}
		if !strings.HasPrefix(c.PublicSiteURL, "https://") {
			errs = append(errs, errors.New("PUBLIC_SITE_URL must use https in production"))
		}
	}
	if len(errs) > 0 {
		return nil, fmt.Errorf("invalid configuration: %w", errors.Join(errs...))
	}
	return c, nil
}

func get(k, def string) string {
	if v := strings.TrimSpace(os.Getenv(k)); v != "" {
		return v
	}
	return def
}

func getInt(k string, def int) int {
	if v, err := strconv.Atoi(os.Getenv(k)); err == nil {
		return v
	}
	return def
}

func getBool(k string, def bool) bool {
	if v, err := strconv.ParseBool(os.Getenv(k)); err == nil {
		return v
	}
	return def
}

func list(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func lower(in []string) []string {
	for i := range in {
		in[i] = strings.ToLower(in[i])
	}
	return in
}

func hostname() string {
	h, _ := os.Hostname()
	if h == "" {
		h = "worker"
	}
	return h + "-" + strconv.Itoa(os.Getpid())
}
