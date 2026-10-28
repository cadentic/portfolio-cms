// Package campaigns manages newsletter campaigns: drafts, audience
// segmentation, previews, test sends, explicit approval, scheduling and the
// duplicate-safe fan-out of one queue job per eligible recipient.
package campaigns

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/cadentic/portfolio-cms/backend/internal/csvsafe"
	"html"
	"io"
	"slices"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/cadentic/portfolio-cms/backend/internal/email"
	"github.com/cadentic/portfolio-cms/backend/internal/queue"
	"github.com/cadentic/portfolio-cms/backend/internal/render"
	"github.com/cadentic/portfolio-cms/backend/internal/settings"
	"github.com/cadentic/portfolio-cms/backend/internal/store"
	"github.com/cadentic/portfolio-cms/backend/internal/subscribers"
	"github.com/cadentic/portfolio-cms/backend/internal/templates"
)

var (
	ErrNotFound      = errors.New("campaign not found")
	ErrNotDraft      = errors.New("only draft campaigns can be changed or sent")
	ErrAudienceDrift = errors.New("the audience changed since you reviewed it; review the recipient count again")
	ErrConfirm       = errors.New("explicit confirmation is required to send a campaign")
)

type Segment struct {
	Topics         []string   `json:"topics,omitempty"`
	Sources        []string   `json:"sources,omitempty"`
	SubscribedFrom *time.Time `json:"subscribed_from,omitempty"`
	SubscribedTo   *time.Time `json:"subscribed_to,omitempty"`
	SubscriberIDs  []string   `json:"subscriber_ids,omitempty"` // explicit selection (still eligibility-filtered)
}

type Campaign struct {
	ID             string     `json:"id"`
	Name           string     `json:"name"`
	Kind           string     `json:"kind"`
	Status         string     `json:"status"`
	Subject        string     `json:"subject"`
	Preheader      string     `json:"preheader"`
	TemplateID     *string    `json:"template_id"`
	ContentHTML    string     `json:"content_html"`
	ContentText    string     `json:"content_text"`
	Segment        Segment    `json:"segment"`
	BlogPostID     *string    `json:"blog_post_id"`
	ScheduledAt    *time.Time `json:"scheduled_at"`
	ApprovedBy     string     `json:"approved_by"`
	ApprovedAt     *time.Time `json:"approved_at"`
	AudienceCount  *int       `json:"audience_count"`
	MaterializedAt *time.Time `json:"materialized_at"`
	SendStartedAt  *time.Time `json:"send_started_at"`
	CompletedAt    *time.Time `json:"completed_at"`
	CreatedAt      time.Time  `json:"created_at"`
	UpdatedAt      time.Time  `json:"updated_at"`
	Stats          *Stats     `json:"stats,omitempty"`
}

type Stats struct {
	Recipients int            `json:"recipients"`
	ByStatus   map[string]int `json:"by_status"`
	Accepted   int            `json:"accepted"`
	Delivered  int            `json:"delivered"`
	Bounced    int            `json:"bounced"`
	Complained int            `json:"complained"`
	Failed     int            `json:"failed"`
	Pending    int            `json:"pending"`
	Opened     int            `json:"opened"`
	Clicked    int            `json:"clicked"`
}

type Service struct {
	DB          *pgxpool.Pool
	Settings    *settings.Store
	Subscribers *subscribers.Service
	SiteURL     string
}

const cols = `id, name, kind, status, subject, preheader, template_id, content_html, content_text, segment, blog_post_id,
	scheduled_at, coalesce(approved_by,''), approved_at, audience_count, materialized_at, send_started_at, completed_at,
	created_at, updated_at`

