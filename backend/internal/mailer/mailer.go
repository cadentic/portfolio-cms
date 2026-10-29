// Package mailer renders a stored template and enqueues it. It is the only way
// application code sends email; delivery itself happens in the queue worker.
package mailer

import (
	"context"
	"fmt"
	"strings"

	"github.com/cadentic/portfolio-cms/backend/internal/email"
	"github.com/cadentic/portfolio-cms/backend/internal/queue"
	"github.com/cadentic/portfolio-cms/backend/internal/render"
	"github.com/cadentic/portfolio-cms/backend/internal/settings"
	"github.com/cadentic/portfolio-cms/backend/internal/store"
	"github.com/cadentic/portfolio-cms/backend/internal/templates"
)

type Mailer struct {
	Settings *settings.Store
	SiteURL  string
}

type Request struct {
	Template       string
	To             email.Address
	Vars           map[string]string
	RawVars        map[string]string
	IdempotencyKey string
	Kind           string
	SubscriberID   string
	Marketing      bool
	UnsubscribeURL string
	Headers        map[string]string
	Priority       int
}

// Send renders and enqueues inside db (pool or transaction).
func (m *Mailer) Send(ctx context.Context, db store.DB, r Request) (string, error) {
	t, err := templates.GetBySlug(ctx, db, r.Template)
	if err != nil {
		return "", fmt.Errorf("template %s: %w", r.Template, err)
	}
	es, _ := m.Settings.Email(ctx)
	r.To.Name = displayName(r.To.Name)
	vars := map[string]string{"site_name": siteName(es), "site_url": m.SiteURL, "email": r.To.Email,
		"first_name": firstName(r.To.Name)}
	for k, v := range r.Vars {
		vars[k] = v
	}
	htmlBody, text, err := render.Render(render.Input{
		Marketing: r.Marketing, Subject: render.Fill(t.Subject, vars, false), Preheader: render.Fill(t.Preheader, vars, false),
		ContentHTML: t.HTMLBody, ContentText: t.TextBody, Vars: vars, RawVars: r.RawVars,
		UnsubscribeURL: r.UnsubscribeURL, SiteURL: m.SiteURL, Settings: es,
	})
	if err != nil {
		return "", err
	}
	cat := email.CategoryTransactional
	if r.Marketing {
		cat = email.CategoryMarketing
	}
	kind := r.Kind
	if kind == "" {
		kind = r.Template
	}
	id, _, err := queue.Enqueue(ctx, db, queue.EnqueueRequest{
		IdempotencyKey: r.IdempotencyKey, Category: cat, Kind: kind, SubscriberID: r.SubscriberID,
		To: r.To, Subject: render.Fill(t.Subject, vars, false), HTML: htmlBody, Text: text,
		Headers: r.Headers, Priority: r.Priority,
	})
	return id, err
}

func siteName(es settings.Email) string {
	if es.OrganizationName != "" {
		return es.OrganizationName
	}
	return es.SenderName
}

// displayName drops a recipient name that carries a link or address, for the
// same reason as firstName: it is shown in the recipient's To line.
func displayName(name string) string {
	if strings.ContainsAny(name, "/:@<>\r\n") || strings.Contains(strings.ToLower(name), "www.") {
		return ""
	}
	return name
}

// firstName is the greeting name. Anyone can type a name into a public form
// and have an acknowledgement sent to any address, so a "name" that looks like
// a link, domain or address is replaced: the site never mails out
// attacker-chosen URLs.
func firstName(name string) string {
	for i, c := range name {
		if c == ' ' {
			name = name[:i]
			break
		}
	}
	if name == "" || len([]rune(name)) > 40 || strings.ContainsAny(name, "./:@\\<>") {
		return "there"
	}
	return name
}
