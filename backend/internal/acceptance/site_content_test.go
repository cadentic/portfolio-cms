package acceptance

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/cadentic/portfolio-cms/backend/internal/sitecontent"
	"github.com/cadentic/portfolio-cms/backend/internal/testenv"
)

// adminDoc is SiteContentAdminDoc as the admin receives it.
type adminDoc struct {
	Content   sitecontent.Content          `json:"content"`
	Defaults  sitecontent.Content          `json:"defaults"`
	Limits    map[string]sitecontent.Limit `json:"limits"`
	UpdatedAt *string                      `json:"updated_at"`
	UpdatedBy *string                      `json:"updated_by"`
	Warnings  []sitecontent.FieldError     `json:"warnings"`
}

func siteDoc(t *testing.T, r testenv.Response) adminDoc {
	t.Helper()
	if r.Status != 200 {
		t.Fatalf("site content: %d %s", r.Status, r.Body)
	}
	var d adminDoc
	if err := r.JSON(&d); err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(r.Body, []byte(`"warnings":[`)) {
		t.Fatalf("warnings must be a list: %s", r.Body)
	}
	return d
}

func publicSite(t *testing.T, e *testenv.Env) sitecontent.Content {
	t.Helper()
	r := e.Do("GET", "/api/public/site-content", nil, nil)
	if r.Status != 200 {
		t.Fatalf("public site content: %d %s", r.Status, r.Body)
	}
	var c sitecontent.Content
	if err := r.JSON(&c); err != nil {
		t.Fatal(err)
	}
	return c
}

func putSite(e *testenv.Env, content string, base any) testenv.Response {
	body := map[string]any{"content": json.RawMessage(content)}
	if base != nil {
		body["base_updated_at"] = base
	}
	if base == "null" {
		body["base_updated_at"] = nil
	}
	return e.Admin("PUT", "/api/admin/site-content", body)
}

func hasWarning(d adminDoc, path string) bool {
	return slices.ContainsFunc(d.Warnings, func(fe sitecontent.FieldError) bool { return fe.Path == path })
}

func upload(t *testing.T, e *testenv.Env, name string) (id, url string) {
	t.Helper()
	r := e.AdminUpload("/api/admin/media", "file", name, pngBytes(t, 8, 8))
	if r.Status != http.StatusCreated {
		t.Fatalf("upload: %d %s", r.Status, r.Body)
	}
	return r.Map()["id"].(string), r.Map()["url"].(string)
}

func TestSiteContent_PublicRead(t *testing.T) {
	e := testenv.New(t)
	r := e.Do("GET", "/api/public/site-content", nil, nil)
	etag := r.Header.Get("ETag")
	if r.Status != 200 || r.Header.Get("Cache-Control") != "public, max-age=0, must-revalidate" ||
		r.Header.Get("Content-Type") != "application/json; charset=utf-8" || !strings.HasPrefix(etag, `"`) {
		t.Fatalf("public read: %d %v", r.Status, r.Header)
	}
	golden, err := os.ReadFile("../sitecontent/testdata/public.golden.json")
	if err != nil {
		t.Fatal(err)
	}
	if !jsonEqual(t, r.Body, golden) {
		t.Fatalf("body differs from public.golden.json:\n%s", r.Body)
	}
	c := e.Do("GET", "/api/public/site-content", nil, map[string]string{"If-None-Match": etag})
	if c.Status != http.StatusNotModified || c.Header.Get("ETag") != etag || c.Header.Get("Cache-Control") != "public, max-age=0, must-revalidate" {
		t.Fatalf("conditional read: %d %v", c.Status, c.Header)
	}
	if c := e.Do("GET", "/api/public/site-content", nil, map[string]string{"If-None-Match": `"other"`}); c.Status != 200 {
		t.Fatalf("other etag: %d", c.Status)
	}

	for _, rt := range []struct{ method, path string }{
		{"GET", "/api/admin/site-content"}, {"PUT", "/api/admin/site-content"}, {"POST", "/api/admin/site-content/reset"},
		{"GET", "/api/admin/site-content/versions"}, {"POST", "/api/admin/site-content/versions/1/restore"},
		{"GET", "/api/admin/resume"}, {"POST", "/api/admin/resume"}, {"DELETE", "/api/admin/resume"}, {"GET", "/api/admin/resume/file"},
		{"GET", "/api/admin/pages/00000000-0000-0000-0000-000000000000/versions"},
	} {
		if r := e.Do(rt.method, rt.path, map[string]any{"content": map[string]any{"hero": map[string]any{"title": "x"}}}, nil); r.Status != http.StatusUnauthorized {
			t.Fatalf("anonymous %s %s: %d, want 401", rt.method, rt.path, r.Status)
		}
	}
}

