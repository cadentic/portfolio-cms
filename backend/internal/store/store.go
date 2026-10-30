// Package store owns the PostgreSQL (Neon) connection pool and schema migrations.
package store

import (
	"context"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

//go:embed migrations/*.sql
var migrationFS embed.FS

// Advisory lock IDs. A session lock and a transaction lock on the same ID block
// each other, so every use has its own.
const (
	LockMigrate     int64 = 7262411001 // Migrate, session lock
	LockProviders   int64 = 7262411002 // provider seeding at API start, session lock
	LockResume      int64 = 7262411003 // resume upload and delete
	LockSiteContent int64 = 7262411004 // site content saves
)

// Open creates a pgx pool. Neon requires sslmode=require in the URL; the pool
// keeps connections short-lived so that Neon's autosuspend does not leave
// dead sockets behind.
func Open(ctx context.Context, url string, maxConns int32) (*pgxpool.Pool, error) {
	cfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		return nil, fmt.Errorf("parse DATABASE_URL: %w", err)
	}
	if maxConns > 0 {
		cfg.MaxConns = maxConns
	}
	cfg.MaxConnIdleTime = 2 * time.Minute
	cfg.MaxConnLifetime = 30 * time.Minute
	cfg.HealthCheckPeriod = 30 * time.Second
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, err
	}
	pingCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	if err := pool.Ping(pingCtx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping database: %w", err)
	}
	return pool, nil
}

// Migrate applies embedded migrations in lexical order inside a single
// advisory lock so concurrent API/worker start-ups do not race.
func Migrate(ctx context.Context, pool *pgxpool.Pool, log *slog.Logger) error {
	conn, err := pool.Acquire(ctx)
	if err != nil {
		return err
	}
	defer conn.Release()

	if _, err := conn.Exec(ctx, `SELECT pg_advisory_lock($1)`, LockMigrate); err != nil {
		return err
	}
	defer conn.Exec(context.Background(), `SELECT pg_advisory_unlock($1)`, LockMigrate) //nolint:errcheck

	if _, err := conn.Exec(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (
		version text PRIMARY KEY, applied_at timestamptz NOT NULL DEFAULT now())`); err != nil {
		return err
	}
	names, err := migrationNames()
	if err != nil {
		return err
	}
	for _, name := range names {
		version := strings.TrimSuffix(name, ".sql")
		var exists bool
		if err := conn.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM schema_migrations WHERE version=$1)`, version).Scan(&exists); err != nil {
			return err
		}
		if exists {
			continue
		}
		sqlBytes, err := migrationFS.ReadFile("migrations/" + name)
		if err != nil {
			return err
		}
		tx, err := conn.Begin(ctx)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, string(sqlBytes)); err != nil {
			tx.Rollback(ctx) //nolint:errcheck
			return fmt.Errorf("migration %s: %w", name, err)
		}
		if _, err := tx.Exec(ctx, `INSERT INTO schema_migrations (version) VALUES ($1)`, version); err != nil {
			tx.Rollback(ctx) //nolint:errcheck
			return err
		}
		if err := tx.Commit(ctx); err != nil {
			return err
		}
		log.Info("migration applied", "version", version)
	}
	return nil
}

func migrationNames() ([]string, error) {
	entries, err := fs.ReadDir(migrationFS, "migrations")
	if err != nil {
		return nil, err
	}
	var names []string
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".sql") {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	return names, nil
}

// IsReadOnly reports whether new sessions default to read-only transactions.
// tools/cloudmigrate sets that on the database while it copies it to another
// host, so the old deployment keeps serving pages but takes no writes.
func IsReadOnly(ctx context.Context, pool *pgxpool.Pool) (bool, error) {
	var v string
	if err := pool.QueryRow(ctx, `SHOW default_transaction_read_only`).Scan(&v); err != nil {
		return false, err
	}
	return v == "on", nil
}

// CheckMigrated returns an error unless every embedded migration has been
// applied. It only reads, so it works on a read-only database.
func CheckMigrated(ctx context.Context, pool *pgxpool.Pool) error {
	names, err := migrationNames()
	if err != nil {
		return err
	}
	var applied []string
	if err := pool.QueryRow(ctx, `SELECT coalesce(array_agg(version), '{}') FROM schema_migrations`).Scan(&applied); err != nil {
		return fmt.Errorf("read schema_migrations: %w", err)
	}
	done := map[string]bool{}
	for _, v := range applied {
		done[v] = true
	}
	var pending []string
	for _, name := range names {
		if v := strings.TrimSuffix(name, ".sql"); !done[v] {
			pending = append(pending, v)
		}
	}
	if len(pending) > 0 {
		return fmt.Errorf("migrations not applied: %s", strings.Join(pending, ", "))
	}
	return nil
}

// InTx runs fn inside a transaction, committing on success.
func InTx(ctx context.Context, pool *pgxpool.Pool, fn func(pgx.Tx) error) error {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return err
	}
	if err := fn(tx); err != nil {
		tx.Rollback(ctx) //nolint:errcheck
		return err
	}
	return tx.Commit(ctx)
}

// IsUniqueViolation reports whether err is a PostgreSQL unique-constraint error.
func IsUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}

// DB is satisfied by *pgxpool.Pool and pgx.Tx.
type DB interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}
