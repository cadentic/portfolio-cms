// Package content is the no-code page & article builder: a hero (top) section
// that can be an image, an uploaded video or a YouTube video, followed by text
// blocks. Content is stored as validated JSON and rendered by Angular (SSR).
package content

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"time"
	"unicode"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/text/unicode/norm"

	"github.com/cadentic/portfolio-cms/backend/internal/render"
	"github.com/cadentic/portfolio-cms/backend/internal/store"
)

var ErrNotFound = errors.New("page not found")

type Hero struct {
	Type       string `json:"type"` // none | image | video | youtube
	URL        string `json:"url,omitempty"`
	YouTubeID  string `json:"youtube_id,omitempty"`
	Alt        string `json:"alt,omitempty"`
	Caption    string `json:"caption,omitempty"`
	Poster     string `json:"poster,omitempty"`
	Heading    string `json:"heading,omitempty"`
	Subheading string `json:"subheading,omitempty"`
}

type Block struct {
	Type     string   `json:"type"`
	Text     string   `json:"text,omitempty"`
	Level    int      `json:"level,omitempty"`
	Style    string   `json:"style,omitempty"`
	Items    []string `json:"items,omitempty"`
	Cite     string   `json:"cite,omitempty"`
	Language string   `json:"language,omitempty"`
	Tone     string   `json:"tone,omitempty"`
	HTML     string   `json:"html,omitempty"`
}

type SEO struct {
	Title       string `json:"title,omitempty"`
	Description string `json:"description,omitempty"`
	OGImage     string `json:"og_image,omitempty"`
	Canonical   string `json:"canonical,omitempty"`
	NoIndex     bool   `json:"noindex,omitempty"`
}

type Page struct {
	ID               string     `json:"id"`
	Kind             string     `json:"kind"`
	ParentID         *string    `json:"parent_id"`
	MenuOrder        int        `json:"menu_order"`
	Slug             string     `json:"slug"`
	Path             string     `json:"path"`
	Title            string     `json:"title"`
	Excerpt          string     `json:"excerpt"`
	Hero             Hero       `json:"hero"`
	Blocks           []Block    `json:"blocks"`
	FeaturedImageURL string     `json:"featured_image_url"`
	AuthorName       string     `json:"author_name"`
	Categories       []string   `json:"categories"`
	Tags             []string   `json:"tags"`
	SEO              SEO        `json:"seo"`
	Status           string     `json:"status"`
	PublishedAt      *time.Time `json:"published_at"`
	FirstPublishedAt *time.Time `json:"first_published_at"`
	Source           string     `json:"source,omitempty"`
	CreatedAt        time.Time  `json:"created_at"`
	UpdatedAt        time.Time  `json:"updated_at"`
	ReadingMinutes   int        `json:"reading_minutes"`
}

const cols = `id, kind, parent_id, menu_order, slug, title, excerpt, hero, blocks, coalesce(featured_image_url,''),
	coalesce(author_name,''), categories, tags, seo, status, published_at, first_published_at, coalesce(source,''),
	created_at, updated_at`

