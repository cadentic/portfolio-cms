// Package email defines the provider-independent email model and the adapter
// contract every transactional email provider (Brevo, Mailjet, SMTP, ...) implements.
//
// Newsletter/application code never talks to an adapter directly: it enqueues
// a job (package queue) and the worker chooses providers by priority, health
// and circuit-breaker state.
package email

import (
	"context"
	"errors"
	"net/http"
	"time"
)

type Category string

const (
	CategoryTransactional Category = "transactional"
	CategoryMarketing     Category = "marketing"
)

type Address struct {
	Email string `json:"email"`
	Name  string `json:"name,omitempty"`
}

// Message is a fully rendered email ready for submission.
type Message struct {
	InternalID string // email_jobs.id — also used as the provider idempotency key / custom ID
	Category   Category
	From       Address
	To         Address
	ReplyTo    *Address
	Subject    string
	HTML       string
	Text       string
	Headers    map[string]string
	Tags       []string
}

// Outcome is the adapter's classification of a single submission attempt.
// The distinction between "not sent", "rejected" and "acceptance unknown" is
// what makes failover safe: only outcomes where the provider definitely did
// not accept the message allow another provider to be tried.
type Outcome string

const (
	OutcomeAccepted          Outcome = "accepted"           // provider confirmed acceptance (NOT delivery)
	OutcomeRejectedTransient Outcome = "rejected_transient" // explicit refusal, retry later may work (5xx, 408)
	OutcomeRejectedPermanent Outcome = "rejected_permanent" // explicit refusal, retrying same request won't help (4xx)
	OutcomeRateLimited       Outcome = "rate_limited"       // 429
	OutcomeNotSent           Outcome = "not_sent"           // request never reached the provider (DNS/dial/TLS failure)
	OutcomeUnknown           Outcome = "acceptance_unknown" // request was written but no definitive answer arrived
)

// SafeToFailover reports whether another provider may be tried without risking
// a duplicate email.
func (o Outcome) SafeToFailover() bool {
	switch o {
	case OutcomeRejectedTransient, OutcomeRejectedPermanent, OutcomeRateLimited, OutcomeNotSent:
		return true
	}
	return false
}

type SendResult struct {
	Outcome           Outcome
	ProviderMessageID string
	HTTPStatus        int
	ErrorCode         string
	ErrorMessage      string
	RetryAfter        time.Duration
	QuotaExhausted    bool
	Response          map[string]any // redacted provider response
}

type HealthResult struct {
	OK      bool
	Message string
	Quota   map[string]any
}

// LookupStatus is the result of asking a provider whether it has a record of
// a message whose acceptance is unknown.
type LookupStatus string

const (
	LookupFound       LookupStatus = "found"
	LookupNotFound    LookupStatus = "not_found"
	LookupUnsupported LookupStatus = "unsupported"
)

type LookupResult struct {
	Status            LookupStatus
	ProviderMessageID string
}

// Event is a provider webhook event mapped into the common model.
type Event struct {
	DedupeKey         string
	ProviderEventID   string
	ProviderMessageID string
	InternalID        string // our job id when the provider echoes it (tag / CustomID)
	Email             string
	Type              EventType
	RawType           string
	OccurredAt        time.Time
	Payload           map[string]any
}

type EventType string

const (
	EventAccepted     EventType = "accepted"
	EventDelivered    EventType = "delivered"
	EventDeferred     EventType = "deferred"
	EventSoftBounce   EventType = "soft_bounce"
	EventHardBounce   EventType = "hard_bounce"
	EventComplaint    EventType = "complaint"
	EventUnsubscribed EventType = "unsubscribed"
	EventOpened       EventType = "opened"
	EventClicked      EventType = "clicked"
	EventBlocked      EventType = "blocked"
	EventInvalid      EventType = "invalid"
	EventError        EventType = "error"
	EventUnknown      EventType = "unknown"
)

// ProviderConfig is the resolved runtime configuration for one provider row.
// Credentials are resolved from server-side secrets at use time and never
// serialised to API responses.
type ProviderConfig struct {
	ID            string
	Name          string
	Kind          string
	Credentials   map[string]string
	WebhookSecret string
	BaseURL       string // non-empty only for local mock / tests
	Settings      map[string]any
}

var ErrWebhookUnauthorized = errors.New("webhook authentication failed")

// Adapter is implemented once per provider kind.
type Adapter interface {
	Kind() string
	// RequiredCredentials lists the credential_refs keys the adapter needs.
	RequiredCredentials() []string
	// SupportsIdempotency reports whether the provider deduplicates retries
	// carrying the same idempotency key, and for how long.
	IdempotencyWindow() time.Duration
	Send(ctx context.Context, cfg ProviderConfig, msg Message) SendResult
	HealthCheck(ctx context.Context, cfg ProviderConfig) HealthResult
	Lookup(ctx context.Context, cfg ProviderConfig, internalID, toEmail string, since time.Time) (LookupResult, error)
	// VerifyWebhook authenticates an incoming webhook request.
	VerifyWebhook(r *http.Request, cfg ProviderConfig) error
	ParseWebhook(body []byte) ([]Event, error)
}

// Registry maps provider kinds to adapters, so new providers can be added
// without touching queue or newsletter code.
type Registry struct{ adapters map[string]Adapter }

func NewRegistry(adapters ...Adapter) *Registry {
	r := &Registry{adapters: map[string]Adapter{}}
	for _, a := range adapters {
		r.adapters[a.Kind()] = a
	}
	return r
}

func (r *Registry) Get(kind string) (Adapter, bool) {
	a, ok := r.adapters[kind]
	return a, ok
}

func (r *Registry) Kinds() []string {
	out := make([]string, 0, len(r.adapters))
	for k := range r.adapters {
		out = append(out, k)
	}
	return out
}
