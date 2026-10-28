package acceptance

import (
	"testing"

	"github.com/cadentic/portfolio-cms/backend/internal/retention"
	"github.com/cadentic/portfolio-cms/backend/internal/testenv"
)

// The privacy notice promises deletion periods; this proves the purge honours
// them without touching data that is still inside its period.
func TestRetentionPurge(t *testing.T) {
	e := testenv.New(t)
	e.Exec(`INSERT INTO resume_otps (email, code_hash, expires_at, created_at) VALUES
		('old@example.com', '\x00', now() - interval '31 days', now() - interval '31 days'),
		('new@example.com', '\x00', now() + interval '10 minutes', now())`)
	e.Exec(`INSERT INTO contact_messages (name, email, message, ip, created_at) VALUES
		('Old', 'old@example.com', 'hi', '203.0.113.9', now() - interval '25 months'),
		('Mid', 'mid@example.com', 'hi', '203.0.113.9', now() - interval '40 days'),
		('New', 'new@example.com', 'hi', '203.0.113.9', now())`)

	got, err := retention.Purge(e.Ctx, e.DB)
	if err != nil {
		t.Fatal(err)
	}
	if got["resume_otps"] != 1 || got["contact_messages"] != 1 {
		t.Fatalf("unexpected purge counts: %v", got)
	}
	if n := e.Count(`SELECT count(*) FROM resume_otps`); n != 1 {
		t.Fatalf("resume_otps left = %d, want 1", n)
	}
	if n := e.Count(`SELECT count(*) FROM contact_messages`); n != 2 {
		t.Fatalf("contact_messages left = %d, want 2", n)
	}
	if n := e.Count(`SELECT count(*) FROM contact_messages WHERE ip IS NOT NULL`); n != 1 {
		t.Fatalf("contact message IPs kept = %d, want only the recent one", n)
	}
}
