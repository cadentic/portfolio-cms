package acceptance

import (
	"bytes"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/cadentic/portfolio-cms/backend/internal/media"
	"github.com/cadentic/portfolio-cms/backend/internal/testenv"
)

// The owner uploads the resume PDF in the admin. It is stored privately,
// never served from /media, and downloaded only through the verified link.
func TestResumeUpload(t *testing.T) {
	e := testenv.New(t)
	site := e.Do("GET", "/api/public/site", nil, nil).Map()
	if site["resume_available"] != false || site["verification_ttl_hours"] != float64(48) {
		t.Fatalf("site config = %v", site)
	}
	if r := e.Do("POST", "/api/public/resume/request", map[string]string{"email": "recruiter@example.com"}, nil); r.Status != 404 {
		t.Fatalf("resume request without a resume: %d", r.Status)
	}
	st := e.Admin("GET", "/api/admin/resume", nil).Map()
	if st["source"] != "none" || st["filename"] != nil || st["download_name"] != "Sayantan-Chakraborty-Resume.pdf" || st["max_bytes"] != float64(1<<20) {
		t.Fatalf("status = %v", st)
	}
	if r := e.Admin("GET", "/api/admin/resume/file", nil); r.Status != 404 {
		t.Fatalf("file without a resume: %d", r.Status)
	}

	pdf := []byte("%PDF-1.4\n%\xe2\xe3\xcf\xd3\n1 0 obj << >> endobj\n%%EOF\n")
	if r := e.AdminUpload("/api/admin/resume", "file", "logo.png", pngBytes(t, 4, 4)); r.Status != http.StatusUnsupportedMediaType {
		t.Fatalf("png upload: %d %s", r.Status, r.Body)
	}
	if r := e.AdminUpload("/api/admin/resume", "file", "big.pdf", append(bytes.Clone(pdf), make([]byte, 1<<20)...)); r.Status != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversized upload: %d %s", r.Status, r.Body)
	}
	first := e.AdminUpload("/api/admin/resume", "file", `C:\Users\me\Old Resume.pdf`, pdf)
	if first.Status != 200 || first.Map()["source"] != "upload" || first.Map()["filename"] != "Old Resume.pdf" {
		t.Fatalf("upload: %d %s", first.Status, first.Body)
	}
	oldKey := e.Str(`SELECT value->>'key' FROM app_settings WHERE key='resume'`)
	pdf2 := append(bytes.Clone(pdf), "% second\n"...)
	r := e.AdminUpload("/api/admin/resume", "file", "Resume 2026.pdf", pdf2)
	m := r.Map()
	if r.Status != 200 || m["source"] != "upload" || m["filename"] != "Resume 2026.pdf" || m["size_bytes"] != float64(len(pdf2)) ||
		m["uploaded_by"] != testenv.AdminEmail || m["uploaded_at"] == nil {
		t.Fatalf("second upload: %d %s", r.Status, r.Body)
	}
	key := e.Str(`SELECT value->>'key' FROM app_settings WHERE key='resume'`)
	if !strings.HasPrefix(key, "private/resume/") || !strings.HasSuffix(key, ".pdf") || key == oldKey {
		t.Fatalf("key = %q (old %q)", key, oldKey)
	}
	if _, err := e.Server.Media.Open(e.Ctx, oldKey); !errors.Is(err, media.ErrNotFound) {
		t.Fatalf("previous upload kept: %v", err)
	}
	if e.Count(`SELECT count(*) FROM email_audit_logs WHERE action='resume.upload'`) != 2 {
		t.Fatal("uploads not audited")
	}

	if site := e.Do("GET", "/api/public/site", nil, nil).Map(); site["resume_available"] != true {
		t.Fatalf("site config after upload = %v", site)
	}
	if r := e.Do("POST", "/api/public/resume/request", map[string]string{"email": "recruiter@example.com"}, nil); r.Status != http.StatusAccepted {
		t.Fatalf("resume request: %d %s", r.Status, r.Body)
	}
	tok := e.Signer.Sign("resume", "recruiter@example.com", time.Minute)
	dl := e.Do("GET", "/api/public/resume/download?token="+tok, nil, nil)
	if dl.Status != 200 || !bytes.Equal(dl.Body, pdf2) || dl.Header.Get("Content-Type") != "application/pdf" ||
		dl.Header.Get("Content-Disposition") != `attachment; filename="Sayantan-Chakraborty-Resume.pdf"` ||
		dl.Header.Get("Cache-Control") != "private, no-store" {
		t.Fatalf("download: %d %v", dl.Status, dl.Header)
	}
	if r := e.Do("GET", "/api/public/resume/download?token=forged", nil, nil); r.Status != http.StatusForbidden {
		t.Fatalf("forged token: %d", r.Status)
	}
	if r := e.Admin("GET", "/api/admin/resume/file", nil); r.Status != 200 || !bytes.Equal(r.Body, pdf2) {
		t.Fatalf("admin file: %d", r.Status)
	}
	for _, p := range []string{"/media/" + key, "/media/Private/resume/" + strings.TrimPrefix(key, "private/resume/")} {
		if r := e.Do("GET", p, nil, nil); r.Status != http.StatusNotFound {
			t.Fatalf("GET %s: %d, want 404", p, r.Status)
		}
	}

	// The download name follows the site profile.
	siteDoc(t, putSite(e, `{"identity":{"name":"Test Owner"}}`, nil))
	if dl := e.Do("GET", "/api/public/resume/download?token="+tok, nil, nil); dl.Header.Get("Content-Disposition") != `attachment; filename="Test-Owner-Resume.pdf"` {
		t.Fatalf("download name: %v", dl.Header)
	}

	d := e.Admin("DELETE", "/api/admin/resume", nil)
	if d.Status != 200 || d.Map()["source"] != "none" || d.Map()["download_name"] != "Test-Owner-Resume.pdf" {
		t.Fatalf("delete: %d %s", d.Status, d.Body)
	}
	if _, err := e.Server.Media.Open(e.Ctx, key); !errors.Is(err, media.ErrNotFound) {
		t.Fatalf("deleted resume still stored: %v", err)
	}
	if site := e.Do("GET", "/api/public/site", nil, nil).Map(); site["resume_available"] != false {
		t.Fatalf("site config after delete = %v", site)
	}
	if r := e.Do("GET", "/api/public/resume/download?token="+tok, nil, nil); r.Status != 404 {
		t.Fatalf("download after delete: %d", r.Status)
	}
	if e.Count(`SELECT count(*) FROM email_audit_logs WHERE action='resume.delete'`) != 1 {
		t.Fatal("delete not audited")
	}
}
