package acceptance

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/cadentic/portfolio-cms/backend/internal/testenv"
)

func brevoEvent(event, to, messageID, jobID string, at time.Time) map[string]any {
	return map[string]any{"event": event, "email": to, "message-id": messageID, "ts_epoch": at.UnixMilli(),
		"ts_event": at.Unix(), "X-Mailin-custom": jobID, "tags": []string{"job:" + jobID}}
}

// AC7: webhooks are authenticated, idempotent and safe out of order.
func TestAC07_WebhooksIdempotentOutOfOrder(t *testing.T) {
	e := testenv.New(t)
	id := e.Enqueue("reader@example.com", "ac7")
	e.Drain(3)
	pmid := e.Str(`SELECT provider_message_id FROM email_jobs WHERE id=$1`, id)
	now := time.Now()

	if r := e.Webhook(e.BrevoID, brevoEvent("delivered", "reader@example.com", pmid, id, now), ""); r.Status != http.StatusUnauthorized {
		t.Fatalf("unauthenticated webhook: %d, want 401", r.Status)
	}
	if r := e.Webhook(e.BrevoID, brevoEvent("delivered", "reader@example.com", pmid, id, now), "wrong"); r.Status != http.StatusUnauthorized {
		t.Fatalf("bad secret: %d, want 401", r.Status)
	}

	delivered := brevoEvent("delivered", "reader@example.com", pmid, id, now)
	for i := 0; i < 3; i++ { // providers retry deliveries
		if r := e.Webhook(e.BrevoID, delivered, testenv.WebhookSecret); r.Status != 200 {
			t.Fatalf("webhook: %d %s", r.Status, r.Body)
		}
	}
	if n := e.Count(`SELECT count(*) FROM email_provider_events WHERE job_id=$1`, id); n != 1 {
		t.Fatalf("stored %d events for 3 identical deliveries, want 1", n)
	}
	// An older "request" event arriving late must not move the status backwards.
	e.Webhook(e.BrevoID, brevoEvent("request", "reader@example.com", pmid, id, now.Add(-time.Minute)), testenv.WebhookSecret)
	e.Tick()
	if got := e.JobStatus("reader@example.com"); got != "delivered" {
		t.Fatalf("status = %s, want delivered", got)
	}
	// Replayed (very old) events are rejected.
	e.Webhook(e.BrevoID, brevoEvent("hard_bounce", "reader@example.com", pmid, id, now.Add(-30*24*time.Hour)), testenv.WebhookSecret)
	e.Tick()
	if e.Count(`SELECT count(*) FROM email_suppressions WHERE email='reader@example.com'`) != 0 {
		t.Fatal("stale replayed event was applied")
	}

	// Mailjet webhook format, delivered before the worker records acceptance:
	// the event waits and is applied afterwards.
	e.Exec(`UPDATE email_providers SET priority = CASE WHEN kind='mailjet' THEN 1 ELSE 50 END`)
	id2 := e.Enqueue("second@example.com", "ac7b")
	early := map[string]any{"event": "sent", "time": now.Unix(), "MessageID": 99, "Message_GUID": "g-1", "email": "second@example.com", "CustomID": id2}
	if r := e.Webhook(e.MailjetID, early, testenv.WebhookSecret); r.Status != 200 {
		t.Fatalf("mailjet webhook: %d %s", r.Status, r.Body)
	}
	e.Drain(3)
	e.Tick()
	if got := e.JobStatus("second@example.com"); got != "delivered" {
		t.Fatalf("early event not applied after acceptance: %s", got)
	}
}

