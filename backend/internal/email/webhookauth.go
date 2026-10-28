package email

import (
	"crypto/sha256"
	"crypto/subtle"
	"net/http"
	"strings"
)

// VerifySharedSecret authenticates a webhook using the mechanisms Brevo and
// Mailjet officially support: HTTP Basic credentials embedded in the webhook
// URL (https://user:pass@host/...) or a bearer token header.
//
// secret is either "user:password" (Basic) or a bare token (Bearer, or Basic
// password with any username). Comparison is constant-time.
func VerifySharedSecret(r *http.Request, secret string) error {
	if secret == "" {
		// Refuse unauthenticated webhooks rather than accepting anything.
		return ErrWebhookUnauthorized
	}
	if h := r.Header.Get("Authorization"); strings.HasPrefix(strings.ToLower(h), "bearer ") {
		if constEq(strings.TrimSpace(h[7:]), secret) {
			return nil
		}
		return ErrWebhookUnauthorized
	}
	if user, pass, ok := r.BasicAuth(); ok {
		if strings.Contains(secret, ":") {
			if constEq(user+":"+pass, secret) {
				return nil
			}
		} else if constEq(pass, secret) {
			return nil
		}
	}
	return ErrWebhookUnauthorized
}

func constEq(a, b string) bool {
	ha := sha256.Sum256([]byte(a))
	hb := sha256.Sum256([]byte(b))
	return subtle.ConstantTimeCompare(ha[:], hb[:]) == 1
}
