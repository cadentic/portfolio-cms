// Package smtp is a generic SMTP-relay adapter (Amazon SES SMTP, Postmark,
// SendGrid, ZeptoMail, ...). It demonstrates how further providers plug into
// the registry without any queue or newsletter changes.
//
// Settings (non-secret, editable in the CMS): host, port.
// Credentials (secret refs): username, password.
package smtp

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"mime"
	"net"
	"net/http"
	"net/smtp"
	"net/textproto"
	"strconv"
	"strings"
	"time"

	"github.com/cadentic/portfolio-cms/backend/internal/email"
)

type Adapter struct{ Timeout time.Duration }

func New() *Adapter { return &Adapter{Timeout: 20 * time.Second} }

func (a *Adapter) Kind() string                     { return "smtp" }
func (a *Adapter) RequiredCredentials() []string    { return []string{"username", "password"} }
func (a *Adapter) IdempotencyWindow() time.Duration { return 0 }

func hostPort(cfg email.ProviderConfig) (string, string) {
	host, _ := cfg.Settings["host"].(string)
	port := "587"
	switch p := cfg.Settings["port"].(type) {
	case float64:
		port = strconv.Itoa(int(p))
	case string:
		if p != "" {
			port = p
		}
	}
	return host, port
}

type phaseErr struct {
	afterData bool
	err       error
}

func (a *Adapter) Send(ctx context.Context, cfg email.ProviderConfig, msg email.Message) email.SendResult {
	host, port := hostPort(cfg)
	if host == "" {
		return email.SendResult{Outcome: email.OutcomeNotSent, ErrorCode: "misconfigured", ErrorMessage: "smtp host not set"}
	}
	pe := a.send(ctx, cfg, host, port, msg)
	if pe == nil {
		return email.SendResult{Outcome: email.OutcomeAccepted, ProviderMessageID: msg.InternalID}
	}
	res := email.SendResult{ErrorMessage: email.RedactString(pe.err.Error())}
	var tpErr *textproto.Error
	if errors.As(pe.err, &tpErr) {
		res.ErrorCode = strconv.Itoa(tpErr.Code)
		if tpErr.Code >= 400 && tpErr.Code < 500 {
			res.Outcome = email.OutcomeRejectedTransient
		} else {
			res.Outcome = email.OutcomeRejectedPermanent
		}
		return res
	}
	if pe.afterData {
		// The message body was transmitted but the final reply was lost.
		res.Outcome = email.OutcomeUnknown
		res.ErrorCode = "timeout_after_submit"
	} else {
		res.Outcome = email.OutcomeNotSent
		res.ErrorCode = "connection_failed"
	}
	return res
}

func (a *Adapter) send(ctx context.Context, cfg email.ProviderConfig, host, port string, msg email.Message) *phaseErr {
	d := net.Dialer{Timeout: a.Timeout}
	conn, err := d.DialContext(ctx, "tcp", net.JoinHostPort(host, port))
	if err != nil {
		return &phaseErr{err: err}
	}
	deadline := time.Now().Add(a.Timeout)
	if dl, ok := ctx.Deadline(); ok && dl.Before(deadline) {
		deadline = dl
	}
	_ = conn.SetDeadline(deadline)
	var c *smtp.Client
	if port == "465" {
		tc := tls.Client(conn, &tls.Config{ServerName: host, MinVersion: tls.VersionTLS12})
		c, err = smtp.NewClient(tc, host)
	} else {
		c, err = smtp.NewClient(conn, host)
	}
	if err != nil {
		conn.Close()
		return &phaseErr{err: err}
	}
	defer c.Close()
	if ok, _ := c.Extension("STARTTLS"); ok {
		if err := c.StartTLS(&tls.Config{ServerName: host, MinVersion: tls.VersionTLS12}); err != nil {
			return &phaseErr{err: err}
		}
	}
	if u := cfg.Credentials["username"]; u != "" {
		if err := c.Auth(smtp.PlainAuth("", u, cfg.Credentials["password"], host)); err != nil {
			return &phaseErr{err: err}
		}
	}
	if err := c.Mail(msg.From.Email); err != nil {
		return &phaseErr{err: err}
	}
	if err := c.Rcpt(msg.To.Email); err != nil {
		return &phaseErr{err: err}
	}
	w, err := c.Data()
	if err != nil {
		return &phaseErr{err: err}
	}
	if _, err := w.Write(buildMIME(msg)); err != nil {
		return &phaseErr{err: err}
	}
	if err := w.Close(); err != nil {
		return &phaseErr{afterData: true, err: err}
	}
	_ = c.Quit()
	return nil
}

func buildMIME(msg email.Message) []byte {
	var b strings.Builder
	boundary := "b-" + msg.InternalID
	enc := func(s string) string { return mime.QEncoding.Encode("utf-8", s) }
	fmt.Fprintf(&b, "From: %s <%s>\r\n", enc(msg.From.Name), msg.From.Email)
	fmt.Fprintf(&b, "To: %s <%s>\r\n", enc(msg.To.Name), msg.To.Email)
	if msg.ReplyTo != nil && msg.ReplyTo.Email != "" {
		fmt.Fprintf(&b, "Reply-To: %s\r\n", msg.ReplyTo.Email)
	}
	fmt.Fprintf(&b, "Subject: %s\r\n", enc(msg.Subject))
	fmt.Fprintf(&b, "Date: %s\r\n", time.Now().UTC().Format(time.RFC1123Z))
	fmt.Fprintf(&b, "X-Portfolio-Message-ID: %s\r\n", msg.InternalID)
	for k, v := range msg.Headers {
		if strings.ContainsAny(k+v, "\r\n") {
			continue
		}
		fmt.Fprintf(&b, "%s: %s\r\n", k, v)
	}
	fmt.Fprintf(&b, "MIME-Version: 1.0\r\nContent-Type: multipart/alternative; boundary=%q\r\n\r\n", boundary)
	fmt.Fprintf(&b, "--%s\r\nContent-Type: text/plain; charset=utf-8\r\n\r\n%s\r\n", boundary, msg.Text)
	fmt.Fprintf(&b, "--%s\r\nContent-Type: text/html; charset=utf-8\r\n\r\n%s\r\n--%s--\r\n", boundary, msg.HTML, boundary)
	return []byte(b.String())
}

func (a *Adapter) HealthCheck(ctx context.Context, cfg email.ProviderConfig) email.HealthResult {
	host, port := hostPort(cfg)
	if host == "" {
		return email.HealthResult{Message: "smtp host not configured"}
	}
	d := net.Dialer{Timeout: 10 * time.Second}
	conn, err := d.DialContext(ctx, "tcp", net.JoinHostPort(host, port))
	if err != nil {
		return email.HealthResult{Message: "connect failed: " + err.Error()}
	}
	conn.Close()
	return email.HealthResult{OK: true, Message: "SMTP endpoint reachable"}
}

func (a *Adapter) Lookup(context.Context, email.ProviderConfig, string, string, time.Time) (email.LookupResult, error) {
	return email.LookupResult{Status: email.LookupUnsupported}, nil
}

func (a *Adapter) VerifyWebhook(r *http.Request, cfg email.ProviderConfig) error {
	return email.VerifySharedSecret(r, cfg.WebhookSecret)
}

func (a *Adapter) ParseWebhook([]byte) ([]email.Event, error) {
	return nil, errors.New("smtp relay has no webhook format")
}