// AC8: double opt-in, expired tokens and duplicate subscription requests.
func TestAC08_DoubleOptIn(t *testing.T) {
	e := testenv.New(t)
	sub := func(addr string) testenv.Response {
		return e.Do("POST", "/api/public/subscribe", map[string]any{"email": addr, "name": "Reader", "consent": true}, nil)
	}
	if r := e.Do("POST", "/api/public/subscribe", map[string]any{"email": "x@example.com", "consent": false}, nil); r.Status != 400 {
		t.Fatalf("missing consent accepted: %d", r.Status)
	}
	if r := sub("not-an-email"); r.Status != 400 {
		t.Fatalf("invalid address accepted: %d", r.Status)
	}
	if r := sub("Reader@Example.com"); r.Status != 202 {
		t.Fatalf("subscribe: %d %s", r.Status, r.Body)
	}
	if got := e.Str(`SELECT status FROM subscribers WHERE email='reader@example.com'`); got != "pending" {
		t.Fatalf("status before confirmation = %s, want pending", got)
	}
	// Pending subscribers are not eligible for marketing.
	if e.Count(`SELECT count(*) FROM eligible_marketing_subscribers`) != 0 {
		t.Fatal("pending subscriber is eligible for marketing")
	}
	tok := e.ConfirmToken("reader@example.com")
	// Only the SHA-256 of the token is stored.
	if e.Count(`SELECT count(*) FROM subscriber_verification_tokens WHERE token_hash = sha256(convert_to($1,'UTF8'))`, tok) != 1 ||
		e.Count(`SELECT count(*) FROM subscriber_verification_tokens WHERE position(convert_to($1,'UTF8') in token_hash) > 0`, tok) != 0 {
		t.Fatal("verification token not stored as a hash")
	}

	// A duplicate request answers identically (no enumeration) and does not
	// create a second subscriber; within the cooldown no new email is sent.
	before := e.Mock.Count("")
	r2 := sub("reader@example.com")
	if r2.Status != 202 || string(r2.Body) != string(sub("other@example.com").Body) {
		t.Fatal("duplicate subscription response differs from a new one")
	}
	e.Drain(3)
	if e.Count(`SELECT count(*) FROM subscribers WHERE email='reader@example.com'`) != 1 {
		t.Fatal("duplicate subscriber row")
	}
	if e.Mock.Count("") != before+1 { // only other@example.com's confirmation
		t.Fatalf("emails sent = %d, want %d", e.Mock.Count(""), before+1)
	}

	if r := e.Do("POST", "/api/public/subscribe/confirm", map[string]string{"token": tok}, nil); r.Status != 200 {
		t.Fatalf("confirm: %d %s", r.Status, r.Body)
	}
	if r := e.Do("POST", "/api/public/subscribe/confirm", map[string]string{"token": tok}, nil); r.Status == 200 && e.Count(`SELECT count(*) FROM subscriber_consents WHERE subscriber_id=(SELECT id FROM subscribers WHERE email='reader@example.com')`) > 2 {
		t.Fatal("token reused")
	}
	if got := e.Str(`SELECT status FROM subscribers WHERE email='reader@example.com'`); got != "verified" {
		t.Fatalf("status = %s, want verified", got)
	}
	if e.Count(`SELECT count(*) FROM eligible_marketing_subscribers WHERE email='reader@example.com'`) != 1 {
		t.Fatal("verified subscriber is not eligible")
	}

	// Expired token.
	tok2 := e.ConfirmToken("other@example.com")
	e.Exec(`UPDATE subscriber_verification_tokens SET expires_at = now() - interval '1 minute'`)
	if r := e.Do("POST", "/api/public/subscribe/confirm", map[string]string{"token": tok2}, nil); r.Status != http.StatusGone {
		t.Fatalf("expired token: %d, want 410", r.Status)
	}
	if r := e.Do("POST", "/api/public/subscribe/confirm", map[string]string{"token": "garbage"}, nil); r.Status != 400 {
		t.Fatalf("invalid token: %d", r.Status)
	}
	// Resending issues a fresh token and invalidates the old one.
	e.Exec(`UPDATE subscribers SET last_verification_sent_at = now() - interval '1 hour' WHERE email='other@example.com'`)
	e.Do("POST", "/api/public/subscribe/resend", map[string]string{"email": "other@example.com"}, nil)
	tok3 := e.ConfirmToken("other@example.com")
	if tok3 == tok2 {
		t.Fatal("resend reused the old token")
	}
	if r := e.Do("POST", "/api/public/subscribe/confirm", map[string]string{"token": tok3}, nil); r.Status != 200 {
		t.Fatalf("confirm fresh token: %d %s", r.Status, r.Body)
	}
}

