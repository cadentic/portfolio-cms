// Package settings stores owner-editable configuration in app_settings so the
// CMS can change behaviour (sender identity, retry policy, concurrency, ...)
// without a redeploy.
package settings

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/mail"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/cadentic/portfolio-cms/backend/internal/store"
)

// Email holds sender identity, compliance and newsletter behaviour.
type Email struct {
	SenderName       string   `json:"sender_name"`
	SenderEmail      string   `json:"sender_email"`
	ReplyTo          string   `json:"reply_to"`
	OrganizationName string   `json:"organization_name"`
	PostalAddress    string   `json:"postal_address"` // legally required sender identity in marketing mail
	PrivacyPolicyURL string   `json:"privacy_policy_url"`
	SendingDomains   []string `json:"sending_domains"`

	DoubleOptIn              bool     `json:"double_opt_in"`
	VerificationTTLHours     int      `json:"verification_ttl_hours"`
	ResendCooldownMinutes    int      `json:"resend_cooldown_minutes"`
	MaxVerificationSends     int      `json:"max_verification_sends"`
	ConsentText              string   `json:"consent_text"`
	PrivacyPolicyVersion     string   `json:"privacy_policy_version"`
	Topics                   []string `json:"topics"`
	TrackOpens               bool     `json:"track_opens"`
	TrackClicks              bool     `json:"track_clicks"`
	TestRecipients           []string `json:"test_recipients"`
	AutoNotifyOnPublish      bool     `json:"auto_notify_on_publish"`
	DuplicateSendWindowHours int      `json:"duplicate_send_window_hours"`
	PendingRetentionDays     int      `json:"pending_retention_days"`
	JobBodyRetentionDays     int      `json:"job_body_retention_days"`

	Brand Brand `json:"brand"`
}

type Brand struct {
	LogoText    string `json:"logo_text"`
	LogoURL     string `json:"logo_url"`
	AccentColor string `json:"accent_color"`
	InkColor    string `json:"ink_color"`
	DarkColor   string `json:"dark_color"`
	FontFamily  string `json:"font_family"`
	HeaderHTML  string `json:"header_html"`
	FooterHTML  string `json:"footer_html"`
}

// Delivery holds the queue/failover policy.
type Delivery struct {
	ProviderRetryBaseSeconds int  `json:"provider_retry_base_seconds"`
	ProviderRetryMaxSeconds  int  `json:"provider_retry_max_seconds"`
	MaxCycles                int  `json:"max_cycles"`
	CycleBackoffBaseSeconds  int  `json:"cycle_backoff_base_seconds"`
	CycleBackoffMaxSeconds   int  `json:"cycle_backoff_max_seconds"`
	CircuitFailureThreshold  int  `json:"circuit_failure_threshold"`
	CircuitCooldownSeconds   int  `json:"circuit_cooldown_seconds"`
	WorkerConcurrency        int  `json:"worker_concurrency"`
	LeaseSeconds             int  `json:"lease_seconds"`
	SendTimeoutSeconds       int  `json:"send_timeout_seconds"`
	UnknownReconcileSeconds  int  `json:"unknown_reconcile_seconds"`
	UnknownMaxReconciles     int  `json:"unknown_max_reconciles"`
	UnknownAutoResend        bool `json:"unknown_auto_resend"`
	UnknownResendGraceMins   int  `json:"unknown_resend_grace_minutes"`
	OutageAlertMinutes       int  `json:"outage_alert_minutes"`
	CampaignBatchSize        int  `json:"campaign_batch_size"`
}

