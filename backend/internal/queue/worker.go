package queue

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"math/rand/v2"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/cadentic/portfolio-cms/backend/internal/alerts"
	"github.com/cadentic/portfolio-cms/backend/internal/email"
	"github.com/cadentic/portfolio-cms/backend/internal/providers"
	"github.com/cadentic/portfolio-cms/backend/internal/settings"
)

// Hook lets other packages run periodic work inside the worker loop
// (campaign scheduling/materialisation, retention purges).
type Hook func(ctx context.Context) error

type Worker struct {
	DB        *pgxpool.Pool
	Providers *providers.Repo
	Registry  *email.Registry
	Settings  *settings.Store
	Alerts    *alerts.Service
	Log       *slog.Logger
	ID        string
	Hooks     []Hook

	PollInterval time.Duration
	Jitter       func() float64 // [0,1); replaced in tests for determinism
}

func (w *Worker) jitter() float64 {
	if w.Jitter != nil {
		return w.Jitter()
	}
	return rand.Float64()
}

// Run processes jobs until ctx is cancelled. Jobs in flight finish (bounded by
// the send timeout) before Run returns; anything interrupted harder than that
// is recovered by lease expiry.
func (w *Worker) Run(ctx context.Context) {
	poll := w.PollInterval
	if poll == 0 {
		poll = time.Second
	}
	w.Log.Info("email worker started", "worker_id", w.ID)
	t := time.NewTicker(poll)
	defer t.Stop()
	for {
		n, err := w.Tick(context.WithoutCancel(ctx))
		if err != nil {
			w.Log.Error("worker tick failed", "err", err)
		}
		if n > 0 {
			continue // drain quickly while there is work
		}
		select {
		case <-ctx.Done():
			w.Log.Info("email worker stopped", "worker_id", w.ID)
			return
		case <-t.C:
		}
	}
}

// Tick performs one round of every worker duty and returns the number of jobs
// handled. Tests call it directly.
func (w *Worker) Tick(ctx context.Context) (int, error) {
	ds, _ := w.Settings.Delivery(ctx)
	if err := w.RecoverStale(ctx, ds); err != nil {
		return 0, fmt.Errorf("recover: %w", err)
	}
	for _, h := range w.Hooks {
		if err := h(ctx); err != nil {
			w.Log.Error("worker hook failed", "err", err)
		}
	}
	if err := w.ProcessPendingEvents(ctx, 200); err != nil {
		w.Log.Error("event processing failed", "err", err)
	}
	jobs, err := w.claim(ctx, ds.WorkerConcurrency, ds.LeaseSeconds)
	if err != nil {
		return 0, fmt.Errorf("claim: %w", err)
	}
	var wg sync.WaitGroup
	for _, j := range jobs {
		wg.Add(1)
		go func(j *Job) {
			defer wg.Done()
			if err := w.process(ctx, j, ds); err != nil {
				w.Log.Error("process job failed", "job_id", j.ID, "err", err)
			}
		}(j)
	}
	wg.Wait()
	n := len(jobs)
	r, err := w.reconcileDue(ctx, ds)
	if err != nil {
		w.Log.Error("reconcile failed", "err", err)
	}
	return n + r, nil
}

