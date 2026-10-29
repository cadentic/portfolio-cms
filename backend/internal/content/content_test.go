package content

import (
	"encoding/json"
	"os"
	"reflect"
	"regexp"
	"strings"
	"testing"

	"github.com/cadentic/portfolio-cms/backend/internal/render"
)

func TestYouTubeID(t *testing.T) {
	for in, want := range map[string]string{
		"https://www.youtube.com/watch?v=gXT9izr0ljQ":      "gXT9izr0ljQ",
		"https://youtu.be/gXT9izr0ljQ?t=10":                "gXT9izr0ljQ",
		"https://youtu.be/gXT9izr0ljQ?si=XKDH-MSqA-o66lk3": "gXT9izr0ljQ",
		"https://www.youtube.com/embed/gXT9izr0ljQ":        "gXT9izr0ljQ",
		"https://www.youtube.com/shorts/gXT9izr0ljQ":       "gXT9izr0ljQ",
		"gXT9izr0ljQ": "gXT9izr0ljQ",
		// Share → Embed with privacy-enhanced mode.
		"https://www.youtube-nocookie.com/embed/gXT9izr0ljQ":            "gXT9izr0ljQ",
		"https://www.youtube-nocookie.com/embed/gXT9izr0ljQ?start=30":   "gXT9izr0ljQ",
		"https://www.youtube.com/watch?feature=share&v=gXT9izr0ljQ#t=5": "gXT9izr0ljQ",
		"https://www.youtube.com/live/gXT9izr0ljQ/":                     "gXT9izr0ljQ",
		// An ID longer than 11 characters is a mistake, not a prefix to keep.
		"https://www.youtube.com/watch?v=gXT9izr0ljQxyz":      "",
		"https://youtu.be/gXT9izr0ljQgXT9izr0ljQ":             "",
		"https://www.youtube.com/embed/gXT9izr0ljQ-":          "",
		"https://evil.example/watch?v=gXT9izr0ljQ\"><script>": "",
		"javascript:alert(1)":                                 "",
	} {
		if got := YouTubeID(in); got != want {
			t.Errorf("YouTubeID(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestHeroValidation(t *testing.T) {
	if _, err := normalizeHero(Hero{Type: "image", URL: "javascript:alert(1)"}); err == nil {
		t.Error("javascript: URL accepted for hero image")
	}
	if h, err := normalizeHero(Hero{Type: "youtube", URL: "https://youtu.be/gXT9izr0ljQ"}); err != nil || h.YouTubeID != "gXT9izr0ljQ" {
		t.Errorf("youtube hero: %+v %v", h, err)
	}
	if _, err := normalizeHero(Hero{Type: "flash"}); err == nil {
		t.Error("unknown hero type accepted")
	}
}

func TestSlugify(t *testing.T) {
	for in, want := range map[string]string{"Oracle Data Guard: In Practice!": "oracle-data-guard-in-practice", "  --x--  ": "x", "Ünïcödé café": "unicode-cafe"} {
		if got := Slugify(in); got != want {
			t.Errorf("Slugify(%q) = %q, want %q", in, got, want)
		}
	}
}

// Text blocks are plain text (the site escapes them when rendering); only the
// "html" block carries markup, and it is sanitised on save.
func TestBlocksSanitised(t *testing.T) {
	blocks := BlocksFromMarkdown("## Title\n\nHello **bold**\n\n- a\n- b\n\n```sql\nSELECT 1;\n```\n")
	if len(blocks) < 4 || blocks[0].Type != "heading" || blocks[2].Type != "list" || blocks[3].Type != "code" {
		t.Fatalf("blocks = %+v", blocks)
	}
	out, err := normalizeBlocks([]Block{{Type: "html", HTML: `<p onclick="x()">ok</p><script>alert(1)</script><a href="javascript:alert(1)">x</a>`}})
	if err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{"<script", "onclick", "javascript:"} {
		if strings.Contains(out[0].HTML, bad) {
			t.Fatalf("%s survived: %s", bad, out[0].HTML)
		}
	}
	if _, err := normalizeBlocks([]Block{{Type: "iframe"}}); err == nil {
		t.Fatal("unknown block type accepted")
	}
}

func TestSafeURL(t *testing.T) {
	for in, want := range map[string]bool{
		"": true, "/media/uploads/x.png": true, "https://example.com/x": true,
		"//evil.com": false, "/\\evil.com": false, "javascript:alert(1)": false, "http://example.com": false,
	} {
		if got := safeURL(in); got != want {
			t.Errorf("safeURL(%q) = %v, want %v", in, got, want)
		}
	}
}

// The legal pages seeded by migration 0007 are already in the form an editor
// save produces, so a save without edits changes nothing.
func TestLookupable(t *testing.T) {
	for s, want := range map[string]bool{"about": true, "über-uns": true, "a\x00b": false, "\xff": false, "\\evil.example#\x00": false} {
		if got := lookupable(s); got != want {
			t.Errorf("lookupable(%q) = %v, want %v", s, got, want)
		}
	}
}

func TestLegalSeedBlocksNormalised(t *testing.T) {
	sql, err := os.ReadFile("../store/migrations/0007_legal_pages.sql")
	if err != nil {
		t.Fatal(err)
	}
	re := regexp.MustCompile(`(?s)\$(privacy|terms|aipolicy)\$(.*?)\$(privacy|terms|aipolicy)\$`)
	found := re.FindAllStringSubmatch(string(sql), -1)
	if len(found) != 3 {
		t.Fatalf("found %d block arrays, want 3", len(found))
	}
	for _, m := range found {
		var blocks []Block
		if err := json.Unmarshal([]byte(m[2]), &blocks); err != nil {
			t.Fatalf("%s: %v", m[1], err)
		}
		norm, err := normalizeBlocks(blocks)
		if err != nil || !reflect.DeepEqual(norm, blocks) {
			t.Fatalf("%s blocks are not in normalised form: %v\n%+v\n%+v", m[1], err, blocks, norm)
		}
		for _, b := range blocks {
			if b.Type == "html" && render.Sanitize(b.HTML) != b.HTML {
				t.Fatalf("%s html changes when sanitised:\n%s\n%s", m[1], b.HTML, render.Sanitize(b.HTML))
			}
			if strings.Contains(b.Text+strings.Join(b.Items, ""), "{{") {
				t.Fatalf("%s: template placeholder left in %+v", m[1], b)
			}
		}
	}
}