// The owner changes the years of experience once and every sentence that
// mentions them follows; saves are checked against concurrent edits.
func TestSiteContent_AdminEdit(t *testing.T) {
	e := testenv.New(t)
	d := siteDoc(t, e.Admin("GET", "/api/admin/site-content", nil))
	if d.UpdatedAt != nil || d.UpdatedBy != nil || len(d.Warnings) != 0 {
		t.Fatalf("fresh doc: %+v %+v %v", d.UpdatedAt, d.UpdatedBy, d.Warnings)
	}
	if l := d.Limits["hero.metrics[].value"]; l.Max != 24 {
		t.Fatalf("hero.metrics[].value limit = %+v", l)
	}
	if l := d.Limits["identity.experience_years"]; l != (sitecontent.Limit{Min: 1, Max: 60, Required: true}) {
		t.Fatalf("identity.experience_years limit = %+v", l)
	}
	a, _ := json.Marshal(d.Content)
	b, _ := json.Marshal(d.Defaults)
	if !bytes.Equal(a, b) {
		t.Fatal("content differs from defaults on an empty database")
	}

	change := `{"identity":{"experience_years":12},"videos":{"featured":{"youtube_id":"https://youtu.be/gXT9izr0ljQ?si=XKDH-MSqA-o66lk3","title":"Featured video"}}}`
	d = siteDoc(t, putSite(e, change, "null"))
	if d.Content.Videos.Featured.YouTubeID != "gXT9izr0ljQ" || d.UpdatedAt == nil || d.UpdatedBy == nil || *d.UpdatedBy != testenv.AdminEmail {
		t.Fatalf("saved doc: %+v %v %v", d.Content.Videos.Featured, d.UpdatedAt, d.UpdatedBy)
	}
	pub := publicSite(t, e)
	for name, s := range map[string]string{"hero.metrics[0].value": pub.Hero.Metrics[0].Value, "identity.summary": pub.Identity.Summary,
		"hero.lead": pub.Hero.Lead, "faq.items[1].a": pub.FAQ.Items[1].A, "seo.home_description": pub.SEO.HomeDescription} {
		if !strings.Contains(s, "12") || strings.Contains(s, "{years}") || strings.Contains(s, "11 years") || strings.Contains(s, "11+") {
			t.Errorf("public %s = %q", name, s)
		}
	}
	if pub.Hero.Metrics[0].Value != "12+ years" {
		t.Fatalf("metric = %q", pub.Hero.Metrics[0].Value)
	}
	if raw := siteDoc(t, e.Admin("GET", "/api/admin/site-content", nil)); raw.Content.Hero.Metrics[0].Value != "{years}+ years" {
		t.Fatalf("admin read lost the placeholder: %q", raw.Content.Hero.Metrics[0].Value)
	}
	if got := e.Str(`SELECT details->>'sections' FROM email_audit_logs WHERE action='site_content.update'`); got != `["identity"]` {
		t.Fatalf("audit sections = %s", got)
	}
	if n := e.Count(`SELECT count(*) FROM content_versions WHERE entity_type='site_content'`); n != 1 {
		t.Fatalf("versions = %d", n)
	}

	// Saving the same content again writes nothing.
	first := *d.UpdatedAt
	d = siteDoc(t, putSite(e, change, first))
	if *d.UpdatedAt != first {
		t.Fatalf("no-op save moved updated_at: %s -> %s", first, *d.UpdatedAt)
	}
	if e.Count(`SELECT count(*) FROM email_audit_logs WHERE action LIKE 'site_content.%'`) != 1 ||
		e.Count(`SELECT count(*) FROM content_versions WHERE entity_type='site_content'`) != 1 {
		t.Fatal("a no-op save was audited or versioned")
	}

	// A save based on an older read is refused.
	d = siteDoc(t, putSite(e, `{"hero":{"eyebrow":"Second"}}`, first))
	r := putSite(e, `{"hero":{"eyebrow":"Third"}}`, first)
	if r.Status != http.StatusConflict || r.Map()["updated_by"] != testenv.AdminEmail || r.Map()["updated_at"] != *d.UpdatedAt ||
		!strings.Contains(r.Map()["error"].(string), "changed elsewhere") {
		t.Fatalf("stale save: %d %s", r.Status, r.Body)
	}
	if r := putSite(e, `{"hero":{"eyebrow":"Third"}}`, "null"); r.Status != http.StatusConflict {
		t.Fatalf("base null with a saved row: %d %s", r.Status, r.Body)
	}
	if r := e.Admin("PUT", "/api/admin/site-content", map[string]any{"contnt": map[string]any{}}); r.Status != 400 {
		t.Fatalf("unknown top-level key: %d %s", r.Status, r.Body)
	}
	if r := e.Admin("PUT", "/api/admin/site-content", map[string]any{"content": map[string]any{}}); r.Status != 400 ||
		r.Map()["error"] != "content is required" {
		t.Fatalf("empty content: %d %s", r.Status, r.Body)
	}
	if r := putSite(e, `{"hero":{"eyebrow":"Third"}}`, "yesterday"); r.Status != 400 {
		t.Fatalf("bad base: %d %s", r.Status, r.Body)
	}

	orgs := make([]string, 25)
	for i := range orgs {
		orgs[i] = `{"name":"Org"}`
	}
	for _, c := range []struct{ body, path, msg string }{
		{`{"organizations":{"items":[{"name":"Acme","logo_url":"https://example.com/l.png"}]}}`, "organizations.items[0].logo_url", "media library"},
		{`{"organizations":{"items":[{"name":"Acme","logo_url":"/media/uploads/2026/10/missing.png"}]}}`, "organizations.items[0].logo_url", "media library"},
		{`{"identity":{"linkedin_url":"http://www.linkedin.com/in/x"}}`, "identity.linkedin_url", "https://"},
		{`{"organizations":{"items":[` + strings.Join(orgs, ",") + `]}}`, "organizations.items", "at most 24 items"},
		{`{"foo":{}}`, "", `unknown section "foo"`},
		{`{"hero":{"bar":1}}`, "hero", `unknown field "bar"`},
		{`{"hero":{"lead":"For {year}+ years"}}`, "hero.lead", "unknown placeholder {year}"},
	} {
		r := putSite(e, c.body, nil)
		var res struct {
			Error  string                   `json:"error"`
			Errors []sitecontent.FieldError `json:"errors"`
		}
		if r.Status != 400 || r.JSON(&res) != nil || len(res.Errors) == 0 || res.Errors[0].Path != c.path ||
			!strings.Contains(res.Errors[0].Message, c.msg) || !strings.Contains(res.Error, c.msg) {
			t.Errorf("%s: %d %s", c.body, r.Status, r.Body)
		}
	}
}