// claim atomically leases due jobs. FOR UPDATE SKIP LOCKED guarantees that two
// workers never receive the same job.
func (w *Worker) claim(ctx context.Context, limit, leaseSecs int) ([]*Job, error) {
	rows, err := w.DB.Query(ctx, `UPDATE email_jobs j SET status='processing', lease_owner=$1,
		lease_expires_at=now() + make_interval(secs => $2), updated_at=now()
		FROM (SELECT id FROM email_jobs WHERE status IN ('queued','retry_scheduled') AND next_attempt_at <= now()
		      ORDER BY priority DESC, next_attempt_at FOR UPDATE SKIP LOCKED LIMIT $3) c
		WHERE j.id = c.id RETURNING `+prefixed("j.", jobCols), w.ID, leaseSecs, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Job
	for rows.Next() {
		j, err := scanJob(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, j)
	}
	return out, rows.Err()
}

// RecoverStale releases jobs whose worker died. If the dead worker had a
// request in flight we cannot know whether the provider accepted it, so the
// job becomes acceptance_unknown instead of being resent.
func (w *Worker) RecoverStale(ctx context.Context, ds settings.Delivery) error {
	rows, err := w.DB.Query(ctx, `WITH stale AS (
		  SELECT id FROM email_jobs WHERE status='processing' AND lease_expires_at < now() FOR UPDATE SKIP LOCKED
		), inflight AS (
		  UPDATE email_attempts a SET outcome='interrupted', finished_at=now(),
		         error_message='worker stopped while the request was in flight'
		  WHERE a.job_id IN (SELECT id FROM stale) AND a.outcome IS NULL
		  RETURNING a.job_id, a.provider_id
		)
		UPDATE email_jobs j SET
		  status = CASE WHEN EXISTS (SELECT 1 FROM inflight i WHERE i.job_id=j.id) THEN 'acceptance_unknown' ELSE 'retry_scheduled' END,
		  unknown_since = CASE WHEN EXISTS (SELECT 1 FROM inflight i WHERE i.job_id=j.id) THEN now() ELSE j.unknown_since END,
		  current_provider_id = COALESCE((SELECT i.provider_id FROM inflight i WHERE i.job_id=j.id LIMIT 1), j.current_provider_id),
		  next_attempt_at = CASE WHEN EXISTS (SELECT 1 FROM inflight i WHERE i.job_id=j.id)
		                         THEN now() + make_interval(secs => $1) ELSE now() END,
		  last_error = 'recovered after worker lease expired',
		  lease_owner = NULL, lease_expires_at = NULL, updated_at = now()
		FROM stale WHERE j.id = stale.id
		RETURNING j.id, j.status`, ds.UnknownReconcileSeconds)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var id, st string
		if err := rows.Scan(&id, &st); err != nil {
			return err
		}
		w.Log.Warn("recovered stale job", "job_id", id, "new_status", st)
	}
	return rows.Err()
}

type cycleAttempt struct {
	providerID string
	outcome    string
	retryAfter int
	errorCode  string
}

func (w *Worker) cycleAttempts(ctx context.Context, j *Job) ([]cycleAttempt, error) {
	rows, err := w.DB.Query(ctx, `SELECT coalesce(provider_id::text,''), coalesce(outcome,''), coalesce(retry_after_ms,0),
		coalesce(error_code,'') FROM email_attempts WHERE job_id=$1 AND cycle=$2 ORDER BY attempt_number`, j.ID, j.Cycle)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []cycleAttempt
	for rows.Next() {
		var a cycleAttempt
		if err := rows.Scan(&a.providerID, &a.outcome, &a.retryAfter, &a.errorCode); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// eligible re-checks consent and suppression at send time, because a
// recipient may unsubscribe or bounce after the job was queued.
func (w *Worker) eligible(ctx context.Context, j *Job) (bool, string, error) {
	marketing := j.Category == email.CategoryMarketing
	var suppressed bool
	if err := w.DB.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM email_suppressions WHERE email=$1 AND lifted_at IS NULL
		AND (scope='all' OR $2))`, j.ToEmail, marketing).Scan(&suppressed); err != nil {
		return false, "", err
	}
	if suppressed {
		return false, "recipient is suppressed", nil
	}
	if marketing && j.Kind == "campaign" {
		if j.SubscriberID == nil {
			return false, "marketing job without subscriber", nil
		}
		var ok bool
		if err := w.DB.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM eligible_marketing_subscribers WHERE id=$1)`, *j.SubscriberID).Scan(&ok); err != nil {
			return false, "", err
		}
		if !ok {
			return false, "subscriber is no longer verified/consented", nil
		}
	}
	return true, "", nil
}

func (w *Worker) process(ctx context.Context, j *Job, ds settings.Delivery) error {
	ok, why, err := w.eligible(ctx, j)
	if err != nil {
		return w.release(ctx, j, time.Now().Add(5*time.Second), "eligibility check failed: "+err.Error())
	}
	if !ok {
		return w.finish(ctx, j, StatusCancelled, why)
	}

	chain, err := w.Providers.Chain(ctx, string(j.Category))
	if err != nil {
		return w.release(ctx, j, time.Now().Add(5*time.Second), "load providers: "+err.Error())
	}
	attempts, err := w.cycleAttempts(ctx, j)
	if err != nil {
		return w.release(ctx, j, time.Now().Add(5*time.Second), "load attempts: "+err.Error())
	}

	provider, skipped, err := w.pickProvider(ctx, chain, attempts, ds)
	if err != nil {
		return w.release(ctx, j, time.Now().Add(5*time.Second), err.Error())
	}
	if provider == nil {
		return w.endCycle(ctx, j, chain, attempts, skipped, ds)
	}
	if !w.Providers.Allow(provider) {
		// Local send-rate cap: wait a moment, do not count an attempt.
		wait := time.Duration(float64(time.Second) / provider.RateLimitPerSecond)
		return w.release(ctx, j, time.Now().Add(wait), "")
	}
	res, err := w.attempt(ctx, j, provider, attempts, ds)
	if err != nil {
		return err
	}
	return w.applyOutcome(ctx, j, provider, attempts, res, ds)
}