func scan(row pgx.Row) (*Campaign, error) {
	var c Campaign
	var seg []byte
	err := row.Scan(&c.ID, &c.Name, &c.Kind, &c.Status, &c.Subject, &c.Preheader, &c.TemplateID, &c.ContentHTML,
		&c.ContentText, &seg, &c.BlogPostID, &c.ScheduledAt, &c.ApprovedBy, &c.ApprovedAt, &c.AudienceCount,
		&c.MaterializedAt, &c.SendStartedAt, &c.CompletedAt, &c.CreatedAt, &c.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	_ = json.Unmarshal(seg, &c.Segment)
	return &c, nil
}

func (s *Service) List(ctx context.Context, status string, limit, offset int) ([]*Campaign, int, error) {
	if limit <= 0 || limit > 100 {
		limit = 25
	}
	var total int
	if err := s.DB.QueryRow(ctx, `SELECT count(*) FROM email_campaigns WHERE ($1='' OR status=$1)`, status).Scan(&total); err != nil {
		return nil, 0, err
	}
	rows, err := s.DB.Query(ctx, `SELECT `+cols+` FROM email_campaigns WHERE ($1='' OR status=$1)
		ORDER BY created_at DESC LIMIT $2 OFFSET $3`, status, limit, offset)
	if err != nil {
		return nil, 0, err
	}
	var out []*Campaign
	for rows.Next() {
		c, err := scan(rows)
		if err != nil {
			rows.Close()
			return nil, 0, err
		}
		out = append(out, c)
	}
	rows.Close()
	for _, c := range out {
		if c.Status != "draft" {
			c.Stats, _ = s.stats(ctx, c.ID)
		}
	}
	if out == nil {
		out = []*Campaign{}
	}
	return out, total, nil
}

func (s *Service) Get(ctx context.Context, id string) (*Campaign, error) {
	c, err := scan(s.DB.QueryRow(ctx, `SELECT `+cols+` FROM email_campaigns WHERE id=$1`, id))
	if err != nil {
		return nil, err
	}
	c.Stats, err = s.stats(ctx, id)
	return c, err
}

type Input struct {
	Name        *string  `json:"name"`
	Kind        *string  `json:"kind"`
	Subject     *string  `json:"subject"`
	Preheader   *string  `json:"preheader"`
	TemplateID  *string  `json:"template_id"`
	ContentHTML *string  `json:"content_html"`
	ContentText *string  `json:"content_text"`
	Segment     *Segment `json:"segment"`
}

func (s *Service) Create(ctx context.Context, in Input, actor string) (*Campaign, error) {
	if in.Name == nil || in.Subject == nil || strings.TrimSpace(*in.Name) == "" || strings.TrimSpace(*in.Subject) == "" {
		return nil, errors.New("name and subject are required")
	}
	kind := "newsletter"
	if in.Kind != nil && (*in.Kind == "announcement" || *in.Kind == "newsletter") {
		kind = *in.Kind
	}
	seg := Segment{}
	if in.Segment != nil {
		seg = *in.Segment
	}
	sb, _ := json.Marshal(seg)
	return scan(s.DB.QueryRow(ctx, `INSERT INTO email_campaigns (name, kind, subject, preheader, template_id, content_html,
		content_text, segment, created_by) VALUES ($1,$2,$3,COALESCE($4,''),NULLIF($5,'')::uuid,COALESCE($6,''),COALESCE($7,''),$8,$9)
		RETURNING `+cols, *in.Name, kind, *in.Subject, in.Preheader, deref(in.TemplateID), sanitizePtr(in.ContentHTML), in.ContentText, sb, actor))
}

func (s *Service) Update(ctx context.Context, id string, in Input) (*Campaign, error) {
	var seg []byte
	if in.Segment != nil {
		seg, _ = json.Marshal(in.Segment)
	}
	var tmpl any
	if in.TemplateID != nil {
		tmpl = *in.TemplateID
	}
	c, err := scan(s.DB.QueryRow(ctx, `UPDATE email_campaigns SET name=COALESCE($2,name), subject=COALESCE($3,subject),
		preheader=COALESCE($4,preheader), template_id=CASE WHEN $5::text IS NULL THEN template_id ELSE NULLIF($5,'')::uuid END,
		content_html=COALESCE($6,content_html), content_text=COALESCE($7,content_text), segment=COALESCE($8,segment),
		updated_at=now() WHERE id=$1 AND status='draft' RETURNING `+cols,
		id, in.Name, in.Subject, in.Preheader, tmpl, sanitizePtr(in.ContentHTML), in.ContentText, seg))
	if errors.Is(err, ErrNotFound) {
		if _, gerr := s.Get(ctx, id); gerr == nil {
			return nil, ErrNotDraft
		}
	}
	return c, err
}

func (s *Service) Delete(ctx context.Context, id string) error {
	tag, err := s.DB.Exec(ctx, `DELETE FROM email_campaigns WHERE id=$1 AND status IN ('draft','cancelled')`, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotDraft
	}
	return nil
}

// --- audience ---------------------------------------------------------------

func segmentSQL(seg Segment, approvedBefore *time.Time) (string, []any) {
	conds := []string{"TRUE"}
	var args []any
	add := func(c string, v any) {
		args = append(args, v)
		conds = append(conds, strings.ReplaceAll(c, "?", fmt.Sprintf("$%d", len(args))))
	}
	if len(seg.Topics) > 0 {
		add("EXISTS (SELECT 1 FROM subscriber_preferences p WHERE p.subscriber_id=e.id AND p.topics && ?)", seg.Topics)
	}
	if len(seg.Sources) > 0 {
		add("e.source = ANY(?)", seg.Sources)
	}
	if seg.SubscribedFrom != nil {
		add("e.created_at >= ?", *seg.SubscribedFrom)
	}
	if seg.SubscribedTo != nil {
		add("e.created_at < ?", *seg.SubscribedTo)
	}
	if len(seg.SubscriberIDs) > 0 {
		add("e.id = ANY(?::uuid[])", seg.SubscriberIDs)
	}
	if approvedBefore != nil {
		// Never extend an approved send to people who joined after approval.
		add("e.verified_at <= ?", *approvedBefore)
	}
	return strings.Join(conds, " AND "), args
}

type Audience struct {
	Count  int      `json:"count"`
	Hash   string   `json:"audience_hash"`
	Sample []string `json:"sample"`
}

// AudienceFor computes the eligible audience and a hash the owner must echo
// back when confirming the send.
func (s *Service) AudienceFor(ctx context.Context, seg Segment) (*Audience, error) {
	where, args := segmentSQL(seg, nil)
	rows, err := s.DB.Query(ctx, `SELECT e.id::text, e.email::text FROM eligible_marketing_subscribers e WHERE `+where+` ORDER BY e.id`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	h := sha256.New()
	a := &Audience{Sample: []string{}}
	for rows.Next() {
		var id, addr string
		if err := rows.Scan(&id, &addr); err != nil {
			return nil, err
		}
		h.Write([]byte(id + "\n"))
		a.Count++
		if len(a.Sample) < 5 {
			a.Sample = append(a.Sample, email.MaskEmail(addr))
		}
	}
	a.Hash = hex.EncodeToString(h.Sum(nil))
	return a, rows.Err()
}

// --- rendering --------------------------------------------------------------

type recipient struct {
	ID, Email, Name string
}

func (s *Service) render(ctx context.Context, db store.DB, c *Campaign, r recipient, es settings.Email) (subject, htmlBody, text string, err error) {
	var t *templates.Template
	if c.TemplateID != nil {
		t, err = templates.Get(ctx, db, *c.TemplateID)
	} else {
		t, err = templates.GetBySlug(ctx, db, "newsletter_default")
	}
	if err != nil {
		return "", "", "", fmt.Errorf("load template: %w", err)
	}
	first := r.Name
	if i := strings.IndexByte(first, ' '); i > 0 {
		first = first[:i]
	}
	if first == "" {
		first = "there"
	}
	unsub := s.Subscribers.UnsubscribeURL(r.ID)
	vars := map[string]string{"name": r.Name, "first_name": first, "email": r.Email, "subject": c.Subject}
	subject = render.Fill(strings.ReplaceAll(t.Subject, "{{subject}}", c.Subject), vars, false)
	htmlBody, text, err = render.Render(render.Input{
		Marketing: true, Subject: subject, Preheader: firstNonEmpty(c.Preheader, t.Preheader),
		ContentHTML: t.HTMLBody, ContentText: c.ContentText, Vars: vars,
		RawVars:        map[string]string{"content": c.ContentHTML},
		UnsubscribeURL: unsub, SiteURL: s.SiteURL, Settings: es,
	})
	return subject, htmlBody, text, err
}

func (s *Service) Preview(ctx context.Context, id string) (map[string]string, error) {
	c, err := s.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	es, _ := s.Settings.Email(ctx)
	subject, h, t, err := s.render(ctx, s.DB, c, recipient{ID: "00000000-0000-0000-0000-000000000000", Email: "reader@example.com", Name: "Sample Reader"}, es)
	if err != nil {
		return nil, err
	}
	return map[string]string{"subject": subject, "html": h, "text": t}, nil
}

// TestSend delivers a "[TEST]" copy to allow-listed test recipients only.
func (s *Service) TestSend(ctx context.Context, id string, to []string, allowed []string) (int, error) {
	c, err := s.Get(ctx, id)
	if err != nil {
		return 0, err
	}
	if len(to) == 0 || len(to) > 5 {
		return 0, errors.New("choose 1-5 test recipients")
	}
	es, _ := s.Settings.Email(ctx)
	allow := append(append([]string{}, es.TestRecipients...), allowed...)
	for i := range allow {
		allow[i] = strings.ToLower(allow[i])
	}
	n := 0
	for _, addr := range to {
		addr = strings.ToLower(strings.TrimSpace(addr))
		if !slices.Contains(allow, addr) {
			return n, fmt.Errorf("%s is not an allowed test recipient (add it in Email settings)", addr)
		}
		subject, h, t, err := s.render(ctx, s.DB, c, recipient{ID: "test", Email: addr, Name: "Test"}, es)
		if err != nil {
			return n, err
		}
		if _, _, err := queue.Enqueue(ctx, s.DB, queue.EnqueueRequest{
			IdempotencyKey: fmt.Sprintf("test:%s:%s:%d", id, addr, time.Now().UnixNano()),
			Category:       email.CategoryMarketing, Kind: "test", CampaignID: id,
			To: email.Address{Email: addr}, Subject: "[TEST] " + subject, HTML: h, Text: t, Priority: 5,
		}); err != nil {
			return n, err
		}
		n++
	}
	return n, nil
}

// --- approval & sending -------------------------------------------------------

type SendInput struct {
	Confirm            bool       `json:"confirm"`
	AudienceHash       string     `json:"audience_hash"`
	ExpectedCount      int        `json:"expected_count"`
	ScheduledAt        *time.Time `json:"scheduled_at"`
	AllowSimilarRecent bool       `json:"allow_similar_recent"`
}

type DuplicateWarning struct {
	CampaignID string    `json:"campaign_id"`
	Name       string    `json:"name"`
	SentAt     time.Time `json:"sent_at"`
}

var ErrSimilarRecent = errors.New("a campaign with the same subject was sent recently; confirm again with allow_similar_recent")

// Approve validates and atomically moves a draft to scheduled/sending. The
// status transition (WHERE status='draft') makes double-clicks and repeated
// API calls harmless: only one request can win.
func (s *Service) Approve(ctx context.Context, id string, in SendInput, actor string) (*Campaign, error) {
	if !in.Confirm {
		return nil, ErrConfirm
	}
	c, err := s.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	if c.Status != "draft" {
		return nil, ErrNotDraft
	}
	es, _ := s.Settings.Email(ctx)
	if strings.TrimSpace(es.PostalAddress) == "" || strings.TrimSpace(es.OrganizationName+es.SenderName) == "" {
		return nil, errors.New("set your sender name and postal address in Email settings before sending marketing email")
	}
	if strings.TrimSpace(c.ContentHTML) == "" && c.Kind != "blog_notification" {
		return nil, errors.New("campaign content is empty")
	}
	aud, err := s.AudienceFor(ctx, c.Segment)
	if err != nil {
		return nil, err
	}
	if aud.Hash != in.AudienceHash || aud.Count != in.ExpectedCount {
		return nil, ErrAudienceDrift
	}
	if aud.Count == 0 {
		return nil, errors.New("no eligible recipients match this segment")
	}
	if !in.AllowSimilarRecent {
		var dup string
		err := s.DB.QueryRow(ctx, `SELECT id FROM email_campaigns WHERE id <> $1 AND lower(subject)=lower($2)
			AND status IN ('scheduled','sending','sent') AND coalesce(approved_at, created_at) > now() - make_interval(hours => $3) LIMIT 1`,
			id, c.Subject, es.DuplicateSendWindowHours).Scan(&dup)
		if err == nil {
			return nil, ErrSimilarRecent
		}
	}
	status, scheduled := "sending", (*time.Time)(nil)
	if in.ScheduledAt != nil && in.ScheduledAt.After(time.Now().Add(time.Minute)) {
		status, scheduled = "scheduled", in.ScheduledAt
	}
	tag, err := s.DB.Exec(ctx, `UPDATE email_campaigns SET status=$2, scheduled_at=$3, approved_by=$4, approved_at=now(),
		audience_hash=$5, audience_count=$6, send_started_at=CASE WHEN $2='sending' THEN now() END, updated_at=now()
		WHERE id=$1 AND status='draft'`, id, status, scheduled, actor, aud.Hash, aud.Count)
	if err != nil {
		return nil, err
	}
	if tag.RowsAffected() == 0 {
		return nil, ErrNotDraft
	}
	return s.Get(ctx, id)
}

// Cancel stops a scheduled campaign, or the not-yet-sent remainder of one in progress.
func (s *Service) Cancel(ctx context.Context, id string) error {
	return store.InTx(ctx, s.DB, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `UPDATE email_campaigns SET status='cancelled', completed_at=now(), updated_at=now()
			WHERE id=$1 AND status IN ('scheduled','sending')`, id)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return errors.New("only scheduled or sending campaigns can be cancelled")
		}
		_, err = tx.Exec(ctx, `UPDATE email_jobs SET status='cancelled', finished_at=now(), last_error='campaign cancelled'
			WHERE campaign_id=$1 AND status IN ('queued','retry_scheduled')`, id)
		return err
	})
}

