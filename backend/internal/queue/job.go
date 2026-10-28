// Package queue is the durable PostgreSQL-backed email queue: enqueueing,
// the background worker with provider failover, reconciliation of
// acceptance-unknown submissions and webhook event application.
package queue

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/cadentic/portfolio-cms/backend/internal/email"
	"github.com/cadentic/portfolio-cms/backend/internal/store"
)

// Job states.
const (
	StatusQueued            = "queued"
	StatusProcessing        = "processing"
	StatusAccepted          = "accepted"
	StatusDelivered         = "delivered"
	StatusDeferred          = "deferred"
	StatusAcceptanceUnknown = "acceptance_unknown"
	StatusRetryScheduled    = "retry_scheduled"
	StatusFailed            = "failed"
	StatusBounced           = "bounced"
	StatusComplained        = "complained"
	StatusCancelled         = "cancelled"
	StatusDeadLetter        = "dead_letter"
)

// Post-acceptance status precedence. Webhook events only move a job forward,
// so a late "deferred" never overwrites "delivered".
var statusRank = map[string]int{
	StatusAccepted:   10,
	StatusDeferred:   20,
	StatusDelivered:  30,
	StatusBounced:    40,
	StatusComplained: 50,
}

type Job struct {
	ID                 string            `json:"id"`
	IdempotencyKey     string            `json:"idempotency_key"`
	Category           email.Category    `json:"category"`
	Kind               string            `json:"kind"`
	Status             string            `json:"status"`
	Priority           int               `json:"priority"`
	CampaignID         *string           `json:"campaign_id"`
	SubscriberID       *string           `json:"subscriber_id"`
	ToEmail            string            `json:"to_email"`
	ToName             string            `json:"to_name"`
	FromEmail          string            `json:"from_email"`
	FromName           string            `json:"from_name"`
	ReplyTo            string            `json:"reply_to"`
	Subject            string            `json:"subject"`
	HTMLBody           string            `json:"-"`
	TextBody           string            `json:"-"`
	Headers            map[string]string `json:"headers"`
	Tags               []string          `json:"tags"`
	AttemptCount       int               `json:"attempt_count"`
	Cycle              int               `json:"cycle"`
	FailoverCount      int               `json:"failover_count"`
	CurrentProviderID  *string           `json:"current_provider_id"`
	AcceptedProviderID *string           `json:"accepted_provider_id"`
	ProviderMessageID  string            `json:"provider_message_id"`
	NextAttemptAt      time.Time         `json:"next_attempt_at"`
	LastError          string            `json:"last_error"`
	UnknownSince       *time.Time        `json:"unknown_since"`
	ReconcileAttempts  int               `json:"reconcile_attempts"`
	StatusRank         int               `json:"status_rank"`
	AcceptedAt         *time.Time        `json:"accepted_at"`
	DeliveredAt        *time.Time        `json:"delivered_at"`
	FinishedAt         *time.Time        `json:"finished_at"`
	CreatedAt          time.Time         `json:"created_at"`
	UpdatedAt          time.Time         `json:"updated_at"`
}

const jobCols = `id, idempotency_key, category, kind, status, priority, campaign_id, subscriber_id, to_email,
	coalesce(to_name,''), coalesce(from_email,''), coalesce(from_name,''), coalesce(reply_to,''), subject, html_body,
	text_body, headers, tags, attempt_count, cycle, failover_count, current_provider_id, accepted_provider_id,
	coalesce(provider_message_id,''), next_attempt_at, coalesce(last_error,''), unknown_since, reconcile_attempts,
	status_rank, accepted_at, delivered_at, finished_at, created_at, updated_at`

func scanJob(row pgx.Row) (*Job, error) {
	var j Job
	var headers []byte
	err := row.Scan(&j.ID, &j.IdempotencyKey, &j.Category, &j.Kind, &j.Status, &j.Priority, &j.CampaignID,
		&j.SubscriberID, &j.ToEmail, &j.ToName, &j.FromEmail, &j.FromName, &j.ReplyTo, &j.Subject, &j.HTMLBody,
		&j.TextBody, &headers, &j.Tags, &j.AttemptCount, &j.Cycle, &j.FailoverCount, &j.CurrentProviderID,
		&j.AcceptedProviderID, &j.ProviderMessageID, &j.NextAttemptAt, &j.LastError, &j.UnknownSince,
		&j.ReconcileAttempts, &j.StatusRank, &j.AcceptedAt, &j.DeliveredAt, &j.FinishedAt, &j.CreatedAt, &j.UpdatedAt)
	if err != nil {
		return nil, err
	}
	_ = json.Unmarshal(headers, &j.Headers)
	return &j, nil
}

