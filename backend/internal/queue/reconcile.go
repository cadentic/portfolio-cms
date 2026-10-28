package queue

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/cadentic/portfolio-cms/backend/internal/alerts"
	"github.com/cadentic/portfolio-cms/backend/internal/email"
	"github.com/cadentic/portfolio-cms/backend/internal/settings"
)

// reconcileDue leases acceptance_unknown jobs whose reconciliation is due.
func (w *Worker) reconcileDue(ctx context.Context, ds settings.Delivery) (int, error) {
	rows, err := w.DB.Query(ctx, `UPDATE email_jobs j SET lease_owner=$1, lease_expires_at=now() + make_interval(secs => $2)
		FROM (SELECT id FROM email_jobs WHERE status='acceptance_unknown' AND next_attempt_at <= now()
		        AND (lease_expires_at IS NULL OR lease_expires_at < now())
		      ORDER BY next_attempt_at FOR UPDATE SKIP LOCKED LIMIT 10) c
		WHERE j.id=c.id RETURNING `+prefixed("j.", jobCols), w.ID, ds.LeaseSeconds)
	if err != nil {
		return 0, err
	}
	var jobs []*Job
	for rows.Next() {
		j, err := scanJob(rows)
		if err != nil {
			rows.Close()
			return 0, err
		}
		jobs = append(jobs, j)
	}
	rows.Close()
	for _, j := range jobs {
		if err := w.reconcile(ctx, j, ds); err != nil {
			w.Log.Error("reconcile job", "job_id", j.ID, "err", err)
		}
	}
	return len(jobs), nil
}