// Dispatch is a worker hook: starts due scheduled campaigns, fans out sending
// campaigns into queue jobs, and marks finished campaigns as sent.
func (s *Service) Dispatch(ctx context.Context) error {
	if _, err := s.DB.Exec(ctx, `UPDATE email_campaigns SET status='sending', send_started_at=now(), updated_at=now()
		WHERE status='scheduled' AND scheduled_at <= now()`); err != nil {
		return err
	}
	rows, err := s.DB.Query(ctx, `SELECT id FROM email_campaigns WHERE status='sending' AND materialized_at IS NULL`)
	if err != nil {
		return err
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		ids = append(ids, id)
	}
	rows.Close()
	ds, _ := s.Settings.Delivery(ctx)
	for _, id := range ids {
		for {
			done, err := s.MaterializeBatch(ctx, id, ds.CampaignBatchSize)
			if err != nil {
				return err
			}
			if done {
				break
			}
		}
	}
	_, err = s.DB.Exec(ctx, `UPDATE email_campaigns c SET status='sent', completed_at=now(), updated_at=now()
		WHERE status='sending' AND materialized_at IS NOT NULL AND NOT EXISTS (
		  SELECT 1 FROM email_jobs j WHERE j.campaign_id=c.id AND j.kind='campaign'
		  AND j.status IN ('queued','processing','retry_scheduled','acceptance_unknown'))`)
	return err
}

