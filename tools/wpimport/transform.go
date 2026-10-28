package main

import (
	"fmt"
	"html"
	"net/url"
	"path"
	"regexp"
	"sort"
	"strings"
	"time"
)

// Snapshot is the CMS-ready result of an import. It is committed to the repo
// (content/wordpress/snapshot.json) and is the only input of the SQL seed.
type Snapshot struct {
	Site      string     `json:"site"`
	Items     []Item     `json:"items"`
	Media     []Media    `json:"media"`
	Redirects []Redirect `json:"redirects"`
}

// Item maps onto one cms_pages row.
type Item struct {
	SourceID            int64    `json:"source_id"`
	Kind                string   `json:"kind"` // "article" (WP post) or "page"
	Slug                string   `json:"slug"`
	Path                string   `json:"path"` // public path on the new site
	LegacyURL           string   `json:"legacy_url"`
	LegacyPath          string   `json:"legacy_path"`
	Title               string   `json:"title"`
	Excerpt             string   `json:"excerpt"`
	Hero                Hero     `json:"hero"`
	Blocks              []Block  `json:"blocks"`
	FeaturedImageURL    string   `json:"featured_image_url,omitempty"`
	AuthorName          string   `json:"author_name"`
	Categories          []string `json:"categories"`
	Tags                []string `json:"tags"`
	SEO                 SEO      `json:"seo"`
	OriginalPublishedAt string   `json:"original_published_at"`
	ParentSourceID      int64    `json:"parent_source_id,omitempty"`
	MenuOrder           int      `json:"menu_order"`
}

type Hero struct {
	Type string `json:"type"` // "none" or "image"
	URL  string `json:"url,omitempty"`
	Alt  string `json:"alt,omitempty"`
}

type SEO struct {
	Title       string `json:"title"`
	Description string `json:"description"`
	OGImage     string `json:"og_image,omitempty"`
	Canonical   string `json:"canonical"`
	Noindex     bool   `json:"noindex"`
}

// Media maps onto one cms_media row; File is where the image is stored.
type Media struct {
	SourceURL string `json:"source_url"`
	File      string `json:"file"` // under the media dir, e.g. 2011/05/rac.png
	URL       string `json:"url"`  // e.g. /media/wordpress/2011/05/rac.png
	Kind      string `json:"kind"` // image | video | file
	Alt       string `json:"alt,omitempty"`
	Width     int    `json:"width,omitempty"`
	Height    int    `json:"height,omitempty"`
}

type Redirect struct {
	From   string `json:"from"`
	To     string `json:"to"`
	Status int    `json:"status"`
}

type Options struct {
	Site        string // e.g. csayantan.wordpress.com
	SiteURL     string // e.g. https://cadentic.dev
	ArticlePath string // public prefix for WP posts, e.g. /blog
	PagePath    string // public prefix for WP pages, e.g. / or /notes
	BlogIndex   string // where taxonomy/date archives redirect, e.g. /blog
	MediaURL    string // public prefix for media, e.g. /media/wordpress
}

var (
	reMediaURL = regexp.MustCompile(`^https?://(?:i[0-9]\.wp\.com/)?(?:[a-z0-9-]+\.files\.wordpress\.com|[a-z0-9-]+\.wordpress\.com/wp-content/uploads)/([^?#]+)`)
	reWPCDN    = regexp.MustCompile(`^https?://i[0-9]\.wp\.com/(.+)$`)
	reTags     = regexp.MustCompile(`(?s)<[^>]*>`)
	reSpace    = regexp.MustCompile(`[ \t\r\f\v]+|\n{2,}`)
)