// reconcile decides what to do with a message whose acceptance is unknown,
// without ever risking a duplicate:
//  1. If the provider deduplicates by idempotency key and we are still inside
//     its window, resubmit to the SAME provider with the same key.
//  2. Otherwise ask the provider whether it has the message (tag/CustomID lookup).
//     Found → accepted. Not found after a grace period → safe to resend/fail over.
//  3. If the provider cannot be queried, keep retrying lookups, then escalate to
//     the admin for a manual decision.
func (w *Worker) reconcile(ctx context.Context, j *Job, ds settings.Delivery) error {
	if j.CurrentProviderID == nil {
		return w.unknownManual(ctx, j, "provider for the uncertain attempt is unknown")
	}
	p, err := w.Providers.Get(ctx, *j.CurrentProviderID)
	if err != nil {
		return w.unknownManual(ctx, j, "provider was removed; cannot reconcile automatically")
	}
	adapter, ok := w.Registry.Get(p.Kind)
	if !ok {
		return w.unknownManual(ctx, j, "no adapter for provider kind "+p.Kind)
	}

	var lastUnknownAt time.Time
	err = w.DB.QueryRow(ctx, `SELECT started_at FROM email_attempts WHERE job_id=$1 AND provider_id=$2
		AND outcome IN ('acceptance_unknown','interrupted') ORDER BY attempt_number DESC LIMIT 1`, j.ID, p.ID).Scan(&lastUnknownAt)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return err
	}

	// 1. Idempotent resubmission to the same provider.
	window := adapter.IdempotencyWindow()
	if window > 0 && j.ReconcileAttempts == 0 && !lastUnknownAt.IsZero() && time.Since(lastUnknownAt) < window-2*time.Minute && p.Enabled {
		tag, err := w.DB.Exec(ctx, `UPDATE email_jobs SET status='processing', reconcile_attempts=reconcile_attempts+1,
			updated_at=now() WHERE id=$1 AND lease_owner=$2 AND status='acceptance_unknown'`, j.ID, w.ID)
		if err != nil || tag.RowsAffected() == 0 {
			return err
		}
		w.Log.Info("reconcile: idempotent resubmission to same provider", "job_id", j.ID, "provider", p.Name)
		attempts, err := w.cycleAttempts(ctx, j)
		if err != nil {
			return err
		}
		res, err := w.attempt(ctx, j, p, attempts, ds)
		if err != nil {
			return err
		}
		if res.Outcome == email.OutcomeAccepted {
			_ = w.Providers.RecordSuccess(ctx, p.ID)
			return w.markAccepted(ctx, j, p.ID, res.ProviderMessageID)
		}
		// Any other answer is ambiguous (a duplicate key may be reported as an
		// error), so stay in acceptance_unknown and fall back to lookups.
		_, err = w.DB.Exec(ctx, `UPDATE email_jobs SET status='acceptance_unknown', last_error=$3,
			next_attempt_at=now() + make_interval(secs => $4), lease_owner=NULL, lease_expires_at=NULL, updated_at=now()
			WHERE id=$1 AND lease_owner=$2 AND status='processing'`,
			j.ID, w.ID, fmt.Sprintf("%s: idempotent retry returned %s", p.Name, res.Outcome), ds.UnknownReconcileSeconds)
		return err
	}

	// 2. Provider lookup.
	since := j.CreatedAt
	if !lastUnknownAt.IsZero() {
		since = lastUnknownAt
	}
	lctx, cancel := context.WithTimeout(ctx, time.Duration(ds.SendTimeoutSeconds)*time.Second)
	lr, lerr := adapter.Lookup(lctx, w.Providers.RuntimeConfig(p), j.ID, j.ToEmail, since)
	cancel()
	if lerr == nil {
		switch lr.Status {
		case email.LookupFound:
			w.Log.Info("reconcile: provider has the message", "job_id", j.ID, "provider", p.Name)
			_ = w.Providers.RecordSuccess(ctx, p.ID)
			return w.markAccepted(ctx, j, p.ID, lr.ProviderMessageID)
		case email.LookupNotFound:
			unknownFor := time.Duration(0)
			if j.UnknownSince != nil {
				unknownFor = time.Since(*j.UnknownSince)
			}
			if ds.UnknownAutoResend && unknownFor >= time.Duration(ds.UnknownResendGraceMins)*time.Minute {
				w.Log.Warn("reconcile: not found at provider after grace period; resending", "job_id", j.ID, "provider", p.Name)
				_, err := w.DB.Exec(ctx, `UPDATE email_jobs SET status='retry_scheduled', next_attempt_at=now(),
					last_error=$3, lease_owner=NULL, lease_expires_at=NULL, updated_at=now()
					WHERE id=$1 AND lease_owner=$2 AND status='acceptance_unknown'`,
					j.ID, w.ID, p.Name+": no record of uncertain submission after grace period")
				return err
			}
		}
	}

	// 3. Not resolvable yet: back off, then escalate.
	attemptsSoFar := j.ReconcileAttempts + 1
	if lr.Status == email.LookupUnsupported || lerr != nil {
		if attemptsSoFar >= ds.UnknownMaxReconciles {
			return w.unknownManual(ctx, j, "automatic reconciliation exhausted; confirm in the provider dashboard")
		}
	}
	if attemptsSoFar >= ds.UnknownMaxReconciles*3 {
		return w.unknownManual(ctx, j, "message still unconfirmed after repeated lookups")
	}
	delay := w.backoff(ds.UnknownReconcileSeconds, ds.CycleBackoffMaxSeconds, min(attemptsSoFar-1, 6))
	_, err = w.DB.Exec(ctx, `UPDATE email_jobs SET reconcile_attempts=$3, next_attempt_at=now() + make_interval(secs => $4),
		lease_owner=NULL, lease_expires_at=NULL, updated_at=now() WHERE id=$1 AND lease_owner=$2`,
		j.ID, w.ID, attemptsSoFar, delay.Seconds())
	return err
}

// unknownManual parks the job for an explicit admin decision.
func (w *Worker) unknownManual(ctx context.Context, j *Job, why string) error {
	w.Alerts.Raise(ctx, "unknown_manual", alerts.Warning, "acceptance_unknown_manual",
		"Some emails need a manual decision: the provider may or may not have accepted them.", map[string]any{"last_job_id": j.ID, "reason": why})
	_, err := w.DB.Exec(ctx, `UPDATE email_jobs SET next_attempt_at='infinity', last_error=$3,
		lease_owner=NULL, lease_expires_at=NULL, updated_at=now() WHERE id=$1 AND lease_owner=$2`, j.ID, w.ID, why)
	return err
}