// MaterializeBatch creates recipients + jobs for up to n subscribers. The
// campaign row lock (SKIP LOCKED) lets only one worker fan out a campaign at a
// time, and UNIQUE(campaign_id,email) + the job idempotency key guarantee one
// email per person even if two workers race or a worker restarts mid-batch.
func (s *Service) MaterializeBatch(ctx context.Context, id string, n int) (done bool, err error) {
	es, _ := s.Settings.Email(ctx)
	err = store.InTx(ctx, s.DB, func(tx pgx.Tx) error {
		c, err := scan(tx.QueryRow(ctx, `SELECT `+cols+` FROM email_campaigns WHERE id=$1 AND status='sending'
			AND materialized_at IS NULL FOR UPDATE SKIP LOCKED`, id))
		if errors.Is(err, ErrNotFound) {
			done = true // another worker holds it, or it is already done
			return nil
		}
		if err != nil {
			return err
		}
		where, args := segmentSQL(c.Segment, c.ApprovedAt)
		args = append(args, c.ID, n)
		q := fmt.Sprintf(`SELECT e.id::text, e.email::text, coalesce(e.name,'') FROM eligible_marketing_subscribers e
			WHERE %s AND NOT EXISTS (SELECT 1 FROM email_campaign_recipients r WHERE r.campaign_id=$%d AND r.email=e.email)
			ORDER BY e.id LIMIT $%d`, where, len(args)-1, len(args))
		rows, err := tx.Query(ctx, q, args...)
		if err != nil {
			return err
		}
		var batch []recipient
		for rows.Next() {
			var r recipient
			if err := rows.Scan(&r.ID, &r.Email, &r.Name); err != nil {
				rows.Close()
				return err
			}
			batch = append(batch, r)
		}
		rows.Close()
		for _, r := range batch {
			subject, h, t, err := s.render(ctx, tx, c, r, es)
			if err != nil {
				return err
			}
			jobID, _, err := queue.Enqueue(ctx, tx, queue.EnqueueRequest{
				IdempotencyKey: "campaign:" + c.ID + ":" + r.ID,
				Category:       email.CategoryMarketing, Kind: "campaign", CampaignID: c.ID, SubscriberID: r.ID,
				To: email.Address{Email: r.Email, Name: r.Name}, Subject: subject, HTML: h, Text: t,
				Headers: s.Subscribers.ListUnsubscribeHeaders(r.ID), Tags: []string{"campaign:" + c.ID},
			})
			if err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, `INSERT INTO email_campaign_recipients (campaign_id, subscriber_id, email, job_id)
				VALUES ($1,$2,$3,$4) ON CONFLICT (campaign_id, email) DO NOTHING`, c.ID, r.ID, r.Email, jobID); err != nil {
				return err
			}
		}
		if len(batch) < n {
			done = true
			_, err = tx.Exec(ctx, `UPDATE email_campaigns SET materialized_at=now(), updated_at=now() WHERE id=$1`, c.ID)
		}
		return err
	})
	return done, err
}

