package acceptance

import (
	"strings"
	"testing"

	"github.com/cadentic/portfolio-cms/backend/internal/testenv"
)

// A contact message with a blocked word is rejected with a message telling the
// sender to stop, nothing is stored or emailed, and the owner can edit the list.
func TestContactBlockedWords(t *testing.T) {
	e := testenv.New(t)

	add := e.Admin("POST", "/api/admin/contact-blocked-words", map[string]string{"word": "  Cheap   Pills "})
	if add.Status != 201 {
		t.Fatalf("add blocked word = %d %s", add.Status, add.Body)
	}
	id, _ := add.Map()["id"].(string)
	if dup := e.Admin("POST", "/api/admin/contact-blocked-words", map[string]string{"word": "cheap pills"}); dup.Status != 409 {
		t.Fatalf("duplicate word = %d, want 409", dup.Status)
	}

	spam := e.Do("POST", "/api/public/contact", map[string]string{
		"name": "Bot", "email": "bot@example.com", "message": "Buy CHEAP pills today, best prices!"}, nil)
	if spam.Status != 422 {
		t.Fatalf("spam contact = %d %s, want 422", spam.Status, spam.Body)
	}
	if msg, _ := spam.Map()["error"].(string); !strings.Contains(msg, "Please stop") {
		t.Fatalf("spam contact message = %q", msg)
	}
	if n := e.Count(`SELECT count(*) FROM contact_messages`); n != 0 {
		t.Fatalf("contact_messages = %d, want 0 after blocked submission", n)
	}
	if n := e.Count(`SELECT count(*) FROM email_jobs`); n != 0 {
		t.Fatalf("email_jobs = %d, want 0 after blocked submission", n)
	}
	if n := e.Count(`SELECT hits FROM contact_blocked_words WHERE id = $1`, id); n != 1 {
		t.Fatalf("hits = %d, want 1", n)
	}

	// The seeded starter list is active too.
	if r := e.Do("POST", "/api/public/contact", map[string]string{
		"name": "Bot", "email": "bot2@example.com", "message": "Visit our online casino for free spins"}, nil); r.Status != 422 {
		t.Fatalf("seeded word contact = %d, want 422", r.Status)
	}

	list := e.Admin("GET", "/api/admin/contact-blocked-words", nil)
	if list.Status != 200 || !strings.Contains(string(list.Body), "Cheap Pills") {
		t.Fatalf("list = %d %s", list.Status, list.Body)
	}
	if del := e.Admin("DELETE", "/api/admin/contact-blocked-words/"+id, nil); del.Status != 204 {
		t.Fatalf("delete = %d", del.Status)
	}

	ok := e.Do("POST", "/api/public/contact", map[string]string{
		"name": "Ana", "email": "ana@example.com", "message": "Buy cheap pills? No, I need an Oracle DBA, a specialist."}, nil)
	if ok.Status != 202 {
		t.Fatalf("genuine contact = %d %s, want 202", ok.Status, ok.Body)
	}
	if n := e.Count(`SELECT count(*) FROM contact_messages`); n != 1 {
		t.Fatalf("contact_messages = %d, want 1", n)
	}
}

// A genuine message waits in the admin: only the sender's acknowledgement is
// queued. The owner's inbox gets it when the owner forwards it, exactly once.
func TestContactReviewThenForward(t *testing.T) {
	e := testenv.New(t)
	e.Server.Cfg.AdminAlertEmail = testenv.AdminEmail

	r := e.Do("POST", "/api/public/contact", map[string]string{
		"name": "Ana", "email": "ana@example.com", "message": "Could you help with an Oracle 19c upgrade?"}, nil)
	if r.Status != 202 {
		t.Fatalf("contact = %d %s", r.Status, r.Body)
	}
	id := e.Str(`SELECT id::text FROM contact_messages`)
	if n := e.Count(`SELECT count(*) FROM email_jobs WHERE idempotency_key LIKE 'contact_notify:%'`); n != 0 {
		t.Fatalf("owner emailed before review: %d jobs", n)
	}
	if n := e.Count(`SELECT count(*) FROM email_jobs WHERE idempotency_key = $1`, "contact_ack:"+id); n != 1 {
		t.Fatalf("ack jobs = %d, want 1", n)
	}

	list := e.Admin("GET", "/api/admin/contact-messages?status=new", nil)
	if list.Status != 200 || !strings.Contains(string(list.Body), id) {
		t.Fatalf("new messages = %d %s", list.Status, list.Body)
	}

	if f := e.Admin("POST", "/api/admin/contact-messages/"+id+"/forward", nil); f.Status != 200 {
		t.Fatalf("forward = %d %s", f.Status, f.Body)
	}
	if got := e.Str(`SELECT to_email::text FROM email_jobs WHERE idempotency_key = $1`, "contact_notify:"+id); got != testenv.AdminEmail {
		t.Fatalf("forwarded to %q, want %q", got, testenv.AdminEmail)
	}
	if st := e.Str(`SELECT status FROM contact_messages WHERE id = $1`, id); st != "read" {
		t.Fatalf("status after forward = %q, want read", st)
	}
	if f := e.Admin("POST", "/api/admin/contact-messages/"+id+"/forward", nil); f.Status != 409 {
		t.Fatalf("second forward = %d, want 409", f.Status)
	}
	if f := e.Admin("POST", "/api/admin/contact-messages/00000000-0000-0000-0000-000000000000/forward", nil); f.Status != 404 {
		t.Fatalf("unknown forward = %d, want 404", f.Status)
	}
	if a := e.Admin("POST", "/api/admin/contact-messages/"+id+"/archive", nil); a.Status != 200 {
		t.Fatalf("archive = %d", a.Status)
	}
	if n := e.Count(`SELECT count(*) FROM email_jobs WHERE idempotency_key LIKE 'contact_notify:%'`); n != 1 {
		t.Fatalf("notify jobs = %d, want 1", n)
	}
}
