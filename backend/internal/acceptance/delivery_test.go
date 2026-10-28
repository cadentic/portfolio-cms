// Package acceptance holds end-to-end tests for the specification's
// acceptance criteria. They run the real HTTP API, worker and Brevo/Mailjet
// adapters against PostgreSQL and the mock providers (see internal/testenv).
package acceptance

import (
	"sync"
	"testing"
	"time"

	"github.com/cadentic/portfolio-cms/backend/internal/testenv"
)

// AC1: a message is submitted through Brevo, the primary provider.
func TestAC01_DeliveryThroughBrevo(t *testing.T) {
	e := testenv.New(t)
	id := e.Enqueue("reader@example.com", "ac1")
	e.Drain(3)

	if got := e.JobStatus("reader@example.com"); got != "accepted" {
		t.Fatalf("status = %s, want accepted", got)
	}
	if got := e.Str(`SELECT accepted_provider_id::text FROM email_jobs WHERE id=$1`, id); got != e.BrevoID {
		t.Fatalf("accepted by %s, want Brevo", got)
	}
	if e.Mock.Count("brevo") != 1 || e.Mock.Count("mailjet") != 0 {
		t.Fatalf("brevo=%d mailjet=%d, want 1/0", e.Mock.Count("brevo"), e.Mock.Count("mailjet"))
	}
	if e.Str(`SELECT coalesce(provider_message_id,'') FROM email_jobs WHERE id=$1`, id) == "" {
		t.Fatal("provider message id not recorded")
	}
}

// AC2: an explicit Brevo failure is followed by a successful Mailjet submission.
func TestAC02_BrevoFailureFailsOverToMailjet(t *testing.T) {
	for _, mode := range []string{"error500", "auth"} {
		t.Run(mode, func(t *testing.T) {
			e := testenv.New(t)
			e.Mock.SetMode("brevo", mode)
			id := e.Enqueue("reader@example.com", "ac2")
			e.Drain(10)

			if got := e.JobStatus("reader@example.com"); got != "accepted" {
				t.Fatalf("status = %s, want accepted", got)
			}
			if got := e.Str(`SELECT accepted_provider_id::text FROM email_jobs WHERE id=$1`, id); got != e.MailjetID {
				t.Fatalf("accepted by %s, want Mailjet", got)
			}
			if e.Count(`SELECT failover_count FROM email_jobs WHERE id=$1`, id) != 1 {
				t.Fatal("failover not recorded")
			}
			if n := e.Mock.Deliveries(id); n != 1 {
				t.Fatalf("message accepted %d times, want exactly once", n)
			}
			// Each attempt is in the audit trail with its provider and outcome.
			if e.Count(`SELECT count(*) FROM email_attempts WHERE job_id=$1 AND provider_id=$2 AND outcome<>'accepted'`, id, e.BrevoID) == 0 {
				t.Fatal("brevo failure attempts not recorded")
			}
		})
	}
}

// AC3: when both providers fail, the message stays durably queued and a later
// retry succeeds.
func TestAC03_BothFailThenLaterRetry(t *testing.T) {
	e := testenv.New(t)
	e.Mock.SetMode("brevo", "error500")
	e.Mock.SetMode("mailjet", "error500")
	id := e.Enqueue("reader@example.com", "ac3")
	for i := 0; i < 6; i++ {
		e.Advance()
		e.Tick()
		if e.JobStatus("reader@example.com") == "retry_scheduled" && e.Count(`SELECT cycle FROM email_jobs WHERE id=$1`, id) >= 1 {
			break
		}
	}
	if got := e.JobStatus("reader@example.com"); got != "retry_scheduled" {
		t.Fatalf("status = %s, want retry_scheduled", got)
	}
	if e.Count(`SELECT cycle FROM email_jobs WHERE id=$1`, id) < 1 {
		t.Fatal("expected a completed provider cycle")
	}
	// The retry is pushed into the future (cycle backoff), not hot-looped.
	if e.Count(`SELECT count(*) FROM email_jobs WHERE id=$1 AND next_attempt_at > now() + interval '1 second'`, id) != 1 {
		t.Fatal("next attempt is not delayed by backoff")
	}
	if e.Count(`SELECT count(*) FROM admin_alerts WHERE kind='all_providers_failed'`) != 1 {
		t.Fatal("all-providers-failed alert not raised")
	}

	// A worker restart loses nothing: the job is in the database.
	e.Worker = e.NewWorker("worker-restarted")
	e.Mock.SetMode("brevo", "ok")
	e.Drain(5)
	if got := e.JobStatus("reader@example.com"); got != "accepted" {
		t.Fatalf("after recovery status = %s, want accepted", got)
	}
	if e.Mock.Deliveries(id) != 1 {
		t.Fatalf("delivered %d times", e.Mock.Deliveries(id))
	}
}