// pickProvider returns the highest-priority provider that still has attempts
// left in this cycle, has not permanently rejected the message, and whose
// circuit admits traffic.
func (w *Worker) pickProvider(ctx context.Context, chain []*providers.Provider, attempts []cycleAttempt, ds settings.Delivery) (*providers.Provider, []string, error) {
	var skipped []string
	for _, p := range chain {
		if w.providerExhausted(p, attempts, ds) {
			continue
		}
		ok, err := w.Providers.Acquire(ctx, p, time.Duration(ds.CircuitCooldownSeconds)*time.Second)
		if err != nil {
			return nil, nil, err
		}
		if !ok {
			skipped = append(skipped, p.Name+": circuit open")
			continue
		}
		return p, skipped, nil
	}
	return nil, skipped, nil
}

func (w *Worker) providerExhausted(p *providers.Provider, attempts []cycleAttempt, ds settings.Delivery) bool {
	n := 0
	for _, a := range attempts {
		if a.providerID != p.ID {
			continue
		}
		n++
		if a.outcome == string(email.OutcomeRejectedPermanent) {
			return true
		}
		if a.outcome == string(email.OutcomeRateLimited) && a.retryAfter > ds.ProviderRetryMaxSeconds*1000 {
			return true // provider asked us to back off longer than we are willing to wait
		}
	}
	return n >= p.MaxAttempts
}

// attempt records an in-flight attempt row (committed before the network call
// so a crash is detectable), submits the message and records the outcome.
func (w *Worker) attempt(ctx context.Context, j *Job, p *providers.Provider, attempts []cycleAttempt, ds settings.Delivery) (email.SendResult, error) {
	adapter, _ := w.Registry.Get(p.Kind)
	providerAttempt := 1
	for _, a := range attempts {
		if a.providerID == p.ID {
			providerAttempt++
		}
	}
	var failoverReason string
	if j.CurrentProviderID != nil && *j.CurrentProviderID != p.ID && j.AttemptCount > 0 {
		failoverReason = "failover from previous provider: " + j.LastError
	}
	attemptNo := j.AttemptCount + 1
	var attemptID string
	err := w.DB.QueryRow(ctx, `WITH upd AS (
		  UPDATE email_jobs SET attempt_count=$3, current_provider_id=$4,
		         failover_count = failover_count + CASE WHEN $9 <> '' THEN 1 ELSE 0 END, updated_at=now()
		  WHERE id=$1 AND lease_owner=$2 AND status='processing' RETURNING id)
		INSERT INTO email_attempts (job_id, provider_id, provider_kind, provider_name, attempt_number, provider_attempt,
		  cycle, worker_id, failover_reason)
		SELECT $1, $4, $5, $6, $3, $7, $8, $2, NULLIF($9,'') FROM upd RETURNING id`,
		j.ID, w.ID, attemptNo, p.ID, p.Kind, p.Name, providerAttempt, j.Cycle, failoverReason).Scan(&attemptID)
	if errors.Is(err, pgx.ErrNoRows) {
		return email.SendResult{}, fmt.Errorf("job %s: lease lost before attempt", j.ID)
	}
	if err != nil {
		return email.SendResult{}, err
	}
	j.AttemptCount = attemptNo
	j.CurrentProviderID = &p.ID
	if failoverReason != "" {
		j.FailoverCount++
		w.Log.Warn("email failover", "job_id", j.ID, "to_provider", p.Name, "reason", email.RedactString(failoverReason))
	}

	msg, ok := w.buildMessage(ctx, j, p)
	var res email.SendResult
	if !ok {
		res = email.SendResult{Outcome: email.OutcomeRejectedPermanent, ErrorCode: "misconfigured",
			ErrorMessage: "no sender address configured for provider or globally"}
	} else {
		sendCtx, cancel := context.WithTimeout(ctx, time.Duration(ds.SendTimeoutSeconds)*time.Second)
		start := time.Now()
		res = adapter.Send(sendCtx, w.Providers.RuntimeConfig(p), msg)
		cancel()
		w.Log.Info("email attempt", "job_id", j.ID, "provider", p.Name, "attempt", attemptNo, "outcome", res.Outcome,
			"http_status", res.HTTPStatus, "duration_ms", time.Since(start).Milliseconds(), "to", email.MaskEmail(j.ToEmail))
	}
	var resp []byte
	if res.Response != nil {
		resp, _ = json.Marshal(res.Response)
	}
	var retryMS *int
	if res.RetryAfter > 0 {
		v := int(res.RetryAfter / time.Millisecond)
		retryMS = &v
	}
	_, err = w.DB.Exec(ctx, `UPDATE email_attempts SET outcome=$2, http_status=NULLIF($3,0), provider_message_id=NULLIF($4,''),
		error_code=NULLIF($5,''), error_message=NULLIF($6,''), response=$7, retry_after_ms=$8, finished_at=now() WHERE id=$1`,
		attemptID, string(res.Outcome), res.HTTPStatus, res.ProviderMessageID, res.ErrorCode,
		email.RedactString(res.ErrorMessage), resp, retryMS)
	return res, err
}

