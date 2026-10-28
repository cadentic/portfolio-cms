package queue

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/cadentic/portfolio-cms/backend/internal/email"
	"github.com/cadentic/portfolio-cms/backend/internal/store"
	"github.com/cadentic/portfolio-cms/backend/internal/suppression"
)

// IngestEvents stores webhook events idempotently. Re-delivered events hit
// the (provider_kind, dedupe_key) unique constraint and are ignored.
func IngestEvents(ctx context.Context, db store.DB, providerID, kind string, events []email.Event) (int, error) {
	inserted := 0
	for _, e := range events {
		payload, _ := json.Marshal(e.Payload)
		var jobID *string
		if _, err := uuid.Parse(e.InternalID); err == nil {
			jobID = &e.InternalID
		}
		tag, err := db.Exec(ctx, `INSERT INTO email_provider_events (provider_id, provider_kind, dedupe_key, provider_event_id,
			provider_message_id, job_id, email, event_type, raw_event, occurred_at, payload)
			VALUES ($1,$2,$3,NULLIF($4,''),NULLIF($5,''),
			        (SELECT id FROM email_jobs WHERE id = $6::uuid), NULLIF($7,''),$8,$9,$10,$11)
			ON CONFLICT (provider_kind, dedupe_key) DO NOTHING`,
			providerID, kind, e.DedupeKey, e.ProviderEventID, e.ProviderMessageID, jobID, e.Email,
			string(e.Type), e.RawType, e.OccurredAt, payload)
		if err != nil {
			return inserted, err
		}
		inserted += int(tag.RowsAffected())
	}
	return inserted, nil
}

// eventStatus maps an event to the job status it implies (if any) and its rank.
func eventStatus(t email.EventType) (string, int) {
	switch t {
	case email.EventAccepted:
		return StatusAccepted, statusRank[StatusAccepted]
	case email.EventDeferred, email.EventSoftBounce:
		return StatusDeferred, statusRank[StatusDeferred]
	case email.EventDelivered:
		return StatusDelivered, statusRank[StatusDelivered]
	case email.EventHardBounce, email.EventInvalid:
		return StatusBounced, statusRank[StatusBounced]
	case email.EventBlocked, email.EventError:
		return StatusFailed, statusRank[StatusBounced]
	case email.EventComplaint:
		return StatusComplained, statusRank[StatusComplained]
	}
	return "", 0
}

// ProcessPendingEvents applies stored events to jobs, subscribers and
// suppressions. It is called right after ingestion and from the worker loop
// (which picks up events that arrived while their job was still processing).
func (w *Worker) ProcessPendingEvents(ctx context.Context, limit int) error {
	return ProcessPendingEvents(ctx, w.DB, limit)
}

func ProcessPendingEvents(ctx context.Context, pool *pgxpool.Pool, limit int) error {
	for i := 0; i < limit; i++ {
		done, err := processOne(ctx, pool)
		if err != nil {
			return err
		}
		if done {
			return nil
		}
	}
	return nil
}

func processOne(ctx context.Context, pool *pgxpool.Pool) (bool, error) {
	empty := false
	err := store.InTx(ctx, pool, func(tx pgx.Tx) error {
		var (
			id, kind, eventType, rawType string
			providerID, jobID, msgID     *string
			emailAddr                    *string
			occurred, received           time.Time
		)
		// Oldest first, so events for one message are applied in arrival order;
		// rank checks below make the result independent of that order anyway.
		err := tx.QueryRow(ctx, `SELECT id, provider_kind, event_type, raw_event, provider_id, job_id, provider_message_id,
			email, occurred_at, received_at FROM email_provider_events
			WHERE processed_at IS NULL
			  AND (job_id IS NULL OR NOT EXISTS (SELECT 1 FROM email_jobs j WHERE j.id = job_id AND j.status = 'processing')
			       OR received_at < now() - interval '10 minutes')
			ORDER BY occurred_at, received_at FOR UPDATE SKIP LOCKED LIMIT 1`).
			Scan(&id, &kind, &eventType, &rawType, &providerID, &jobID, &msgID, &emailAddr, &occurred, &received)
		if errors.Is(err, pgx.ErrNoRows) {
			empty = true
			return nil
		}
		if err != nil {
			return err
		}
		if jobID == nil && msgID != nil {
			var jid string
			if err := tx.QueryRow(ctx, `SELECT id FROM email_jobs WHERE provider_message_id=$1 LIMIT 1`, *msgID).Scan(&jid); err == nil {
				jobID = &jid
			}
		}
		perr := applyEvent(ctx, tx, id, email.EventType(eventType), providerID, jobID, emailAddr, occurred)
		var errText *string
		if perr != nil {
			s := perr.Error()
			errText = &s
		}
		_, err = tx.Exec(ctx, `UPDATE email_provider_events SET processed_at=now(), job_id=COALESCE($2, job_id),
			process_error=$3 WHERE id=$1`, id, jobID, errText)
		return err
	})
	return empty, err
}

func applyEvent(ctx context.Context, tx pgx.Tx, eventID string, t email.EventType, providerID, jobID, emailAddr *string, occurred time.Time) error {
	if jobID != nil {
		if status, rank := eventStatus(t); status != "" {
			// Only move forward. 'processing' jobs are excluded by the caller; a
			// pending retry of an acceptance_unknown job is cancelled here because
			// the provider has now confirmed it has the original message.
			_, err := tx.Exec(ctx, `UPDATE email_jobs SET status=$2, status_rank=$3, last_event_at=$4,
				accepted_provider_id = COALESCE(accepted_provider_id, $5),
				accepted_at = COALESCE(accepted_at, $4),
				delivered_at = CASE WHEN $2 = 'delivered' THEN $4 ELSE delivered_at END,
				finished_at = CASE WHEN $2 IN ('delivered','bounced','complained','failed') THEN COALESCE(finished_at, now()) ELSE finished_at END,
				next_attempt_at = CASE WHEN status IN ('retry_scheduled','queued','acceptance_unknown') THEN 'infinity' ELSE next_attempt_at END,
				updated_at=now()
				WHERE id=$1 AND status <> 'processing' AND status_rank < $3 AND status <> 'cancelled'`,
				*jobID, status, rank, occurred, providerID)
			if err != nil {
				return err
			}
		}
		switch t {
		case email.EventOpened:
			if _, err := tx.Exec(ctx, `UPDATE email_campaign_recipients SET opened_at=COALESCE(opened_at,$2) WHERE job_id=$1`, *jobID, occurred); err != nil {
				return err
			}
		case email.EventClicked:
			if _, err := tx.Exec(ctx, `UPDATE email_campaign_recipients SET clicked_at=COALESCE(clicked_at,$2),
				opened_at=COALESCE(opened_at,$2) WHERE job_id=$1`, *jobID, occurred); err != nil {
				return err
			}
		}
	}
	if emailAddr == nil || *emailAddr == "" {
		return nil
	}
	switch t {
	case email.EventHardBounce:
		return suppression.HardBounce(ctx, tx, *emailAddr, "provider_webhook", &eventID, "hard_bounce")
	case email.EventInvalid:
		return suppression.HardBounce(ctx, tx, *emailAddr, "provider_webhook", &eventID, "invalid")
	case email.EventComplaint:
		return suppression.Complaint(ctx, tx, *emailAddr, "provider_webhook", &eventID)
	case email.EventUnsubscribed:
		return suppression.Unsubscribe(ctx, tx, *emailAddr, "provider_webhook", "provider")
	}
	return nil
}
