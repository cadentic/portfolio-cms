package main

import (
	"bytes"
	"strings"
	"testing"
)

func testSnapshot(t *testing.T) *Snapshot {
	t.Helper()
	posts, err := loadRaw("testdata/raw")
	if err != nil {
		t.Fatal(err)
	}
	snap, err := Transform(posts, Options{
		Site: "csayantan.wordpress.com", SiteURL: "https://cadentic.dev",
		ArticlePath: "/blog", PagePath: "/", BlogIndex: "/blog", MediaURL: "/media/wordpress",
	})
	if err != nil {
		t.Fatal(err)
	}
	return snap
}

func item(t *testing.T, s *Snapshot, id int64) Item {
	t.Helper()
	for _, it := range s.Items {
		if it.SourceID == id {
			return it
		}
	}
	t.Fatalf("item %d missing", id)
	return Item{}
}

func TestPathsAndSlugs(t *testing.T) {
	s := testSnapshot(t)
	cases := map[int64][2]string{
		1:  {"hello-world", "/blog/hello-world"},
		10: {"11gr2-rac-installation", "/11gr2-rac-installation"},
		11: {"oracleasm", "/11gr2-rac-installation/oracleasm"},
		12: {"raw-device-creation", "/11gr2-rac-installation/oracleasm/raw-device-creation"},
		20: {"storage", "/storage"},
		// slug clash with page 20: qualified by the parent slug
		21: {"11gr2-rac-installation-storage", "/11gr2-rac-installation/11gr2-rac-installation-storage"},
	}
	for id, want := range cases {
		it := item(t, s, id)
		if it.Slug != want[0] || it.Path != want[1] {
			t.Errorf("item %d: got %s %s, want %v", id, it.Slug, it.Path, want)
		}
	}
	if p := item(t, s, 12).ParentSourceID; p != 11 {
		t.Errorf("parent of 12 = %d", p)
	}
	if got := item(t, s, 1).Title; got != "About – me" {
		t.Errorf("title not unescaped: %q", got)
	}
}

func TestContentOnlyBlocks(t *testing.T) {
	s := testSnapshot(t)
	var sb strings.Builder
	for _, b := range item(t, s, 1).Blocks {
		sb.WriteString(b.Type + ":" + b.Text + b.HTML + "|")
	}
	got := sb.String()
	for _, bad := range []string{"<script", "alert", "Share this", "srcset", "data-orig", "style=", "class=", "wordpress.com"} {
		if strings.Contains(got, bad) {
			t.Errorf("blocks still contain %q: %s", bad, got)
		}
	}
	for _, want := range []string{
		`<a href="/11gr2-rac-installation/oracleasm">oracleasm notes</a>`,
		"paragraph:Plain text paragraph\nsecond line|",
		`<img src="/media/wordpress/2011/05/rac.png" alt="RAC diagram"`,
		`<figcaption>RAC layout</figcaption>`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("blocks missing %s\n got %s", want, got)
		}
	}

	blocks := item(t, s, 11).Blocks
	types := []string{}
	for _, b := range blocks {
		types = append(types, b.Type)
	}
	if strings.Join(types, ",") != "paragraph,code,html,quote,divider" {
		t.Fatalf("block types = %v", types)
	}
	if c := blocks[1]; c.Language != "bash" || c.Text != "# /etc/init.d/oracleasm configure\nDefault user [oracle]: <grid>" {
		t.Errorf("code block = %+v", c)
	}
	if h := blocks[2].HTML; h != "<table><tbody><tr><th>Disk</th><td>/dev/sdb1</td></tr></tbody></table>" {
		t.Errorf("table = %s", h)
	}

	page := item(t, s, 10).Blocks
	if page[0].Type != "heading" || page[0].Level != 2 || page[1].Type != "list" || page[1].Style != "ordered" || len(page) != 2 {
		t.Errorf("page 10 blocks = %+v (gallery shortcode must be dropped)", page)
	}
}

