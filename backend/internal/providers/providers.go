// Package providers manages email_providers rows: CMS configuration, secret
// resolution, health state and the shared circuit breaker.
package providers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/mail"
	"os"
	"regexp"
	"slices"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"golang.org/x/time/rate"

	"github.com/cadentic/portfolio-cms/backend/internal/email"
	"github.com/cadentic/portfolio-cms/backend/internal/store"
)

type Provider struct {
	ID                  string            `json:"id"`
	Name                string            `json:"name"`
	Kind                string            `json:"kind"`
	Enabled             bool              `json:"enabled"`
	Priority            int               `json:"priority"`
	Streams             []string          `json:"streams"`
	CredentialRefs      map[string]string `json:"credential_refs"`
	WebhookSecretRef    string            `json:"webhook_secret_ref"`
	SenderEmail         string            `json:"sender_email"`
	SenderName          string            `json:"sender_name"`
	ReplyTo             string            `json:"reply_to"`
	SendingDomain       string            `json:"sending_domain"`
	MaxAttempts         int               `json:"max_attempts"`
	RateLimitPerSecond  float64           `json:"rate_limit_per_second"`
	Settings            map[string]any    `json:"settings"`
	CircuitState        string            `json:"circuit_state"`
	CircuitOpenedAt     *time.Time        `json:"circuit_opened_at"`
	ConsecutiveFailures int               `json:"consecutive_failures"`
	LastSuccessAt       *time.Time        `json:"last_success_at"`
	LastFailureAt       *time.Time        `json:"last_failure_at"`
	LastError           string            `json:"last_error"`
	LastRateLimitedAt   *time.Time        `json:"last_rate_limited_at"`
	LastHealthCheckAt   *time.Time        `json:"last_health_check_at"`
	LastHealthOK        *bool             `json:"last_health_ok"`
	LastHealthMessage   string            `json:"last_health_message"`
	LastTestAt          *time.Time        `json:"last_test_at"`
	LastTestOK          *bool             `json:"last_test_ok"`
	QuotaInfo           map[string]any    `json:"quota_info"`
	CreatedAt           time.Time         `json:"created_at"`
	UpdatedAt           time.Time         `json:"updated_at"`

	// Derived, never the secret values themselves.
	CredentialsConfigured   bool `json:"credentials_configured"`
	WebhookSecretConfigured bool `json:"webhook_secret_configured"`
}

func (p *Provider) Handles(stream string) bool { return slices.Contains(p.Streams, stream) }

// SecretResolver resolves a secret reference to its value. The default
// implementation reads environment variables, which Terraform/Ansible
// populate from secret.tfvars or the cloud secret manager.
type SecretResolver interface {
	Resolve(ref string) (string, bool)
}

// Only references with this shape may be resolved, so a CMS user cannot point a
// provider at unrelated process secrets such as DATABASE_URL or APP_SECRET.
var refPattern = regexp.MustCompile(`^EMAIL_[A-Z0-9_]{2,64}$`)

func ValidRef(ref string) bool { return refPattern.MatchString(ref) }

type EnvSecrets struct{}

func (EnvSecrets) Resolve(ref string) (string, bool) {
	if !ValidRef(ref) {
		return "", false
	}
	v, ok := os.LookupEnv(ref)
	return v, ok && v != ""
}

// MapSecrets is used by tests.
type MapSecrets map[string]string

func (m MapSecrets) Resolve(ref string) (string, bool) {
	v, ok := m[ref]
	return v, ok && v != ""
}

type Repo struct {
	DB       store.DB
	Secrets  SecretResolver
	Registry *email.Registry
	BaseURLs map[string]string // kind -> override (local mock/tests only)

	mu       sync.Mutex
	limiters map[string]*rate.Limiter
}

const cols = `id, name, kind, enabled, priority, streams, credential_refs, coalesce(webhook_secret_ref,''),
	coalesce(sender_email,''), coalesce(sender_name,''), coalesce(reply_to,''), coalesce(sending_domain,''),
	max_attempts, rate_limit_per_second::float8, settings, circuit_state, circuit_opened_at, consecutive_failures,
	last_success_at, last_failure_at, coalesce(last_error,''), last_rate_limited_at, last_health_check_at,
	last_health_ok, coalesce(last_health_message,''), last_test_at, last_test_ok, quota_info, created_at, updated_at`

