// Package retention deletes or scrubs personal data once the periods stated in
// the privacy notice (/privacy) have passed. Each rule is a single bounded
// statement so a run never holds long locks.
package retention

import (
	"context"
	"fmt"

	"github.com/cadentic/portfolio-cms/backend/internal/store"
)

type rule struct {
	name string
	sql  string
}

// Keep these periods in sync with the retention table in the privacy notice.
var rules = []rule{
	{"resume_otps", `DELETE FROM resume_otps WHERE created_at < now() - interval '30 days'`},
	{"contact_messages", `DELETE FROM contact_messages WHERE created_at < now() - interval '24 months'`},
	{"contact_message_ips", `UPDATE contact_messages SET ip = NULL WHERE ip IS NOT NULL AND created_at < now() - interval '30 days'`},
	{"provider_event_payloads", `UPDATE email_provider_events SET raw_event = '', payload = '{}'::jsonb
		WHERE processed_at IS NOT NULL AND raw_event <> '' AND received_at < now() - interval '90 days'`},
	{"import_batches", `DELETE FROM subscriber_import_batches WHERE expires_at < now() - interval '1 day'`},
	{"oauth_codes", `DELETE FROM oauth_codes WHERE expires_at < now() - interval '1 day'`},
	{"oauth_tokens", `DELETE FROM oauth_tokens WHERE expires_at < now() - interval '30 days'
		OR (revoked_at IS NOT NULL AND revoked_at < now() - interval '30 days')`},
}

// Purge applies every rule and returns the number of rows touched per rule.
func Purge(ctx context.Context, db store.DB) (map[string]int64, error) {
	out := make(map[string]int64, len(rules))
	for _, r := range rules {
		tag, err := db.Exec(ctx, r.sql)
		if err != nil {
			return out, fmt.Errorf("retention %s: %w", r.name, err)
		}
		out[r.name] = tag.RowsAffected()
	}
	return out, nil
}
