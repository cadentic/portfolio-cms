package queue

import (
	"context"
	"time"

	"github.com/cadentic/portfolio-cms/backend/internal/store"
)

type ProviderStats struct {
	ProviderID   string  `json:"provider_id"`
	ProviderName string  `json:"provider_name"`
	Attempts     int     `json:"attempts"`
	Accepted     int     `json:"accepted"`
	Failures     int     `json:"failures"`
	RateLimited  int     `json:"rate_limited"`
	Unknown      int     `json:"unknown"`
	Delivered    int     `json:"delivered"`
	Bounced      int     `json:"bounced"`
	Complained   int     `json:"complained"`
	ErrorRate    float64 `json:"error_rate"`
}

type FailoverReason struct {
	Reason string `json:"reason"`
	Count  int    `json:"count"`
}

type Monitor struct {
	WindowHours       int              `json:"window_hours"`
	QueueDepth        int              `json:"queue_depth"`
	Processing        int              `json:"processing"`
	OldestPendingAt   *time.Time       `json:"oldest_pending_at"`
	StatusCounts      map[string]int   `json:"status_counts"`
	Retries           int              `json:"retries"`
	Failovers         int              `json:"failovers"`
	FailoverReasons   []FailoverReason `json:"failover_reasons"`
	DeadLetter        int              `json:"dead_letter"`
	AcceptanceUnknown int              `json:"acceptance_unknown"`
	BounceRate        float64          `json:"bounce_rate"`
	ComplaintRate     float64          `json:"complaint_rate"`
	Providers         []ProviderStats  `json:"providers"`
	UnprocessedEvents int              `json:"unprocessed_events"`
}

func GetMonitor(ctx context.Context, db store.DB, windowHours int) (*Monitor, error) {
	if windowHours <= 0 {
		windowHours = 24
	}
	m := &Monitor{WindowHours: windowHours, StatusCounts: map[string]int{}}
	since := time.Now().Add(-time.Duration(windowHours) * time.Hour)

	if err := db.QueryRow(ctx, `SELECT count(*) FILTER (WHERE status IN ('queued','retry_scheduled')),
		count(*) FILTER (WHERE status='processing'),
		min(created_at) FILTER (WHERE status IN ('queued','retry_scheduled','processing')),
		count(*) FILTER (WHERE status='dead_letter'),
		count(*) FILTER (WHERE status='acceptance_unknown')
		FROM email_jobs`).Scan(&m.QueueDepth, &m.Processing, &m.OldestPendingAt, &m.DeadLetter, &m.AcceptanceUnknown); err != nil {
		return nil, err
	}
	rows, err := db.Query(ctx, `SELECT status, count(*) FROM email_jobs WHERE created_at >= $1 GROUP BY status`, since)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var s string
		var n int
		if err := rows.Scan(&s, &n); err != nil {
			rows.Close()
			return nil, err
		}
		m.StatusCounts[s] = n
	}
	rows.Close()

	if err := db.QueryRow(ctx, `SELECT count(*) FILTER (WHERE attempt_number > 1),
		count(*) FILTER (WHERE failover_reason IS NOT NULL) FROM email_attempts WHERE started_at >= $1`, since).
		Scan(&m.Retries, &m.Failovers); err != nil {
		return nil, err
	}
	rows, err = db.Query(ctx, `SELECT left(regexp_replace(failover_reason, '^failover from previous provider: ', ''), 120) r, count(*)
		FROM email_attempts WHERE started_at >= $1 AND failover_reason IS NOT NULL GROUP BY r ORDER BY count(*) DESC LIMIT 10`, since)
	if err != nil {
		return nil, err
	}
	m.FailoverReasons = []FailoverReason{}
	for rows.Next() {
		var f FailoverReason
		if err := rows.Scan(&f.Reason, &f.Count); err != nil {
			rows.Close()
			return nil, err
		}
		m.FailoverReasons = append(m.FailoverReasons, f)
	}
	rows.Close()

	var sent, bounced, complained int
	if err := db.QueryRow(ctx, `SELECT count(*) FILTER (WHERE accepted_at IS NOT NULL),
		count(*) FILTER (WHERE status='bounced'), count(*) FILTER (WHERE status='complained')
		FROM email_jobs WHERE category='marketing' AND created_at >= now() - interval '30 days'`).Scan(&sent, &bounced, &complained); err != nil {
		return nil, err
	}
	if sent > 0 {
		m.BounceRate = float64(bounced) / float64(sent)
		m.ComplaintRate = float64(complained) / float64(sent)
	}
	if err := db.QueryRow(ctx, `SELECT count(*) FROM email_provider_events WHERE processed_at IS NULL`).Scan(&m.UnprocessedEvents); err != nil {
		return nil, err
	}

	rows, err = db.Query(ctx, `SELECT p.id, p.name,
		count(a.id),
		count(a.id) FILTER (WHERE a.outcome='accepted'),
		count(a.id) FILTER (WHERE a.outcome IN ('rejected_transient','rejected_permanent','not_sent','interrupted')),
		count(a.id) FILTER (WHERE a.outcome='rate_limited'),
		count(a.id) FILTER (WHERE a.outcome='acceptance_unknown'),
		(SELECT count(*) FROM email_jobs j WHERE j.accepted_provider_id=p.id AND j.status='delivered' AND j.created_at >= $1),
		(SELECT count(*) FROM email_jobs j WHERE j.accepted_provider_id=p.id AND j.status='bounced' AND j.created_at >= $1),
		(SELECT count(*) FROM email_jobs j WHERE j.accepted_provider_id=p.id AND j.status='complained' AND j.created_at >= $1)
		FROM email_providers p LEFT JOIN email_attempts a ON a.provider_id=p.id AND a.started_at >= $1
		GROUP BY p.id, p.name, p.priority ORDER BY p.priority`, since)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	m.Providers = []ProviderStats{}
	for rows.Next() {
		var s ProviderStats
		if err := rows.Scan(&s.ProviderID, &s.ProviderName, &s.Attempts, &s.Accepted, &s.Failures, &s.RateLimited,
			&s.Unknown, &s.Delivered, &s.Bounced, &s.Complained); err != nil {
			return nil, err
		}
		if s.Attempts > 0 {
			s.ErrorRate = float64(s.Failures+s.RateLimited+s.Unknown) / float64(s.Attempts)
		}
		m.Providers = append(m.Providers, s)
	}
	return m, rows.Err()
}

