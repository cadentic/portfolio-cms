package api

import (
	"errors"
	"net/http"

	"github.com/cadentic/portfolio-cms/backend/internal/email"
	"github.com/cadentic/portfolio-cms/backend/internal/mailer"
	"github.com/jackc/pgx/v5"
)

// forwardContactMessage sends a reviewed contact message to the owner's inbox
// through the email queue (Brevo first, Mailjet as fallback) and marks it read.
func (s *Server) forwardContactMessage(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	owner := s.ownerEmail(r)
	if owner == "" {
		writeError(w, 400, "no inbox to forward to: set ADMIN_ALERT_EMAIL or a reply-to address in Email settings")
		return
	}
	tx, err := s.DB.Begin(ctx)
	if err != nil {
		s.internal(w, r, err)
		return
	}
	defer tx.Rollback(ctx)
	var id, name, addr, msg string
	err = tx.QueryRow(ctx, `UPDATE contact_messages SET forwarded_at = now(), forwarded_by = $2,
		status = CASE WHEN status = 'new' THEN 'read' ELSE status END
		WHERE id::text = $1 AND forwarded_at IS NULL RETURNING id, name, email::text, message`, r.PathValue("id"), actor(r)).
		Scan(&id, &name, &addr, &msg)
	if errors.Is(err, pgx.ErrNoRows) {
		var exists bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM contact_messages WHERE id::text = $1)`, r.PathValue("id")).Scan(&exists); err != nil {
			s.internal(w, r, err)
			return
		}
		if exists {
			writeError(w, 409, "this message was already forwarded")
		} else {
			writeError(w, 404, "not found")
		}
		return
	}
	if err != nil {
		s.internal(w, r, err)
		return
	}
	jobID, err := s.Mailer.Send(ctx, tx, mailer.Request{Template: "contact_notify", To: email.Address{Email: owner},
		IdempotencyKey: "contact_notify:" + id,
		Vars:           map[string]string{"sender_name": name, "sender_email": addr, "message": msg}})
	if err != nil {
		s.internal(w, r, err)
		return
	}
	if err := tx.Commit(ctx); err != nil {
		s.internal(w, r, err)
		return
	}
	s.audit(r, "contact.forward", "contact_message", id, map[string]any{"to": owner, "job_id": jobID})
	writeJSON(w, 200, map[string]any{"status": "forwarded", "job_id": jobID})
}

func (s *Server) archiveContactMessage(w http.ResponseWriter, r *http.Request) {
	tag, err := s.DB.Exec(r.Context(), `UPDATE contact_messages SET status = 'archived' WHERE id::text = $1`, r.PathValue("id"))
	if err != nil {
		s.internal(w, r, err)
		return
	}
	if tag.RowsAffected() == 0 {
		writeError(w, 404, "not found")
		return
	}
	s.audit(r, "contact.archive", "contact_message", r.PathValue("id"), nil)
	writeJSON(w, 200, map[string]string{"status": "archived"})
}