func DefaultEmail() Email {
	return Email{
		SenderName:               "Sayantan Chakraborty",
		SenderEmail:              os.Getenv("EMAIL_FROM_ADDRESS"),
		OrganizationName:         "Sayantan Chakraborty",
		PrivacyPolicyURL:         "/privacy",
		DoubleOptIn:              true,
		VerificationTTLHours:     48,
		ResendCooldownMinutes:    5,
		MaxVerificationSends:     5,
		ConsentText:              "I agree to receive the newsletter (new articles, videos and professional updates) by email. I can unsubscribe at any time.",
		PrivacyPolicyVersion:     "2026-10",
		Topics:                   []string{"articles", "videos", "career"},
		DuplicateSendWindowHours: 24,
		PendingRetentionDays:     30,
		JobBodyRetentionDays:     90,
		Brand: Brand{
			LogoText:    "SC",
			AccentColor: "#8ef0c4",
			InkColor:    "#111923",
			DarkColor:   "#10151d",
			FontFamily:  "Inter, -apple-system, 'Segoe UI', Roboto, Arial, sans-serif",
		},
	}
}

// Site holds public-site branding applied to every page's meta tags and image
// alt text.
type Site struct {
	// BrandSuffix ends every title, description, Open Graph/Twitter tag and
	// image alt text on the public site. Empty turns the suffix off.
	BrandSuffix string `json:"brand_suffix"`
}

const DefaultBrandSuffix = "Sayantan Chakraborty, Oracle DBA"

func DefaultSite() Site { return Site{BrandSuffix: DefaultBrandSuffix} }

func (si *Site) Validate() error {
	si.BrandSuffix = strings.Join(strings.Fields(si.BrandSuffix), " ")
	if len([]rune(si.BrandSuffix)) > 80 {
		return errors.New("brand_suffix must be at most 80 characters")
	}
	if strings.ContainsAny(si.BrandSuffix, "<>\"") {
		return errors.New("brand_suffix cannot contain <, > or \"")
	}
	return nil
}

func DefaultDelivery() Delivery {
	return Delivery{
		ProviderRetryBaseSeconds: 2,
		ProviderRetryMaxSeconds:  30,
		MaxCycles:                6,
		CycleBackoffBaseSeconds:  60,
		CycleBackoffMaxSeconds:   3600,
		CircuitFailureThreshold:  5,
		CircuitCooldownSeconds:   60,
		WorkerConcurrency:        4,
		LeaseSeconds:             120,
		SendTimeoutSeconds:       20,
		UnknownReconcileSeconds:  120,
		UnknownMaxReconciles:     10,
		UnknownAutoResend:        true,
		UnknownResendGraceMins:   20,
		OutageAlertMinutes:       10,
		CampaignBatchSize:        500,
	}
}

func (e *Email) Validate() error {
	var errs []string
	if e.SenderEmail != "" {
		if _, err := mail.ParseAddress(e.SenderEmail); err != nil {
			errs = append(errs, "sender_email is invalid")
		}
	}
	if e.ReplyTo != "" {
		if _, err := mail.ParseAddress(e.ReplyTo); err != nil {
			errs = append(errs, "reply_to is invalid")
		}
	}
	for _, r := range e.TestRecipients {
		if _, err := mail.ParseAddress(r); err != nil {
			errs = append(errs, "test recipient "+r+" is invalid")
		}
	}
	if e.VerificationTTLHours < 1 || e.VerificationTTLHours > 168 {
		errs = append(errs, "verification_ttl_hours must be 1-168")
	}
	if e.MaxVerificationSends < 1 {
		e.MaxVerificationSends = 1
	}
	if len(errs) > 0 {
		return errors.New(strings.Join(errs, "; "))
	}
	return nil
}