func scan(row pgx.Row) (*Page, error) {
	var p Page
	var hero, blocks, seo []byte
	err := row.Scan(&p.ID, &p.Kind, &p.ParentID, &p.MenuOrder, &p.Slug, &p.Title, &p.Excerpt, &hero, &blocks,
		&p.FeaturedImageURL, &p.AuthorName, &p.Categories, &p.Tags, &seo, &p.Status, &p.PublishedAt,
		&p.FirstPublishedAt, &p.Source, &p.CreatedAt, &p.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	_ = json.Unmarshal(hero, &p.Hero)
	_ = json.Unmarshal(blocks, &p.Blocks)
	_ = json.Unmarshal(seo, &p.SEO)
	if p.Blocks == nil {
		p.Blocks = []Block{}
	}
	if p.Kind == "article" {
		p.Path = "/blog/" + p.Slug
	} else {
		p.Path = "/" + p.Slug
	}
	words := 0
	for _, b := range p.Blocks {
		words += len(strings.Fields(b.Text + " " + strings.Join(b.Items, " ") + " " + render.HTMLToText(b.HTML)))
	}
	p.ReadingMinutes = max(1, (words+199)/200)
	return &p, nil
}

type Service struct {
	DB      *pgxpool.Pool
	SiteURL string
}

// --- validation ---------------------------------------------------------------

var (
	slugRe   = regexp.MustCompile(`^[a-z0-9]+(?:-[a-z0-9]+)*$`)
	ytIDRe   = regexp.MustCompile(`^[A-Za-z0-9_-]{11}$`)
	ytURLRe  = regexp.MustCompile(`(?:youtube\.com/(?:watch\?(?:.*&)?v=|embed/|shorts/|live/)|youtu\.be/)([A-Za-z0-9_-]{11})`)
	reserved = map[string]bool{"admin": true, "api": true, "blog": true, "subscribe": true, "unsubscribe": true, "privacy": true,
		"media": true, "assets": true, "sitemap.xml": true, "robots.txt": true, "oauth": true, "mcp": true, "videos": true}
	blockKind = map[string]bool{"heading": true, "paragraph": true, "list": true, "quote": true, "code": true, "callout": true, "divider": true, "html": true}
)

// Slugify converts a title into a URL slug.
func Slugify(s string) string {
	s = norm.NFKD.String(strings.ToLower(s))
	var b strings.Builder
	dash := false
	for _, r := range s {
		switch {
		case r < 128 && (unicode.IsLetter(r) || unicode.IsDigit(r)):
			b.WriteRune(r)
			dash = false
		case unicode.Is(unicode.Mn, r):
		default:
			if !dash && b.Len() > 0 {
				b.WriteByte('-')
				dash = true
			}
		}
	}
	out := strings.Trim(b.String(), "-")
	if len(out) > 80 {
		out = strings.Trim(out[:80], "-")
	}
	return out
}

// YouTubeID extracts an 11-character video ID from an ID or any common YouTube URL.
func YouTubeID(s string) string {
	s = strings.TrimSpace(s)
	if ytIDRe.MatchString(s) {
		return s
	}
	if m := ytURLRe.FindStringSubmatch(s); m != nil {
		return m[1]
	}
	return ""
}

func safeURL(s string) bool {
	if s == "" {
		return true
	}
	if strings.HasPrefix(s, "/") && !strings.HasPrefix(s, "//") {
		return true
	}
	u, err := url.Parse(s)
	return err == nil && u.Scheme == "https" && u.Host != ""
}

func clip(s string, n int) string {
	s = strings.TrimSpace(s)
	if r := []rune(s); len(r) > n {
		return string(r[:n])
	}
	return s
}

func normalizeHero(h Hero) (Hero, error) {
	h.Alt, h.Caption = clip(h.Alt, 300), clip(h.Caption, 300)
	h.Heading, h.Subheading = clip(h.Heading, 200), clip(h.Subheading, 400)
	switch h.Type {
	case "", "none":
		return Hero{Type: "none", Heading: h.Heading, Subheading: h.Subheading}, nil
	case "image":
		if h.URL == "" || !safeURL(h.URL) {
			return h, errors.New("hero image needs an https:// or /media/ URL")
		}
		if h.Alt == "" {
			return h, errors.New("hero image needs alt text for accessibility")
		}
		h.YouTubeID, h.Poster = "", ""
	case "video":
		if h.URL == "" || !safeURL(h.URL) || !safeURL(h.Poster) {
			return h, errors.New("hero video needs an https:// or /media/ URL")
		}
		h.YouTubeID = ""
	case "youtube":
		id := YouTubeID(firstNonEmpty(h.YouTubeID, h.URL))
		if id == "" {
			return h, errors.New("enter a YouTube video URL or 11-character video ID")
		}
		h.YouTubeID, h.URL = id, "https://www.youtube.com/watch?v="+id
		if h.Poster != "" && !safeURL(h.Poster) {
			return h, errors.New("poster must be an https:// or /media/ URL")
		}
	default:
		return h, fmt.Errorf("unknown hero type %q (use none, image, video or youtube)", h.Type)
	}
	return h, nil
}

func normalizeBlocks(in []Block) ([]Block, error) {
	if len(in) > 300 {
		return nil, errors.New("too many blocks (max 300)")
	}
	out := make([]Block, 0, len(in))
	for i, b := range in {
		if !blockKind[b.Type] {
			return nil, fmt.Errorf("block %d: unsupported type %q", i+1, b.Type)
		}
		nb := Block{Type: b.Type}
		switch b.Type {
		case "heading":
			nb.Text, nb.Level = clip(b.Text, 200), b.Level
			if nb.Level < 2 || nb.Level > 4 {
				nb.Level = 2
			}
		case "paragraph":
			nb.Text = clip(b.Text, 20000)
		case "list":
			nb.Style = "bullet"
			if b.Style == "numbered" {
				nb.Style = "numbered"
			}
			for _, it := range b.Items {
				if it = clip(it, 2000); it != "" {
					nb.Items = append(nb.Items, it)
				}
			}
		case "quote":
			nb.Text, nb.Cite = clip(b.Text, 4000), clip(b.Cite, 200)
		case "code":
			nb.Text, nb.Language = clip(b.Text, 40000), clip(b.Language, 30)
			nb.Text = b.Text
		case "callout":
			nb.Text, nb.Tone = clip(b.Text, 4000), "info"
			if b.Tone == "warning" || b.Tone == "success" {
				nb.Tone = b.Tone
			}
		case "html":
			nb.HTML = render.Sanitize(b.HTML)
		}
		out = append(out, nb)
	}
	return out, nil
}

// --- CRUD -----------------------------------------------------------------

type Input struct {
	Kind             *string  `json:"kind"`
	ParentID         *string  `json:"parent_id"`
	MenuOrder        *int     `json:"menu_order"`
	Slug             *string  `json:"slug"`
	Title            *string  `json:"title"`
	Excerpt          *string  `json:"excerpt"`
	Hero             *Hero    `json:"hero"`
	Blocks           []Block  `json:"blocks"`
	FeaturedImageURL *string  `json:"featured_image_url"`
	AuthorName       *string  `json:"author_name"`
	Categories       []string `json:"categories"`
	Tags             []string `json:"tags"`
	SEO              *SEO     `json:"seo"`
}

func (s *Service) prepare(in *Input, kind string) (map[string]any, error) {
	v := map[string]any{}
	if in.Title != nil {
		t := clip(*in.Title, 200)
		if t == "" {
			return nil, errors.New("title is required")
		}
		v["title"] = t
	}
	if in.Slug != nil {
		sl := Slugify(*in.Slug)
		if !slugRe.MatchString(sl) {
			return nil, errors.New("slug may contain lowercase letters, numbers and dashes")
		}
		if kind == "page" && reserved[sl] {
			return nil, fmt.Errorf("%q is reserved; choose another slug", sl)
		}
		v["slug"] = sl
	}
	if in.Excerpt != nil {
		v["excerpt"] = clip(*in.Excerpt, 500)
	}
	if in.Hero != nil {
		h, err := normalizeHero(*in.Hero)
		if err != nil {
			return nil, err
		}
		b, _ := json.Marshal(h)
		v["hero"] = b
	}
	if in.Blocks != nil {
		bl, err := normalizeBlocks(in.Blocks)
		if err != nil {
			return nil, err
		}
		b, _ := json.Marshal(bl)
		v["blocks"] = b
	}
	if in.FeaturedImageURL != nil {
		if !safeURL(*in.FeaturedImageURL) {
			return nil, errors.New("featured image must be an https:// or /media/ URL")
		}
		v["featured_image_url"] = *in.FeaturedImageURL
	}
	if in.AuthorName != nil {
		v["author_name"] = clip(*in.AuthorName, 100)
	}
	if in.Categories != nil {
		v["categories"] = cleanList(in.Categories)
	}
	if in.Tags != nil {
		v["tags"] = cleanList(in.Tags)
	}
	if in.SEO != nil {
		seo := *in.SEO
		seo.Title, seo.Description = clip(seo.Title, 70), clip(seo.Description, 170)
		if !safeURL(seo.OGImage) || !safeURL(seo.Canonical) {
			return nil, errors.New("SEO image/canonical must be https:// or site-relative URLs")
		}
		b, _ := json.Marshal(seo)
		v["seo"] = b
	}
	if in.MenuOrder != nil {
		v["menu_order"] = *in.MenuOrder
	}
	if in.ParentID != nil {
		if kind != "page" && *in.ParentID != "" {
			return nil, errors.New("only pages can have a parent")
		}
		if *in.ParentID == "" {
			v["parent_id"] = nil
		} else {
			v["parent_id"] = *in.ParentID
		}
	}
	return v, nil
}

func cleanList(in []string) []string {
	out := []string{}
	seen := map[string]bool{}
	for _, t := range in {
		t = clip(strings.ToLower(t), 40)
		if t != "" && !seen[t] && len(out) < 20 {
			seen[t] = true
			out = append(out, t)
		}
	}
	return out
}

func (s *Service) Create(ctx context.Context, in Input, actor string) (*Page, error) {
	kind := "article"
	if in.Kind != nil && *in.Kind == "page" {
		kind = "page"
	}
	if in.Title == nil {
		return nil, errors.New("title is required")
	}
	if in.Slug == nil || *in.Slug == "" {
		sl := Slugify(*in.Title)
		in.Slug = &sl
	}
	v, err := s.prepare(&in, kind)
	if err != nil {
		return nil, err
	}
	keys := []string{"kind", "created_by"}
	vals := []any{kind, actor}
	for k, val := range v {
		keys = append(keys, k)
		vals = append(vals, val)
	}
	ph := make([]string, len(keys))
	for i := range keys {
		ph[i] = fmt.Sprintf("$%d", i+1)
	}
	p, err := scan(s.DB.QueryRow(ctx, `INSERT INTO cms_pages (`+strings.Join(keys, ",")+`) VALUES (`+strings.Join(ph, ",")+`) RETURNING `+cols, vals...))
	if store.IsUniqueViolation(err) {
		return nil, errors.New("another " + kind + " already uses this slug")
	}
	return p, err
}

func (s *Service) Update(ctx context.Context, id string, in Input) (*Page, error) {
	cur, err := s.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	v, err := s.prepare(&in, cur.Kind)
	if err != nil {
		return nil, err
	}
	if pid, ok := v["parent_id"].(string); ok && pid == id {
		return nil, errors.New("a page cannot be its own parent")
	}
	if len(v) == 0 {
		return cur, nil
	}
	sets := []string{"updated_at=now()"}
	args := []any{id}
	for k, val := range v {
		args = append(args, val)
		sets = append(sets, fmt.Sprintf("%s=$%d", k, len(args)))
	}
	p, err := scan(s.DB.QueryRow(ctx, `UPDATE cms_pages SET `+strings.Join(sets, ",")+` WHERE id=$1 RETURNING `+cols, args...))
	if store.IsUniqueViolation(err) {
		return nil, errors.New("another " + cur.Kind + " already uses this slug")
	}
	return p, err
}

func (s *Service) Get(ctx context.Context, id string) (*Page, error) {
	return scan(s.DB.QueryRow(ctx, `SELECT `+cols+` FROM cms_pages WHERE id=$1`, id))
}

type ListFilter struct {
	Kind, Status, Query, Tag string
	Limit, Offset            int
}

func (s *Service) List(ctx context.Context, f ListFilter) ([]*Page, int, error) {
	if f.Limit <= 0 || f.Limit > 100 {
		f.Limit = 25
	}
	where := `WHERE ($1='' OR kind=$1) AND ($2='' OR status=$2) AND ($3='' OR title ILIKE '%'||$3||'%' OR slug ILIKE '%'||$3||'%')
		AND ($4='' OR $4 = ANY(tags) OR $4 = ANY(categories))`
	var total int
	if err := s.DB.QueryRow(ctx, `SELECT count(*) FROM cms_pages `+where, f.Kind, f.Status, f.Query, f.Tag).Scan(&total); err != nil {
		return nil, 0, err
	}
	order := "updated_at DESC"
	if f.Status == "published" {
		order = "published_at DESC NULLS LAST"
	}
	rows, err := s.DB.Query(ctx, `SELECT `+cols+` FROM cms_pages `+where+` ORDER BY `+order+` LIMIT $5 OFFSET $6`,
		f.Kind, f.Status, f.Query, f.Tag, f.Limit, f.Offset)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	out := []*Page{}
	for rows.Next() {
		p, err := scan(rows)
		if err != nil {
			return nil, 0, err
		}
		out = append(out, p)
	}
	return out, total, rows.Err()
}

type PublishResult struct {
	Page         *Page `json:"page"`
	FirstPublish bool  `json:"first_publish"`
}

// Publish makes a page/article public. FirstPublish is true only for native
// content published for the first time — imported content (source set) or a
// re-publish never counts, so routine edits never notify subscribers.
func (s *Service) Publish(ctx context.Context, id string) (*PublishResult, error) {
	var res PublishResult
	err := store.InTx(ctx, s.DB, func(tx pgx.Tx) error {
		var first bool
		err := tx.QueryRow(ctx, `SELECT first_published_at IS NULL AND source IS NULL FROM cms_pages WHERE id=$1 FOR UPDATE`, id).Scan(&first)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		p, err := scan(tx.QueryRow(ctx, `UPDATE cms_pages SET status='published', published_at=COALESCE(published_at, now()),
			first_published_at=COALESCE(first_published_at, now()), updated_at=now() WHERE id=$1 RETURNING `+cols, id))
		if err != nil {
			return err
		}
		res.Page, res.FirstPublish = p, first
		return nil
	})
	return &res, err
}

func (s *Service) Unpublish(ctx context.Context, id string) (*Page, error) {
	return scan(s.DB.QueryRow(ctx, `UPDATE cms_pages SET status='draft', updated_at=now() WHERE id=$1 RETURNING `+cols, id))
}

func (s *Service) Delete(ctx context.Context, id string) error {
	_, err := s.DB.Exec(ctx, `DELETE FROM cms_pages WHERE id=$1`, id)
	return err
}

// --- public reads ----------------------------------------------------------

func (s *Service) PublishedArticle(ctx context.Context, slug string) (*Page, error) {
	return scan(s.DB.QueryRow(ctx, `SELECT `+cols+` FROM cms_pages WHERE kind='article' AND slug=$1 AND status='published'`, slug))
}

// PublishedPage resolves "/parent/child" paths by walking parent_id.
func (s *Service) PublishedPage(ctx context.Context, path string) (*Page, error) {
	segs := strings.Split(strings.Trim(path, "/"), "/")
	if len(segs) == 0 || segs[0] == "" || len(segs) > 6 {
		return nil, ErrNotFound
	}
	var parent *string
	var p *Page
	for _, seg := range segs {
		var err error
		p, err = scan(s.DB.QueryRow(ctx, `SELECT `+cols+` FROM cms_pages WHERE kind='page' AND slug=$1 AND status='published'
			AND parent_id IS NOT DISTINCT FROM $2`, seg, parent))
		if err != nil {
			// Fall back to a flat lookup for a single segment (child page linked directly).
			if len(segs) == 1 {
				return scan(s.DB.QueryRow(ctx, `SELECT `+cols+` FROM cms_pages WHERE kind='page' AND slug=$1 AND status='published'`, seg))
			}
			return nil, ErrNotFound
		}
		parent = &p.ID
	}
	return p, nil
}

func (s *Service) Redirect(ctx context.Context, path string) (string, int, bool) {
	var to string
	var code int
	if err := s.DB.QueryRow(ctx, `SELECT to_path, status_code FROM cms_redirects WHERE from_path=$1`, path).Scan(&to, &code); err != nil {
		return "", 0, false
	}
	return to, code, true
}

type SitemapEntry struct {
	Path    string    `json:"path"`
	Updated time.Time `json:"updated"`
}

func (s *Service) Sitemap(ctx context.Context) ([]SitemapEntry, error) {
	rows, err := s.DB.Query(ctx, `SELECT kind, slug, updated_at, coalesce((seo->>'noindex')::boolean,false) FROM cms_pages
		WHERE status='published' ORDER BY published_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []SitemapEntry{}
	for rows.Next() {
		var kind, slug string
		var upd time.Time
		var noindex bool
		if err := rows.Scan(&kind, &slug, &upd, &noindex); err != nil {
			return nil, err
		}
		if noindex {
			continue
		}
		p := "/" + slug
		if kind == "article" {
			p = "/blog/" + slug
		}
		out = append(out, SitemapEntry{Path: p, Updated: upd})
	}
	return out, rows.Err()
}

func firstNonEmpty(v ...string) string {
	for _, s := range v {
		if s != "" {
			return s
		}
	}
	return ""
}
