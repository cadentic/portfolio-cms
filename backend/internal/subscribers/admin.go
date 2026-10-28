package subscribers

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"errors"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/cadentic/portfolio-cms/backend/internal/store"
	"github.com/cadentic/portfolio-cms/backend/internal/suppression"
	"github.com/cadentic/portfolio-cms/backend/internal/tokens"
)

type Subscriber struct {
	ID             string     `json:"id"`
	Email          string     `json:"email"`
	Name           string     `json:"name"`
	Status         string     `json:"status"`
	Source         string     `json:"source"`
	Topics         []string   `json:"topics"`
	Frequency      string     `json:"frequency"`
	VerifiedAt     *time.Time `json:"verified_at"`
	UnsubscribedAt *time.Time `json:"unsubscribed_at"`
	CreatedAt      time.Time  `json:"created_at"`
	UpdatedAt      time.Time  `json:"updated_at"`
	Eligible       bool       `json:"eligible"`
}

type Filter struct {
	Query    string
	Status   string
	Source   string
	Topic    string
	From     *time.Time
	To       *time.Time
	Sort     string
	Page     int
	PageSize int
	Eligible bool
}

const subCols = `s.id, s.email, coalesce(s.name,''), s.status, s.source, coalesce(p.topics,'{}'), coalesce(p.frequency,'weekly'),
	s.verified_at, s.unsubscribed_at, s.created_at, s.updated_at,
	EXISTS (SELECT 1 FROM eligible_marketing_subscribers e WHERE e.id = s.id)`

func scanSub(row pgx.Row) (*Subscriber, error) {
	var s Subscriber
	err := row.Scan(&s.ID, &s.Email, &s.Name, &s.Status, &s.Source, &s.Topics, &s.Frequency, &s.VerifiedAt,
		&s.UnsubscribedAt, &s.CreatedAt, &s.UpdatedAt, &s.Eligible)
	return &s, err
}

func (f Filter) where() (string, []any) {
	var conds []string
	var args []any
	add := func(cond string, v any) {
		args = append(args, v)
		conds = append(conds, strings.ReplaceAll(cond, "?", "$"+itoa(len(args))))
	}
	if f.Query != "" {
		add("(s.email ILIKE '%' || ? || '%' OR s.name ILIKE '%' || ? || '%')", f.Query)
	}
	if f.Status != "" {
		add("s.status = ?", f.Status)
	}
	if f.Source != "" {
		add("s.source = ?", f.Source)
	}
	if f.Topic != "" {
		add("? = ANY(p.topics)", f.Topic)
	}
	if f.From != nil {
		add("s.created_at >= ?", *f.From)
	}
	if f.To != nil {
		add("s.created_at < ?", *f.To)
	}
	if f.Eligible {
		conds = append(conds, "s.id IN (SELECT id FROM eligible_marketing_subscribers)")
	}
	if len(conds) == 0 {
		return "", args
	}
	return " WHERE " + strings.Join(conds, " AND "), args
}

func orderBy(sort string) string {
	switch sort {
	case "oldest":
		return "s.created_at ASC"
	case "email":
		return "s.email ASC"
	case "email_desc":
		return "s.email DESC"
	case "status":
		return "s.status, s.created_at DESC"
	}
	return "s.created_at DESC"
}