func (r *Repo) scan(row pgx.Row) (*Provider, error) {
	var p Provider
	var refs, settings, quota []byte
	err := row.Scan(&p.ID, &p.Name, &p.Kind, &p.Enabled, &p.Priority, &p.Streams, &refs, &p.WebhookSecretRef,
		&p.SenderEmail, &p.SenderName, &p.ReplyTo, &p.SendingDomain, &p.MaxAttempts, &p.RateLimitPerSecond,
		&settings, &p.CircuitState, &p.CircuitOpenedAt, &p.ConsecutiveFailures, &p.LastSuccessAt, &p.LastFailureAt,
		&p.LastError, &p.LastRateLimitedAt, &p.LastHealthCheckAt, &p.LastHealthOK, &p.LastHealthMessage,
		&p.LastTestAt, &p.LastTestOK, &quota, &p.CreatedAt, &p.UpdatedAt)
	if err != nil {
		return nil, err
	}
	_ = json.Unmarshal(refs, &p.CredentialRefs)
	_ = json.Unmarshal(settings, &p.Settings)
	if len(quota) > 0 {
		_ = json.Unmarshal(quota, &p.QuotaInfo)
	}
	if p.CredentialRefs == nil {
		p.CredentialRefs = map[string]string{}
	}
	if p.Settings == nil {
		p.Settings = map[string]any{}
	}
	p.CredentialsConfigured = r.credentialsConfigured(&p)
	if p.WebhookSecretRef != "" {
		_, p.WebhookSecretConfigured = r.Secrets.Resolve(p.WebhookSecretRef)
	}
	return &p, nil
}

func (r *Repo) credentialsConfigured(p *Provider) bool {
	a, ok := r.Registry.Get(p.Kind)
	if !ok {
		return false
	}
	for _, k := range a.RequiredCredentials() {
		if _, ok := r.Secrets.Resolve(p.CredentialRefs[k]); !ok {
			return false
		}
	}
	return true
}

