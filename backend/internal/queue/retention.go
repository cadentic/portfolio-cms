package queue

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"
)

// PurgeBodies blanks the rendered body of finished jobs older than the
// retention window. Metadata (recipient, status, attempts) is kept for the
// audit trail; the content itself is personal data we no longer need.
func PurgeBodies(ctx context.Context, db *pgxpool.Pool, days int) (int64, error) {
	if days <= 0 {
		return 0, nil
	}
	tag, err := db.Exec(ctx, `UPDATE email_jobs SET html_body = '', text_body = '', updated_at = now()
		WHERE finished_at < now() - make_interval(days => $1) AND html_body <> ''`, days)
	return tag.RowsAffected(), err
}
