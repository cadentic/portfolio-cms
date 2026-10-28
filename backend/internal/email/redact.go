package email

import (
	"encoding/json"
	"regexp"
	"strings"
)

var sensitiveKey = regexp.MustCompile(`(?i)(api[-_]?key|secret|token|password|authorization|auth|credential|signature)`)

var (
	emailRe  = regexp.MustCompile(`([A-Za-z0-9._%+\-])[A-Za-z0-9._%+\-]*@([A-Za-z0-9.\-]+\.[A-Za-z]{2,})`)
	bearerRe = regexp.MustCompile(`(?i)(bearer|basic)\s+[A-Za-z0-9._~+/=\-]+`)
	keyRe    = regexp.MustCompile(`(?i)\b(xkeysib-|xsmtpsib-)[A-Za-z0-9\-]+`)
)

// RedactString masks credentials and email local-parts in free text (log lines,
// provider error messages).
func RedactString(s string) string {
	s = keyRe.ReplaceAllString(s, "$1[REDACTED]")
	s = bearerRe.ReplaceAllString(s, "$1 [REDACTED]")
	s = emailRe.ReplaceAllString(s, "$1***@$2")
	return s
}

// MaskEmail returns a privacy-preserving rendering of an address: j***@example.com.
func MaskEmail(e string) string {
	at := strings.LastIndex(e, "@")
	if at <= 0 {
		return "***"
	}
	return e[:1] + "***" + e[at:]
}

// RedactJSON parses a JSON document and removes credential-like fields. Non-JSON
// input is returned as a truncated, redacted string.
func RedactJSON(b []byte) map[string]any {
	if len(b) == 0 {
		return nil
	}
	var v any
	if err := json.Unmarshal(b, &v); err != nil {
		return map[string]any{"raw": truncate(RedactString(string(b)), 500)}
	}
	v = redactValue(v)
	if m, ok := v.(map[string]any); ok {
		return m
	}
	return map[string]any{"data": v}
}

// RedactMap redacts an already-decoded payload in place and returns it.
func RedactMap(m map[string]any) map[string]any {
	out, _ := redactValue(m).(map[string]any)
	return out
}

func redactValue(v any) any {
	switch t := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(t))
		for k, val := range t {
			if sensitiveKey.MatchString(k) {
				out[k] = "[REDACTED]"
				continue
			}
			out[k] = redactValue(val)
		}
		return out
	case []any:
		for i := range t {
			t[i] = redactValue(t[i])
		}
		return t
	case string:
		return RedactString(t)
	}
	return v
}