func TestHeroSEOAndMedia(t *testing.T) {
	s := testSnapshot(t)
	it := item(t, s, 1)
	if it.Hero.Type != "image" || it.Hero.URL != "/media/wordpress/2011/05/rac.png" {
		t.Errorf("hero = %+v", it.Hero)
	}
	if it.SEO.Canonical != "https://cadentic.dev/blog/hello-world" || it.SEO.Description != "I am an Oracle DBA. See oracleasm notes." {
		t.Errorf("seo = %+v", it.SEO)
	}
	if strings.Join(it.Categories, ",") != "Oracle Database Talk" || strings.Join(it.Tags, ",") != "RAC" {
		t.Errorf("terms = %v %v", it.Categories, it.Tags)
	}
	if len(s.Media) != 1 {
		t.Fatalf("media = %+v", s.Media)
	}
	m := s.Media[0]
	if m.SourceURL != "https://csayantan.files.wordpress.com/2011/05/rac.png" || m.Width != 800 || m.Kind != "image" {
		t.Errorf("media = %+v", m)
	}
	if item(t, s, 20).Hero.Type != "none" {
		t.Error("page without featured image should have hero none")
	}
}

func TestRedirects(t *testing.T) {
	s := testSnapshot(t)
	got := map[string]string{}
	for _, r := range s.Redirects {
		got[r.From] = r.To
	}
	want := map[string]string{
		"/2009/04/28/hello-world":         "/blog/hello-world",
		"/2009/04":                        "/blog",
		"/category/oracle-database-talk":  "/blog?category=Oracle+Database+Talk",
		"/tag/rac":                        "/blog?tag=RAC",
		"/11gr2-rac-installation/storage": "/11gr2-rac-installation/11gr2-rac-installation-storage",
		"/feed":                           "/blog",
	}
	for from, to := range want {
		if got[from] != to {
			t.Errorf("redirect %s = %q, want %q", from, got[from], to)
		}
	}
	// Pages whose path did not change need no redirect.
	if _, ok := got["/11gr2-rac-installation/oracleasm"]; ok {
		t.Error("unchanged page path should not redirect")
	}
}

func TestSeedIsDraftOnly(t *testing.T) {
	var buf bytes.Buffer
	if err := writeSeed(&buf, testSnapshot(t)); err != nil {
		t.Fatal(err)
	}
	sql := buf.String()
	if strings.Contains(sql, "'published'") {
		t.Error("seed must never publish")
	}
	if strings.Count(sql, "'draft', NULL") != 6 {
		t.Error("every item must be inserted as draft with published_at NULL")
	}
}

func TestBlockquoteCommandsBecomeCode(t *testing.T) {
	b := htmlToBlocks("<blockquote><p>From sqlplus:</p>\n<p>connect / as sysdba</p>\n<p>SELECT name<br />\nFROM v$asm_alias;</p></blockquote><blockquote><p>Keep it simple, and test your backups.</p></blockquote>",
		func(s string) string { return s })
	if len(b) != 2 || b[0].Type != "code" || b[0].Language != "sql" ||
		b[0].Text != "From sqlplus:\nconnect / as sysdba\nSELECT name\nFROM v$asm_alias;" {
		t.Fatalf("code block = %+v", b)
	}
	if b[1].Type != "quote" || b[1].Text != "Keep it simple, and test your backups." {
		t.Fatalf("quote block = %+v", b[1])
	}
}

func TestBoldParagraphBecomesHeading(t *testing.T) {
	b := htmlToBlocks("<p><strong>Reference Links :</strong></p><p><strong>step 1)</strong> edit the file<code></code></p>",
		func(s string) string { return s })
	if len(b) != 2 || b[0].Type != "heading" || b[0].Level != 3 || b[0].Text != "Reference Links" {
		t.Fatalf("heading = %+v", b)
	}
	if b[1].Type != "html" || b[1].HTML != "<p><strong>step 1)</strong> edit the file</p>" {
		t.Fatalf("paragraph = %+v", b[1])
	}
}

func TestDocumentsAreNotCopied(t *testing.T) {
	posts := []wpPost{{ID: 1, Type: "page", Slug: "about", Status: "publish", URL: "https://csayantan.wordpress.com/about/",
		Content: `<p><a href="https://csayantan.wordpress.com/wp-content/uploads/2011/06/resume.pdf">resume</a></p>`}}
	s, err := Transform(posts, Options{Site: "csayantan.wordpress.com", PagePath: "/", ArticlePath: "/blog", MediaURL: "/media/wordpress"})
	if err != nil {
		t.Fatal(err)
	}
	if len(s.Media) != 0 || !strings.Contains(s.Items[0].Blocks[0].HTML, "https://csayantan.wordpress.com/wp-content/uploads/2011/06/resume.pdf") {
		t.Fatalf("media=%v blocks=%+v", s.Media, s.Items[0].Blocks)
	}
}
