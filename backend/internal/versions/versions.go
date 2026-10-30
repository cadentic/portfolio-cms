// Package versions keeps full snapshots of owner-edited content (site content
// and the protected legal pages) in content_versions, so any earlier state
// can be listed and restored.
package versions

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/cadentic/portfolio-cms/backend/internal/store"
)

type Version struct {
	ID        int64           `json:"id"`
	Action    string          `json:"action"`
	Actor     string          `json:"actor"`
	Changed   []string        `json:"changed"`
	SHA256    string          `json:"sha256"`
	CreatedAt time.Time       `json:"created_at"`
	Snapshot  json.RawMessage `json:"snapshot,omitempty"`
}

var ErrNotFound = errors.New("version not found")

// Record inserts a snapshot; sha256 is computed in SQL as encode(sha256(convert_to($snapshot::jsonb::text,'UTF8')),'hex')
// so that it matches rows seeded by migrations. keep > 0 prunes older rows of the same entity.
func Record(ctx context.Context, q store.DB, entityType, entityID, action string, changed []string, snapshot []byte, actor string, keep int) (int64, error) {
	if changed == nil {
		changed = []string{}
	}
	var id int64
	err := q.QueryRow(ctx, `INSERT INTO content_versions (entity_type, entity_id, action, changed, snapshot, sha256, actor)
		VALUES ($1, $2, $3, $4, $5::jsonb, encode(sha256(convert_to($5::jsonb::text, 'UTF8')), 'hex'), $6)
		RETURNING id`, entityType, entityID, action, changed, snapshot, actor).Scan(&id)
	if err != nil {
		return 0, err
	}
	if keep > 0 {
		// Everything at or below the (keep+1)th newest id goes.
		if _, err := q.Exec(ctx, `DELETE FROM content_versions WHERE entity_type=$1 AND entity_id=$2 AND id <= (
			SELECT id FROM content_versions WHERE entity_type=$1 AND entity_id=$2 ORDER BY id DESC OFFSET $3 LIMIT 1)`,
			entityType, entityID, keep); err != nil {
			return 0, err
		}
	}
	return id, nil
}

const cols = `id, action, actor, changed, sha256, created_at`

// List returns the newest versions first, without snapshots. limit is
// clamped to 1-200 (50 when not positive).
func List(ctx context.Context, q store.DB, entityType, entityID string, limit int) ([]Version, error) {
	if limit <= 0 {
		limit = 50
	}
	limit = min(limit, 200)
	rows, err := q.Query(ctx, `SELECT `+cols+` FROM content_versions
		WHERE entity_type=$1 AND entity_id=$2 ORDER BY id DESC LIMIT $3`, entityType, entityID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Version{}
	for rows.Next() {
		var v Version
		if err := rows.Scan(&v.ID, &v.Action, &v.Actor, &v.Changed, &v.SHA256, &v.CreatedAt); err != nil {
			return nil, err
		}
		if v.Changed == nil {
			v.Changed = []string{}
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// Get returns one version of the entity, with its snapshot.
func Get(ctx context.Context, q store.DB, entityType, entityID string, id int64) (Version, error) {
	var v Version
	var snap []byte
	err := q.QueryRow(ctx, `SELECT `+cols+`, snapshot FROM content_versions
		WHERE entity_type=$1 AND entity_id=$2 AND id=$3`, entityType, entityID, id).
		Scan(&v.ID, &v.Action, &v.Actor, &v.Changed, &v.SHA256, &v.CreatedAt, &snap)
	if errors.Is(err, pgx.ErrNoRows) {
		return Version{}, ErrNotFound
	}
	if err != nil {
		return Version{}, err
	}
	if v.Changed == nil {
		v.Changed = []string{}
	}
	v.Snapshot = snap
	return v, nil
}
