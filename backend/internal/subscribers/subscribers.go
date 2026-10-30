// Package subscribers implements double opt-in subscription, verification,
// unsubscribe and the admin subscriber management features.
package subscribers

import (
	"context"
	"errors"
	"net"
	"net/mail"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/cadentic/portfolio-cms/backend/internal/email"
	"github.com/cadentic/portfolio-cms/backend/internal/mailer"
	"github.com/cadentic/portfolio-cms/backend/internal/settings"
	"github.com/cadentic/portfolio-cms/backend/internal/store"
	"github.com/cadentic/portfolio-cms/backend/internal/suppression"
	"github.com/cadentic/portfolio-cms/backend/internal/tokens"
)

var (
	ErrInvalidEmail   = errors.New("please enter a valid email address")
	ErrConsentMissing = errors.New("consent is required to subscribe")
	ErrTokenInvalid   = errors.New("this confirmation link is invalid")
	ErrTokenExpired   = errors.New("this confirmation link has expired")
	ErrNotFound       = errors.New("subscriber not found")
)

// GoogleContactsSource marks subscribers synced from Google Contacts. They form
// a separate list that the regular newsletter leaves out (package campaigns);
// confirming a sign-up on the site moves them onto the regular list.
const GoogleContactsSource = "google_contacts"

type Service struct {
	DB       *pgxpool.Pool
	Settings *settings.Store
	Mailer   *mailer.Mailer
	Signer   tokens.Signer
	SiteURL  string
}

// NormalizeEmail validates and canonicalises an address (lower-case, trimmed).
func NormalizeEmail(s string) (string, error) {
	s = strings.TrimSpace(s)
	if s == "" || len(s) > 254 || !utf8.ValidString(s) || strings.ContainsAny(s, " <>\r\n,;\"") {
		return "", ErrInvalidEmail
	}
	a, err := mail.ParseAddress(s)
	if err != nil || a.Address != s {
		return "", ErrInvalidEmail
	}
	at := strings.LastIndex(s, "@")
	local, domain := s[:at], s[at+1:]
	if len(local) == 0 || len(local) > 64 || !strings.Contains(domain, ".") || strings.HasPrefix(domain, ".") ||
		strings.HasSuffix(domain, ".") || strings.Contains(domain, "..") {
		return "", ErrInvalidEmail
	}
	return strings.ToLower(s), nil
}

func cleanName(s string) string {
	s = strings.TrimSpace(strings.Map(func(r rune) rune {
		if r < 32 || r == '<' || r == '>' {
			return -1
		}
		return r
	}, s))
	if utf8.RuneCountInString(s) > 100 {
		s = string([]rune(s)[:100])
	}
	return s
}

func (s *Service) UnsubscribeURL(subscriberID string) string {
	return s.SiteURL + "/unsubscribe?token=" + s.Signer.Sign("unsub", subscriberID, 0)
}

func (s *Service) OneClickUnsubscribeURL(subscriberID string) string {
	return s.SiteURL + "/api/public/unsubscribe/one-click?token=" + s.Signer.Sign("unsub", subscriberID, 0)
}

// ListUnsubscribeHeaders returns RFC 2369 / RFC 8058 headers for marketing mail.
func (s *Service) ListUnsubscribeHeaders(subscriberID string) map[string]string {
	return map[string]string{
		"List-Unsubscribe":      "<" + s.OneClickUnsubscribeURL(subscriberID) + ">",
		"List-Unsubscribe-Post": "List-Unsubscribe=One-Click",
	}
}

type SubscribeInput struct {
	Email     string   `json:"email"`
	Name      string   `json:"name"`
	Consent   bool     `json:"consent"`
	Topics    []string `json:"topics"`
	Source    string   `json:"source"`
	IP        string   `json:"-"`
	UserAgent string   `json:"-"`
}

func ipArg(ip string) any {
	if net.ParseIP(ip) == nil {
		return nil
	}
	return ip
}

func allowedSource(s string) string {
	switch s {
	case "website", "blog", "article", "footer", "video":
		return s
	}
	return "website"
}

