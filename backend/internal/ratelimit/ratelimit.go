// Package ratelimit implements fixed-window counters in PostgreSQL so limits
// hold across every API instance (Cloud Run / Container Apps scale out).
package ratelimit

import (
	"context"
	"time"

	"github.com/cadentic/portfolio-cms/backend/internal/store"
)

type Limiter struct{ DB store.DB }

// Allow increments the counter for key in the current window and reports
// whether the caller is still within limit. Errors fail open for reads but
// callers on abuse-sensitive paths treat them as "not allowed".
func (l *Limiter) Allow(ctx context.Context, key string, limit int, window time.Duration) (bool, error) {
	start := time.Now().UTC().Truncate(window)
	var n int
	err := l.DB.QueryRow(ctx, `INSERT INTO rate_limit_counters (key, window_start, count) VALUES ($1,$2,1)
		ON CONFLICT (key, window_start) DO UPDATE SET count = rate_limit_counters.count + 1 RETURNING count`,
		key, start).Scan(&n)
	if err != nil {
		return false, err
	}
	return n <= limit, nil
}

type Rule struct {
	Limit  int
	Window time.Duration
}

// AllowAll checks several rules (e.g. per-IP and per-email) and only allows
// when all pass.
func (l *Limiter) AllowAll(ctx context.Context, checks map[string]Rule) (bool, error) {
	ok := true
	for k, r := range checks {
		allowed, err := l.Allow(ctx, k, r.Limit, r.Window)
		if err != nil {
			return false, err
		}
		ok = ok && allowed
	}
	return ok, nil
}

// Cleanup drops expired fixed-window counters.
func Cleanup(ctx context.Context, db store.DB) error {
	_, err := db.Exec(ctx, `DELETE FROM rate_limit_counters WHERE window_start < now() - interval '2 days'`)
	return err
}