func (s *Service) stats(ctx context.Context, id string) (*Stats, error) {
	st := &Stats{ByStatus: map[string]int{}}
	rows, err := s.DB.Query(ctx, `SELECT j.status, count(*) FROM email_jobs j WHERE j.campaign_id=$1 AND j.kind='campaign' GROUP BY j.status`, id)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var k string
		var n int
		if err := rows.Scan(&k, &n); err != nil {
			rows.Close()
			return nil, err
		}
		st.ByStatus[k] = n
		st.Recipients += n
		switch k {
		case "accepted", "deferred":
			st.Accepted += n
		case "delivered":
			st.Accepted += n
			st.Delivered += n
		case "bounced":
			st.Accepted += n
			st.Bounced += n
		case "complained":
			st.Accepted += n
			st.Delivered += n
			st.Complained += n
		case "failed", "dead_letter", "cancelled":
			st.Failed += n
		default:
			st.Pending += n
		}
	}
	rows.Close()
	err = s.DB.QueryRow(ctx, `SELECT count(*) FILTER (WHERE opened_at IS NOT NULL), count(*) FILTER (WHERE clicked_at IS NOT NULL)
		FROM email_campaign_recipients WHERE campaign_id=$1`, id).Scan(&st.Opened, &st.Clicked)
	return st, err
}