// Subscribe records consent and starts double opt-in. It deliberately returns
// the same outcome whether or not the address already exists, so the public
// endpoint cannot be used to enumerate subscribers.
func (s *Service) Subscribe(ctx context.Context, in SubscribeInput) error {
	addr, err := NormalizeEmail(in.Email)
	if err != nil {
		return err
	}
	if !in.Consent {
		return ErrConsentMissing
	}
	es, _ := s.Settings.Email(ctx)
	name := cleanName(in.Name)
	topics := filterTopics(in.Topics, es.Topics)
	source := allowedSource(in.Source)

	return store.InTx(ctx, s.DB, func(tx pgx.Tx) error {
		var id, status, existingSource string
		var lastSent *time.Time
		var sendCount int
		err := tx.QueryRow(ctx, `SELECT id, status, source, last_verification_sent_at, verification_send_count
			FROM subscribers WHERE email=$1 FOR UPDATE`, addr).Scan(&id, &status, &existingSource, &lastSent, &sendCount)
		isNew := errors.Is(err, pgx.ErrNoRows)
		if err != nil && !isNew {
			return err
		}
		if isNew {
			if err := tx.QueryRow(ctx, `INSERT INTO subscribers (email, name, status, source) VALUES ($1, NULLIF($2,''), 'pending', $3)
				ON CONFLICT (email) DO UPDATE SET updated_at=now() RETURNING id, status`, addr, name, source).Scan(&id, &status); err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, `INSERT INTO subscriber_preferences (subscriber_id, topics) VALUES ($1,$2)
				ON CONFLICT (subscriber_id) DO NOTHING`, id, topics); err != nil {
				return err
			}
		}
		cooldownOK := lastSent == nil || time.Since(*lastSent) >= time.Duration(es.ResendCooldownMinutes)*time.Minute

		switch {
		case status == "verified" && existingSource == GoogleContactsSource:
			// Synced from the owner's Google Contacts: they join the regular
			// newsletter only by confirming this sign-up (handled below).
		case status == "verified":
			if !cooldownOK {
				return nil
			}
			if _, err := tx.Exec(ctx, `UPDATE subscribers SET last_verification_sent_at=now() WHERE id=$1`, id); err != nil {
				return err
			}
			_, err := s.Mailer.Send(ctx, tx, mailer.Request{Template: "already_subscribed",
				To: email.Address{Email: addr, Name: name}, SubscriberID: id,
				IdempotencyKey: "already:" + id + ":" + strconv.FormatInt(time.Now().Unix()/3600, 10),
				Vars:           map[string]string{"unsubscribe_url": s.UnsubscribeURL(id)}})
			return err
		case status == "bounced":
			return nil // address cannot receive mail; nothing useful to send
		}

		// New, pending, unsubscribed or complained: (re)start double opt-in with
		// fresh consent evidence.
		if !isNew && !cooldownOK {
			return nil
		}
		if !isNew && sendCount >= es.MaxVerificationSends && lastSent != nil && time.Since(*lastSent) < 24*time.Hour {
			return nil // bounded resends per day prevent email bombing
		}
		if _, err := tx.Exec(ctx, `INSERT INTO subscriber_consents (subscriber_id, action, source, consent_text,
			privacy_policy_version, ip, user_agent, evidence) VALUES ($1,'granted',$2,$3,$4,$5,$6,$7)`,
			id, source, es.ConsentText, es.PrivacyPolicyVersion, ipArg(in.IP), truncate(in.UserAgent, 300),
			map[string]any{"form": source, "double_opt_in": es.DoubleOptIn}); err != nil {
			return err
		}
		if len(topics) > 0 {
			if _, err := tx.Exec(ctx, `INSERT INTO subscriber_preferences (subscriber_id, topics) VALUES ($1,$2)
				ON CONFLICT (subscriber_id) DO UPDATE SET topics=EXCLUDED.topics, updated_at=now()`, id, topics); err != nil {
				return err
			}
		}
		if name != "" {
			if _, err := tx.Exec(ctx, `UPDATE subscribers SET name=$2 WHERE id=$1`, id, name); err != nil {
				return err
			}
		}
		if !es.DoubleOptIn {
			return s.markVerified(ctx, tx, id, addr, name, in.IP, in.UserAgent, "single_opt_in")
		}
		resetCount := ""
		if lastSent != nil && time.Since(*lastSent) >= 24*time.Hour {
			resetCount = ", verification_send_count=0"
		}
		if _, err := tx.Exec(ctx, `UPDATE subscribers SET status=CASE WHEN status='verified' THEN status ELSE 'pending' END,
			updated_at=now()`+resetCount+` WHERE id=$1`, id); err != nil {
			return err
		}
		return s.sendConfirmation(ctx, tx, id, addr, name, es)
	})
}