// Files used by the site content, the email brand logo or email HTML are
// protected from deletion; a forced delete never breaks the site or blocks
// later saves.
func TestSiteContent_MediaInUse(t *testing.T) {
	e := testenv.New(t)
	logoID, logo := upload(t, e, "acme-logo.png")
	siteDoc(t, putSite(e, `{"organizations":{"items":[{"name":"Acme","note":"Client engagement","logo_url":"`+logo+`"}]}}`, nil))
	if pub := publicSite(t, e); pub.Organizations.Items[0].LogoURL != logo {
		t.Fatalf("public logo = %q", pub.Organizations.Items[0].LogoURL)
	}
	r := e.Admin("DELETE", "/api/admin/media/"+logoID, nil)
	sets, _ := r.Map()["settings"].([]any)
	if r.Status != http.StatusConflict || !strings.Contains(r.Map()["error"].(string), "Organisations") || len(sets) != 1 ||
		sets[0] != "Home page › Organisations › Acme logo" || r.Map()["pages"] == nil || r.Map()["email"] == nil {
		t.Fatalf("delete in use: %d %s", r.Status, r.Body)
	}
	if r := e.Admin("DELETE", "/api/admin/media/"+logoID+"?force=true", nil); r.Status != http.StatusNoContent {
		t.Fatalf("forced delete: %d %s", r.Status, r.Body)
	}
	if pub := publicSite(t, e); pub.Organizations.Items[0].LogoURL != "" || pub.Organizations.Items[0].Name != "Acme" {
		t.Fatalf("public org after delete = %+v", pub.Organizations.Items[0])
	}
	if d := siteDoc(t, e.Admin("GET", "/api/admin/site-content", nil)); !hasWarning(d, "organizations.items[0].logo_url") ||
		d.Content.Organizations.Items[0].LogoURL != logo {
		t.Fatalf("admin doc after delete: %v %+v", d.Warnings, d.Content.Organizations.Items[0])
	}

	// A missing image never blocks saving other fields.
	photoID, photo := upload(t, e, "me.png")
	siteDoc(t, putSite(e, `{"identity":{"photo_url":"`+photo+`"}}`, nil))
	if r := e.Admin("DELETE", "/api/admin/media/"+photoID+"?force=true", nil); r.Status != http.StatusNoContent {
		t.Fatalf("forced delete: %d", r.Status)
	}
	d := siteDoc(t, e.Admin("GET", "/api/admin/site-content", nil))
	d.Content.Hero.Title = "Changed title"
	all, _ := json.Marshal(d.Content)
	d = siteDoc(t, putSite(e, string(all), *d.UpdatedAt))
	if d.Content.Hero.Title != "Changed title" || !hasWarning(d, "identity.photo_url") || !hasWarning(d, "organizations.items[0].logo_url") {
		t.Fatalf("whole-document save: %+v %v", d.Content.Hero.Title, d.Warnings)
	}
	rpc := mcpClient(t, e)
	if _, text, isErr := toolResult(rpc, 1, "update_site_content", map[string]any{"sections": map[string]any{"hero": map[string]any{"eyebrow": "X"}}}); isErr {
		t.Fatalf("MCP save with a missing image: %s", text)
	}
	if pub := publicSite(t, e); pub.Identity.PhotoURL != "/images/sayantan.jpg" || pub.Hero.Eyebrow != "X" {
		t.Fatalf("public photo = %q, eyebrow = %q", pub.Identity.PhotoURL, pub.Hero.Eyebrow)
	}

	// The email brand logo, templates and unsent campaigns count as uses.
	brandID, brand := upload(t, e, "brand.png")
	es := e.Admin("GET", "/api/admin/settings/email", nil).Map()
	es["brand"].(map[string]any)["logo_url"] = brand
	if r := e.Admin("PUT", "/api/admin/settings/email", es); r.Status != 200 {
		t.Fatalf("save email settings: %d %s", r.Status, r.Body)
	}
	if r := e.Admin("DELETE", "/api/admin/media/"+brandID, nil); r.Status != http.StatusConflict ||
		!strings.Contains(r.Map()["error"].(string), "Email settings › Brand logo") {
		t.Fatalf("delete brand logo: %d %s", r.Status, r.Body)
	}
	campID, camp := upload(t, e, "banner.png")
	e.Exec(`INSERT INTO email_campaigns (name, subject, content_html) VALUES ('Spring update', 'Hello', $1)`, `<p><img src="`+camp+`" alt=""></p>`)
	e.Exec(`INSERT INTO email_templates (slug, name, category, subject, html_body) VALUES ('promo', 'Promo', 'marketing', 'Hi', $1)`, `<img src="`+camp+`">`)
	r = e.Admin("DELETE", "/api/admin/media/"+campID, nil)
	mails, _ := r.Map()["email"].([]any)
	if r.Status != http.StatusConflict || !strings.Contains(r.Map()["error"].(string), "Campaign: Spring update") ||
		!slices.Contains(mails, any("Email template: Promo")) {
		t.Fatalf("delete campaign image: %d %s", r.Status, r.Body)
	}
	e.Exec(`UPDATE email_campaigns SET status='sent' WHERE name='Spring update'`)
	e.Exec(`DELETE FROM email_templates WHERE slug='promo'`)
	if r := e.Admin("DELETE", "/api/admin/media/"+campID, nil); r.Status != http.StatusNoContent {
		t.Fatalf("delete after the campaign was sent: %d %s", r.Status, r.Body)
	}
}

