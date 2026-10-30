package api

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/cadentic/portfolio-cms/backend/internal/config"
)

func TestCSRFBlocksCrossSiteWrites(t *testing.T) {
	s := &Server{Cfg: &config.Config{PublicSiteURL: "https://cadentic.dev", CORSOrigins: []string{"https://cadentic.dev"}},
		Log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	h := s.csrf(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNoContent) }))
	cases := []struct {
		name, method, path, site, origin string
		want                             int
	}{
		{"same-origin form post", "POST", "/api/public/contact", "same-origin", "https://cadentic.dev", 204},
		{"cross-site form post", "POST", "/api/public/contact", "cross-site", "https://evil.example", 403},
		{"cross-site admin call", "DELETE", "/api/admin/pages/1", "cross-site", "https://evil.example", 403},
		{"www is same-site but trusted", "POST", "/api/public/subscribe", "same-site", "https://cadentic.dev", 204},
		{"old browser, foreign origin", "POST", "/api/public/subscribe", "", "https://evil.example", 403},
		{"server-to-server", "POST", "/api/public/contact", "", "", 204},
		{"cross-site GET", "GET", "/api/public/site", "cross-site", "https://evil.example", 204},
		{"provider webhook", "POST", "/api/webhooks/brevo", "cross-site", "https://evil.example", 204},
		{"one-click unsubscribe", "POST", "/api/public/unsubscribe/one-click", "cross-site", "", 204},
		{"oauth token", "POST", "/oauth/token", "cross-site", "http://localhost:6274", 204},
		{"mcp", "POST", "/mcp", "cross-site", "http://localhost:6274", 204},
	}
	for _, c := range cases {
		r := httptest.NewRequest(c.method, c.path, nil)
		r.Host = "api:8080" // behind the web proxy the Host is the API's, not the site's
		if c.site != "" {
			r.Header.Set("Sec-Fetch-Site", c.site)
		}
		if c.origin != "" {
			r.Header.Set("Origin", c.origin)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != c.want {
			t.Errorf("%s: got %d, want %d", c.name, w.Code, c.want)
		}
	}
}

func TestDecodeRequiresJSON(t *testing.T) {
	for _, ct := range []string{"text/plain", "application/x-www-form-urlencoded", ""} {
		r := httptest.NewRequest("POST", "/api/public/contact", strings.NewReader(`{"name":"x"}`))
		if ct != "" {
			r.Header.Set("Content-Type", ct)
		}
		w := httptest.NewRecorder()
		var v map[string]any
		if decode(w, r, &v) || w.Code != http.StatusUnsupportedMediaType {
			t.Errorf("Content-Type %q: accepted (status %d)", ct, w.Code)
		}
	}
	r := httptest.NewRequest("POST", "/api/public/contact", strings.NewReader(`{"name":"x"}`))
	r.Header.Set("Content-Type", "application/json; charset=utf-8")
	var v map[string]any
	if !decode(httptest.NewRecorder(), r, &v) || v["name"] != "x" {
		t.Errorf("JSON body refused")
	}
}

func TestRequestIDIsSanitised(t *testing.T) {
	s := &Server{Log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	h := s.requestLog(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	for id, keep := range map[string]bool{"abc-123_x.y": true, "bad\nline": false, "<script>": false, strings.Repeat("a", 65): false} {
		r := httptest.NewRequest("GET", "/x", nil)
		r.Header.Set("X-Request-ID", id)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if got := w.Header().Get("X-Request-ID"); (got == id) != keep {
			t.Errorf("request id %q: echoed %q", id, got)
		}
	}
}
