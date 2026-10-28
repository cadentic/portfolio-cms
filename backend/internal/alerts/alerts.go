// Package alerts records admin-facing alerts (outages, quota exhaustion,
// repeated failures). Open alerts are deduplicated by key so a long outage is a
// single alert with an occurrence count rather than thousands of rows.
package alerts

import (
	"context"
	"encoding/json"
	"log/slog"
	"time"

	"github.com/cadentic/portfolio-cms/backend/internal/store"
)

type Severity string

const (
	Info     Severity = "info"
	Warning  Severity = "warning"
	Critical Severity = "critical"
)

// Notifier is called the first time an alert opens (e.g. to email the owner).
type Notifier func(ctx context.Context, alertID string, severity Severity, kind, message string)

type Service struct {
	DB     store.DB
	Log    *slog.Logger
	Notify Notifier
}

type Alert struct {
	ID             string         `json:"id"`
	Severity       string         `json:"severity"`
	Kind           string         `json:"kind"`
	Message        string         `json:"message"`
	Details        map[string]any `json:"details"`
	Occurrences    int            `json:"occurrences"`
	FirstSeenAt    time.Time      `json:"first_seen_at"`
	LastSeenAt     time.Time      `json:"last_seen_at"`
	AcknowledgedAt *time.Time     `json:"acknowledged_at"`
}

func (s *Service) Raise(ctx context.Context, key string, sev Severity, kind, msg string, details map[string]any) {
	if details == nil {
		details = map[string]any{}
	}
	b, _ := json.Marshal(details)
	var id string
	var inserted bool
	err := s.DB.QueryRow(ctx, `INSERT INTO admin_alerts (dedupe_key, severity, kind, message, details)
		VALUES ($1,$2,$3,$4,$5)
		ON CONFLICT (dedupe_key) WHERE acknowledged_at IS NULL
		DO UPDATE SET occurrences = admin_alerts.occurrences + 1, last_seen_at = now(),
		              message = EXCLUDED.message, details = EXCLUDED.details
		RETURNING id, (xmax = 0)`, key, sev, kind, msg, b).Scan(&id, &inserted)
	if err != nil {
		s.Log.Error("raise alert failed", "kind", kind, "err", err)
		return
	}
	s.Log.Warn("admin alert", "kind", kind, "severity", sev, "message", msg, "new", inserted)
	if inserted && s.Notify != nil && sev != Info {
		s.Notify(ctx, id, sev, kind, msg)
	}
}

func (s *Service) List(ctx context.Context, includeAcked bool, limit int) ([]Alert, error) {
	rows, err := s.DB.Query(ctx, `SELECT id, severity, kind, message, details, occurrences, first_seen_at, last_seen_at, acknowledged_at
		FROM admin_alerts WHERE ($1 OR acknowledged_at IS NULL) ORDER BY last_seen_at DESC LIMIT $2`, includeAcked, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Alert{}
	for rows.Next() {
		var a Alert
		var d []byte
		if err := rows.Scan(&a.ID, &a.Severity, &a.Kind, &a.Message, &d, &a.Occurrences, &a.FirstSeenAt, &a.LastSeenAt, &a.AcknowledgedAt); err != nil {
			return nil, err
		}
		_ = json.Unmarshal(d, &a.Details)
		out = append(out, a)
	}
	return out, rows.Err()
}

func (s *Service) Ack(ctx context.Context, id, actor string) error {
	_, err := s.DB.Exec(ctx, `UPDATE admin_alerts SET acknowledged_at=now(), acknowledged_by=$2 WHERE id=$1 AND acknowledged_at IS NULL`, id, actor)
	return err
}