// AC9: unsubscribe works from every marketing email and is enforced.
func TestAC09_UnsubscribeAndSuppression(t *testing.T) {
	e := testenv.New(t)
	subID := e.Subscribe("reader@example.com")
	e.Subscribe("stays@example.com")

	cid := createCampaign(t, e, "Issue 1")
	approve(t, e, cid)
	e.Drain(5)
	var unsubURL string
	for _, m := range e.Mock.Messages() {
		if m.To == "reader@example.com" && m.Subject == "Issue 1" {
			if !strings.Contains(m.HTML, "/unsubscribe?token=") {
				t.Fatal("marketing email has no unsubscribe link")
			}
			unsubURL = m.Headers["List-Unsubscribe"]
			if m.Headers["List-Unsubscribe-Post"] != "List-Unsubscribe=One-Click" {
				t.Fatal("missing RFC 8058 one-click header")
			}
		}
	}
	if unsubURL == "" {
		t.Fatal("campaign email not delivered with List-Unsubscribe")
	}
	start := strings.Index(unsubURL, "/api/public/unsubscribe/one-click?token=")
	path := strings.TrimSuffix(unsubURL[start:], ">")
	if r := e.Do("POST", path, "List-Unsubscribe=One-Click", map[string]string{"Content-Type": "application/x-www-form-urlencoded"}); r.Status != 200 {
		t.Fatalf("one-click unsubscribe: %d %s", r.Status, r.Body)
	}
	if got := e.Str(`SELECT status FROM subscribers WHERE id=$1`, subID); got != "unsubscribed" {
		t.Fatalf("status = %s", got)
	}

	cid2 := createCampaign(t, e, "Issue 2")
	approve(t, e, cid2)
	e.Drain(5)
	for _, m := range e.Mock.Messages() {
		if m.To == "reader@example.com" && m.Subject == "Issue 2" {
			t.Fatal("unsubscribed reader received a later campaign")
		}
	}
	// A manual suppression also blocks marketing, even for queued jobs.
	if r := e.Admin("POST", "/api/admin/suppressions", map[string]any{"email": "stays@example.com", "scope": "marketing", "reason": "manual"}); r.Status != 200 && r.Status != 201 {
		t.Fatalf("add suppression: %d %s", r.Status, r.Body)
	}
	cid3 := createCampaign(t, e, "Issue 3")
	if r := e.Admin("POST", "/api/admin/campaigns/"+cid3+"/audience", nil); r.Map()["count"].(float64) != 0 {
		t.Fatalf("suppressed address still in audience: %s", r.Body)
	}
	// Transactional mail to a marketing-suppressed address is still allowed.
	e.Enqueue("stays@example.com", "ac9-tx")
	e.Drain(3)
	if got := e.JobStatus("stays@example.com"); got != "accepted" {
		t.Fatalf("transactional to marketing-suppressed: %s", got)
	}
}