// EnqueueRequest describes one email to deliver.
type EnqueueRequest struct {
	IdempotencyKey string // required; the same key never creates a second job
	Category       email.Category
	Kind           string
	CampaignID     string
	SubscriberID   string
	To             email.Address
	From           *email.Address // optional; defaults to provider/global sender
	ReplyTo        string
	Subject        string
	HTML           string
	Text           string
	Headers        map[string]string
	Tags           []string
	Priority       int
	NotBefore      time.Time
}

// Enqueue persists the job inside the caller's transaction (or pool). It
// returns created=false when a job with the same idempotency key already exists.
func Enqueue(ctx context.Context, db store.DB, r EnqueueRequest) (id string, created bool, err error) {
	if r.IdempotencyKey == "" {
		return "", false, errors.New("idempotency key required")
	}
	if r.Category != email.CategoryTransactional && r.Category != email.CategoryMarketing {
		return "", false, errors.New("invalid category")
	}
	id = uuid.NewString()
	headers := r.Headers
	if headers == nil {
		headers = map[string]string{}
	}
	hb, _ := json.Marshal(headers)
	var fromEmail, fromName *string
	if r.From != nil {
		fromEmail, fromName = &r.From.Email, &r.From.Name
	}
	next := r.NotBefore
	if next.IsZero() {
		next = time.Now()
	}
	tags := r.Tags
	if tags == nil {
		tags = []string{}
	}
	err = db.QueryRow(ctx, `INSERT INTO email_jobs (id, idempotency_key, category, kind, priority, campaign_id, subscriber_id,
		to_email, to_name, from_email, from_name, reply_to, subject, html_body, text_body, headers, tags, next_attempt_at)
		VALUES ($1,$2,$3,$4,$5,NULLIF($6,'')::uuid,NULLIF($7,'')::uuid,$8,NULLIF($9,''),$10,$11,NULLIF($12,''),$13,$14,$15,$16,$17,$18)
		ON CONFLICT (idempotency_key) DO NOTHING RETURNING id`,
		id, r.IdempotencyKey, r.Category, r.Kind, r.Priority, r.CampaignID, r.SubscriberID, r.To.Email, r.To.Name,
		fromEmail, fromName, r.ReplyTo, r.Subject, r.HTML, r.Text, hb, tags, next).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		err = db.QueryRow(ctx, `SELECT id FROM email_jobs WHERE idempotency_key=$1`, r.IdempotencyKey).Scan(&id)
		return id, false, err
	}
	return id, err == nil, err
}

func GetJob(ctx context.Context, db store.DB, id string) (*Job, error) {
	return scanJob(db.QueryRow(ctx, `SELECT `+jobCols+` FROM email_jobs WHERE id=$1`, id))
}

type Attempt struct {
	ID                string         `json:"id"`
	ProviderID        *string        `json:"provider_id"`
	ProviderKind      string         `json:"provider_kind"`
	ProviderName      string         `json:"provider_name"`
	AttemptNumber     int            `json:"attempt_number"`
	ProviderAttempt   int            `json:"provider_attempt"`
	Cycle             int            `json:"cycle"`
	WorkerID          string         `json:"worker_id"`
	Outcome           *string        `json:"outcome"`
	HTTPStatus        *int           `json:"http_status"`
	ProviderMessageID string         `json:"provider_message_id"`
	ErrorCode         string         `json:"error_code"`
	ErrorMessage      string         `json:"error_message"`
	Response          map[string]any `json:"response"`
	RetryAfterMS      *int           `json:"retry_after_ms"`
	FailoverReason    string         `json:"failover_reason"`
	StartedAt         time.Time      `json:"started_at"`
	FinishedAt        *time.Time     `json:"finished_at"`
}

func ListAttempts(ctx context.Context, db store.DB, jobID string) ([]Attempt, error) {
	rows, err := db.Query(ctx, `SELECT id, provider_id, provider_kind, provider_name, attempt_number, provider_attempt, cycle,
		worker_id, outcome, http_status, coalesce(provider_message_id,''), coalesce(error_code,''), coalesce(error_message,''),
		response, retry_after_ms, coalesce(failover_reason,''), started_at, finished_at
		FROM email_attempts WHERE job_id=$1 ORDER BY attempt_number`, jobID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Attempt{}
	for rows.Next() {
		var a Attempt
		var resp []byte
		if err := rows.Scan(&a.ID, &a.ProviderID, &a.ProviderKind, &a.ProviderName, &a.AttemptNumber, &a.ProviderAttempt,
			&a.Cycle, &a.WorkerID, &a.Outcome, &a.HTTPStatus, &a.ProviderMessageID, &a.ErrorCode, &a.ErrorMessage, &resp,
			&a.RetryAfterMS, &a.FailoverReason, &a.StartedAt, &a.FinishedAt); err != nil {
			return nil, err
		}
		if len(resp) > 0 {
			_ = json.Unmarshal(resp, &a.Response)
		}
		out = append(out, a)
	}
	return out, rows.Err()
}