func TestSiteContent_ResetVersionsRestore(t *testing.T) {
	e := testenv.New(t)
	siteDoc(t, putSite(e, `{"hero":{"eyebrow":"Version A"}}`, nil))
	siteDoc(t, putSite(e, `{"identity":{"experience_years":13}}`, nil))

	var list struct {
		Items []struct {
			ID      int64    `json:"id"`
			Action  string   `json:"action"`
			Actor   string   `json:"actor"`
			Changed []string `json:"changed"`
			SHA256  string   `json:"sha256"`
		} `json:"items"`
	}
	if err := e.Admin("GET", "/api/admin/site-content/versions?limit=10", nil).JSON(&list); err != nil || len(list.Items) != 2 {
		t.Fatalf("versions: %+v %v", list, err)
	}
	newer, older := list.Items[0], list.Items[1]
	if newer.ID <= older.ID || !slices.Equal(newer.Changed, []string{"identity"}) || !slices.Equal(older.Changed, []string{"hero"}) ||
		newer.Action != "update" || newer.Actor != testenv.AdminEmail || len(newer.SHA256) != 64 {
		t.Fatalf("versions = %+v", list.Items)
	}

	d := siteDoc(t, e.Admin("POST", "/api/admin/site-content/versions/"+testenv.Sprint(older.ID)+"/restore", map[string]any{}))
	if d.Content.Hero.Eyebrow != "Version A" || d.Content.Identity.ExperienceYears != 11 {
		t.Fatalf("restored = %q %d", d.Content.Hero.Eyebrow, d.Content.Identity.ExperienceYears)
	}
	snap := e.Str(`SELECT snapshot::text FROM content_versions WHERE id=$1`, older.ID)
	got, _ := json.Marshal(d.Content)
	if !jsonEqual(t, got, []byte(snap)) {
		t.Fatal("restored content differs from the snapshot")
	}
	if e.Count(`SELECT count(*) FROM email_audit_logs WHERE action='site_content.restore' AND (details->>'version_id')::bigint=$1`, older.ID) != 1 ||
		e.Count(`SELECT count(*) FROM content_versions WHERE entity_type='site_content' AND action='restore'`) != 1 {
		t.Fatal("restore was not audited or versioned")
	}
	if r := e.Admin("POST", "/api/admin/site-content/versions/999999/restore", nil); r.Status != 404 {
		t.Fatalf("unknown version: %d", r.Status)
	}
	if r := e.Admin("POST", "/api/admin/site-content/versions/"+testenv.Sprint(older.ID)+"/restore", map[string]any{"base_updated_at": nil}); r.Status != 409 {
		t.Fatalf("stale restore: %d %s", r.Status, r.Body)
	}

	// A version whose logo has since been deleted restores without it.
	logoID, logo := upload(t, e, "logo.png")
	siteDoc(t, putSite(e, `{"organizations":{"items":[{"name":"Acme","logo_url":"`+logo+`"}]}}`, nil))
	withLogo := e.Count(`SELECT max(id) FROM content_versions WHERE entity_type='site_content'`)
	siteDoc(t, putSite(e, `{"organizations":{"items":[{"name":"Other"}]}}`, nil))
	if r := e.Admin("DELETE", "/api/admin/media/"+logoID+"?force=true", nil); r.Status != http.StatusNoContent {
		t.Fatalf("forced delete: %d", r.Status)
	}
	d = siteDoc(t, e.Admin("POST", "/api/admin/site-content/versions/"+testenv.Sprint(withLogo)+"/restore", nil))
	if o := d.Content.Organizations.Items; len(o) != 1 || o[0].Name != "Acme" || o[0].LogoURL != "" || !hasWarning(d, "organizations.items[0].logo_url") {
		t.Fatalf("restore with a deleted logo: %+v %v", o, d.Warnings)
	}

	// Reset one section, then everything.
	d = siteDoc(t, e.Admin("POST", "/api/admin/site-content/reset", map[string]any{"sections": []string{"hero"}}))
	def := sitecontent.Default()
	if d.Content.Hero.Eyebrow != def.Hero.Eyebrow || d.Content.Organizations.Items[0].Name != "Acme" {
		t.Fatalf("reset hero: %q %+v", d.Content.Hero.Eyebrow, d.Content.Organizations.Items)
	}
	if r := e.Admin("POST", "/api/admin/site-content/reset", map[string]any{}); r.Status != 400 || r.Map()["error"] != "choose sections to reset" {
		t.Fatalf("empty reset: %d %s", r.Status, r.Body)
	}
	if r := e.Admin("POST", "/api/admin/site-content/reset", map[string]any{"sections": []string{"heros"}}); r.Status != 400 {
		t.Fatalf("unknown section reset: %d %s", r.Status, r.Body)
	}
	d = siteDoc(t, e.Admin("POST", "/api/admin/site-content/reset", map[string]any{"all": true}))
	a, _ := json.Marshal(d.Content)
	b, _ := json.Marshal(def)
	if !bytes.Equal(a, b) {
		t.Fatal("reset all did not restore the defaults")
	}
	if n := e.Count(`SELECT count(*) FROM email_audit_logs WHERE action='site_content.reset'`); n != 2 {
		t.Fatalf("reset audit rows = %d", n)
	}
	if n := e.Count(`SELECT count(*) FROM content_versions WHERE entity_type='site_content' AND action='reset'`); n != 2 {
		t.Fatalf("reset versions = %d", n)
	}
}