// AC3b: permanent rejection by every provider dead-letters; a controlled
// replay sends it once the problem is fixed.
func TestAC03b_DeadLetterAndReplay(t *testing.T) {
	e := testenv.New(t)
	e.Mock.SetMode("brevo", "auth")
	e.Mock.SetMode("mailjet", "auth")
	id := e.Enqueue("reader@example.com", "ac3b")
	e.Drain(6)
	if got := e.JobStatus("reader@example.com"); got != "dead_letter" {
		t.Fatalf("status = %s, want dead_letter", got)
	}
	e.Mock.SetMode("brevo", "ok")
	e.Mock.SetMode("mailjet", "ok")
	e.Exec(`UPDATE email_providers SET circuit_state='closed', consecutive_failures=0`)

	r := e.Admin("POST", "/api/admin/email/jobs/replay-dead-letters", map[string]any{"confirm": true, "ids": []string{id}})
	if r.Status != 200 {
		t.Fatalf("replay: %d %s", r.Status, r.Body)
	}
	e.Drain(4)
	if got := e.JobStatus("reader@example.com"); got != "accepted" {
		t.Fatalf("after replay status = %s", got)
	}
	if e.Count(`SELECT count(*) FROM email_audit_logs WHERE action LIKE 'email_job.replay%'`) == 0 {
		t.Fatal("replay not audited")
	}
}

// AC4: rate limits and transient errors back off exponentially; a 429's
// Retry-After is honoured.
func TestAC04_RateLimitTransientBackoff(t *testing.T) {
	e := testenv.New(t)
	e.Mock.SetMode("brevo", "ratelimit")
	e.Mock.SetMode("mailjet", "ratelimit")
	id := e.Enqueue("reader@example.com", "ac4")
	e.Tick()
	if got := e.Str(`SELECT outcome FROM email_attempts WHERE job_id=$1 ORDER BY attempt_number LIMIT 1`, id); got != "rate_limited" {
		t.Fatalf("first outcome = %s, want rate_limited", got)
	}
	// The mock sends Retry-After: 5, longer than the configured per-provider
	// retry ceiling, so the worker fails over at once instead of waiting.
	if e.Count(`SELECT count(*) FROM email_providers WHERE id=$1 AND last_rate_limited_at IS NOT NULL`, e.BrevoID) != 1 {
		t.Fatal("provider rate limit not recorded")
	}
	for i := 0; i < 4; i++ {
		e.Tick()
	}
	if e.Count(`SELECT count(*) FROM email_attempts WHERE job_id=$1 AND provider_id=$2 AND outcome='rate_limited'`, id, e.MailjetID) == 0 {
		t.Fatal("did not fail over to Mailjet while Brevo was rate limited")
	}
	// Both rate limited: the job waits (backoff) rather than hammering either API.
	if got := e.JobStatus("reader@example.com"); got != "retry_scheduled" {
		t.Fatalf("status = %s, want retry_scheduled", got)
	}
	if e.Count(`SELECT count(*) FROM email_jobs WHERE id=$1 AND next_attempt_at >= now() + interval '2 seconds'`, id) != 1 {
		t.Fatal("job not delayed after every provider rate limited it")
	}
	attempts := e.Count(`SELECT count(*) FROM email_attempts WHERE job_id=$1`, id)
	e.Tick()
	if e.Count(`SELECT count(*) FROM email_attempts WHERE job_id=$1`, id) != attempts {
		t.Fatal("job retried before its backoff elapsed")
	}
	e.Mock.SetMode("brevo", "ok")
	e.Mock.SetMode("mailjet", "ok")
	e.Drain(5)
	if got := e.JobStatus("reader@example.com"); got != "accepted" {
		t.Fatalf("status = %s", got)
	}
}