// AC10: hard bounces and complaints suppress the address.
func TestAC10_HardBounceAndComplaint(t *testing.T) {
	e := testenv.New(t)
	bounceID := e.Subscribe("bounce@example.com")
	e.Subscribe("complain@example.com")
	now := time.Now()

	for _, c := range []struct{ to, event string }{{"bounce@example.com", "hard_bounce"}, {"complain@example.com", "spam"}} {
		jobID := e.JobID(c.to)
		pmid := e.Str(`SELECT provider_message_id FROM email_jobs WHERE id=$1`, jobID)
		if r := e.Webhook(e.BrevoID, brevoEvent(c.event, c.to, pmid, jobID, now), testenv.WebhookSecret); r.Status != 200 {
			t.Fatalf("webhook: %d", r.Status)
		}
	}
	e.Tick()
	if e.Str(`SELECT scope FROM email_suppressions WHERE email='bounce@example.com' AND lifted_at IS NULL`) != "all" {
		t.Fatal("hard bounce should suppress all mail")
	}
	if e.Str(`SELECT scope FROM email_suppressions WHERE email='complain@example.com' AND lifted_at IS NULL`) != "marketing" {
		t.Fatal("complaint should suppress marketing")
	}
	if e.JobStatus("bounce@example.com") != "bounced" || e.JobStatus("complain@example.com") != "complained" {
		t.Fatalf("job statuses: %s / %s", e.JobStatus("bounce@example.com"), e.JobStatus("complain@example.com"))
	}
	if e.Count(`SELECT count(*) FROM eligible_marketing_subscribers`) != 0 {
		t.Fatal("bounced/complaining subscribers remain eligible")
	}
	// Even transactional mail to a hard-bounced address is not attempted.
	before := e.Mock.Count("")
	e.Enqueue("bounce@example.com", "ac10-tx")
	e.Drain(3)
	if e.Mock.Count("") != before || e.JobStatus("bounce@example.com") != "cancelled" ||
		!strings.Contains(e.Str(`SELECT last_error FROM email_jobs WHERE to_email=$1 ORDER BY created_at DESC LIMIT 1`, "bounce@example.com"), "suppressed") {
		t.Fatalf("mail sent to hard-bounced address (status %s)", e.JobStatus("bounce@example.com"))
	}
	_ = bounceID
}

// AC11: only the Firebase-authenticated owner reaches admin APIs, and provider
// secrets never leave the server.
func TestAC11_AdminAuthAndCredentialProtection(t *testing.T) {
	e := testenv.New(t)
	if r := e.Do("GET", "/api/admin/providers", nil, nil); r.Status != 401 {
		t.Fatalf("anonymous: %d, want 401", r.Status)
	}
	if r := e.Do("GET", "/api/admin/providers", nil, map[string]string{"Authorization": "Bearer not.a.jwt"}); r.Status != 401 {
		t.Fatalf("garbage token: %d, want 401", r.Status)
	}
	stranger := testenv.EmulatorToken("someone", "someone@example.com")
	if r := e.Do("GET", "/api/admin/providers", nil, map[string]string{"Authorization": "Bearer " + stranger}); r.Status != 403 {
		t.Fatalf("signed-in non-owner: %d, want 403", r.Status)
	}
	r := e.Admin("GET", "/api/admin/providers", nil)
	if r.Status != 200 {
		t.Fatalf("owner: %d %s", r.Status, r.Body)
	}
	for _, secret := range []string{"brevo-test-key", "mj-key", "mj-secret", testenv.WebhookSecret} {
		if strings.Contains(string(r.Body), secret) {
			t.Fatalf("provider API response leaks secret %q", secret)
		}
	}
	if !strings.Contains(string(r.Body), `"credentials_configured":true`) {
		t.Fatal("expected credentials_configured flag")
	}
	// Credentials can only reference EMAIL_* environment variables, so the
	// admin UI cannot be used to exfiltrate other secrets.
	bad := e.Admin("PATCH", "/api/admin/providers/"+e.BrevoID, map[string]any{"credential_refs": map[string]string{"api_key": "DATABASE_URL"}})
	if bad.Status != 400 {
		t.Fatalf("non-EMAIL_ credential ref accepted: %d", bad.Status)
	}
	// Sending provider tests and health checks go through the server only.
	if r := e.Admin("POST", "/api/admin/providers/"+e.BrevoID+"/health", nil); r.Status != 200 || strings.Contains(string(r.Body), "brevo-test-key") {
		t.Fatalf("health: %d %s", r.Status, r.Body)
	}
}