func TestSiteContent_LLMs(t *testing.T) {
	e := testenv.New(t)
	siteDoc(t, putSite(e, `{"identity":{"name":"Test Owner","experience_years":12,"other_links":[{"label":"Mastodon","url":"https://example.social/@x"}]},
		"seo":{"llms_summary":"Summary X {years}"},
		"videos":{"items":[{"youtube_id":"gXT9izr0ljQ","title":"Featured video","description":"A walkthrough"}]}}`, nil))
	r := e.Do("GET", "/llms.txt", nil, nil)
	body := string(r.Body)
	for _, want := range []string{"# Test Owner — Oracle DBA & Database Reliability Engineer\n", "> Summary X 12\n", "\n## Videos\n",
		"- [Featured video](https://www.youtube.com/watch?v=gXT9izr0ljQ): A walkthrough\n", "- Mastodon: https://example.social/@x\n",
		"- LinkedIn: https://www.linkedin.com/in/sayantan-chakraborty-y2022\n", sitecontent.Default().SEO.BlogDescription} {
		if !strings.Contains(body, want) {
			t.Errorf("llms.txt lacks %q:\n%s", want, body)
		}
	}
	if r.Header.Get("Cache-Control") != "public, max-age=300" {
		t.Fatalf("cache-control = %q", r.Header.Get("Cache-Control"))
	}

	// The default byline follows the site profile.
	p := e.Admin("POST", "/api/admin/pages", map[string]any{"kind": "article", "title": "Byline check"})
	if p.Status != 201 || p.Map()["author_name"] != "Test Owner" {
		t.Fatalf("create page: %d %s", p.Status, p.Body)
	}
}

