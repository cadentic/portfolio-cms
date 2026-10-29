package acceptance

import (
	"encoding/json"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/cadentic/portfolio-cms/backend/internal/content"
	"github.com/cadentic/portfolio-cms/backend/internal/render"
	"github.com/cadentic/portfolio-cms/backend/internal/testenv"
)

// The privacy notice, terms and AI policy are CMS pages seeded by migration
// 0007: editable, versioned, and never deleted, unpublished, renamed or moved.
func TestLegalPages(t *testing.T) {
	e := testenv.New(t)
	seeded := time.Date(2026, 10, 28, 6, 30, 0, 0, time.UTC)
	ids := map[string]string{}
	for slug, desc := range map[string]string{
		"privacy":   "How personal data is collected, used and protected on cadentic.dev under the DPDP Act 2023 and DPDP Rules 2025.",
		"terms":     "Terms of use for cadentic.dev: content, intellectual property, acceptable use and governing law.",
		"ai-policy": "How AI is used for writing and content management on cadentic.dev, and guidance for AI crawlers.",
	} {
		r := e.Do("GET", "/api/public/pages/"+slug, nil, nil)
		var p content.Page
		if r.Status != 200 || r.JSON(&p) != nil || p.Status != "published" || p.SEO.Description != desc || !p.UpdatedAt.Equal(seeded) ||
			p.PublishedAt == nil || !p.PublishedAt.Equal(seeded) || p.Path != "/"+slug || p.Source != "system" || len(p.Blocks) == 0 {
			t.Fatalf("%s: %d %s", slug, r.Status, r.Body)
		}
		ids[slug] = p.ID

		// Seeded blocks are in normalised form, with no keys the editor drops.
		raw := e.Str(`SELECT blocks::text FROM cms_pages WHERE id=$1`, p.ID)
		var blocks []content.Block
		if err := json.Unmarshal([]byte(raw), &blocks); err != nil {
			t.Fatal(err)
		}
		again, _ := json.Marshal(blocks)
		if !jsonEqual(t, []byte(raw), again) {
			t.Fatalf("%s blocks carry unknown keys", slug)
		}
		if _, changed, err := e.Content.Update(e.Ctx, p.ID, content.Input{Blocks: blocks}, "test"); err != nil || len(changed) != 0 {
			t.Fatalf("%s blocks are not normalised: %v %v", slug, changed, err)
		}
	}
	privacy := ids["privacy"]

	var table string
	for _, b := range getPage(t, e, privacy).Blocks {
		if b.Type == "html" && strings.HasPrefix(b.HTML, "<table>") {
			table = b.HTML
		}
	}
	if table == "" || render.Sanitize(table) != table {
		t.Fatalf("privacy table missing or changed by sanitising: %q", table)
	}
	ai := e.Str(`SELECT blocks::text FROM cms_pages WHERE id=$1`, ids["ai-policy"])
	for _, want := range []string{"**Content management.** I can create and update draft pages", "nothing is published or mailed without my approval",
		"[privacy notice](/privacy)", "please attribute \\\"Sayantan Chakraborty\\\""} {
		if !strings.Contains(ai, want) {
			t.Fatalf("AI policy lacks %q", want)
		}
	}
	if n := e.Count(`SELECT count(*) FROM content_versions WHERE entity_type='page' AND action='seed' AND actor='migration'`); n != 3 {
		t.Fatalf("seed versions = %d", n)
	}
	if sm := string(e.Do("GET", "/sitemap.xml", nil, nil).Body); strings.Count(sm, "/privacy</loc>") != 1 || strings.Count(sm, "/terms</loc>") != 1 {
		t.Fatalf("sitemap: %s", sm)
	}

	// Protected: no delete, unpublish, rename, move or kind change.
	other := e.Admin("POST", "/api/admin/pages", map[string]any{"kind": "page", "title": "About me"})
	if other.Status != 201 {
		t.Fatalf("create page: %d %s", other.Status, other.Body)
	}
	otherID := other.Map()["id"].(string)
	for _, c := range []struct {
		method, path string
		body         any
	}{
		{"DELETE", "/api/admin/pages/" + privacy, nil},
		{"POST", "/api/admin/pages/" + privacy + "/unpublish", nil},
		{"PATCH", "/api/admin/pages/" + privacy, map[string]any{"slug": "privacy-notice"}},
		{"PATCH", "/api/admin/pages/" + privacy, map[string]any{"parent_id": otherID}},
		{"PATCH", "/api/admin/pages/" + privacy, map[string]any{"kind": "article"}},
	} {
		if r := e.Admin(c.method, c.path, c.body); r.Status != http.StatusConflict || !strings.Contains(r.Map()["error"].(string), "legal page") {
			t.Fatalf("%s %s %v: %d %s", c.method, c.path, c.body, r.Status, r.Body)
		}
	}
	if p := getPage(t, e, privacy); p.Status != "published" || p.Slug != "privacy" || p.ParentID != nil || !p.UpdatedAt.Equal(seeded) {
		t.Fatalf("privacy page changed: %+v", p)
	}
	// Other pages keep the old behaviour, and unknown ids are 404.
	if r := e.Admin("POST", "/api/admin/pages/"+otherID+"/unpublish", nil); r.Status != 200 {
		t.Fatalf("unpublish other page: %d %s", r.Status, r.Body)
	}
	if r := e.Admin("DELETE", "/api/admin/pages/"+otherID, nil); r.Status != http.StatusNoContent {
		t.Fatalf("delete other page: %d %s", r.Status, r.Body)
	}
	for _, path := range []string{"/api/admin/pages/" + otherID, "/api/admin/pages/not-a-uuid"} {
		if r := e.Admin("DELETE", path, nil); r.Status != 404 {
			t.Fatalf("delete %s: %d", path, r.Status)
		}
		if r := e.Admin("POST", path+"/unpublish", nil); r.Status != 404 {
			t.Fatalf("unpublish %s: %d", path, r.Status)
		}
	}

	// The owner edits the text in the page editor, which sends every field.
	cur := getPage(t, e, privacy)
	body := editorBody(cur)
	body["blocks"] = append(cur.Blocks, content.Block{Type: "paragraph", Text: "Edited by the owner."})
	if r := e.Admin("PATCH", "/api/admin/pages/"+privacy, body); r.Status != 200 {
		t.Fatalf("edit: %d %s", r.Status, r.Body)
	}
	if r := e.Do("GET", "/api/public/pages/privacy", nil, nil); !strings.Contains(string(r.Body), "Edited by the owner.") {
		t.Fatalf("public page lacks the edit: %s", r.Body)
	}
	if got := e.Str(`SELECT details->>'changed' FROM email_audit_logs WHERE action='page.update' AND entity_id=$1`, privacy); got != `["blocks"]` {
		t.Fatalf("audit changed = %s", got)
	}
	versions := pageVersions(t, e, privacy)
	if len(versions) != 2 || versions[0].Action != "update" || versions[1].Action != "seed" || versions[0].Actor != testenv.AdminEmail ||
		strings.Join(versions[0].Changed, ",") != "blocks" {
		t.Fatalf("versions = %+v", versions)
	}

	// Saving without changes writes nothing.
	edited := getPage(t, e, privacy)
	if r := e.Admin("PATCH", "/api/admin/pages/"+privacy, editorBody(edited)); r.Status != 200 {
		t.Fatalf("no-op save: %d %s", r.Status, r.Body)
	}
	if p := getPage(t, e, privacy); !p.UpdatedAt.Equal(edited.UpdatedAt) || len(pageVersions(t, e, privacy)) != 2 ||
		e.Count(`SELECT count(*) FROM email_audit_logs WHERE action='page.update' AND entity_id=$1`, privacy) != 1 {
		t.Fatalf("no-op save changed something: %v -> %v", edited.UpdatedAt, p.UpdatedAt)
	}

	// Restore the seed text.
	seed := versions[1]
	detail := e.Admin("GET", "/api/admin/pages/"+privacy+"/versions/"+testenv.Sprint(seed.ID), nil)
	var snap struct {
		Snapshot struct {
			Title  string          `json:"title"`
			Blocks []content.Block `json:"blocks"`
			SEO    content.SEO     `json:"seo"`
		} `json:"snapshot"`
	}
	if detail.Status != 200 || detail.JSON(&snap) != nil || snap.Snapshot.Title != "Privacy notice" || len(snap.Snapshot.Blocks) != len(cur.Blocks) {
		t.Fatalf("version detail: %d %s", detail.Status, detail.Body)
	}
	if r := e.Admin("POST", "/api/admin/pages/"+privacy+"/versions/"+testenv.Sprint(seed.ID)+"/restore", nil); r.Status != 200 {
		t.Fatalf("restore: %d %s", r.Status, r.Body)
	}
	restored := getPage(t, e, privacy)
	a, _ := json.Marshal(restored.Blocks)
	b, _ := json.Marshal(cur.Blocks)
	if string(a) != string(b) || strings.Contains(string(a), "Edited by the owner.") {
		t.Fatal("restore did not bring back the seed text")
	}
	if v := pageVersions(t, e, privacy); len(v) != 3 || v[0].Action != "restore" {
		t.Fatalf("versions after restore = %+v", v)
	}
	if e.Count(`SELECT count(*) FROM email_audit_logs WHERE action='page.restore' AND entity_id=$1`, privacy) != 1 {
		t.Fatal("restore not audited")
	}
	if r := e.Admin("POST", "/api/admin/pages/"+privacy+"/versions/999999/restore", nil); r.Status != 404 {
		t.Fatalf("unknown version: %d", r.Status)
	}

	// The slugs are no longer reserved, but stay unique.
	if r := e.Admin("POST", "/api/admin/pages", map[string]any{"kind": "page", "title": "Terms", "slug": "terms"}); r.Status != 400 ||
		!strings.Contains(r.Map()["error"].(string), "another page already uses this slug") {
		t.Fatalf("duplicate legal slug: %d %s", r.Status, r.Body)
	}

	// Running 0007 again changes nothing.
	sql, err := os.ReadFile("../store/migrations/0007_legal_pages.sql")
	if err != nil {
		t.Fatal(err)
	}
	pages, vers := e.Count(`SELECT count(*) FROM cms_pages`), e.Count(`SELECT count(*) FROM content_versions`)
	e.Exec(string(sql))
	if e.Count(`SELECT count(*) FROM cms_pages`) != pages || e.Count(`SELECT count(*) FROM content_versions`) != vers ||
		!getPage(t, e, privacy).UpdatedAt.Equal(restored.UpdatedAt) {
		t.Fatal("re-running 0007 changed the database")
	}
}