// AC12: publishing an article can create a notification campaign, which needs
// explicit approval and is never created or sent twice.
func TestAC12_BlogNotificationApprovalNoDuplicates(t *testing.T) {
	e := testenv.New(t)
	e.Subscribe("reader@example.com", "articles")
	before := e.Mock.Count("")

	page := e.Admin("POST", "/api/admin/pages", map[string]any{"kind": "article", "title": "Data Guard in practice",
		"excerpt": "Lessons from production.", "hero": map[string]any{"type": "youtube", "url": "https://youtu.be/dQw4w9WgXcQ"},
		"blocks": []map[string]any{{"type": "paragraph", "text": "Hello"}}})
	if page.Status != 200 && page.Status != 201 {
		t.Fatalf("create article: %d %s", page.Status, page.Body)
	}
	pid := page.Map()["id"].(string)

	pub := e.Admin("POST", "/api/admin/pages/"+pid+"/publish", map[string]any{"notify_subscribers": true})
	if pub.Status != 200 || pub.Map()["campaign_created"] != true {
		t.Fatalf("publish: %d %s", pub.Status, pub.Body)
	}
	cid := pub.Map()["campaign"].(map[string]any)["id"].(string)
	if got := e.Str(`SELECT status FROM email_campaigns WHERE id=$1`, cid); got != "draft" {
		t.Fatalf("campaign status = %s, want draft (needs approval)", got)
	}
	e.Drain(3)
	if e.Mock.Count("") != before {
		t.Fatal("notification sent without approval")
	}

	// Re-publishing does not create another notification.
	e.Admin("POST", "/api/admin/pages/"+pid+"/unpublish", nil)
	pub2 := e.Admin("POST", "/api/admin/pages/"+pid+"/publish", map[string]any{"notify_subscribers": true})
	if pub2.Map()["campaign_created"] == true || e.Count(`SELECT count(*) FROM email_campaigns WHERE blog_post_id=$1`, pid) != 1 {
		t.Fatalf("duplicate notification campaign: %s", pub2.Body)
	}

	// Preview and test send are available before approval.
	if r := e.Admin("GET", "/api/admin/campaigns/"+cid+"/preview", nil); r.Status != 200 || !strings.Contains(string(r.Body), "Data Guard in practice") {
		t.Fatalf("preview: %d %s", r.Status, r.Body)
	}
	if r := e.Admin("POST", "/api/admin/campaigns/"+cid+"/send", map[string]any{"confirm": false}); r.Status < 400 {
		t.Fatal("send without confirmation accepted")
	}
	if r := e.Admin("POST", "/api/admin/campaigns/"+cid+"/send", map[string]any{"confirm": true, "audience_hash": "stale", "expected_count": 1}); r.Status < 400 {
		t.Fatal("send with stale audience accepted")
	}
	approve(t, e, cid)
	if r := e.Admin("POST", "/api/admin/campaigns/"+cid+"/send", map[string]any{"confirm": true}); r.Status < 400 {
		t.Fatal("second approval accepted")
	}
	e.Drain(5)
	e.Tick()
	sent := 0
	for _, m := range e.Mock.Messages() {
		if m.To == "reader@example.com" && strings.Contains(m.Subject, "Data Guard") {
			sent++
		}
	}
	if sent != 1 {
		t.Fatalf("notification delivered %d times, want 1", sent)
	}
	if got := e.Str(`SELECT status FROM email_campaigns WHERE id=$1`, cid); got != "sent" {
		t.Fatalf("campaign status = %s", got)
	}
	// Approval is in the audit log.
	if e.Count(`SELECT count(*) FROM email_audit_logs WHERE action='campaign.approve_send' AND entity_id=$1`, cid) != 1 {
		t.Fatal("approval not audited")
	}
}