// A warm API keeps serving the last good document through a database outage.
func TestSiteContent_StaleIfError(t *testing.T) {
	e := testenv.New(t)
	pool, err := pgxpool.New(e.Ctx, e.DB.Config().ConnString())
	if err != nil {
		t.Fatal(err)
	}
	quiet := slog.New(slog.NewTextHandler(io.Discard, nil))
	svc := &sitecontent.Service{DB: pool, Log: quiet}
	body, etag, stale, err := svc.Public(e.Ctx)
	if err != nil || stale {
		t.Fatalf("prime: %v %v", stale, err)
	}
	body = bytes.Clone(body)
	pool.Close()
	again, etag2, stale, err := svc.Public(e.Ctx)
	if err != nil || !stale || !bytes.Equal(again, body) || etag2 != etag {
		t.Fatalf("after the outage: stale=%v err=%v same=%v", stale, err, bytes.Equal(again, body))
	}
	cold := &sitecontent.Service{DB: pool, Log: quiet}
	if _, _, _, err := cold.Public(e.Ctx); err == nil {
		t.Fatal("a cold service with no database returned content")
	}

	// The same through HTTP: stale bytes uncached, or 503, never the defaults.
	for _, c := range []struct {
		svc    *sitecontent.Service
		status int
	}{{svc, 200}, {cold, 503}} {
		srv := *e.Server
		srv.SiteContent, srv.Log = c.svc, quiet
		ts := httptest.NewServer(srv.Routes())
		resp, err := http.Get(ts.URL + "/api/public/site-content")
		if err != nil {
			t.Fatal(err)
		}
		got, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		ts.Close()
		if resp.StatusCode != c.status || resp.Header.Get("Cache-Control") != "no-store" {
			t.Fatalf("status %d, cache-control %q, want %d no-store", resp.StatusCode, resp.Header.Get("Cache-Control"), c.status)
		}
		if c.status == 200 && (!bytes.Equal(got, body) || resp.Header.Get("X-Content-Stale") != "1") {
			t.Fatalf("stale response: %v %s", resp.Header, got)
		}
		if c.status == 503 && !strings.Contains(string(got), "temporarily unavailable") {
			t.Fatalf("503 body: %s", got)
		}
	}
}