// AC5: a timeout with unknown acceptance must not trigger an immediate
// duplicate send, on either provider.
func TestAC05_UnknownAcceptanceNoDuplicate(t *testing.T) {
	t.Run("brevo-idempotency-key", func(t *testing.T) {
		e := testenv.New(t)
		e.Mock.SetMode("brevo", "unknown") // Brevo stores the email, then answers 504
		id := e.Enqueue("reader@example.com", "ac5a")
		e.Tick()
		if got := e.JobStatus("reader@example.com"); got != "acceptance_unknown" {
			t.Fatalf("status = %s, want acceptance_unknown", got)
		}
		// No failover to Mailjet and no immediate resend.
		e.Tick()
		e.Tick()
		if e.Mock.Count("mailjet") != 0 || e.Mock.Deliveries(id) != 1 {
			t.Fatalf("duplicate risk: mailjet=%d deliveries=%d", e.Mock.Count("mailjet"), e.Mock.Deliveries(id))
		}
		// Reconciliation re-submits with the same idempotency key; Brevo
		// recognises it and nothing is sent twice.
		e.Mock.SetMode("brevo", "ok")
		e.Advance()
		e.Tick()
		if got := e.JobStatus("reader@example.com"); got != "accepted" {
			t.Fatalf("after reconcile status = %s", got)
		}
		if n := e.Mock.Deliveries(id); n != 1 {
			t.Fatalf("delivered %d times, want 1", n)
		}
	})

	t.Run("mailjet-lookup", func(t *testing.T) {
		e := testenv.New(t)
		e.Exec(`UPDATE email_providers SET priority = CASE WHEN kind='mailjet' THEN 1 ELSE 50 END`)
		e.Mock.SetMode("mailjet", "unknown")
		id := e.Enqueue("reader@example.com", "ac5b")
		e.Tick()
		if got := e.JobStatus("reader@example.com"); got != "acceptance_unknown" {
			t.Fatalf("status = %s", got)
		}
		e.Mock.SetMode("mailjet", "ok")
		e.Advance()
		e.Tick() // looks the message up by CustomID instead of resending
		if got := e.JobStatus("reader@example.com"); got != "accepted" {
			t.Fatalf("after reconcile status = %s", got)
		}
		if n := e.Mock.Deliveries(id); n != 1 || e.Mock.Count("brevo") != 0 {
			t.Fatalf("deliveries=%d brevo=%d, want 1/0", n, e.Mock.Count("brevo"))
		}
	})

	t.Run("timeout-not-found-waits-for-grace", func(t *testing.T) {
		e := testenv.New(t)
		e.Exec(`UPDATE email_providers SET priority = CASE WHEN kind='mailjet' THEN 1 ELSE 50 END`)
		e.Mock.SetMode("mailjet", "timeout") // request never answered, never stored
		id := e.Enqueue("reader@example.com", "ac5c")
		start := time.Now()
		e.Tick()
		if time.Since(start) > 10*time.Second {
			t.Fatal("send timeout not enforced")
		}
		if got := e.JobStatus("reader@example.com"); got != "acceptance_unknown" {
			t.Fatalf("status = %s", got)
		}
		e.Mock.SetMode("mailjet", "ok")
		e.Advance()
		e.Tick() // lookup: not found, but still inside the grace period -> no resend
		if e.Mock.Deliveries(id) != 0 {
			t.Fatal("resent before the grace period elapsed")
		}
		if got := e.JobStatus("reader@example.com"); got != "acceptance_unknown" {
			t.Fatalf("status = %s, want still unknown", got)
		}
		// After the grace period the message is resent exactly once.
		e.Exec(`UPDATE email_jobs SET unknown_since = now() - interval '2 hours' WHERE id=$1`, id)
		e.Advance()
		e.Tick()
		e.Drain(4)
		if got := e.JobStatus("reader@example.com"); got != "accepted" {
			t.Fatalf("status = %s", got)
		}
		if e.Mock.Deliveries(id) != 1 {
			t.Fatalf("deliveries = %d", e.Mock.Deliveries(id))
		}
	})
}