func (s *Service) List(ctx context.Context, f Filter) ([]*Subscriber, int, error) {
	if f.PageSize <= 0 || f.PageSize > 200 {
		f.PageSize = 25
	}
	if f.Page < 1 {
		f.Page = 1
	}
	where, args := f.where()
	from := ` FROM subscribers s LEFT JOIN subscriber_preferences p ON p.subscriber_id = s.id`
	var total int
	if err := s.DB.QueryRow(ctx, `SELECT count(*)`+from+where, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	args = append(args, f.PageSize, (f.Page-1)*f.PageSize)
	rows, err := s.DB.Query(ctx, `SELECT `+subCols+from+where+` ORDER BY `+orderBy(f.Sort)+
		` LIMIT $`+itoa(len(args)-1)+` OFFSET $`+itoa(len(args)), args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	out := []*Subscriber{}
	for rows.Next() {
		sub, err := scanSub(rows)
		if err != nil {
			return nil, 0, err
		}
		out = append(out, sub)
	}
	return out, total, rows.Err()
}

type Consent struct {
	Action               string         `json:"action"`
	Purpose              string         `json:"purpose"`
	LawfulBasis          string         `json:"lawful_basis"`
	Source               string         `json:"source"`
	ConsentText          string         `json:"consent_text"`
	PrivacyPolicyVersion string         `json:"privacy_policy_version"`
	IP                   string         `json:"ip"`
	Evidence             map[string]any `json:"evidence"`
	Actor                string         `json:"actor"`
	CreatedAt            time.Time      `json:"created_at"`
}

type HistoryItem struct {
	JobID     string    `json:"job_id"`
	Kind      string    `json:"kind"`
	Subject   string    `json:"subject"`
	Status    string    `json:"status"`
	CreatedAt time.Time `json:"created_at"`
}

type Detail struct {
	Subscriber
	Consents     []Consent     `json:"consents"`
	History      []HistoryItem `json:"history"`
	Suppressions []string      `json:"suppressions"`
}

func (s *Service) Get(ctx context.Context, id string) (*Detail, error) {
	sub, err := scanSub(s.DB.QueryRow(ctx, `SELECT `+subCols+` FROM subscribers s LEFT JOIN subscriber_preferences p
		ON p.subscriber_id=s.id WHERE s.id=$1`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	d := &Detail{Subscriber: *sub, Consents: []Consent{}, History: []HistoryItem{}, Suppressions: []string{}}
	rows, err := s.DB.Query(ctx, `SELECT action, purpose, lawful_basis, source, coalesce(consent_text,''),
		coalesce(privacy_policy_version,''), coalesce(host(ip),''), evidence, coalesce(actor,''), created_at
		FROM subscriber_consents WHERE subscriber_id=$1 ORDER BY created_at DESC`, id)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var c Consent
		var ev []byte
		if err := rows.Scan(&c.Action, &c.Purpose, &c.LawfulBasis, &c.Source, &c.ConsentText, &c.PrivacyPolicyVersion,
			&c.IP, &ev, &c.Actor, &c.CreatedAt); err != nil {
			rows.Close()
			return nil, err
		}
		_ = json.Unmarshal(ev, &c.Evidence)
		d.Consents = append(d.Consents, c)
	}
	rows.Close()
	rows, err = s.DB.Query(ctx, `SELECT id, kind, subject, status, created_at FROM email_jobs
		WHERE subscriber_id=$1 OR to_email=$2 ORDER BY created_at DESC LIMIT 50`, id, sub.Email)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var h HistoryItem
		if err := rows.Scan(&h.JobID, &h.Kind, &h.Subject, &h.Status, &h.CreatedAt); err != nil {
			rows.Close()
			return nil, err
		}
		d.History = append(d.History, h)
	}
	rows.Close()
	rows, err = s.DB.Query(ctx, `SELECT scope || ':' || reason FROM email_suppressions WHERE email=$1 AND lifted_at IS NULL`, sub.Email)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var x string
		if err := rows.Scan(&x); err != nil {
			return nil, err
		}
		d.Suppressions = append(d.Suppressions, x)
	}
	return d, rows.Err()
}

// ManualInput is used by the owner to add a contact. A lawful basis and a
// description of the consent evidence are mandatory; by default the contact
// still has to confirm by email (double opt-in).
type ManualInput struct {
	Email             string   `json:"email"`
	Name              string   `json:"name"`
	Topics            []string `json:"topics"`
	LawfulBasis       string   `json:"lawful_basis"`
	ConsentEvidence   string   `json:"consent_evidence"`
	ConsentObtainedAt string   `json:"consent_obtained_at"`
	SendConfirmation  *bool    `json:"send_confirmation"`
}

func (in *ManualInput) validate() error {
	if in.LawfulBasis != "consent" {
		return errors.New("newsletter subscribers require lawful_basis \"consent\" (with evidence)")
	}
	if len(strings.TrimSpace(in.ConsentEvidence)) < 10 {
		return errors.New("describe how and when consent was obtained (consent_evidence)")
	}
	return nil
}

func (s *Service) CreateManual(ctx context.Context, in ManualInput, actor string) (string, error) {
	addr, err := NormalizeEmail(in.Email)
	if err != nil {
		return "", err
	}
	if err := in.validate(); err != nil {
		return "", err
	}
	sendDOI := in.SendConfirmation == nil || *in.SendConfirmation
	es, _ := s.Settings.Email(ctx)
	var id string
	err = store.InTx(ctx, s.DB, func(tx pgx.Tx) error {
		err := tx.QueryRow(ctx, `INSERT INTO subscribers (email, name, status, source) VALUES ($1, NULLIF($2,''), 'pending', 'manual')
			RETURNING id`, addr, cleanName(in.Name)).Scan(&id)
		if store.IsUniqueViolation(err) {
			return errors.New("this email address is already in the list")
		}
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO subscriber_preferences (subscriber_id, topics) VALUES ($1,$2)`, id, filterTopics(in.Topics, es.Topics)); err != nil {
			return err
		}
		ev := map[string]any{"evidence": in.ConsentEvidence, "obtained_at": in.ConsentObtainedAt, "entered_by": actor}
		if _, err := tx.Exec(ctx, `INSERT INTO subscriber_consents (subscriber_id, action, lawful_basis, source, consent_text, evidence, actor)
			VALUES ($1,'granted',$2,'manual',$3,$4,$5)`, id, in.LawfulBasis, es.ConsentText, ev, actor); err != nil {
			return err
		}
		if sendDOI {
			return s.sendConfirmation(ctx, tx, id, addr, in.Name, es)
		}
		// Owner attests that verified consent already exists (e.g. migrated list).
		if _, err := tx.Exec(ctx, `INSERT INTO subscriber_consents (subscriber_id, action, lawful_basis, source, evidence, actor)
			VALUES ($1,'attested',$2,'manual',$3,$4)`, id, in.LawfulBasis, ev, actor); err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `UPDATE subscribers SET status='verified', verified_at=now() WHERE id=$1`, id)
		return err
	})
	return id, err
}

type UpdateInput struct {
	Name      *string  `json:"name"`
	Topics    []string `json:"topics"`
	Frequency *string  `json:"frequency"`
}

func (s *Service) Update(ctx context.Context, id string, in UpdateInput) error {
	es, _ := s.Settings.Email(ctx)
	return store.InTx(ctx, s.DB, func(tx pgx.Tx) error {
		if in.Name != nil {
			if _, err := tx.Exec(ctx, `UPDATE subscribers SET name=NULLIF($2,''), updated_at=now() WHERE id=$1`, id, cleanName(*in.Name)); err != nil {
				return err
			}
		}
		if in.Topics != nil || in.Frequency != nil {
			freq := "weekly"
			if in.Frequency != nil && (*in.Frequency == "weekly" || *in.Frequency == "monthly" || *in.Frequency == "all") {
				freq = *in.Frequency
			}
			if _, err := tx.Exec(ctx, `INSERT INTO subscriber_preferences (subscriber_id, topics, frequency) VALUES ($1,$2,$3)
				ON CONFLICT (subscriber_id) DO UPDATE SET topics=COALESCE($4, subscriber_preferences.topics), frequency=$3, updated_at=now()`,
				id, filterTopics(in.Topics, es.Topics), freq, nilIfNil(in.Topics, filterTopics(in.Topics, es.Topics))); err != nil {
				return err
			}
		}
		return nil
	})
}

func nilIfNil(in []string, v []string) any {
	if in == nil {
		return nil
	}
	return v
}

// AdminUnsubscribe records an unsubscribe on the subscriber's behalf (e.g. a reply asking to stop).
func (s *Service) AdminUnsubscribe(ctx context.Context, id, actor string) error {
	return store.InTx(ctx, s.DB, func(tx pgx.Tx) error {
		var addr string
		if err := tx.QueryRow(ctx, `SELECT email FROM subscribers WHERE id=$1 FOR UPDATE`, id).Scan(&addr); err != nil {
			return ErrNotFound
		}
		return suppressionUnsubscribe(ctx, tx, addr, actor)
	})
}

// Erase deletes a subscriber's personal data (right to erasure). A marketing
// suppression keyed by email is retained so the person is never re-added by
// an import, as permitted for honouring an objection.
func (s *Service) Erase(ctx context.Context, id, actor string) error {
	return store.InTx(ctx, s.DB, func(tx pgx.Tx) error {
		var addr string
		if err := tx.QueryRow(ctx, `SELECT email FROM subscribers WHERE id=$1 FOR UPDATE`, id).Scan(&addr); err != nil {
			return ErrNotFound
		}
		if err := suppressionAdd(ctx, tx, addr, actor); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE email_jobs SET to_name=NULL, html_body='[erased]', text_body='[erased]'
			WHERE subscriber_id=$1 AND status NOT IN ('queued','processing','retry_scheduled')`, id); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `DELETE FROM subscribers WHERE id=$1`, id)
		return err
	})
}

