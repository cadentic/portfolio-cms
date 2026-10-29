package acceptance

import (
	"bytes"
	"image"
	"image/png"
	"mime/multipart"
	"net/http"
	"strings"
	"testing"

	"github.com/cadentic/portfolio-cms/backend/internal/testenv"
)

func pngBytes(t *testing.T, w, h int) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := png.Encode(&buf, image.NewRGBA(image.Rect(0, 0, w, h))); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// Owner uploads go to private storage, are served from /media/<key> with
// caching and ranges, and are listed, protected while in use, and deleted.
func TestMediaUploadServeDelete(t *testing.T) {
	e := testenv.New(t)
	img := pngBytes(t, 40, 30)

	if r := e.Do("POST", "/api/admin/media", nil, nil); r.Status != http.StatusUnauthorized {
		t.Fatalf("anonymous upload: %d, want 401", r.Status)
	}
	r := e.AdminUpload("/api/admin/media", "file", `..\..\My Photo.PNG`, img)
	if r.Status != http.StatusCreated {
		t.Fatalf("upload: %d %s", r.Status, r.Body)
	}
	m := r.Map()
	url, _ := m["url"].(string)
	if !strings.HasPrefix(url, "/media/uploads/") || !strings.HasSuffix(url, "-my-photo.png") || m["kind"] != "image" ||
		m["width"] != float64(40) || m["height"] != float64(30) || m["content_type"] != "image/png" {
		t.Fatalf("upload result = %v", m)
	}
	id := m["id"].(string)

	g := e.Do("GET", url, nil, nil)
	if g.Status != 200 || !bytes.Equal(g.Body, img) || g.Header.Get("Content-Type") != "image/png" ||
		!strings.Contains(g.Header.Get("Cache-Control"), "max-age=31536000") || g.Header.Get("ETag") == "" {
		t.Fatalf("serve: %d %v", g.Status, g.Header)
	}
	if c := e.Do("GET", url, nil, map[string]string{"If-None-Match": g.Header.Get("ETag")}); c.Status != http.StatusNotModified {
		t.Fatalf("conditional GET: %d, want 304", c.Status)
	}
	if p := e.Do("GET", url, nil, map[string]string{"Range": "bytes=0-7"}); p.Status != http.StatusPartialContent || !bytes.Equal(p.Body, img[:8]) {
		t.Fatalf("range GET: %d %q", p.Status, p.Body)
	}
	for _, bad := range []string{"/media/uploads/missing.png", "/media/.hidden", "/media/uploads/%2e%2e/x.png"} {
		if b := e.Do("GET", bad, nil, nil); b.Status != http.StatusNotFound {
			t.Fatalf("GET %s: %d, want 404", bad, b.Status)
		}
	}

	// Content is checked from the bytes, not the name.
	if b := e.AdminUpload("/api/admin/media", "file", "logo.png", []byte(`<svg xmlns="http://www.w3.org/2000/svg"><script>alert(1)</script></svg>`)); b.Status != http.StatusUnsupportedMediaType {
		t.Fatalf("svg upload: %d, want 415", b.Status)
	}
	if b := e.AdminUpload("/api/admin/media", "file", "x.png", append([]byte("\x89PNG\r\n\x1a\n"), "garbage"...)); b.Status != http.StatusUnsupportedMediaType {
		t.Fatalf("corrupt png: %d, want 415", b.Status)
	}
	if b := e.AdminUpload("/api/admin/media", "file", "big.pdf", append([]byte("%PDF-1.7\n"), make([]byte, 2<<20)...)); b.Status != http.StatusRequestEntityTooLarge && b.Status != http.StatusBadRequest {
		t.Fatalf("oversized upload: %d", b.Status)
	}
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	mw.WriteField("alt", "Diagram")
	fw, _ := mw.CreateFormFile("file", "doc.pdf")
	fw.Write([]byte("%PDF-1.4\n%%EOF\n"))
	mw.Close()
	pdf := e.Do("POST", "/api/admin/media", buf.Bytes(), map[string]string{"Content-Type": mw.FormDataContentType(),
		"Authorization": "Bearer " + testenv.EmulatorToken("owner-uid", testenv.AdminEmail)})
	if pdf.Status != http.StatusCreated || pdf.Map()["kind"] != "file" || pdf.Map()["alt"] != "Diagram" {
		t.Fatalf("pdf upload: %d %s", pdf.Status, pdf.Body)
	}

	var list struct {
		Items []map[string]any `json:"items"`
		Total int              `json:"total"`
	}
	if err := e.Admin("GET", "/api/admin/media?kind=image&q=photo", nil).JSON(&list); err != nil || list.Total != 1 || list.Items[0]["id"] != id {
		t.Fatalf("list = %+v %v", list, err)
	}
	if u := e.Admin("PATCH", "/api/admin/media/"+id, map[string]string{"alt": " Team photo "}); u.Status != 200 || u.Map()["alt"] != "Team photo" {
		t.Fatalf("patch: %d %s", u.Status, u.Body)
	}

	// In use on a page: deletion needs force.
	e.Exec(`INSERT INTO cms_pages (kind, slug, title, hero) VALUES ('page', 'about-media', 'About', jsonb_build_object('type','image','url',$1::text))`, url)
	if d := e.Admin("DELETE", "/api/admin/media/"+id, nil); d.Status != http.StatusConflict {
		t.Fatalf("delete in use: %d, want 409", d.Status)
	}
	if d := e.Admin("DELETE", "/api/admin/media/"+id+"?force=true", nil); d.Status != http.StatusNoContent {
		t.Fatalf("forced delete: %d %s", d.Status, d.Body)
	}
	if g := e.Do("GET", url, nil, nil); g.Status != http.StatusNotFound {
		t.Fatalf("deleted file still served: %d", g.Status)
	}
	if n := e.Count(`SELECT count(*) FROM email_audit_logs WHERE action LIKE 'media.%'`); n < 4 {
		t.Fatalf("audit rows = %d", n)
	}
}