func (w *Worker) buildMessage(ctx context.Context, j *Job, p *providers.Provider) (email.Message, bool) {
	es, _ := w.Settings.Email(ctx)
	from := email.Address{Email: j.FromEmail, Name: j.FromName}
	if from.Email == "" {
		from = email.Address{Email: p.SenderEmail, Name: p.SenderName}
	}
	if from.Email == "" {
		from = email.Address{Email: es.SenderEmail, Name: es.SenderName}
	}
	if from.Name == "" {
		from.Name = es.SenderName
	}
	replyTo := j.ReplyTo
	if replyTo == "" {
		replyTo = p.ReplyTo
	}
	if replyTo == "" {
		replyTo = es.ReplyTo
	}
	msg := email.Message{
		InternalID: j.ID, Category: j.Category, From: from,
		To: email.Address{Email: j.ToEmail, Name: j.ToName}, Subject: j.Subject,
		HTML: j.HTMLBody, Text: j.TextBody, Headers: j.Headers, Tags: append([]string{j.Kind}, j.Tags...),
	}
	if replyTo != "" {
		msg.ReplyTo = &email.Address{Email: replyTo}
	}
	return msg, from.Email != ""
}

func (w *Worker) applyOutcome(ctx context.Context, j *Job, p *providers.Provider, attempts []cycleAttempt, res email.SendResult, ds settings.Delivery) error {
	errText := strings.TrimSpace(fmt.Sprintf("%s: %s %s", p.Name, res.Outcome, firstNonEmpty(res.ErrorCode, res.ErrorMessage)))
	switch res.Outcome {
	case email.OutcomeAccepted:
		_ = w.Providers.RecordSuccess(ctx, p.ID)
		return w.markAccepted(ctx, j, p.ID, res.ProviderMessageID)

	case email.OutcomeUnknown:
		// Never fail over here: the provider may already have the message.
		w.providerFailure(ctx, p, errText, ds)
		tag, err := w.DB.Exec(ctx, `UPDATE email_jobs SET status='acceptance_unknown', unknown_since=now(),
			last_error=$3, next_attempt_at=now() + make_interval(secs => $4),
			lease_owner=NULL, lease_expires_at=NULL, updated_at=now()
			WHERE id=$1 AND lease_owner=$2 AND status='processing'`, j.ID, w.ID, errText, ds.UnknownReconcileSeconds)
		if err == nil && tag.RowsAffected() == 0 {
			err = fmt.Errorf("job %s: lease lost", j.ID)
		}
		w.Alerts.Raise(ctx, "acceptance_unknown", alerts.Warning, "acceptance_unknown",
			"Some emails have unknown acceptance after provider timeouts; reconciliation is running before any resend.",
			map[string]any{"last_job_id": j.ID, "provider": p.Name})
		return err

	case email.OutcomeRejectedPermanent:
		if res.QuotaExhausted || res.ErrorCode == "auth_failed" || res.ErrorCode == "misconfigured" || res.HTTPStatus == 401 || res.HTTPStatus == 403 {
			w.providerFailure(ctx, p, errText, ds)
		}
		if res.QuotaExhausted {
			w.Alerts.Raise(ctx, "quota:"+p.ID, alerts.Critical, "quota_exhausted",
				fmt.Sprintf("%s reports its sending quota/credits are exhausted.", p.Name), map[string]any{"provider": p.Name})
		}
		return w.release(ctx, j, time.Now(), errText) // try next provider immediately

	default: // rejected_transient, rate_limited, not_sent
		if res.Outcome == email.OutcomeRateLimited {
			_ = w.Providers.RecordRateLimited(ctx, p.ID)
		} else {
			w.providerFailure(ctx, p, errText, ds)
		}
		used := 1
		for _, a := range attempts {
			if a.providerID == p.ID {
				used++
			}
		}
		longBackoff := res.RetryAfter > time.Duration(ds.ProviderRetryMaxSeconds)*time.Second
		if used < p.MaxAttempts && !longBackoff {
			delay := w.backoff(ds.ProviderRetryBaseSeconds, ds.ProviderRetryMaxSeconds, used-1)
			if res.RetryAfter > delay {
				delay = res.RetryAfter
			}
			return w.reschedule(ctx, j, time.Now().Add(delay), errText)
		}
		return w.release(ctx, j, time.Now(), errText) // retries exhausted → next provider now
	}
}

