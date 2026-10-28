package queue

import (
	"context"
	"errors"

	"github.com/cadentic/portfolio-cms/backend/internal/store"
)

var ErrInvalidState = errors.New("job is not in a state that allows this action")

// Retry re-queues a failed or dead-lettered job. History is preserved; the
// internal message ID and idempotency key stay the same.
func Retry(ctx context.Context, db store.DB, id string) error {
	tag, err := db.Exec(ctx, `UPDATE email_jobs SET status='queued', cycle=cycle+1, next_attempt_at=now(),
		finished_at=NULL, last_error='manually re-queued', updated_at=now()
		WHERE id=$1 AND status IN ('failed','dead_letter') AND accepted_at IS NULL`, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrInvalidState
	}
	return nil
}

// ReplayDeadLetters re-queues dead-lettered jobs (all, or the given IDs).
func ReplayDeadLetters(ctx context.Context, db store.DB, ids []string) (int64, error) {
	tag, err := db.Exec(ctx, `UPDATE email_jobs SET status='queued', cycle=cycle+1, next_attempt_at=now(),
		finished_at=NULL, last_error='replayed from dead-letter', updated_at=now()
		WHERE status='dead_letter' AND accepted_at IS NULL AND (cardinality($1::uuid[]) = 0 OR id = ANY($1::uuid[]))`, ids)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}

// ResolveUnknown applies the owner's decision for an acceptance_unknown job:
// "accepted" (confirmed in the provider dashboard) or "resend" (confirmed the
// provider does not have it).
func ResolveUnknown(ctx context.Context, db store.DB, id, resolution string) error {
	var q string
	switch resolution {
	case "accepted":
		q = `UPDATE email_jobs SET status='accepted', status_rank=GREATEST(status_rank,10), accepted_at=COALESCE(accepted_at, now()),
			accepted_provider_id=COALESCE(accepted_provider_id, current_provider_id), reconcile_attempts=0,
			last_error='marked accepted by admin', updated_at=now(), lease_owner=NULL, lease_expires_at=NULL
			WHERE id=$1 AND status='acceptance_unknown'`
	case "resend":
		q = `UPDATE email_jobs SET status='retry_scheduled', next_attempt_at=now(), reconcile_attempts=0, unknown_since=NULL,
			last_error='resend confirmed by admin', updated_at=now(), lease_owner=NULL, lease_expires_at=NULL
			WHERE id=$1 AND status='acceptance_unknown'`
	case "reconcile":
		q = `UPDATE email_jobs SET next_attempt_at=now(), reconcile_attempts=0, updated_at=now()
			WHERE id=$1 AND status='acceptance_unknown'`
	default:
		return errors.New("resolution must be accepted, resend or reconcile")
	}
	tag, err := db.Exec(ctx, q, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrInvalidState
	}
	return nil
}

func Cancel(ctx context.Context, db store.DB, id string) error {
	tag, err := db.Exec(ctx, `UPDATE email_jobs SET status='cancelled', finished_at=now(), last_error='cancelled by admin', updated_at=now()
		WHERE id=$1 AND status IN ('queued','retry_scheduled')`, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrInvalidState
	}
	return nil
}
