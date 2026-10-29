package acceptance

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/cadentic/portfolio-cms/backend/internal/sitecontent"
	"github.com/cadentic/portfolio-cms/backend/internal/testenv"
)

// An AI assistant connected over MCP reads and edits the site content through
// the same validation as the admin, and cannot touch the legal pages.
func TestSiteContentMCP(t *testing.T) {
	e := testenv.New(t)
	rpc := mcpClient(t, e)

	init := rpc(1, "initialize", map[string]any{"protocolVersion": "2025-06-18", "capabilities": map[string]any{}, "clientInfo": map[string]any{"name": "test", "version": "1"}})
	if s, _ := dig(init, "result", "instructions").(string); !strings.Contains(s, "update_site_content") {
		t.Fatalf("instructions: %v", init)
	}
	tools := map[string]map[string]any{}
	for _, tl := range dig(rpc(2, "tools/list", map[string]any{}), "result", "tools").([]any) {
		m := tl.(map[string]any)
		name := m["name"].(string)
		if strings.Contains(name, "send") {
			t.Fatalf("MCP must not expose sending tools: %s", name)
		}
		tools[name] = m
	}
	if dig(tools["get_site_content"], "annotations", "readOnlyHint") != true || tools["update_site_content"] == nil ||
		dig(tools["update_site_content"], "annotations") != nil {
		t.Fatalf("site content tools: %v %v", tools["get_site_content"], tools["update_site_content"])
	}

	got, text, isErr := toolResult(rpc, 3, "get_site_content", map[string]any{"sections": []string{"hero"}})
	secs, _ := got["content"].(map[string]any)
	if isErr || len(secs) != 1 || dig(secs, "hero", "metrics", 0, "value") != "{years}+ years" ||
		dig(got, "placeholders", "{years}") != "identity.experience_years" || got["updated_at"] != nil || len(got["sections"].([]any)) != 14 {
		t.Fatalf("get_site_content: %s", text)
	}
	if _, _, isErr := toolResult(rpc, 4, "get_site_content", map[string]any{"sections": []string{"heroes"}}); !isErr {
		t.Fatal("unknown section accepted")
	}

	// Dry run validates and reports, without saving.
	dry, text, isErr := toolResult(rpc, 5, "update_site_content", map[string]any{"sections": map[string]any{"hero": map[string]any{"title": "New"}}, "dry_run": true})
	if isErr || !slices.Equal(dry["changed_sections"].([]any), []any{"hero"}) || dry["dry_run"] != true || dig(dry, "content", "hero", "title") != "New" {
		t.Fatalf("dry run: %s", text)
	}
	if e.Count(`SELECT count(*) FROM app_settings WHERE key='site_content'`) != 0 {
		t.Fatal("dry run saved")
	}

	// A partial section keeps every other field of it.
	saved, text, isErr := toolResult(rpc, 6, "update_site_content", map[string]any{"sections": map[string]any{"hero": map[string]any{"title": "New"}}})
	if isErr || saved["updated_at"] == nil || saved["dry_run"] != false {
		t.Fatalf("update: %s", text)
	}
	d := siteDoc(t, e.Admin("GET", "/api/admin/site-content", nil))
	h, def := d.Content.Hero, sitecontent.Default().Hero
	if h.Title != "New" || h.Lead != def.Lead || h.PrimaryCTALabel != def.PrimaryCTALabel || h.ResumeCTALabel != def.ResumeCTALabel ||
		!h.AvailabilityVisible || len(h.Metrics) != len(def.Metrics) || h.Metrics[0] != def.Metrics[0] {
		t.Fatalf("hero after a partial update = %+v", h)
	}
	if *d.UpdatedBy != "mcp:"+testenv.AdminEmail ||
		e.Str(`SELECT actor || ' ' || (details->>'via') FROM email_audit_logs WHERE action='site_content.update'`) != "mcp:"+testenv.AdminEmail+" mcp" ||
		e.Count(`SELECT count(*) FROM content_versions WHERE entity_type='site_content' AND actor=$1`, "mcp:"+testenv.AdminEmail) != 1 {
		t.Fatalf("not audited or versioned as the MCP actor: %v", *d.UpdatedBy)
	}

	for _, c := range []struct {
		args map[string]any
		want string
	}{
		{map[string]any{"sections": map[string]any{"organizations": map[string]any{"items": []any{map[string]any{"name": "Acme", "logo_url": "https://cdn.example/l.png"}}}}},
			"organizations.items[0].logo_url"},
		{map[string]any{"sections": map[string]any{"hero": map[string]any{"title": "X"}}, "publish": true}, "unknown field"},
		{map[string]any{"sections": map[string]any{"hero": map[string]any{"title": "X"}}, "base_updated_at": "2020-01-01T00:00:00Z"}, "changed since"},
		{map[string]any{"sections": map[string]any{"hero": map[string]any{"title": "X"}}, "base_updated_at": nil}, "changed since"},
		{map[string]any{}, "reset_sections"},
	} {
		if _, text, isErr := toolResult(rpc, 7, "update_site_content", c.args); !isErr || !strings.Contains(text, c.want) {
			t.Errorf("%v: %v %s", c.args, isErr, text)
		}
	}
	if _, text, isErr := toolResult(rpc, 8, "update_site_content", map[string]any{"sections": map[string]any{"hero": map[string]any{"eyebrow": "Y"}},
		"base_updated_at": saved["updated_at"]}); isErr {
		t.Fatalf("update with the current base: %s", text)
	}

	reset, text, isErr := toolResult(rpc, 9, "update_site_content", map[string]any{"reset_sections": []string{"hero"}})
	if isErr || !slices.Equal(reset["changed_sections"].([]any), []any{"hero"}) || dig(reset, "content", "hero", "title") != def.Title {
		t.Fatalf("reset: %s", text)
	}

	// Legal pages are the owner's alone.
	privacy := e.Str(`SELECT id::text FROM cms_pages WHERE kind='page' AND slug='privacy'`)
	for _, tool := range []string{"update_page", "unpublish_page"} {
		_, text, isErr := toolResult(rpc, 10, tool, map[string]any{"id": privacy, "title": "Changed"})
		if !isErr || !strings.Contains(text, "Legal pages") || !strings.Contains(text, "admin") {
			t.Fatalf("%s on the privacy page: %v %s", tool, isErr, text)
		}
	}
	if e.Str(`SELECT title || ' ' || status FROM cms_pages WHERE id=$1`, privacy) != "Privacy notice published" {
		t.Fatal("privacy page changed through MCP")
	}

	// The default byline follows the site profile; an unchanged update is not audited.
	if _, text, isErr := toolResult(rpc, 11, "update_site_content", map[string]any{"sections": map[string]any{"identity": map[string]any{"name": "Test Owner"}}}); isErr {
		t.Fatalf("rename: %s", text)
	}
	created, text, isErr := toolResult(rpc, 12, "create_page", map[string]any{"kind": "article", "title": "Byline check"})
	if isErr {
		t.Fatalf("create_page: %s", text)
	}
	id := dig(created, "page", "id").(string)
	if e.Str(`SELECT author_name FROM cms_pages WHERE id=$1`, id) != "Test Owner" {
		t.Fatal("default author is not identity.name")
	}
	if _, text, isErr := toolResult(rpc, 13, "update_page", map[string]any{"id": id, "title": "Byline check"}); isErr {
		t.Fatalf("no-op update_page: %s", text)
	}
	if _, text, isErr := toolResult(rpc, 14, "update_page", map[string]any{"id": id, "title": "Byline checked"}); isErr {
		t.Fatalf("update_page: %s", text)
	}
	if got := e.Str(`SELECT string_agg(details->>'changed', ' ') FROM email_audit_logs WHERE action='page.update' AND entity_id=$1`, id); got != `["title"]` {
		t.Fatalf("page.update audit = %s", got)
	}
}

// A request that reaches the MCP handler without an identity is refused.
func TestMCPRequiresIdentity(t *testing.T) {
	e := testenv.New(t)
	body, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/call",
		"params": map[string]any{"name": "get_site_content", "arguments": map[string]any{}}})
	rec := httptest.NewRecorder()
	e.Server.MCP.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/mcp", bytes.NewReader(body)))
	var resp map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &resp)
	if dig(resp, "result", "isError") != true || dig(resp, "result", "content", 0, "text") != "not authorised" {
		t.Fatalf("anonymous tool call: %s", rec.Body)
	}
}