func (w *Worker) providerFailure(ctx context.Context, p *providers.Provider, msg string, ds settings.Delivery) {
	opened, err := w.Providers.RecordFailure(ctx, p.ID, msg, ds.CircuitFailureThreshold)
	if err != nil {
		w.Log.Error("record provider failure", "err", err)
		return
	}
	if opened {
		w.Log.Warn("provider circuit opened", "provider", p.Name)
		w.Alerts.Raise(ctx, "circuit:"+p.ID, alerts.Warning, "circuit_open",
			fmt.Sprintf("%s circuit breaker opened after repeated failures; traffic is failing over.", p.Name),
			map[string]any{"provider": p.Name, "last_error": msg})
	}
}

// endCycle runs when no provider in the chain can take the job right now.
func (w *Worker) endCycle(ctx context.Context, j *Job, chain []*providers.Provider, attempts []cycleAttempt, skipped []string, ds settings.Delivery) error {
	if len(chain) == 0 {
		w.Alerts.Raise(ctx, "no_providers", alerts.Critical, "no_providers",
			"No enabled email provider can send "+string(j.Category)+" email. Messages are queued until one is enabled.", nil)
		return w.reschedule(ctx, j, time.Now().Add(time.Duration(ds.CycleBackoffBaseSeconds)*time.Second),
			"no enabled provider for "+string(j.Category))
	}
	allPermanent := len(attempts) > 0
	for _, a := range attempts {
		if a.outcome != string(email.OutcomeRejectedPermanent) {
			allPermanent = false
		}
	}
	next := j.Cycle + 1
	reason := j.LastError
	if len(skipped) > 0 {
		reason = strings.Join(skipped, "; ") + "; " + reason
	}
	if allPermanent && len(skipped) == 0 {
		w.Alerts.Raise(ctx, "dead_letter", alerts.Warning, "dead_letter",
			"Emails were rejected by every provider and moved to the dead-letter queue.", map[string]any{"last_job_id": j.ID})
		return w.deadLetter(ctx, j, next, "rejected by all providers: "+reason)
	}
	if next >= ds.MaxCycles {
		w.Alerts.Raise(ctx, "dead_letter", alerts.Critical, "dead_letter",
			"Emails exhausted every retry cycle across all providers and moved to the dead-letter queue.", map[string]any{"last_job_id": j.ID})
		return w.deadLetter(ctx, j, next, "all providers failed for "+fmt.Sprint(next)+" cycles: "+reason)
	}
	w.Alerts.Raise(ctx, "all_providers_failed", alerts.Critical, "all_providers_failed",
		"All email providers failed for at least one message. It is queued and will retry with backoff.",
		map[string]any{"last_job_id": j.ID, "cycle": next})
	delay := w.backoff(ds.CycleBackoffBaseSeconds, ds.CycleBackoffMaxSeconds, next-1)
	tag, err := w.DB.Exec(ctx, `UPDATE email_jobs SET status='retry_scheduled', cycle=$3, last_error=$4,
		next_attempt_at=$5, lease_owner=NULL, lease_expires_at=NULL, updated_at=now()
		WHERE id=$1 AND lease_owner=$2 AND status='processing'`, j.ID, w.ID, next, truncate(reason, 1000), time.Now().Add(delay))
	if err == nil && tag.RowsAffected() == 0 {
		return fmt.Errorf("job %s: lease lost", j.ID)
	}
	return err
}