func (d *Delivery) Validate() error {
	clamp := func(v *int, lo, hi int) {
		if *v < lo {
			*v = lo
		}
		if *v > hi {
			*v = hi
		}
	}
	clamp(&d.ProviderRetryBaseSeconds, 1, 300)
	clamp(&d.ProviderRetryMaxSeconds, d.ProviderRetryBaseSeconds, 3600)
	clamp(&d.MaxCycles, 1, 50)
	clamp(&d.CycleBackoffBaseSeconds, 5, 3600)
	clamp(&d.CycleBackoffMaxSeconds, d.CycleBackoffBaseSeconds, 86400)
	clamp(&d.CircuitFailureThreshold, 1, 100)
	clamp(&d.CircuitCooldownSeconds, 5, 3600)
	clamp(&d.WorkerConcurrency, 1, 64)
	clamp(&d.LeaseSeconds, 30, 3600)
	clamp(&d.SendTimeoutSeconds, 2, 120)
	if d.LeaseSeconds <= d.SendTimeoutSeconds*2 {
		d.LeaseSeconds = d.SendTimeoutSeconds*2 + 10
	}
	clamp(&d.UnknownReconcileSeconds, 10, 3600)
	clamp(&d.UnknownMaxReconciles, 1, 100)
	clamp(&d.UnknownResendGraceMins, 1, 1440)
	clamp(&d.OutageAlertMinutes, 1, 1440)
	clamp(&d.CampaignBatchSize, 10, 5000)
	return nil
}

// Store reads and writes settings, with a short cache for hot paths.
type Store struct {
	db  store.DB
	ttl time.Duration

	mu       sync.Mutex
	email    *Email
	delivery *Delivery
	site     *Site
	loadedAt time.Time
}

func NewStore(db store.DB, ttl time.Duration) *Store { return &Store{db: db, ttl: ttl} }

func (s *Store) load(ctx context.Context, key string, dst any) error {
	var raw []byte
	err := s.db.QueryRow(ctx, `SELECT value FROM app_settings WHERE key=$1`, key).Scan(&raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	// Unmarshal over defaults so new fields keep their default values.
	return json.Unmarshal(raw, dst)
}

func (s *Store) refresh(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.email != nil && time.Since(s.loadedAt) < s.ttl {
		return nil
	}
	e, d, si := DefaultEmail(), DefaultDelivery(), DefaultSite()
	if err := s.load(ctx, "email", &e); err != nil {
		return err
	}
	if err := s.load(ctx, "delivery", &d); err != nil {
		return err
	}
	if err := s.load(ctx, "site", &si); err != nil {
		return err
	}
	_ = d.Validate()
	if si.Validate() != nil {
		si = DefaultSite()
	}
	s.email, s.delivery, s.site, s.loadedAt = &e, &d, &si, time.Now()
	return nil
}

func (s *Store) Email(ctx context.Context) (Email, error) {
	if err := s.refresh(ctx); err != nil {
		return DefaultEmail(), err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return *s.email, nil
}

func (s *Store) Delivery(ctx context.Context) (Delivery, error) {
	if err := s.refresh(ctx); err != nil {
		return DefaultDelivery(), err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return *s.delivery, nil
}

func (s *Store) Site(ctx context.Context) (Site, error) {
	if err := s.refresh(ctx); err != nil {
		return DefaultSite(), err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return *s.site, nil
}

func (s *Store) Invalidate() {
	s.mu.Lock()
	s.email = nil
	s.mu.Unlock()
}

func (s *Store) save(ctx context.Context, key string, v any, actor string) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	_, err = s.db.Exec(ctx, `INSERT INTO app_settings (key, value, updated_by) VALUES ($1,$2,$3)
		ON CONFLICT (key) DO UPDATE SET value=EXCLUDED.value, updated_at=now(), updated_by=EXCLUDED.updated_by`, key, b, actor)
	s.Invalidate()
	return err
}

func (s *Store) SaveEmail(ctx context.Context, e Email, actor string) error {
	if err := e.Validate(); err != nil {
		return fmt.Errorf("invalid email settings: %w", err)
	}
	return s.save(ctx, "email", e, actor)
}

func (s *Store) SaveDelivery(ctx context.Context, d Delivery, actor string) error {
	_ = d.Validate()
	return s.save(ctx, "delivery", d, actor)
}

func (s *Store) SaveSite(ctx context.Context, si Site, actor string) error {
	if err := si.Validate(); err != nil {
		return fmt.Errorf("invalid site settings: %w", err)
	}
	return s.save(ctx, "site", si, actor)
}