// Migration 0008 deletes the old e2e test's mock articles and their unsent
// campaigns, and nothing else.
func TestRemoveMockArticlesMigration(t *testing.T) {
	e := testenv.New(t)
	mock := e.Str(`INSERT INTO cms_pages (kind, slug, title, excerpt, hero, status) VALUES ('article', 'rolling-patch-walkthrough-1761800000000',
		'Rolling patch walkthrough 1761800000000', 'Patching every RAC node without downtime.',
		'{"type":"youtube","youtube_id":"dQw4w9WgXcQ","url":"https://www.youtube.com/watch?v=dQw4w9WgXcQ"}', 'published') RETURNING id::text`)
	sentMock := e.Str(`INSERT INTO cms_pages (kind, slug, title, excerpt, hero) VALUES ('article', 'rolling-patch-walkthrough-1761800000001',
		'Rolling patch walkthrough 1761800000001', 'Patching every RAC node without downtime.',
		'{"type":"youtube","youtube_id":"dQw4w9WgXcQ"}') RETURNING id::text`)
	owned := e.Str(`INSERT INTO cms_pages (kind, slug, title, excerpt, hero) VALUES ('article', 'rolling-patch-walkthrough',
		'Rolling patch walkthrough', 'Patching every RAC node without downtime.', '{"type":"youtube","youtube_id":"dQw4w9WgXcQ"}') RETURNING id::text`)
	e.Exec(`INSERT INTO email_campaigns (name, subject, kind, blog_post_id) VALUES ('Mock', 'Mock', 'blog_notification', $1)`, mock)
	e.Exec(`INSERT INTO email_campaigns (name, subject, kind, status, blog_post_id) VALUES ('Sent mock', 'Mock', 'blog_notification', 'sent', $1)`, sentMock)
	e.Exec(`INSERT INTO email_campaigns (name, subject, kind, blog_post_id) VALUES ('Real', 'Real', 'blog_notification', $1)`, owned)

	sql, err := os.ReadFile("../store/migrations/0008_remove_e2e_mock_articles.sql")
	if err != nil {
		t.Fatal(err)
	}
	e.Exec(string(sql))
	if e.Count(`SELECT count(*) FROM cms_pages WHERE id IN ($1, $2)`, mock, sentMock) != 0 {
		t.Fatal("mock articles kept")
	}
	if e.Count(`SELECT count(*) FROM cms_pages WHERE id=$1`, owned) != 1 || e.Count(`SELECT count(*) FROM email_campaigns WHERE name IN ('Real', 'Sent mock')`) != 2 {
		t.Fatal("0008 deleted the owner's article or a sent campaign")
	}
	if e.Count(`SELECT count(*) FROM email_campaigns WHERE name='Mock'`) != 0 {
		t.Fatal("the mock article's draft campaign was kept")
	}
	if e.Count(`SELECT count(*) FROM cms_pages WHERE kind='page' AND source='system'`) != 3 {
		t.Fatal("0008 touched the legal pages")
	}
}

