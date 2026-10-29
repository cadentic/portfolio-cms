package store_test

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"io"
	"log/slog"
	"net/url"
	"os"
	"testing"

	"github.com/cadentic/portfolio-cms/backend/internal/store"
	"github.com/jackc/pgx/v5"
)

// A database frozen for a move (default_transaction_read_only = on) still
// lets the API start as long as every migration is applied.
func TestReadOnlyDatabase(t *testing.T) {
	base := os.Getenv("TEST_DATABASE_URL")
	if base == "" {
		t.Skip("TEST_DATABASE_URL not set; skipping database-backed test")
	}
	ctx := context.Background()
	admin, err := pgx.Connect(ctx, base)
	if err != nil {
		t.Fatal(err)
	}
	// A cleanup, not a defer: the DROP DATABASE cleanups below run after the
	// test returns and need this connection, and cleanups run last-in first-out.
	t.Cleanup(func() { _ = admin.Close(context.Background()) })

	open := func(name string) string {
		b := make([]byte, 6)
		_, _ = rand.Read(b)
		db := name + hex.EncodeToString(b)
		if _, err := admin.Exec(ctx, "CREATE DATABASE "+db); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _, _ = admin.Exec(context.Background(), "DROP DATABASE IF EXISTS "+db+" WITH (FORCE)") })
		u, _ := url.Parse(base)
		u.Path = "/" + db
		return u.String()
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))

	migrated := open("pcms_ro_")
	pool, err := store.Open(ctx, migrated, 2)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Migrate(ctx, pool, log); err != nil {
		t.Fatal(err)
	}
	if ro, err := store.IsReadOnly(ctx, pool); err != nil || ro {
		t.Fatalf("fresh database read-only = %v, %v", ro, err)
	}
	pool.Close()
	freeze := func(dsn string) {
		u, _ := url.Parse(dsn)
		if _, err := admin.Exec(ctx, "ALTER DATABASE "+u.Path[1:]+" SET default_transaction_read_only = on"); err != nil {
			t.Fatal(err)
		}
	}
	freeze(migrated)
	pool, err = store.Open(ctx, migrated, 2)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	if ro, err := store.IsReadOnly(ctx, pool); err != nil || !ro {
		t.Fatalf("frozen database read-only = %v, %v", ro, err)
	}
	if err := store.CheckMigrated(ctx, pool); err != nil {
		t.Fatalf("CheckMigrated: %v", err)
	}

	empty := open("pcms_ro_empty_")
	freeze(empty)
	pool2, err := store.Open(ctx, empty, 2)
	if err != nil {
		t.Fatal(err)
	}
	defer pool2.Close()
	if err := store.CheckMigrated(ctx, pool2); err == nil {
		t.Fatal("CheckMigrated on an empty database should fail")
	}
}
