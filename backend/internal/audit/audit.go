// Package audit writes the append-only email_audit_logs trail for admin actions.
package audit

import (
	"context"
	"encoding/json"
	"net"
	"time"

	"github.com/cadentic/portfolio-cms/backend/internal/store"
)

type Entry struct {
	ID         string         `json:"id"`
	Actor      string         `json:"actor"`
	Action     string         `json:"action"`
	EntityType string         `json:"entity_type"`
	EntityID   string         `json:"entity_id"`
	Details    map[string]any `json:"details"`
	CreatedAt  time.Time      `json:"created_at"`
}

func Log(ctx context.Context, db store.DB, actor, action, entityType, entityID string, details map[string]any, ip string) error {
	if details == nil {
		details = map[string]any{}
	}
	b, _ := json.Marshal(details)
	var ipArg any
	if net.ParseIP(ip) != nil {
		ipArg = ip
	}
	_, err := db.Exec(ctx, `INSERT INTO email_audit_logs (actor, action, entity_type, entity_id, details, ip)
		VALUES ($1,$2,$3,$4,$5,$6)`, actor, action, entityType, entityID, b, ipArg)
	return err
}

func List(ctx context.Context, db store.DB, entityType string, limit, offset int) ([]Entry, error) {
	rows, err := db.Query(ctx, `SELECT id, actor, action, entity_type, coalesce(entity_id,''), details, created_at
		FROM email_audit_logs WHERE ($1 = '' OR entity_type = $1) ORDER BY created_at DESC LIMIT $2 OFFSET $3`, entityType, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Entry{}
	for rows.Next() {
		var e Entry
		var d []byte
		if err := rows.Scan(&e.ID, &e.Actor, &e.Action, &e.EntityType, &e.EntityID, &d, &e.CreatedAt); err != nil {
			return nil, err
		}
		_ = json.Unmarshal(d, &e.Details)
		out = append(out, e)
	}
	return out, rows.Err()
}