func (s *Service) sendConfirmation(ctx context.Context, tx pgx.Tx, id, addr, name string, es settings.Email) error {
	// Invalidate older links so only the newest one works.
	if _, err := tx.Exec(ctx, `UPDATE subscriber_verification_tokens SET used_at=now() WHERE subscriber_id=$1 AND used_at IS NULL`, id); err != nil {
		return err
	}
	raw, hash := tokens.NewRandom()
	var tokenID string
	if err := tx.QueryRow(ctx, `INSERT INTO subscriber_verification_tokens (subscriber_id, token_hash, expires_at)
		VALUES ($1,$2, now() + make_interval(hours => $3)) RETURNING id`, id, hash, es.VerificationTTLHours).Scan(&tokenID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `UPDATE subscribers SET last_verification_sent_at=now(),
		verification_send_count=verification_send_count+1 WHERE id=$1`, id); err != nil {
		return err
	}
	_, err := s.Mailer.Send(ctx, tx, mailer.Request{Template: "double_opt_in", To: email.Address{Email: addr, Name: name},
		SubscriberID: id, IdempotencyKey: "doi:" + tokenID, Priority: 10,
		Vars: map[string]string{"confirm_url": s.SiteURL + "/subscribe/confirm?token=" + raw,
			"ttl_hours": strconv.Itoa(es.VerificationTTLHours)}})
	return err
}

// Resend sends a fresh confirmation link to a pending subscriber (rate limited
// by cooldown and per-day cap). Always reports success to the caller.
func (s *Service) Resend(ctx context.Context, rawEmail string) error {
	addr, err := NormalizeEmail(rawEmail)
	if err != nil {
		return err
	}
	es, _ := s.Settings.Email(ctx)
	return store.InTx(ctx, s.DB, func(tx pgx.Tx) error {
		var id, status, name string
		var lastSent *time.Time
		var count int
		err := tx.QueryRow(ctx, `SELECT id, status, coalesce(name,''), last_verification_sent_at, verification_send_count
			FROM subscribers WHERE email=$1 FOR UPDATE`, addr).Scan(&id, &status, &name, &lastSent, &count)
		if errors.Is(err, pgx.ErrNoRows) || status != "pending" {
			return nil
		}
		if err != nil {
			return err
		}
		if lastSent != nil && time.Since(*lastSent) < time.Duration(es.ResendCooldownMinutes)*time.Minute {
			return nil
		}
		if count >= es.MaxVerificationSends && lastSent != nil && time.Since(*lastSent) < 24*time.Hour {
			return nil
		}
		return s.sendConfirmation(ctx, tx, id, addr, name, es)
	})
}

type ConfirmResult struct {
	Email            string `json:"email"`
	AlreadyConfirmed bool   `json:"already_confirmed"`
}

