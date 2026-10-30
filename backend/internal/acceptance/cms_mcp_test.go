package acceptance

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/cadentic/portfolio-cms/backend/internal/testenv"
)

// The owner connects an MCP client (e.g. Claude) with OAuth 2.1 + PKCE,
// signing in with Firebase on the consent page, then creates and publishes
// an article without writing code.
func TestMCP_OAuthLoginAndCreatePage(t *testing.T) {
	e := testenv.New(t)

	// Discovery: unauthenticated MCP calls point at the metadata.
	r := e.Do("POST", "/mcp", `{"jsonrpc":"2.0","id":1,"method":"initialize"}`, nil)
	if r.Status != 401 || !strings.Contains(r.Header.Get("WWW-Authenticate"), "oauth-protected-resource") {
		t.Fatalf("unauthenticated MCP: %d %q", r.Status, r.Header.Get("WWW-Authenticate"))
	}
	if r := e.Do("GET", "/.well-known/oauth-authorization-server", nil, nil); r.Status != 200 || r.Map()["registration_endpoint"] == nil {
		t.Fatalf("AS metadata: %d %s", r.Status, r.Body)
	}

	// Dynamic client registration.
	reg := e.Do("POST", "/oauth/register", map[string]any{"client_name": "Claude", "redirect_uris": []string{"https://claude.ai/api/mcp/auth_callback"}}, nil)
	if reg.Status != 201 {
		t.Fatalf("register: %d %s", reg.Status, reg.Body)
	}
	clientID := reg.Map()["client_id"].(string)

	verifier := strings.Repeat("v", 50)
	sum := sha256.Sum256([]byte(verifier))
	challenge := base64.RawURLEncoding.EncodeToString(sum[:])
	authz := map[string]string{"client_id": clientID, "redirect_uri": "https://claude.ai/api/mcp/auth_callback", "response_type": "code",
		"code_challenge": challenge, "code_challenge_method": "S256", "state": "xyz", "resource": testenv.SiteURL + "/mcp"}

	// Authorize redirects the browser to the CMS consent page.
	q := url.Values{}
	for k, v := range authz {
		q.Set(k, v)
	}
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := client.Get(e.API.URL + "/oauth/authorize?" + q.Encode())
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 302 || !strings.HasPrefix(resp.Header.Get("Location"), testenv.SiteURL+"/admin/connect?") {
		t.Fatalf("authorize: %d %s", resp.StatusCode, resp.Header.Get("Location"))
	}

	// Only the Firebase-authenticated owner can approve.
	stranger := testenv.EmulatorToken("x", "x@example.com")
	if r := e.Do("POST", "/api/oauth/approve", authz, map[string]string{"Authorization": "Bearer " + stranger}); r.Status != 403 {
		t.Fatalf("non-owner approve: %d", r.Status)
	}
	ap := e.Admin("POST", "/api/oauth/approve", authz)
	if ap.Status != 200 {
		t.Fatalf("approve: %d %s", ap.Status, ap.Body)
	}
	cb, _ := url.Parse(ap.Map()["redirect"].(string))
	code := cb.Query().Get("code")
	if cb.Query().Get("state") != "xyz" || code == "" {
		t.Fatalf("callback: %s", cb)
	}

	form := func(v url.Values) testenv.Response {
		return e.Do("POST", "/oauth/token", v.Encode(), map[string]string{"Content-Type": "application/x-www-form-urlencoded"})
	}
	if r := form(url.Values{"grant_type": {"authorization_code"}, "code": {code}, "client_id": {clientID},
		"redirect_uri": {authz["redirect_uri"]}, "code_verifier": {strings.Repeat("w", 50)}}); r.Status != 400 {
		t.Fatalf("wrong PKCE verifier accepted: %d", r.Status)
	}
	// A failed attempt burns the code (single use).
	ap2 := e.Admin("POST", "/api/oauth/approve", authz)
	cb2, _ := url.Parse(ap2.Map()["redirect"].(string))
	tok := form(url.Values{"grant_type": {"authorization_code"}, "code": {cb2.Query().Get("code")}, "client_id": {clientID},
		"redirect_uri": {authz["redirect_uri"]}, "code_verifier": {verifier}})
	if tok.Status != 200 {
		t.Fatalf("token: %d %s", tok.Status, tok.Body)
	}
	access := tok.Map()["access_token"].(string)
	refresh := tok.Map()["refresh_token"].(string)

	rpc := func(id int, method string, params any) map[string]any {
		t.Helper()
		body, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": id, "method": method, "params": params})
		r := e.Do("POST", "/mcp", body, map[string]string{"Authorization": "Bearer " + access, "Accept": "application/json, text/event-stream"})
		if r.Status != 200 {
			t.Fatalf("%s: %d %s", method, r.Status, r.Body)
		}
		return r.Map()
	}
	init := rpc(1, "initialize", map[string]any{"protocolVersion": "2025-06-18", "capabilities": map[string]any{}, "clientInfo": map[string]any{"name": "test", "version": "1"}})
	if init["result"] == nil {
		t.Fatalf("initialize: %v", init)
	}
	tools := rpc(2, "tools/list", map[string]any{})
	names := ""
	for _, tl := range tools["result"].(map[string]any)["tools"].([]any) {
		names += tl.(map[string]any)["name"].(string) + " "
	}
	for _, want := range []string{"create_page", "publish_page", "list_pages"} {
		if !strings.Contains(names, want) {
			t.Fatalf("tool %s missing: %s", want, names)
		}
	}
	if strings.Contains(names, "send") {
		t.Fatalf("MCP must not expose sending tools: %s", names)
	}

	created := rpc(3, "tools/call", map[string]any{"name": "create_page", "arguments": map[string]any{
		"kind": "article", "title": "RMAN restores without drama",
		"hero":            map[string]any{"type": "youtube", "url": "https://www.youtube.com/watch?v=gXT9izr0ljQ"},
		"body_markdown":   "## Why\n\nBecause **backups** are only as good as restores.\n\n- one\n- two",
		"seo_description": "A practical restore checklist.",
	}})
	res := created["result"].(map[string]any)
	if res["isError"] == true {
		t.Fatalf("create_page error: %v", res)
	}
	pageID := e.Str(`SELECT id::text FROM cms_pages WHERE title='RMAN restores without drama'`)
	if got := e.Str(`SELECT hero->>'type' FROM cms_pages WHERE id=$1`, pageID); got != "youtube" {
		t.Fatalf("hero type = %s", got)
	}
	rpc(4, "tools/call", map[string]any{"name": "publish_page", "arguments": map[string]any{"id": pageID}})

	// The published article is public, indexed in the sitemap and llms.txt,
	// and carries SEO metadata for the SSR renderer.
	slug := e.Str(`SELECT slug FROM cms_pages WHERE id=$1`, pageID)
	art := e.Do("GET", "/api/public/articles/"+slug, nil, nil)
	if art.Status != 200 || !strings.Contains(string(art.Body), "A practical restore checklist.") {
		t.Fatalf("public article: %d %s", art.Status, art.Body)
	}
	if sm := e.Do("GET", "/sitemap.xml", nil, nil); !strings.Contains(string(sm.Body), "/blog/"+slug) {
		t.Fatalf("sitemap missing article: %s", sm.Body)
	}
	if l := e.Do("GET", "/llms.txt", nil, nil); !strings.Contains(string(l.Body), "RMAN restores without drama") {
		t.Fatalf("llms.txt missing article: %s", l.Body)
	}

	// Refresh tokens rotate; the old one stops working.
	if r := form(url.Values{"grant_type": {"refresh_token"}, "refresh_token": {refresh}, "client_id": {clientID}}); r.Status != 200 {
		t.Fatalf("refresh: %d %s", r.Status, r.Body)
	}
	if r := form(url.Values{"grant_type": {"refresh_token"}, "refresh_token": {refresh}, "client_id": {clientID}}); r.Status != 400 {
		t.Fatalf("refresh token reuse accepted: %d", r.Status)
	}
	// Replaying an authorization code fails and revokes what it was exchanged for.
	body, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 9, "method": "tools/list"})
	if r := form(url.Values{"grant_type": {"authorization_code"}, "code": {cb2.Query().Get("code")}, "client_id": {clientID},
		"redirect_uri": {authz["redirect_uri"]}, "code_verifier": {verifier}}); r.Status != 400 {
		t.Fatalf("code replay accepted: %d", r.Status)
	}
	if r := e.Do("POST", "/mcp", body, map[string]string{"Authorization": "Bearer " + access}); r.Status != 401 {
		t.Fatalf("token from a replayed code still works: %d", r.Status)
	}
	// The owner can disconnect every MCP client from the admin area.
	e.Admin("POST", "/api/admin/mcp/revoke-all", nil)
	if r := e.Do("POST", "/mcp", body, map[string]string{"Authorization": "Bearer " + access}); r.Status != 401 {
		t.Fatalf("revoked token still works: %d", r.Status)
	}
}