type JobFilter struct {
	Status     string
	Category   string
	CampaignID string
	Email      string
	Limit      int
	Offset     int
}

func ListJobs(ctx context.Context, db store.DB, f JobFilter) ([]*Job, int, error) {
	if f.Limit <= 0 || f.Limit > 200 {
		f.Limit = 50
	}
	where := `WHERE ($1 = '' OR status = $1) AND ($2 = '' OR category = $2)
		AND ($3 = '' OR campaign_id = NULLIF($3,'')::uuid) AND ($4 = '' OR to_email ILIKE '%' || $4 || '%')`
	var total int
	if err := db.QueryRow(ctx, `SELECT count(*) FROM email_jobs `+where, f.Status, f.Category, f.CampaignID, f.Email).Scan(&total); err != nil {
		return nil, 0, err
	}
	rows, err := db.Query(ctx, `SELECT `+jobCols+` FROM email_jobs `+where+` ORDER BY created_at DESC LIMIT $5 OFFSET $6`,
		f.Status, f.Category, f.CampaignID, f.Email, f.Limit, f.Offset)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	out := []*Job{}
	for rows.Next() {
		j, err := scanJob(rows)
		if err != nil {
			return nil, 0, err
		}
		out = append(out, j)
	}
	return out, total, rows.Err()
}

type Event struct {
	ID                string    `json:"id"`
	ProviderKind      string    `json:"provider_kind"`
	EventType         string    `json:"event_type"`
	RawEvent          string    `json:"raw_event"`
	ProviderMessageID string    `json:"provider_message_id"`
	OccurredAt        time.Time `json:"occurred_at"`
	ReceivedAt        time.Time `json:"received_at"`
	Processed         bool      `json:"processed"`
}

func ListJobEvents(ctx context.Context, db store.DB, jobID string) ([]Event, error) {
	rows, err := db.Query(ctx, `SELECT id, provider_kind, event_type, raw_event, coalesce(provider_message_id,''),
		occurred_at, received_at, processed_at IS NOT NULL FROM email_provider_events WHERE job_id=$1 ORDER BY occurred_at`, jobID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Event{}
	for rows.Next() {
		var e Event
		if err := rows.Scan(&e.ID, &e.ProviderKind, &e.EventType, &e.RawEvent, &e.ProviderMessageID, &e.OccurredAt, &e.ReceivedAt, &e.Processed); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}
