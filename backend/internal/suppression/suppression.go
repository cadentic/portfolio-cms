// Package suppression maintains email_suppressions and the matching subscriber
// state. Marketing-scope suppressions block newsletters only; 'all' scope
// (hard bounces, invalid addresses) blocks every email because the address
// cannot receive mail.
package suppression

import (
	"context"
	"time"

	"github.com/cadentic/portfolio-cms/backend/internal/store"
)

const (
	ScopeMarketing = "marketing"
	ScopeAll       = "all"
)

// Add inserts an active suppression unless an identical one exists.
func Add(ctx context.Context, db store.DB, emailAddr, scope, reason, source string, providerEventID *string, note string) error {
	_, err := db.Exec(ctx, `INSERT INTO email_suppressions (email, scope, reason, source, provider_event_id, note)
		VALUES ($1,$2,$3,$4,$5,NULLIF($6,''))
		ON CONFLICT (email, scope, reason) WHERE lifted_at IS NULL DO NOTHING`,
		emailAddr, scope, reason, source, providerEventID, note)
	return err
}

// Unsubscribe withdraws marketing consent: subscriber status, consent history
// and a marketing suppression, in one transaction-friendly call.
func Unsubscribe(ctx context.Context, db store.DB, emailAddr, source, actor string) error {
	if _, err := db.Exec(ctx, `UPDATE subscribers SET status='unsubscribed', unsubscribed_at=now(), updated_at=now()
		WHERE email=$1 AND status <> 'unsubscribed'`, emailAddr); err != nil {
		return err
	}
	if err := withdrawConsent(ctx, db, emailAddr, source, actor); err != nil {
		return err
	}
	return Add(ctx, db, emailAddr, ScopeMarketing, "unsubscribed", source, nil, "")
}

// HardBounce suppresses the address for every category.
func HardBounce(ctx context.Context, db store.DB, emailAddr, source string, eventID *string, reason string) error {
	if _, err := db.Exec(ctx, `UPDATE subscribers SET status='bounced', updated_at=now() WHERE email=$1`, emailAddr); err != nil {
		return err
	}
	return Add(ctx, db, emailAddr, ScopeAll, reason, source, eventID, "")
}

// Complaint (spam report) stops marketing but keeps essential transactional mail possible.
func Complaint(ctx context.Context, db store.DB, emailAddr, source string, eventID *string) error {
	if _, err := db.Exec(ctx, `UPDATE subscribers SET status='complained', updated_at=now() WHERE email=$1`, emailAddr); err != nil {
		return err
	}
	if err := withdrawConsent(ctx, db, emailAddr, source, "provider"); err != nil {
		return err
	}
	return Add(ctx, db, emailAddr, ScopeMarketing, "complaint", source, eventID, "")
}

func withdrawConsent(ctx context.Context, db store.DB, emailAddr, source, actor string) error {
	_, err := db.Exec(ctx, `INSERT INTO subscriber_consents (subscriber_id, action, source, actor)
		SELECT id, 'withdrawn', $2, $3 FROM subscribers WHERE email=$1`, emailAddr, source, actor)
	return err
}

type Suppression struct {
	ID        string     `json:"id"`
	Email     string     `json:"email"`
	Scope     string     `json:"scope"`
	Reason    string     `json:"reason"`
	Source    string     `json:"source"`
	Note      string     `json:"note"`
	CreatedAt time.Time  `json:"created_at"`
	LiftedAt  *time.Time `json:"lifted_at"`
	LiftedBy  string     `json:"lifted_by"`
}

func List(ctx context.Context, db store.DB, q, reason string, active bool, limit, offset int) ([]Suppression, int, error) {
	var total int
	where := `WHERE ($1 = '' OR email ILIKE '%' || $1 || '%') AND ($2 = '' OR reason = $2) AND (NOT $3 OR lifted_at IS NULL)`
	if err := db.QueryRow(ctx, `SELECT count(*) FROM email_suppressions `+where, q, reason, active).Scan(&total); err != nil {
		return nil, 0, err
	}
	rows, err := db.Query(ctx, `SELECT id, email, scope, reason, source, coalesce(note,''), created_at, lifted_at, coalesce(lifted_by,'')
		FROM email_suppressions `+where+` ORDER BY created_at DESC LIMIT $4 OFFSET $5`, q, reason, active, limit, offset)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	out := []Suppression{}
	for rows.Next() {
		var s Suppression
		if err := rows.Scan(&s.ID, &s.Email, &s.Scope, &s.Reason, &s.Source, &s.Note, &s.CreatedAt, &s.LiftedAt, &s.LiftedBy); err != nil {
			return nil, 0, err
		}
		out = append(out, s)
	}
	return out, total, rows.Err()
}

// Lift deactivates a suppression. Lifting does not re-subscribe anyone; a
// fresh double opt-in is still required for marketing.
func Lift(ctx context.Context, db store.DB, id, actor string) error {
	_, err := db.Exec(ctx, `UPDATE email_suppressions SET lifted_at=now(), lifted_by=$2 WHERE id=$1 AND lifted_at IS NULL`, id, actor)
	return err
}