// User input never reaches SQL as code: hostile search, sort and path values
// are treated as data.
func TestSecurity_SQLInjection(t *testing.T) {
	e := testenv.New(t)
	e.Subscribe("reader@example.com")
	payloads := []string{
		"' OR '1'='1", "'; DROP TABLE subscribers; --", "\" OR 1=1 --", "1) UNION SELECT password FROM pg_shadow --",
		"%' AND pg_sleep(5) IS NULL AND '%'='", "status; DELETE FROM email_jobs",
	}
	for _, p := range payloads {
		q := url.QueryEscape(p)
		for _, path := range []string{
			"/api/admin/subscribers?q=" + q + "&sort=" + q + "&status=" + q,
			"/api/admin/email/jobs?q=" + q + "&status=" + q + "&provider=" + q,
			"/api/admin/suppressions?q=" + q,
			"/api/admin/pages?q=" + q + "&status=" + q + "&kind=" + q,
			"/api/admin/audit?entity_type=" + q,
		} {
			r := e.Admin("GET", path, nil)
			if r.Status >= 500 {
				t.Fatalf("%s -> %d %s", path, r.Status, r.Body)
			}
		}
		for _, path := range []string{"/api/public/articles/" + url.PathEscape(p), "/api/public/pages/" + url.PathEscape(p),
			"/api/public/redirect?path=" + q, "/api/public/articles?tag=" + q} {
			if r := e.Do("GET", path, nil, nil); r.Status >= 500 {
				t.Fatalf("%s -> %d %s", path, r.Status, r.Body)
			}
		}
		e.Do("POST", "/api/public/subscribe", map[string]any{"email": p + "@example.com", "name": p, "consent": true}, nil)
		e.Do("POST", "/api/public/subscribe/confirm", map[string]string{"token": p}, nil)
	}
	if e.Count(`SELECT count(*) FROM subscribers WHERE email='reader@example.com'`) != 1 {
		t.Fatal("subscribers table damaged")
	}
	// A search that would match everything if injected matches nothing.
	r := e.Admin("GET", "/api/admin/subscribers?q="+url.QueryEscape("' OR '1'='1"), nil)
	if r.Map()["total"].(float64) != 0 {
		t.Fatalf("injection changed the query: %s", r.Body)
	}
}