// AC5b: enqueueing twice with the same idempotency key yields one job.
func TestAC05b_EnqueueIdempotent(t *testing.T) {
	e := testenv.New(t)
	a := e.Enqueue("reader@example.com", "same-key")
	b := e.Enqueue("reader@example.com", "same-key")
	if a != b || e.Count(`SELECT count(*) FROM email_jobs`) != 1 {
		t.Fatalf("duplicate job created: %s %s", a, b)
	}
}

// AC14: a worker that dies mid-request leaves the job in acceptance_unknown
// (never blindly resent); a worker that dies before sending is retried.
// Database connections dropped mid-run are re-established.
func TestAC14_RecoveryAfterRestartAndInterruption(t *testing.T) {
	e := testenv.New(t)
	inflight := e.Enqueue("inflight@example.com", "ac14a")
	notSent := e.Enqueue("notsent@example.com", "ac14b")
	// Simulate a crashed worker: both jobs leased, one with a request in flight.
	e.Exec(`UPDATE email_jobs SET status='processing', lease_owner='dead-worker', lease_expires_at=now() - interval '1 minute',
		current_provider_id=$1 WHERE id IN ($2,$3)`, e.BrevoID, inflight, notSent)
	e.Exec(`INSERT INTO email_attempts (job_id, provider_id, provider_kind, provider_name, attempt_number, provider_attempt, cycle, worker_id) VALUES ($1,$2,'brevo','Brevo',1,1,0,'dead-worker')`, inflight, e.BrevoID)

	e.Worker = e.NewWorker("worker-after-restart")
	e.Tick()
	if got := e.JobStatus("inflight@example.com"); got != "acceptance_unknown" {
		t.Fatalf("in-flight job status = %s, want acceptance_unknown", got)
	}
	if e.Mock.Deliveries(inflight) != 0 {
		t.Fatal("in-flight job was resent without reconciliation")
	}
	e.Drain(3)
	if got := e.JobStatus("notsent@example.com"); got != "accepted" {
		t.Fatalf("not-sent job status = %s, want accepted", got)
	}

	// Database interruption: kill every pooled connection, then keep working.
	e.Exec(`SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE datname = current_database() AND pid <> pg_backend_pid()`)
	id := e.Enqueue("after-db-blip@example.com", "ac14c")
	for i := 0; i < 5 && e.Mock.Deliveries(id) == 0; i++ {
		e.Advance()
		_, _ = e.Worker.Tick(e.Ctx) // the first tick may hit a dead connection
	}
	if e.Mock.Deliveries(id) != 1 {
		t.Fatal("worker did not recover after database interruption")
	}
}

// AC6: several workers running at once never process a job or a campaign
// twice.
func TestAC06_ConcurrentWorkersNoDuplicates(t *testing.T) {
	e := testenv.New(t)
	e.Exec(`UPDATE email_providers SET rate_limit_per_second = 1000`)
	var ids []string
	for i := 0; i < 25; i++ {
		ids = append(ids, e.Enqueue(testenv.Sprint("r", i, "@example.com"), testenv.Sprint("ac6-", i)))
	}
	workers := []*testenvWorker{{e.NewWorker("w1")}, {e.NewWorker("w2")}, {e.NewWorker("w3")}}
	var wg sync.WaitGroup
	for _, w := range workers {
		wg.Add(1)
		go func(w *testenvWorker) {
			defer wg.Done()
			for i := 0; i < 40 && e.Count(`SELECT count(*) FROM email_jobs WHERE status<>'accepted'`) > 0; i++ {
				_, _ = e.DB.Exec(e.Ctx, `UPDATE email_jobs SET next_attempt_at=now() WHERE status='retry_scheduled'`)
				_, _ = w.Tick(e.Ctx)
				time.Sleep(50 * time.Millisecond) // let the per-provider rate limiter refill
			}
		}(w)
	}
	wg.Wait()
	for _, id := range ids {
		if n := e.Mock.Deliveries(id); n != 1 {
			t.Fatalf("job %s delivered %d times (status %s, %s)", id, n, e.Str(`SELECT status FROM email_jobs WHERE id=$1`, id), e.Str(`SELECT coalesce(last_error,'') FROM email_jobs WHERE id=$1`, id))
		}
	}
}
