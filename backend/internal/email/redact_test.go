package email

import (
	"strings"
	"testing"
)

func TestRedaction(t *testing.T) {
	s := RedactString(`api-key: xkeysib-abcdef0123456789 Authorization: Basic dXNlcjpwYXNz token=sk_live_123456789012`)
	for _, leak := range []string{"xkeysib-abcdef0123456789", "dXNlcjpwYXNz"} {
		if strings.Contains(s, leak) {
			t.Fatalf("secret %q survived: %s", leak, s)
		}
	}
	if got := MaskEmail("reader@example.com"); got == "reader@example.com" || !strings.Contains(got, "@example.com") {
		t.Fatalf("MaskEmail = %s", got)
	}
	m := RedactMap(map[string]any{"api_key": "secret-value", "nested": map[string]any{"password": "p"}, "event": "delivered"})
	if m["api_key"] == "secret-value" || m["nested"].(map[string]any)["password"] == "p" || m["event"] != "delivered" {
		t.Fatalf("RedactMap = %v", m)
	}
}
