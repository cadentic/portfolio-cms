package api

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"net/http"
	"strings"
	"time"

	"github.com/cadentic/portfolio-cms/backend/internal/email"
	"github.com/cadentic/portfolio-cms/backend/internal/mailer"
	"github.com/cadentic/portfolio-cms/backend/internal/ratelimit"
	"github.com/cadentic/portfolio-cms/backend/internal/subscribers"
)

func (s *Server) contactOTPHash(emailAddr, code string) []byte {
	m := hmac.New(sha256.New, s.Cfg.AppSecret)
	m.Write([]byte("contact-otp|" + strings.ToLower(emailAddr) + "|" + code))
	return m.Sum(nil)
}

// contactCode emails a one-time code the sender must enter before the
// contact form accepts their message.
func (s *Server) contactCode(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Email string `json:"email"`
	}
	if !decode(w, r, &req) {
		return
	}
	addr, err := subscribers.NormalizeEmail(req.Email)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if !s.allow(r.Context(), map[string]ratelimit.Rule{
		"contact_otp:ip:" + s.clientIP(r): {Limit: 5, Window: time.Hour},
		"contact_otp:email:" + addr:       {Limit: 3, Window: time.Hour},
	}) {
		writeError(w, http.StatusTooManyRequests, "too many code requests; please wait and try again")
		return
	}
	code := tokensNumeric()
	var id string
	if err := s.DB.QueryRow(r.Context(), `INSERT INTO resume_otps (purpose, email, code_hash, expires_at)
		VALUES ('contact', $1, $2, now() + interval '10 minutes') RETURNING id`,
		addr, s.contactOTPHash(addr, code)).Scan(&id); err != nil {
		s.internal(w, r, err)
		return
	}
	if _, err := s.Mailer.Send(r.Context(), s.DB, mailer.Request{Template: "contact_otp", To: email.Address{Email: addr},
		IdempotencyKey: "contact_otp:" + id, Priority: 20, Vars: map[string]string{"code": code}}); err != nil {
		s.internal(w, r, err)
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]string{"message": "We've sent a 6-digit code to your email."})
}

// verifyContactCode checks and uses up the newest unexpired contact code for
// addr. Each check counts as an attempt; a code allows five.
func (s *Server) verifyContactCode(ctx context.Context, addr, code string) (bool, error) {
	if len(code) != 6 {
		return false, nil
	}
	var id string
	var hash []byte
	err := s.DB.QueryRow(ctx, `UPDATE resume_otps SET attempts = attempts + 1
		WHERE id = (SELECT id FROM resume_otps WHERE purpose = 'contact' AND email = $1 AND used_at IS NULL
		            AND expires_at > now() AND attempts < 5 ORDER BY created_at DESC LIMIT 1)
		RETURNING id, code_hash`, addr).Scan(&id, &hash)
	if err != nil || !hmac.Equal(hash, s.contactOTPHash(addr, code)) {
		return false, nil
	}
	tag, err := s.DB.Exec(ctx, `UPDATE resume_otps SET used_at = now() WHERE id = $1 AND used_at IS NULL`, id)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() == 1, nil
}
