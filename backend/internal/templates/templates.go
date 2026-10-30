// Package templates manages reusable email templates (system transactional
// templates and marketing templates) stored in email_templates.
package templates

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/cadentic/portfolio-cms/backend/internal/store"
)

type Template struct {
	ID        string    `json:"id"`
	Slug      string    `json:"slug"`
	Name      string    `json:"name"`
	Category  string    `json:"category"`
	Subject   string    `json:"subject"`
	Preheader string    `json:"preheader"`
	HTMLBody  string    `json:"html_body"`
	TextBody  string    `json:"text_body"`
	IsSystem  bool      `json:"is_system"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

var ErrNotFound = errors.New("template not found")

const cols = `id, slug, name, category, subject, preheader, html_body, text_body, is_system, created_at, updated_at`

func scan(row pgx.Row) (*Template, error) {
	var t Template
	err := row.Scan(&t.ID, &t.Slug, &t.Name, &t.Category, &t.Subject, &t.Preheader, &t.HTMLBody, &t.TextBody, &t.IsSystem, &t.CreatedAt, &t.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	return &t, err
}

func List(ctx context.Context, db store.DB) ([]*Template, error) {
	rows, err := db.Query(ctx, `SELECT `+cols+` FROM email_templates ORDER BY is_system DESC, name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*Template{}
	for rows.Next() {
		t, err := scan(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

func Get(ctx context.Context, db store.DB, id string) (*Template, error) {
	return scan(db.QueryRow(ctx, `SELECT `+cols+` FROM email_templates WHERE id=$1`, id))
}

func GetBySlug(ctx context.Context, db store.DB, slug string) (*Template, error) {
	return scan(db.QueryRow(ctx, `SELECT `+cols+` FROM email_templates WHERE slug=$1`, slug))
}

type Input struct {
	Slug      string `json:"slug"`
	Name      string `json:"name"`
	Category  string `json:"category"`
	Subject   string `json:"subject"`
	Preheader string `json:"preheader"`
	HTMLBody  string `json:"html_body"`
	TextBody  string `json:"text_body"`
}

func (in Input) validate() error {
	if in.Slug == "" || in.Name == "" || in.Subject == "" || in.HTMLBody == "" {
		return errors.New("slug, name, subject and html_body are required")
	}
	if in.Category != "transactional" && in.Category != "marketing" {
		return errors.New("category must be transactional or marketing")
	}
	return nil
}

func Create(ctx context.Context, db store.DB, in Input) (*Template, error) {
	if err := in.validate(); err != nil {
		return nil, err
	}
	t, err := scan(db.QueryRow(ctx, `INSERT INTO email_templates (slug, name, category, subject, preheader, html_body, text_body)
		VALUES ($1,$2,$3,$4,$5,$6,$7) RETURNING `+cols, in.Slug, in.Name, in.Category, in.Subject, in.Preheader, in.HTMLBody, in.TextBody))
	if store.IsUniqueViolation(err) {
		return nil, errors.New("a template with this slug already exists")
	}
	return t, err
}

// Update edits a template. System templates keep their slug and category so
// the code paths that rely on them keep working.
func Update(ctx context.Context, db store.DB, id string, in Input) (*Template, error) {
	cur, err := Get(ctx, db, id)
	if err != nil {
		return nil, err
	}
	if cur.IsSystem {
		in.Slug, in.Category = cur.Slug, cur.Category
	}
	if err := in.validate(); err != nil {
		return nil, err
	}
	return scan(db.QueryRow(ctx, `UPDATE email_templates SET slug=$2, name=$3, category=$4, subject=$5, preheader=$6,
		html_body=$7, text_body=$8, updated_at=now() WHERE id=$1 RETURNING `+cols,
		id, in.Slug, in.Name, in.Category, in.Subject, in.Preheader, in.HTMLBody, in.TextBody))
}

func Delete(ctx context.Context, db store.DB, id string) error {
	tag, err := db.Exec(ctx, `DELETE FROM email_templates WHERE id=$1 AND NOT is_system`, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return errors.New("system templates cannot be deleted")
	}
	return nil
}

// System templates seeded on start-up (never overwritten once edited).
var System = []Input{
	{Slug: "double_opt_in", Name: "Newsletter confirmation (double opt-in)", Category: "transactional",
		Subject: "Confirm your subscription", Preheader: "One click to confirm — the link expires in {{ttl_hours}} hours.",
		HTMLBody: `<h1 style="font-size:24px;margin:0 0 12px">Please confirm your subscription</h1>
<p>Hi {{first_name}},</p><p>Someone (hopefully you) asked to receive the newsletter from {{site_name}} at this address. Confirm to start receiving new articles, videos and updates.</p>
<p style="margin:26px 0"><a href="{{confirm_url}}" style="background:#8ef0c4;color:#09291f;text-decoration:none;font-weight:700;padding:13px 20px;border-radius:999px;display:inline-block">Confirm subscription</a></p>
<p style="font-size:13px;color:#596675">This link expires in {{ttl_hours}} hours and can be used once. If you didn't ask for this, ignore this email — you won't be subscribed.</p>`},
	{Slug: "already_subscribed", Name: "Already subscribed notice", Category: "transactional",
		Subject: "You're already subscribed", HTMLBody: `<p>Hi {{first_name}},</p><p>This address is already subscribed to the {{site_name}} newsletter, so nothing changed. If that wasn't you, you can ignore this email or <a href="{{unsubscribe_url}}">unsubscribe</a>.</p>`},
	{Slug: "subscription_confirmed", Name: "Welcome (after confirmation)", Category: "transactional",
		Subject: "You're subscribed", HTMLBody: `<h1 style="font-size:24px;margin:0 0 12px">Welcome aboard</h1><p>Thanks for confirming, {{first_name}}. You'll get new articles, videos and occasional professional updates. You can <a href="{{unsubscribe_url}}">unsubscribe</a> at any time.</p>`},
	{Slug: "contact_ack", Name: "Contact form acknowledgement", Category: "transactional",
		Subject: "Thanks for getting in touch", HTMLBody: `<p>Hi {{first_name}},</p><p>Thanks for your message. I've received it and will reply as soon as I can.</p><p style="color:#596675;font-size:13px">This is an automatic acknowledgement of a message sent from {{site_name}}.</p>`},
	{Slug: "contact_ack_resume", Name: "Contact form acknowledgement (with resume link)", Category: "transactional",
		Subject: "Thanks for getting in touch", HTMLBody: `<p>Hi {{first_name}},</p><p>Thanks for your message. I've received it.</p><p>While you wait, you can download my resume. You'll confirm your email address once more with a one-time code, then the download starts. I'll reply to your message after that.</p>
<p style="margin:26px 0"><a href="{{resume_url}}" style="background:#8ef0c4;color:#09291f;text-decoration:none;font-weight:700;padding:13px 20px;border-radius:999px;display:inline-block">Get the resume</a></p>
<p style="color:#596675;font-size:13px">This is an automatic acknowledgement of a message sent from {{site_name}}.</p>`},
	{Slug: "contact_alert_owner", Name: "New enquiry alert (owner)", Category: "transactional",
		Subject: "Someone might have enquired about a project", HTMLBody: `<p>Someone might have enquired about the project. Please look into the matter by logging in to the system.</p>
<p style="margin:26px 0"><a href="{{admin_url}}" style="background:#8ef0c4;color:#09291f;text-decoration:none;font-weight:700;padding:13px 20px;border-radius:999px;display:inline-block">Open messages</a></p>
<p style="color:#596675;font-size:13px">The message itself is only shown after you sign in.</p>`},
	{Slug: "contact_notify", Name: "Contact form notification (owner)", Category: "transactional",
		Subject: "New enquiry from {{sender_name}}", HTMLBody: `<p><strong>{{sender_name}}</strong> &lt;{{sender_email}}&gt; wrote:</p><blockquote style="border-left:3px solid #8ef0c4;padding-left:12px;color:#334">{{message}}</blockquote>`},
	{Slug: "contact_otp", Name: "Contact form email verification code", Category: "transactional",
		Subject: "Your verification code: {{code}}", HTMLBody: `<p>Use this code to verify your email and send your message:</p><p style="font-size:32px;letter-spacing:8px;font-weight:800;margin:18px 0">{{code}}</p><p style="font-size:13px;color:#596675">The code expires in 10 minutes. If you didn't use the contact form on {{site_name}}, ignore this email.</p>`},
	{Slug: "resume_otp", Name: "Resume download code", Category: "transactional",
		Subject: "Your verification code: {{code}}", HTMLBody: `<p>Use this code to download the resume:</p><p style="font-size:32px;letter-spacing:8px;font-weight:800;margin:18px 0">{{code}}</p><p style="font-size:13px;color:#596675">The code expires in 10 minutes. If you didn't request it, ignore this email.</p>`},
	{Slug: "admin_alert", Name: "Admin alert", Category: "transactional",
		Subject: "[{{severity}}] Email system alert: {{kind}}", HTMLBody: `<p><strong>{{kind}}</strong></p><p>{{message}}</p><p><a href="{{admin_url}}">Open the email monitor</a></p>`},
	{Slug: "newsletter_default", Name: "Newsletter (default)", Category: "marketing",
		Subject: "{{subject}}", HTMLBody: `{{content}}`},
	{Slug: "blog_notification", Name: "New article notification", Category: "marketing",
		Subject: "New article: {{title}}", Preheader: "{{excerpt}}",
		HTMLBody: `{{featured_image}}<p style="font-size:12px;letter-spacing:.12em;text-transform:uppercase;color:#16865e;font-weight:800;margin:0">New article · {{published_date}}</p>
<h1 style="font-size:26px;line-height:1.2;margin:10px 0 12px">{{title}}</h1><p>{{excerpt}}</p>
<p style="margin:24px 0"><a href="{{url}}" style="background:#8ef0c4;color:#09291f;text-decoration:none;font-weight:700;padding:13px 20px;border-radius:999px;display:inline-block">Read the article</a></p>
<p style="font-size:13px;color:#596675">By {{author}}</p>`},
}

// Seed inserts missing system templates.
func Seed(ctx context.Context, db store.DB) error {
	for _, t := range System {
		if _, err := db.Exec(ctx, `INSERT INTO email_templates (slug, name, category, subject, preheader, html_body, text_body, is_system)
			VALUES ($1,$2,$3,$4,$5,$6,$7,true) ON CONFLICT (slug) DO NOTHING`,
			t.Slug, t.Name, t.Category, t.Subject, t.Preheader, t.HTMLBody, t.TextBody); err != nil {
			return err
		}
	}
	return nil
}