// Transform converts raw API posts into a Snapshot.
func Transform(posts []wpPost, opt Options) (*Snapshot, error) {
	byID := map[int64]wpPost{}
	for _, p := range posts {
		if p.Status != "" && p.Status != "publish" {
			continue
		}
		byID[p.ID] = p
	}
	ids := make([]int64, 0, len(byID))
	for id := range byID {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })

	// Pass 1: slugs and new paths, so internal links can be rewritten.
	items := map[int64]*Item{}
	slugOwner := map[string]int64{}
	legacyToNew := map[string]string{}
	for _, id := range ids {
		p := byID[id]
		it := &Item{
			SourceID:            id,
			Kind:                "article",
			Slug:                p.Slug,
			LegacyURL:           p.URL,
			LegacyPath:          legacyPath(p.URL),
			Title:               cleanText(html.UnescapeString(p.Title)),
			AuthorName:          p.Author.Name,
			OriginalPublishedAt: normalizeTime(p.Date),
			ParentSourceID:      p.parentID(),
			MenuOrder:           int(p.MenuOrder),
			Categories:          []string{},
			Tags:                []string{},
		}
		if p.Type == "page" {
			it.Kind = "page"
		}
		if it.Title == "" {
			it.Title = p.Slug
		}
		items[id] = it
	}
	for _, id := range ids {
		it := items[id]
		if _, ok := items[it.ParentSourceID]; !ok {
			it.ParentSourceID = 0
		}
		// cms_pages has UNIQUE(kind, slug); WordPress only guarantees page
		// slugs unique per parent, so qualify a clash with the parent slug.
		key := it.Kind + "/" + it.Slug
		if _, dup := slugOwner[key]; dup {
			if parent, ok := items[it.ParentSourceID]; ok {
				it.Slug = parent.Slug + "-" + it.Slug
				key = it.Kind + "/" + it.Slug
			}
			if _, dup := slugOwner[key]; dup {
				it.Slug = fmt.Sprintf("%s-%d", it.Slug, id)
				key = it.Kind + "/" + it.Slug
			}
		}
		slugOwner[key] = id
	}
	// Articles live at /blog/{slug}; pages at /{parent-slug}/.../{slug}, which
	// the Angular router resolves by walking parent_id.
	var pagePath func(it *Item, depth int) string
	pagePath = func(it *Item, depth int) string {
		if parent, ok := items[it.ParentSourceID]; ok && depth < 16 {
			return pagePath(parent, depth+1) + "/" + it.Slug
		}
		return joinPath(opt.PagePath, it.Slug)
	}
	for _, id := range ids {
		it := items[id]
		if it.Kind == "page" {
			it.Path = pagePath(it, 0)
		} else {
			it.Path = joinPath(opt.ArticlePath, it.Slug)
		}
		legacyToNew[it.LegacyPath] = it.Path
	}

	// Pass 2: content, media, SEO.
	media := map[string]*Media{}
	mediaURL := func(raw, alt string, w, h int) string {
		m := reMediaURL.FindStringSubmatch(raw)
		if m == nil {
			return raw
		}
		file := m[1]
		// Only images are copied into the public folder. Documents (e.g. the
		// old resume PDFs with personal contact details) keep their original
		// URL so the owner decides about them while reviewing the draft.
		if mediaKind(file) != "image" {
			return canonicalMediaURL(strings.SplitN(raw, "?", 2)[0])
		}
		md, ok := media[file]
		if !ok {
			md = &Media{
				SourceURL: canonicalMediaURL(strings.SplitN(raw, "?", 2)[0]),
				File:      file,
				URL:       strings.TrimRight(opt.MediaURL, "/") + "/" + file,
				Kind:      mediaKind(file),
			}
			media[file] = md
		}
		if alt != "" && md.Alt == "" {
			md.Alt = alt
		}
		if w > 0 && md.Width == 0 {
			md.Width, md.Height = w, h
		}
		return md.URL
	}
	siteLink := regexp.MustCompile(`^https?://` + regexp.QuoteMeta(opt.Site) + `(/[^#?]*)?([?#].*)?$`)
	rewrite := func(u string) string {
		u = html.UnescapeString(strings.TrimSpace(u))
		if strings.HasPrefix(u, "//") {
			u = "https:" + u
		}
		if reMediaURL.MatchString(u) {
			return mediaURL(u, "", 0, 0)
		}
		if g := siteLink.FindStringSubmatch(u); g != nil {
			p := strings.TrimRight(g[1], "/")
			if p == "" {
				return "/"
			}
			if np, ok := legacyToNew[p]; ok {
				return np + g[2]
			}
		}
		return u
	}

	snap := &Snapshot{Site: opt.Site}
	for _, id := range ids {
		p, it := byID[id], items[id]
		for _, a := range p.Attachments {
			mediaURL(a.URL, a.Alt, int(a.Width), int(a.Height))
		}
		it.Blocks = htmlToBlocks(p.Content, rewrite)
		if it.Blocks == nil {
			it.Blocks = []Block{}
		}
		it.Excerpt = plainText(p.Excerpt, 300)
		it.Hero = Hero{Type: "none"}
		if p.FeaturedImage != "" {
			it.FeaturedImageURL = rewrite(p.FeaturedImage)
			it.Hero = Hero{Type: "image", URL: it.FeaturedImageURL, Alt: it.Title}
		}
		for _, t := range p.Categories {
			if t.Slug != "uncategorized" {
				it.Categories = append(it.Categories, html.UnescapeString(t.Name))
			}
		}
		for _, t := range p.Tags {
			it.Tags = append(it.Tags, html.UnescapeString(t.Name))
		}
		sort.Strings(it.Categories)
		sort.Strings(it.Tags)
		desc := plainText(p.Excerpt, 160)
		if desc == "" {
			desc = plainText(p.Content, 160)
		}
		it.SEO = SEO{
			Title:       it.Title,
			Description: desc,
			OGImage:     it.FeaturedImageURL,
			Canonical:   strings.TrimRight(opt.SiteURL, "/") + it.Path,
		}
		snap.Items = append(snap.Items, *it)
	}
	sort.SliceStable(snap.Items, func(i, j int) bool {
		a, b := snap.Items[i], snap.Items[j]
		if a.Kind != b.Kind {
			return a.Kind < b.Kind
		}
		return a.SourceID < b.SourceID
	})
	for _, m := range media {
		snap.Media = append(snap.Media, *m)
	}
	sort.Slice(snap.Media, func(i, j int) bool { return snap.Media[i].File < snap.Media[j].File })
	snap.Redirects = buildRedirects(snap, byID, opt)
	return snap, nil
}