// AC6b: duplicate dispatcher runs never materialise a campaign twice.
func TestAC06b_ConcurrentCampaignDispatch(t *testing.T) {
	e := testenv.New(t)
	e.Exec(`UPDATE email_providers SET rate_limit_per_second = 1000`)
	for _, a := range []string{"a@example.com", "b@example.com", "c@example.com"} {
		e.Subscribe(a)
	}
	cid := createCampaign(t, e, "Concurrent")
	approve(t, e, cid)
	done := make(chan struct{})
	for i := 0; i < 4; i++ {
		go func() { _ = e.Campaigns.Dispatch(e.Ctx); done <- struct{}{} }()
	}
	for i := 0; i < 4; i++ {
		<-done
	}
	if n := e.Count(`SELECT count(*) FROM email_jobs WHERE campaign_id=$1`, cid); n != 3 {
		t.Fatalf("campaign jobs = %d, want 3", n)
	}
	e.Drain(5)
	if n := e.Count(`SELECT count(*) FROM email_campaign_recipients WHERE campaign_id=$1`, cid); n != 3 {
		t.Fatalf("recipients = %d", n)
	}
}

// AC13: provider order, enablement and credentials change at runtime.
func TestAC13_ProviderConfigWithoutRedeploy(t *testing.T) {
	e := testenv.New(t)
	r := e.Admin("PATCH", "/api/admin/providers/"+e.BrevoID, map[string]any{"enabled": false})
	if r.Status != 200 {
		t.Fatalf("disable brevo: %d %s", r.Status, r.Body)
	}
	e.Enqueue("one@example.com", "ac13a")
	e.Drain(3)
	if e.Mock.Count("mailjet") != 1 || e.Mock.Count("brevo") != 0 {
		t.Fatal("disabled provider still used")
	}
	e.Admin("PATCH", "/api/admin/providers/"+e.BrevoID, map[string]any{"enabled": true})
	if r := e.Admin("POST", "/api/admin/providers/reorder", map[string]any{"ids": []string{e.MailjetID, e.BrevoID}}); r.Status != 200 {
		t.Fatalf("reorder: %d %s", r.Status, r.Body)
	}
	e.Enqueue("two@example.com", "ac13b")
	e.Drain(3)
	if e.Mock.Count("mailjet") != 2 {
		t.Fatal("new priority order not applied")
	}
	// A provider whose secret is missing from the environment is skipped.
	delete(e.Secrets, "EMAIL_MAILJET_API_KEY")
	e.Enqueue("three@example.com", "ac13c")
	e.Drain(3)
	if e.Mock.Count("brevo") != 1 {
		t.Fatal("provider without credentials was not skipped")
	}
	// Delivery settings (retry policy) are editable at runtime too.
	if r := e.Admin("PUT", "/api/admin/settings/delivery", map[string]any{"max_cycles": 3}); r.Status != 200 {
		t.Fatalf("update delivery settings: %d %s", r.Status, r.Body)
	}
	ds, _ := e.Settings.Delivery(e.Ctx)
	if ds.MaxCycles != 3 {
		t.Fatalf("max cycles = %d", ds.MaxCycles)
	}
}

// --- helpers ------------------------------------------------------------------

func createCampaign(t *testing.T, e *testenv.Env, subject string) string {
	t.Helper()
	r := e.Admin("POST", "/api/admin/campaigns", map[string]any{"name": subject, "subject": subject,
		"content_html": "<p>Hello {{first_name}}</p>"})
	if r.Status != 200 && r.Status != 201 {
		t.Fatalf("create campaign: %d %s", r.Status, r.Body)
	}
	return r.Map()["id"].(string)
}

func approve(t *testing.T, e *testenv.Env, cid string) {
	t.Helper()
	aud := e.Admin("POST", "/api/admin/campaigns/"+cid+"/audience", nil).Map()
	r := e.Admin("POST", "/api/admin/campaigns/"+cid+"/send", map[string]any{"confirm": true,
		"audience_hash": aud["audience_hash"], "expected_count": aud["count"], "allow_similar_recent": true})
	if r.Status != 200 {
		t.Fatalf("approve: %d %s", r.Status, r.Body)
	}
}
