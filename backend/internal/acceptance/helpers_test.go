package acceptance

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/url"
	"reflect"
	"strings"
	"testing"

	"github.com/cadentic/portfolio-cms/backend/internal/queue"
	"github.com/cadentic/portfolio-cms/backend/internal/testenv"
)

type testenvWorker struct{ *queue.Worker }

// mcpClient connects an MCP client as the owner (dynamic registration, PKCE
// code approved through the admin, token exchange) and returns a JSON-RPC
// caller. TestMCP_OAuthLoginAndCreatePage covers the flow's failure cases.
func mcpClient(t *testing.T, e *testenv.Env) func(id int, method string, params any) map[string]any {
	t.Helper()
	const redirect = "https://client.example/callback"
	reg := e.Do("POST", "/oauth/register", map[string]any{"client_name": "Test client", "redirect_uris": []string{redirect}}, nil)
	if reg.Status != 201 {
		t.Fatalf("register: %d %s", reg.Status, reg.Body)
	}
	clientID := reg.Map()["client_id"].(string)
	verifier := strings.Repeat("v", 50)
	sum := sha256.Sum256([]byte(verifier))
	ap := e.Admin("POST", "/api/oauth/approve", map[string]string{"client_id": clientID, "redirect_uri": redirect,
		"response_type": "code", "code_challenge": base64.RawURLEncoding.EncodeToString(sum[:]), "code_challenge_method": "S256",
		"state": "s", "resource": testenv.SiteURL + "/mcp"})
	if ap.Status != 200 {
		t.Fatalf("approve: %d %s", ap.Status, ap.Body)
	}
	cb, _ := url.Parse(ap.Map()["redirect"].(string))
	tok := e.Do("POST", "/oauth/token", url.Values{"grant_type": {"authorization_code"}, "code": {cb.Query().Get("code")},
		"client_id": {clientID}, "redirect_uri": {redirect}, "code_verifier": {verifier}}.Encode(),
		map[string]string{"Content-Type": "application/x-www-form-urlencoded"})
	if tok.Status != 200 {
		t.Fatalf("token: %d %s", tok.Status, tok.Body)
	}
	access := tok.Map()["access_token"].(string)
	return func(id int, method string, params any) map[string]any {
		t.Helper()
		body, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": id, "method": method, "params": params})
		r := e.Do("POST", "/mcp", body, map[string]string{"Authorization": "Bearer " + access, "Accept": "application/json, text/event-stream"})
		if r.Status != 200 {
			t.Fatalf("%s: %d %s", method, r.Status, r.Body)
		}
		return r.Map()
	}
}

// toolResult calls an MCP tool and returns its result, its text and whether
// it is a tool error.
func toolResult(rpc func(int, string, any) map[string]any, id int, name string, args any) (map[string]any, string, bool) {
	res, _ := rpc(id, "tools/call", map[string]any{"name": name, "arguments": args})["result"].(map[string]any)
	text := ""
	if c, ok := res["content"].([]any); ok && len(c) > 0 {
		text, _ = c[0].(map[string]any)["text"].(string)
	}
	sc, _ := res["structuredContent"].(map[string]any)
	return sc, text, res["isError"] == true
}

// jsonEqual compares two JSON documents as values.
func jsonEqual(t *testing.T, a, b []byte) bool {
	t.Helper()
	var x, y any
	if err := json.Unmarshal(a, &x); err != nil {
		t.Fatalf("decode %s: %v", a, err)
	}
	if err := json.Unmarshal(b, &y); err != nil {
		t.Fatalf("decode %s: %v", b, err)
	}
	return reflect.DeepEqual(x, y)
}

// dig walks decoded JSON by object keys and list indexes.
func dig(v any, path ...any) any {
	for _, p := range path {
		switch k := p.(type) {
		case string:
			m, _ := v.(map[string]any)
			v = m[k]
		case int:
			l, _ := v.([]any)
			if k >= len(l) {
				return nil
			}
			v = l[k]
		}
	}
	return v
}