type Stats struct {
	Total        int     `json:"total"`
	Verified     int     `json:"verified"`
	Pending      int     `json:"pending"`
	Unsubscribed int     `json:"unsubscribed"`
	Bounced      int     `json:"bounced"`
	Complained   int     `json:"complained"`
	Suppressed   int     `json:"suppressed"`
	Eligible     int     `json:"eligible"`
	New30d       int     `json:"new_30d"`
	Prev30d      int     `json:"prev_30d"`
	GrowthPct    float64 `json:"growth_pct"`
}

func (s *Service) Stats(ctx context.Context) (*Stats, error) {
	var st Stats
	err := s.DB.QueryRow(ctx, `SELECT count(*), count(*) FILTER (WHERE status='verified'), count(*) FILTER (WHERE status='pending'),
		count(*) FILTER (WHERE status='unsubscribed'), count(*) FILTER (WHERE status='bounced'),
		count(*) FILTER (WHERE status='complained'),
		count(*) FILTER (WHERE created_at >= now() - interval '30 days'),
		count(*) FILTER (WHERE created_at >= now() - interval '60 days' AND created_at < now() - interval '30 days'),
		(SELECT count(DISTINCT email) FROM email_suppressions WHERE lifted_at IS NULL),
		(SELECT count(*) FROM eligible_marketing_subscribers)
		FROM subscribers`).Scan(&st.Total, &st.Verified, &st.Pending, &st.Unsubscribed, &st.Bounced, &st.Complained,
		&st.New30d, &st.Prev30d, &st.Suppressed, &st.Eligible)
	if st.Prev30d > 0 {
		st.GrowthPct = float64(st.New30d-st.Prev30d) / float64(st.Prev30d) * 100
	}
	return &st, err
}