// Confirm consumes a single-use verification token.
func (s *Service) Confirm(ctx context.Context, raw, ip, ua string) (*ConfirmResult, error) {
	if raw == "" || len(raw) > 100 {
		return nil, ErrTokenInvalid
	}
	var res ConfirmResult
	err := store.InTx(ctx, s.DB, func(tx pgx.Tx) error {
		var tokenID, subID, addr, name, status string
		var expires time.Time
		var used *time.Time
		err := tx.QueryRow(ctx, `SELECT t.id, t.subscriber_id, t.expires_at, t.used_at, s.email, coalesce(s.name,''), s.status
			FROM subscriber_verification_tokens t JOIN subscribers s ON s.id=t.subscriber_id
			WHERE t.token_hash=$1 FOR UPDATE OF t, s`, tokens.Hash(raw)).Scan(&tokenID, &subID, &expires, &used, &addr, &name, &status)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrTokenInvalid
		}
		if err != nil {
			return err
		}
		res.Email = email.MaskEmail(addr)
		if used != nil {
			if status == "verified" {
				res.AlreadyConfirmed = true
				return nil
			}
			return ErrTokenInvalid
		}
		if time.Now().After(expires) {
			return ErrTokenExpired
		}
		if _, err := tx.Exec(ctx, `UPDATE subscriber_verification_tokens SET used_at=now() WHERE id=$1`, tokenID); err != nil {
			return err
		}
		return s.markVerified(ctx, tx, subID, addr, name, ip, ua, "double_opt_in")
	})
	if err != nil {
		return nil, err
	}
	return &res, nil
}

func (s *Service) markVerified(ctx context.Context, tx pgx.Tx, id, addr, name, ip, ua, method string) error {
	if _, err := tx.Exec(ctx, `UPDATE subscribers SET status='verified', verified_at=now(), unsubscribed_at=NULL,
		source=CASE WHEN source=$2 THEN 'website' ELSE source END, updated_at=now() WHERE id=$1`, id, GoogleContactsSource); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO subscriber_consents (subscriber_id, action, source, ip, user_agent, evidence)
		VALUES ($1,'verified','email_confirmation',$2,$3,$4)`, id, ipArg(ip), truncate(ua, 300), map[string]any{"method": method}); err != nil {
		return err
	}
	// A fresh, verified opt-in supersedes an earlier unsubscribe/complaint.
	// Hard-bounce ('all') suppressions stay until an admin lifts them.
	if _, err := tx.Exec(ctx, `UPDATE email_suppressions SET lifted_at=now(), lifted_by='double_opt_in'
		WHERE email=$1 AND scope='marketing' AND reason IN ('unsubscribed','complaint') AND lifted_at IS NULL`, addr); err != nil {
		return err
	}
	_, err := s.Mailer.Send(ctx, tx, mailer.Request{Template: "subscription_confirmed", To: email.Address{Email: addr, Name: name},
		SubscriberID: id, IdempotencyKey: "welcome:" + id + ":" + strconv.FormatInt(time.Now().Unix(), 10),
		Vars: map[string]string{"unsubscribe_url": s.UnsubscribeURL(id)}})
	return err
}

type UnsubscribeInfo struct {
	Email  string `json:"email"`
	Status string `json:"status"`
}

func (s *Service) UnsubscribeInfo(ctx context.Context, token string) (*UnsubscribeInfo, error) {
	id, err := s.Signer.Verify("unsub", token)
	if err != nil {
		return nil, ErrTokenInvalid
	}
	var info UnsubscribeInfo
	var addr string
	if err := s.DB.QueryRow(ctx, `SELECT email, status FROM subscribers WHERE id=$1`, id).Scan(&addr, &info.Status); err != nil {
		return nil, ErrTokenInvalid
	}
	info.Email = email.MaskEmail(addr)
	return &info, nil
}

// Unsubscribe processes an unsubscribe link (web page or RFC 8058 one-click).
func (s *Service) Unsubscribe(ctx context.Context, token, source string) error {
	id, err := s.Signer.Verify("unsub", token)
	if err != nil {
		return ErrTokenInvalid
	}
	return store.InTx(ctx, s.DB, func(tx pgx.Tx) error {
		var addr string
		if err := tx.QueryRow(ctx, `SELECT email FROM subscribers WHERE id=$1 FOR UPDATE`, id).Scan(&addr); err != nil {
			return ErrTokenInvalid
		}
		return suppression.Unsubscribe(ctx, tx, addr, source, "subscriber")
	})
}

func filterTopics(in, allowed []string) []string {
	out := []string{}
	for _, t := range in {
		for _, a := range allowed {
			if t == a {
				out = append(out, t)
				break
			}
		}
	}
	return out
}

func truncate(s string, n int) string {
	if len(s) > n {
		return s[:n]
	}
	return s
}