func buildRedirects(snap *Snapshot, byID map[int64]wpPost, opt Options) []Redirect {
	seen := map[string]bool{}
	var out []Redirect
	add := func(from, to string) {
		if from == "" || from == "/" || from == to || seen[from] {
			return
		}
		seen[from] = true
		out = append(out, Redirect{From: from, To: to, Status: 301})
	}
	// Item URLs first, so they win over archive paths.
	for _, it := range snap.Items {
		add(it.LegacyPath, it.Path)
	}
	blog := opt.BlogIndex
	for _, it := range snap.Items {
		if it.Kind != "article" {
			continue
		}
		if t, err := time.Parse(time.RFC3339, it.OriginalPublishedAt); err == nil {
			add(fmt.Sprintf("/%04d", t.Year()), blog)
			add(fmt.Sprintf("/%04d/%02d", t.Year(), t.Month()), blog)
			add(fmt.Sprintf("/%04d/%02d/%02d", t.Year(), t.Month(), t.Day()), blog)
		}
		for _, t := range byID[it.SourceID].Categories {
			add("/category/"+t.Slug, blog+"?category="+url.QueryEscape(html.UnescapeString(t.Name)))
		}
		for _, t := range byID[it.SourceID].Tags {
			add("/tag/"+t.Slug, blog+"?tag="+url.QueryEscape(html.UnescapeString(t.Name)))
		}
	}
	add("/feed", blog)
	sort.Slice(out, func(i, j int) bool { return out[i].From < out[j].From })
	return out
}

// legacyPath returns the URL path without a trailing slash ("/a/b").
func legacyPath(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	p := strings.TrimRight(u.Path, "/")
	if p == "" {
		return "/"
	}
	return p
}

func joinPath(prefix, p string) string {
	return path.Clean("/" + strings.Trim(prefix, "/") + "/" + strings.Trim(p, "/"))
}

func canonicalMediaURL(u string) string {
	// iN.wp.com/<sub>.files.wordpress.com/... is a CDN proxy; fetch the origin.
	if m := reWPCDN.FindStringSubmatch(u); m != nil {
		return "https://" + m[1]
	}
	return strings.Replace(u, "http://", "https://", 1)
}

func mediaKind(file string) string {
	switch strings.ToLower(path.Ext(file)) {
	case ".png", ".jpg", ".jpeg", ".gif", ".webp", ".svg", ".bmp":
		return "image"
	case ".mp4", ".webm", ".mov", ".ogv":
		return "video"
	}
	return "file"
}

func plainText(h string, max int) string {
	s := cleanText(stripTags(h))
	s = strings.ReplaceAll(s, "\n", " ")
	for _, suffix := range []string{"[…]", "[...]", "…", "Continue reading →"} {
		s = strings.TrimSpace(strings.TrimSuffix(s, suffix))
	}
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	cut := string(r[:max-1])
	if i := strings.LastIndex(cut, " "); i > max/2 {
		cut = cut[:i]
	}
	return strings.TrimRight(cut, " ,.;:") + "…"
}

func normalizeTime(s string) string {
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}