func getPage(t *testing.T, e *testenv.Env, id string) content.Page {
	t.Helper()
	r := e.Admin("GET", "/api/admin/pages/"+id, nil)
	var p content.Page
	if r.Status != 200 || r.JSON(&p) != nil {
		t.Fatalf("get page: %d %s", r.Status, r.Body)
	}
	return p
}

// editorBody is what the admin page editor sends on save.
func editorBody(p content.Page) map[string]any {
	return map[string]any{"kind": p.Kind, "title": p.Title, "slug": p.Slug, "excerpt": p.Excerpt, "hero": p.Hero, "blocks": p.Blocks,
		"featured_image_url": p.FeaturedImageURL, "author_name": p.AuthorName, "categories": p.Categories, "tags": p.Tags, "seo": p.SEO}
}

type pageVersion struct {
	ID      int64    `json:"id"`
	Action  string   `json:"action"`
	Actor   string   `json:"actor"`
	Changed []string `json:"changed"`
}

func pageVersions(t *testing.T, e *testenv.Env, id string) []pageVersion {
	t.Helper()
	var out struct {
		Items []pageVersion `json:"items"`
	}
	if r := e.Admin("GET", "/api/admin/pages/"+id+"/versions", nil); r.Status != 200 || r.JSON(&out) != nil {
		t.Fatalf("versions: %d %s", r.Status, r.Body)
	}
	return out.Items
}