// backoff returns an exponential delay with "equal jitter": half fixed, half
// random, so retries spread out but never collapse to zero.
func (w *Worker) backoff(baseSecs, maxSecs, n int) time.Duration {
	d := float64(baseSecs) * math.Pow(2, float64(n))
	if d > float64(maxSecs) {
		d = float64(maxSecs)
	}
	d = d/2 + w.jitter()*d/2
	return time.Duration(d * float64(time.Second))
}

func (w *Worker) markAccepted(ctx context.Context, j *Job, providerID, providerMsgID string) error {
	tag, err := w.DB.Exec(ctx, `UPDATE email_jobs SET
		status = CASE WHEN status_rank > 10 THEN status ELSE 'accepted' END,
		status_rank = GREATEST(status_rank, 10),
		accepted_provider_id=$3, provider_message_id=NULLIF($4,''), accepted_at=now(), last_error=NULL,
		lease_owner=NULL, lease_expires_at=NULL, updated_at=now()
		WHERE id=$1 AND lease_owner=$2`, j.ID, w.ID, providerID, providerMsgID)
	if err == nil && tag.RowsAffected() == 0 {
		return fmt.Errorf("job %s: lease lost", j.ID)
	}
	return err
}

// release puts the job back for an immediate (or near) re-pick, typically to
// let the next provider in the chain try.
func (w *Worker) release(ctx context.Context, j *Job, at time.Time, lastErr string) error {
	return w.reschedule(ctx, j, at, lastErr)
}

func (w *Worker) reschedule(ctx context.Context, j *Job, at time.Time, lastErr string) error {
	tag, err := w.DB.Exec(ctx, `UPDATE email_jobs SET status='retry_scheduled', next_attempt_at=$3,
		last_error=COALESCE(NULLIF($4,''), last_error), lease_owner=NULL, lease_expires_at=NULL, updated_at=now()
		WHERE id=$1 AND lease_owner=$2 AND status='processing'`, j.ID, w.ID, at, truncate(lastErr, 1000))
	if err == nil && tag.RowsAffected() == 0 {
		return fmt.Errorf("job %s: lease lost", j.ID)
	}
	return err
}

func (w *Worker) finish(ctx context.Context, j *Job, status, reason string) error {
	_, err := w.DB.Exec(ctx, `UPDATE email_jobs SET status=$3, last_error=$4, finished_at=now(),
		lease_owner=NULL, lease_expires_at=NULL, updated_at=now()
		WHERE id=$1 AND lease_owner=$2 AND status='processing'`, j.ID, w.ID, status, reason)
	return err
}

func (w *Worker) deadLetter(ctx context.Context, j *Job, cycle int, reason string) error {
	w.Log.Error("email dead-lettered", "job_id", j.ID, "reason", email.RedactString(reason))
	_, err := w.DB.Exec(ctx, `UPDATE email_jobs SET status='dead_letter', cycle=$3, last_error=$4, finished_at=now(),
		lease_owner=NULL, lease_expires_at=NULL, updated_at=now()
		WHERE id=$1 AND lease_owner=$2 AND status='processing'`, j.ID, w.ID, cycle, truncate(reason, 1000))
	return err
}

func prefixed(p, cols string) string {
	parts := strings.Split(cols, ",")
	for i, c := range parts {
		c = strings.TrimSpace(c)
		if strings.HasPrefix(c, "coalesce(") {
			parts[i] = "coalesce(" + p + strings.TrimPrefix(c, "coalesce(")
		} else {
			parts[i] = p + c
		}
	}
	return strings.Join(parts, ", ")
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}

func firstNonEmpty(v ...string) string {
	for _, s := range v {
		if s != "" {
			return s
		}
	}
	return ""
}