// List returns all providers ordered by priority (lowest number = highest priority).
func (r *Repo) List(ctx context.Context) ([]*Provider, error) {
	rows, err := r.DB.Query(ctx, `SELECT `+cols+` FROM email_providers ORDER BY priority, created_at`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Provider
	for rows.Next() {
		p, err := r.scan(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// Chain returns the enabled providers able to send the given stream, in priority order.
func (r *Repo) Chain(ctx context.Context, stream string) ([]*Provider, error) {
	all, err := r.List(ctx)
	if err != nil {
		return nil, err
	}
	var out []*Provider
	for _, p := range all {
		if p.Enabled && p.CredentialsConfigured && p.Handles(stream) {
			if _, ok := r.Registry.Get(p.Kind); ok {
				out = append(out, p)
			}
		}
	}
	return out, nil
}

var ErrNotFound = errors.New("provider not found")

func (r *Repo) Get(ctx context.Context, id string) (*Provider, error) {
	p, err := r.scan(r.DB.QueryRow(ctx, `SELECT `+cols+` FROM email_providers WHERE id=$1`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	return p, err
}

// RuntimeConfig resolves secrets for one send/health/webhook operation.
func (r *Repo) RuntimeConfig(p *Provider) email.ProviderConfig {
	creds := map[string]string{}
	for k, ref := range p.CredentialRefs {
		if v, ok := r.Secrets.Resolve(ref); ok {
			creds[k] = v
		}
	}
	var whSecret string
	if p.WebhookSecretRef != "" {
		whSecret, _ = r.Secrets.Resolve(p.WebhookSecretRef)
	}
	return email.ProviderConfig{
		ID: p.ID, Name: p.Name, Kind: p.Kind,
		Credentials: creds, WebhookSecret: whSecret,
		BaseURL: r.BaseURLs[p.Kind], Settings: p.Settings,
	}
}

// Input is the CMS-editable subset of a provider.
type Input struct {
	Name               *string           `json:"name"`
	Kind               *string           `json:"kind"`
	Enabled            *bool             `json:"enabled"`
	Priority           *int              `json:"priority"`
	Streams            []string          `json:"streams"`
	CredentialRefs     map[string]string `json:"credential_refs"`
	WebhookSecretRef   *string           `json:"webhook_secret_ref"`
	SenderEmail        *string           `json:"sender_email"`
	SenderName         *string           `json:"sender_name"`
	ReplyTo            *string           `json:"reply_to"`
	SendingDomain      *string           `json:"sending_domain"`
	MaxAttempts        *int              `json:"max_attempts"`
	RateLimitPerSecond *float64          `json:"rate_limit_per_second"`
	Settings           map[string]any    `json:"settings"`
}

func (r *Repo) validate(in *Input, kind string) error {
	if _, ok := r.Registry.Get(kind); !ok {
		return fmt.Errorf("unsupported provider kind %q", kind)
	}
	for k, ref := range in.CredentialRefs {
		if ref != "" && !ValidRef(ref) {
			return fmt.Errorf("credential ref for %s must be an environment secret name like EMAIL_BREVO_API_KEY", k)
		}
	}
	if in.WebhookSecretRef != nil && *in.WebhookSecretRef != "" && !ValidRef(*in.WebhookSecretRef) {
		return errors.New("webhook secret ref must look like EMAIL_BREVO_WEBHOOK_SECRET")
	}
	for _, s := range in.Streams {
		if s != "transactional" && s != "marketing" {
			return fmt.Errorf("invalid stream %q", s)
		}
	}
	for _, e := range []*string{in.SenderEmail, in.ReplyTo} {
		if e != nil && *e != "" {
			if _, err := mail.ParseAddress(*e); err != nil {
				return fmt.Errorf("invalid email address %q", *e)
			}
		}
	}
	if in.MaxAttempts != nil && (*in.MaxAttempts < 1 || *in.MaxAttempts > 10) {
		return errors.New("max_attempts must be 1-10")
	}
	if in.RateLimitPerSecond != nil && (*in.RateLimitPerSecond <= 0 || *in.RateLimitPerSecond > 1000) {
		return errors.New("rate_limit_per_second must be between 0 and 1000")
	}
	// base URLs are deliberately not editable: credentials may only ever be
	// sent to the provider's official API host.
	delete(in.Settings, "base_url")
	return nil
}

func (r *Repo) Create(ctx context.Context, in Input) (*Provider, error) {
	if in.Name == nil || in.Kind == nil || *in.Name == "" {
		return nil, errors.New("name and kind are required")
	}
	if err := r.validate(&in, *in.Kind); err != nil {
		return nil, err
	}
	refs, _ := json.Marshal(orEmpty(in.CredentialRefs))
	settings, _ := json.Marshal(orEmptyAny(in.Settings))
	streams := in.Streams
	if len(streams) == 0 {
		streams = []string{"transactional", "marketing"}
	}
	var id string
	err := r.DB.QueryRow(ctx, `INSERT INTO email_providers
		(name, kind, enabled, priority, streams, credential_refs, webhook_secret_ref, sender_email, sender_name,
		 reply_to, sending_domain, max_attempts, rate_limit_per_second, settings)
		VALUES ($1,$2,$3,COALESCE($4,(SELECT COALESCE(max(priority),0)+10 FROM email_providers)),$5,$6,$7,$8,$9,$10,$11,
		        COALESCE($12,3),COALESCE($13,5),$14) RETURNING id`,
		*in.Name, *in.Kind, deref(in.Enabled), in.Priority, streams, refs, in.WebhookSecretRef, in.SenderEmail,
		in.SenderName, in.ReplyTo, in.SendingDomain, in.MaxAttempts, in.RateLimitPerSecond, settings).Scan(&id)
	if err != nil {
		if store.IsUniqueViolation(err) {
			return nil, errors.New("a provider with this name already exists")
		}
		return nil, err
	}
	return r.Get(ctx, id)
}

func (r *Repo) Update(ctx context.Context, id string, in Input) (*Provider, error) {
	cur, err := r.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	kind := cur.Kind
	if in.Kind != nil {
		kind = *in.Kind
	}
	if err := r.validate(&in, kind); err != nil {
		return nil, err
	}
	var refs, settings []byte
	if in.CredentialRefs != nil {
		refs, _ = json.Marshal(in.CredentialRefs)
	}
	if in.Settings != nil {
		settings, _ = json.Marshal(in.Settings)
	}
	var streams []string
	if in.Streams != nil {
		streams = in.Streams
	}
	_, err = r.DB.Exec(ctx, `UPDATE email_providers SET
		name=COALESCE($2,name), kind=COALESCE($3,kind), enabled=COALESCE($4,enabled), priority=COALESCE($5,priority),
		streams=COALESCE($6,streams), credential_refs=COALESCE($7,credential_refs),
		webhook_secret_ref=COALESCE($8,webhook_secret_ref), sender_email=COALESCE($9,sender_email),
		sender_name=COALESCE($10,sender_name), reply_to=COALESCE($11,reply_to), sending_domain=COALESCE($12,sending_domain),
		max_attempts=COALESCE($13,max_attempts), rate_limit_per_second=COALESCE($14,rate_limit_per_second),
		settings=COALESCE($15,settings), updated_at=now()
		WHERE id=$1`,
		id, in.Name, in.Kind, in.Enabled, in.Priority, streams, refs, in.WebhookSecretRef, in.SenderEmail,
		in.SenderName, in.ReplyTo, in.SendingDomain, in.MaxAttempts, in.RateLimitPerSecond, settings)
	if err != nil {
		return nil, err
	}
	return r.Get(ctx, id)
}

// Reorder assigns priorities 10, 20, 30... in the given order.
func (r *Repo) Reorder(ctx context.Context, ids []string) error {
	for i, id := range ids {
		if _, err := r.DB.Exec(ctx, `UPDATE email_providers SET priority=$2, updated_at=now() WHERE id=$1`, id, (i+1)*10); err != nil {
			return err
		}
	}
	return nil
}

func (r *Repo) Delete(ctx context.Context, id string) error {
	_, err := r.DB.Exec(ctx, `DELETE FROM email_providers WHERE id=$1`, id)
	return err
}

// --- circuit breaker -------------------------------------------------------

// Acquire decides whether the circuit lets a request through. When the
// cool-down has elapsed exactly one caller moves it to half_open and becomes
// the probe; everyone else keeps skipping the provider.
func (r *Repo) Acquire(ctx context.Context, p *Provider, cooldown time.Duration) (bool, error) {
	if p.CircuitState == "closed" {
		return true, nil
	}
	tag, err := r.DB.Exec(ctx, `UPDATE email_providers SET circuit_state='half_open', circuit_opened_at=now()
		WHERE id=$1 AND circuit_state IN ('open','half_open') AND circuit_opened_at < now() - make_interval(secs => $2)`,
		p.ID, cooldown.Seconds())
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() == 1, nil
}

func (r *Repo) RecordSuccess(ctx context.Context, id string) error {
	_, err := r.DB.Exec(ctx, `UPDATE email_providers SET consecutive_failures=0, circuit_state='closed',
		circuit_opened_at=NULL, last_success_at=now() WHERE id=$1`, id)
	return err
}

// RecordFailure counts a provider-level failure and returns true when this
// call opened the circuit.
func (r *Repo) RecordFailure(ctx context.Context, id, msg string, threshold int) (bool, error) {
	var opened bool
	err := r.DB.QueryRow(ctx, `WITH old AS (SELECT circuit_state FROM email_providers WHERE id=$1 FOR UPDATE)
		UPDATE email_providers p SET
		  consecutive_failures = p.consecutive_failures + 1,
		  last_failure_at = now(), last_error = $2,
		  circuit_state = CASE WHEN p.circuit_state = 'half_open' OR p.consecutive_failures + 1 >= $3 THEN 'open' ELSE p.circuit_state END,
		  circuit_opened_at = CASE WHEN p.circuit_state = 'half_open' OR p.consecutive_failures + 1 >= $3 THEN now() ELSE p.circuit_opened_at END
		FROM old WHERE p.id=$1
		RETURNING (old.circuit_state <> 'open' AND p.circuit_state = 'open')`, id, email.RedactString(msg), threshold).Scan(&opened)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	return opened, err
}

func (r *Repo) RecordRateLimited(ctx context.Context, id string) error {
	_, err := r.DB.Exec(ctx, `UPDATE email_providers SET last_rate_limited_at=now() WHERE id=$1`, id)
	return err
}

func (r *Repo) RecordQuota(ctx context.Context, id string, quota map[string]any) error {
	b, _ := json.Marshal(quota)
	_, err := r.DB.Exec(ctx, `UPDATE email_providers SET quota_info=$2 WHERE id=$1`, id, b)
	return err
}

func (r *Repo) RecordHealth(ctx context.Context, id string, h email.HealthResult) error {
	var quota []byte
	if h.Quota != nil {
		quota, _ = json.Marshal(h.Quota)
	}
	_, err := r.DB.Exec(ctx, `UPDATE email_providers SET last_health_check_at=now(), last_health_ok=$2,
		last_health_message=$3, quota_info=COALESCE($4, quota_info) WHERE id=$1`, id, h.OK, email.RedactString(h.Message), quota)
	return err
}

func (r *Repo) RecordTest(ctx context.Context, id string, ok bool) error {
	_, err := r.DB.Exec(ctx, `UPDATE email_providers SET last_test_at=now(), last_test_ok=$2 WHERE id=$1`, id, ok)
	return err
}

// --- per-provider rate limit (per process) ---------------------------------

// Allow applies the provider's configured send rate. With N worker instances
// set rate_limit_per_second to (provider limit / N).
func (r *Repo) Allow(p *Provider) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.limiters == nil {
		r.limiters = map[string]*rate.Limiter{}
	}
	l, ok := r.limiters[p.ID]
	burst := int(p.RateLimitPerSecond)
	if burst < 1 {
		burst = 1
	}
	if !ok || l.Limit() != rate.Limit(p.RateLimitPerSecond) {
		l = rate.NewLimiter(rate.Limit(p.RateLimitPerSecond), burst)
		r.limiters[p.ID] = l
	}
	return l.Allow()
}

func deref(b *bool) bool { return b != nil && *b }

func orEmpty(m map[string]string) map[string]string {
	if m == nil {
		return map[string]string{}
	}
	return m
}

func orEmptyAny(m map[string]any) map[string]any {
	if m == nil {
		return map[string]any{}
	}
	return m
}
