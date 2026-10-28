package content

import (
	"strings"
	"testing"
)

func TestYouTubeID(t *testing.T) {
	for in, want := range map[string]string{
		"https://www.youtube.com/watch?v=dQw4w9WgXcQ": "dQw4w9WgXcQ",
		"https://youtu.be/dQw4w9WgXcQ?t=10":           "dQw4w9WgXcQ",
		"https://www.youtube.com/embed/dQw4w9WgXcQ":   "dQw4w9WgXcQ",
		"https://www.youtube.com/shorts/dQw4w9WgXcQ":  "dQw4w9WgXcQ",
		"dQw4w9WgXcQ": "dQw4w9WgXcQ",
		"https://evil.example/watch?v=dQw4w9WgXcQ\"><script>": "",
		"javascript:alert(1)": "",
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
	if h, err := normalizeHero(Hero{Type: "youtube", URL: "https://youtu.be/dQw4w9WgXcQ"}); err != nil || h.YouTubeID != "dQw4w9WgXcQ" {
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