// csvSafe neutralises spreadsheet formula injection.
func csvSafe(v string) string {
	if v != "" && strings.ContainsAny(v[:1], "=+-@\t\r") {
		return "'" + v
	}
	return v
}

// ExportCSV writes subscribers matching f (eligible-only by default).
func (s *Service) ExportCSV(ctx context.Context, w io.Writer, f Filter) error {
	where, args := f.where()
	rows, err := s.DB.Query(ctx, `SELECT `+subCols+`, (SELECT max(created_at) FROM subscriber_consents c
		WHERE c.subscriber_id=s.id AND c.action IN ('verified','attested')) FROM subscribers s
		LEFT JOIN subscriber_preferences p ON p.subscriber_id=s.id`+where+` ORDER BY s.created_at`, args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	cw := csv.NewWriter(w)
	_ = cw.Write([]string{"email", "name", "status", "source", "topics", "subscribed_at", "verified_at", "consent_verified_at", "eligible"})
	for rows.Next() {
		var sub Subscriber
		var consentAt *time.Time
		if err := rows.Scan(&sub.ID, &sub.Email, &sub.Name, &sub.Status, &sub.Source, &sub.Topics, &sub.Frequency,
			&sub.VerifiedAt, &sub.UnsubscribedAt, &sub.CreatedAt, &sub.UpdatedAt, &sub.Eligible, &consentAt); err != nil {
			return err
		}
		_ = cw.Write([]string{csvSafe(sub.Email), csvSafe(sub.Name), sub.Status, sub.Source, strings.Join(sub.Topics, ";"),
			sub.CreatedAt.UTC().Format(time.RFC3339), fmtTime(sub.VerifiedAt), fmtTime(consentAt), boolStr(sub.Eligible)})
	}
	cw.Flush()
	return cw.Error()
}

// --- CSV import ------------------------------------------------------------

type ImportRow struct {
	Line   int      `json:"line"`
	Email  string   `json:"email"`
	Name   string   `json:"name"`
	Topics []string `json:"topics"`
	Status string   `json:"status"` // new | duplicate_in_file | exists | suppressed | invalid
	Error  string   `json:"error,omitempty"`
}

type ImportPreview struct {
	Token   string         `json:"token"`
	Summary map[string]int `json:"summary"`
	Rows    []ImportRow    `json:"rows"`
}

const maxImportRows = 20000

// PreviewImport validates a CSV (header: email,name,topics) and stages the
// result. Nothing is imported until CommitImport is called with the token.
func (s *Service) PreviewImport(ctx context.Context, r io.Reader, actor string) (*ImportPreview, error) {
	cr := csv.NewReader(io.LimitReader(r, 10<<20))
	cr.FieldsPerRecord = -1
	cr.TrimLeadingSpace = true
	header, err := cr.Read()
	if err != nil {
		return nil, errors.New("could not read CSV header")
	}
	idx := map[string]int{}
	for i, h := range header {
		idx[strings.ToLower(strings.TrimSpace(strings.TrimPrefix(h, "\ufeff")))] = i
	}
	ei, ok := idx["email"]
	if !ok {
		return nil, errors.New(`CSV must have an "email" column`)
	}
	es, _ := s.Settings.Email(ctx)
	seen := map[string]bool{}
	var rows []ImportRow
	line := 1
	for {
		rec, err := cr.Read()
		if err == io.EOF {
			break
		}
		line++
		if err != nil {
			rows = append(rows, ImportRow{Line: line, Status: "invalid", Error: "malformed CSV row"})
			continue
		}
		if len(rows) >= maxImportRows {
			return nil, errors.New("CSV exceeds 20,000 rows; split it into smaller files")
		}
		get := func(col string) string {
			if i, ok := idx[col]; ok && i < len(rec) {
				return strings.TrimSpace(rec[i])
			}
			return ""
		}
		row := ImportRow{Line: line, Name: cleanName(get("name"))}
		if ei < len(rec) {
			row.Email = strings.TrimSpace(rec[ei])
		}
		if t := get("topics"); t != "" {
			row.Topics = filterTopics(strings.FieldsFunc(t, func(r rune) bool { return r == ';' || r == '|' }), es.Topics)
		}
		addr, err := NormalizeEmail(row.Email)
		switch {
		case err != nil:
			row.Status, row.Error = "invalid", "invalid email address"
		case seen[addr]:
			row.Email, row.Status = addr, "duplicate_in_file"
		default:
			row.Email, row.Status = addr, "new"
			seen[addr] = true
		}
		rows = append(rows, row)
	}
	// Check existing subscribers and suppressions in one round trip.
	var emails []string
	for _, r := range rows {
		if r.Status == "new" {
			emails = append(emails, r.Email)
		}
	}
	existing := map[string]bool{}
	suppressed := map[string]bool{}
	if len(emails) > 0 {
		q, err := s.DB.Query(ctx, `SELECT email::text, 'exists' FROM subscribers WHERE email = ANY($1::citext[])
			UNION ALL SELECT email::text, 'suppressed' FROM email_suppressions WHERE lifted_at IS NULL AND email = ANY($1::citext[])`, emails)
		if err != nil {
			return nil, err
		}
		for q.Next() {
			var e, kind string
			if err := q.Scan(&e, &kind); err != nil {
				q.Close()
				return nil, err
			}
			if kind == "exists" {
				existing[strings.ToLower(e)] = true
			} else {
				suppressed[strings.ToLower(e)] = true
			}
		}
		q.Close()
	}
	summary := map[string]int{"total": len(rows)}
	for i := range rows {
		if rows[i].Status == "new" {
			if suppressed[rows[i].Email] {
				rows[i].Status = "suppressed"
			} else if existing[rows[i].Email] {
				rows[i].Status = "exists"
			}
		}
		summary[rows[i].Status]++
	}
	raw, hash := tokens.NewRandom()
	b, _ := json.Marshal(rows)
	sb, _ := json.Marshal(summary)
	if _, err := s.DB.Exec(ctx, `INSERT INTO subscriber_import_batches (token_hash, rows, summary, created_by, expires_at)
		VALUES ($1,$2,$3,$4, now() + interval '1 hour')`, hash, b, sb, actor); err != nil {
		return nil, err
	}
	preview := rows
	if len(preview) > 200 {
		preview = preview[:200]
	}
	return &ImportPreview{Token: raw, Summary: summary, Rows: preview}, nil
}

type CommitInput struct {
	Token            string `json:"token"`
	Confirm          bool   `json:"confirm"`
	LawfulBasis      string `json:"lawful_basis"`
	ConsentEvidence  string `json:"consent_evidence"`
	SendConfirmation *bool  `json:"send_confirmation"`
}

type CommitResult struct {
	Imported int `json:"imported"`
	Skipped  int `json:"skipped"`
}

// CommitImport imports the staged "new" rows. Contacts are added as pending
// and receive a double opt-in email unless the owner explicitly attests to
// existing verified consent.
func (s *Service) CommitImport(ctx context.Context, in CommitInput, actor string) (*CommitResult, error) {
	if !in.Confirm {
		return nil, errors.New("explicit confirmation is required")
	}
	mi := ManualInput{LawfulBasis: in.LawfulBasis, ConsentEvidence: in.ConsentEvidence}
	if err := mi.validate(); err != nil {
		return nil, err
	}
	sendDOI := in.SendConfirmation == nil || *in.SendConfirmation
	es, _ := s.Settings.Email(ctx)
	res := &CommitResult{}
	err := store.InTx(ctx, s.DB, func(tx pgx.Tx) error {
		var batchID string
		var raw []byte
		err := tx.QueryRow(ctx, `UPDATE subscriber_import_batches SET committed_at=now()
			WHERE token_hash=$1 AND committed_at IS NULL AND expires_at > now() RETURNING id, rows`, tokens.Hash(in.Token)).Scan(&batchID, &raw)
		if errors.Is(err, pgx.ErrNoRows) {
			return errors.New("import preview expired or already committed; upload the file again")
		}
		if err != nil {
			return err
		}
		var rows []ImportRow
		if err := json.Unmarshal(raw, &rows); err != nil {
			return err
		}
		ev := map[string]any{"evidence": in.ConsentEvidence, "import_batch": batchID, "entered_by": actor}
		for _, r := range rows {
			if r.Status != "new" {
				res.Skipped++
				continue
			}
			var id string
			err := tx.QueryRow(ctx, `INSERT INTO subscribers (email, name, status, source) VALUES ($1, NULLIF($2,''), 'pending', 'import')
				ON CONFLICT (email) DO NOTHING RETURNING id`, r.Email, r.Name).Scan(&id)
			if errors.Is(err, pgx.ErrNoRows) {
				res.Skipped++
				continue
			}
			if err != nil {
				return err
			}
			var suppressed bool
			if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM email_suppressions WHERE email=$1 AND lifted_at IS NULL)`, r.Email).Scan(&suppressed); err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, `INSERT INTO subscriber_preferences (subscriber_id, topics) VALUES ($1,$2)`, id, orEmpty(r.Topics)); err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, `INSERT INTO subscriber_consents (subscriber_id, action, lawful_basis, source, evidence, actor)
				VALUES ($1,'granted',$2,'import',$3,$4)`, id, in.LawfulBasis, ev, actor); err != nil {
				return err
			}
			if sendDOI && !suppressed {
				if err := s.sendConfirmation(ctx, tx, id, r.Email, r.Name, es); err != nil {
					return err
				}
			} else if !sendDOI {
				if _, err := tx.Exec(ctx, `INSERT INTO subscriber_consents (subscriber_id, action, lawful_basis, source, evidence, actor)
					VALUES ($1,'attested',$2,'import',$3,$4)`, id, in.LawfulBasis, ev, actor); err != nil {
					return err
				}
				if _, err := tx.Exec(ctx, `UPDATE subscribers SET status='verified', verified_at=now() WHERE id=$1`, id); err != nil {
					return err
				}
			}
			res.Imported++
		}
		return nil
	})
	return res, err
}

// PurgeStalePending removes never-confirmed sign-ups after the retention period.
func (s *Service) PurgeStalePending(ctx context.Context) error {
	es, _ := s.Settings.Email(ctx)
	_, err := s.DB.Exec(ctx, `DELETE FROM subscribers WHERE status='pending' AND verified_at IS NULL
		AND created_at < now() - make_interval(days => $1)
		AND NOT EXISTS (SELECT 1 FROM email_jobs j WHERE j.subscriber_id = subscribers.id AND j.status IN ('queued','processing','retry_scheduled'))`,
		es.PendingRetentionDays)
	if err != nil {
		return err
	}
	_, err = s.DB.Exec(ctx, `DELETE FROM rate_limit_counters WHERE window_start < now() - interval '2 days'`)
	if err != nil {
		return err
	}
	_, err = s.DB.Exec(ctx, `UPDATE email_jobs SET html_body='[purged]', text_body='[purged]'
		WHERE finished_at < now() - make_interval(days => $1) AND html_body <> '[purged]'`, es.JobBodyRetentionDays)
	if err != nil {
		return err
	}
	_, err = s.DB.Exec(ctx, `DELETE FROM subscriber_import_batches WHERE expires_at < now() - interval '1 day'`)
	return err
}

func orEmpty(v []string) []string {
	if v == nil {
		return []string{}
	}
	return v
}

func fmtTime(t *time.Time) string {
	if t == nil {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}

func boolStr(b bool) string {
	if b {
		return "yes"
	}
	return "no"
}

func itoa(i int) string { return strconv.Itoa(i) }

func suppressionUnsubscribe(ctx context.Context, tx pgx.Tx, addr, actor string) error {
	return suppression.Unsubscribe(ctx, tx, addr, "admin", actor)
}

func suppressionAdd(ctx context.Context, tx pgx.Tx, addr, actor string) error {
	return suppression.Add(ctx, tx, addr, suppression.ScopeMarketing, "manual", "erasure", nil, "erased by "+actor)
}