// CSV import validates, previews and needs an explicit confirmation; export
// neutralises spreadsheet formulas.
func TestBulkImportAndExport(t *testing.T) {
	e := testenv.New(t)
	csv := "email,name,consent_source\nalice@example.com,Alice,event signup 2026\nbad-address,Bob,x\nalice@example.com,Dup,x\n=cmd@example.com,\"=HYPERLINK(\"\"x\"\")\",x\n"
	prev := e.AdminUpload("/api/admin/subscribers/import/preview", "file", "subscribers.csv", []byte(csv))
	if prev.Status != 200 {
		t.Fatalf("preview: %d %s", prev.Status, prev.Body)
	}
	if e.Count(`SELECT count(*) FROM subscribers`) != 0 {
		t.Fatal("preview wrote subscribers")
	}
	token := prev.Map()["token"].(string)
	no := false
	commit := map[string]any{"token": token, "confirm": false, "lawful_basis": "consent",
		"consent_evidence": "Signed sheet at DBA meetup, 2026-10-01", "send_confirmation": &no}
	if r := e.Admin("POST", "/api/admin/subscribers/import/commit", commit); r.Status < 400 {
		t.Fatal("commit without confirmation accepted")
	}
	commit["confirm"] = true
	if r := e.Admin("POST", "/api/admin/subscribers/import/commit", commit); r.Status != 200 {
		t.Fatalf("commit: %d %s", r.Status, r.Body)
	}
	if r := e.Admin("POST", "/api/admin/subscribers/import/commit", commit); r.Status == 200 && e.Count(`SELECT count(*) FROM subscribers`) > 2 {
		t.Fatal("import token reused")
	}
	if n := e.Count(`SELECT count(*) FROM subscribers WHERE email='alice@example.com'`); n != 1 {
		t.Fatalf("alice rows = %d", n)
	}
	if e.Count(`SELECT count(*) FROM subscribers WHERE email='bad-address'`) != 0 {
		t.Fatal("invalid address imported")
	}
	if e.Count(`SELECT count(*) FROM subscriber_consents c JOIN subscribers s ON s.id=c.subscriber_id WHERE s.email='alice@example.com'`) == 0 {
		t.Fatal("lawful basis / consent evidence not recorded")
	}
	exp := e.Admin("GET", "/api/admin/subscribers/export.csv", nil)
	for _, line := range strings.Split(string(exp.Body), "\n") {
		for _, cell := range strings.Split(line, ",") {
			c := strings.Trim(cell, "\"\ufeff")
			if strings.HasPrefix(c, "=") || strings.HasPrefix(c, "+") || strings.HasPrefix(c, "@") {
				t.Fatalf("formula not neutralised in export: %q", line)
			}
		}
	}
}