type RecipientRow struct {
	Email       string     `json:"email"`
	Status      string     `json:"status"`
	Provider    string     `json:"provider"`
	Attempts    int        `json:"attempts"`
	AcceptedAt  *time.Time `json:"accepted_at"`
	DeliveredAt *time.Time `json:"delivered_at"`
	OpenedAt    *time.Time `json:"opened_at"`
	ClickedAt   *time.Time `json:"clicked_at"`
	LastError   string     `json:"last_error"`
	JobID       string     `json:"job_id"`
}

func (s *Service) Recipients(ctx context.Context, id string, limit, offset int) ([]RecipientRow, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := s.DB.Query(ctx, `SELECT r.email, coalesce(j.status,'queued'), coalesce(p.name,''), coalesce(j.attempt_count,0),
		j.accepted_at, j.delivered_at, r.opened_at, r.clicked_at, coalesce(j.last_error,''), coalesce(j.id::text,'')
		FROM email_campaign_recipients r LEFT JOIN email_jobs j ON j.id=r.job_id
		LEFT JOIN email_providers p ON p.id=j.accepted_provider_id
		WHERE r.campaign_id=$1 ORDER BY r.created_at LIMIT $2 OFFSET $3`, id, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []RecipientRow{}
	for rows.Next() {
		var r RecipientRow
		if err := rows.Scan(&r.Email, &r.Status, &r.Provider, &r.Attempts, &r.AcceptedAt, &r.DeliveredAt, &r.OpenedAt,
			&r.ClickedAt, &r.LastError, &r.JobID); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// ReportCSV exports recipient-level outcomes for one campaign.
func (s *Service) ReportCSV(ctx context.Context, id string, w io.Writer) error {
	rows, err := s.Recipients(ctx, id, 1_000_000, 0)
	if err != nil {
		return err
	}
	cw := csvsafe.NewWriter(w)
	_ = cw.Write([]string{"email", "status", "provider", "attempts", "accepted_at", "delivered_at", "opened_at", "clicked_at", "last_error"})
	for _, r := range rows {
		_ = cw.Write([]string{r.Email, r.Status, r.Provider, fmt.Sprint(r.Attempts), ts(r.AcceptedAt), ts(r.DeliveredAt),
			ts(r.OpenedAt), ts(r.ClickedAt), r.LastError})
	}
	cw.Flush()
	return cw.Error()
}

// --- blog integration ---------------------------------------------------------

type BlogPost struct {
	ID               string
	Title            string
	Excerpt          string
	URL              string
	FeaturedImageURL string
	Author           string
	PublishedAt      time.Time
	Topics           []string
}

// CreateBlogNotification creates (once per post, ever) a draft campaign
// announcing a published article. The owner previews, test-sends and
// explicitly approves it like any other campaign.
func (s *Service) CreateBlogNotification(ctx context.Context, p BlogPost, actor string) (*Campaign, bool, error) {
	if existing, err := scan(s.DB.QueryRow(ctx, `SELECT `+cols+` FROM email_campaigns WHERE blog_post_id=$1`, p.ID)); err == nil {
		return existing, false, nil
	}
	t, err := templates.GetBySlug(ctx, s.DB, "blog_notification")
	if err != nil {
		return nil, false, err
	}
	img := ""
	if strings.HasPrefix(p.FeaturedImageURL, "https://") || strings.HasPrefix(p.FeaturedImageURL, "http://") {
		img = `<img src="` + html.EscapeString(p.FeaturedImageURL) + `" alt="" width="564" style="width:100%;max-width:564px;height:auto;border-radius:12px;margin-bottom:18px">`
	}
	vars := map[string]string{"title": p.Title, "excerpt": p.Excerpt, "url": p.URL, "author": p.Author,
		"published_date": p.PublishedAt.Format("2 Jan 2006")}
	content := strings.ReplaceAll(t.HTMLBody, "{{featured_image}}", img)
	content = render.Fill(content, vars, true)
	subject := render.Fill(t.Subject, vars, false)
	seg, _ := json.Marshal(Segment{})
	c, err := scan(s.DB.QueryRow(ctx, `INSERT INTO email_campaigns (name, kind, subject, preheader, content_html, segment, blog_post_id, created_by)
		VALUES ($1,'blog_notification',$2,$3,$4,$5,$6,$7) ON CONFLICT (blog_post_id) WHERE blog_post_id IS NOT NULL DO NOTHING RETURNING `+cols,
		"Article: "+p.Title, subject, truncate(p.Excerpt, 140), content, seg, p.ID, actor))
	if errors.Is(err, ErrNotFound) {
		existing, gerr := scan(s.DB.QueryRow(ctx, `SELECT `+cols+` FROM email_campaigns WHERE blog_post_id=$1`, p.ID))
		return existing, false, gerr
	}
	return c, err == nil, err
}

func sanitizePtr(s *string) *string {
	if s == nil {
		return nil
	}
	v := render.Sanitize(*s)
	return &v
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func firstNonEmpty(v ...string) string {
	for _, s := range v {
		if s != "" {
			return s
		}
	}
	return ""
}

func ts(t *time.Time) string {
	if t == nil {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}

func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) > n {
		return string(r[:n])
	}
	return s
}
