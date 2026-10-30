package versions_test

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/url"
	"os"
	"slices"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/cadentic/portfolio-cms/backend/internal/store"
	"github.com/cadentic/portfolio-cms/backend/internal/versions"
)

// newDB creates a migrated throwaway database.
func newDB(t *testing.T) *pgxpool.Pool {
	t.Helper()
	base := os.Getenv("TEST_DATABASE_URL")
	if base == "" {
		t.Skip("TEST_DATABASE_URL not set; skipping database-backed test")
	}
	ctx := context.Background()
	admin, err := pgx.Connect(ctx, base)
	if err != nil {
		t.Fatal(err)
	}
	b := make([]byte, 6)
	_, _ = rand.Read(b)
	name := "pcms_versions_" + hex.EncodeToString(b)
	if _, err := admin.Exec(ctx, "CREATE DATABASE "+name); err != nil {
		t.Fatal(err)
	}
	u, _ := url.Parse(base)
	u.Path = "/" + name
	pool, err := store.Open(ctx, u.String(), 4)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		pool.Close()
		_, _ = admin.Exec(context.Background(), "DROP DATABASE IF EXISTS "+name+" WITH (FORCE)")
		_ = admin.Close(context.Background())
	})
	if err := store.Migrate(ctx, pool, slog.New(slog.NewTextHandler(io.Discard, nil))); err != nil {
		t.Fatal(err)
	}
	return pool
}

func TestRecordListGet(t *testing.T) {
	db := newDB(t)
	ctx := context.Background()

	var ids []int64
	for i, snap := range []string{`{"b": 1, "a": "x"}`, `{"n": 2}`, `{"n": 3}`, `{"n": 4}`} {
		changed := []string{"hero"}
		if i == 0 {
			changed = nil
		}
		id, err := versions.Record(ctx, db, "site_content", "site_content", "update", changed, []byte(snap), "owner@example.com", 0)
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, id)
	}
	if _, err := versions.Record(ctx, db, "page", "p1", "seed", []string{"title"}, []byte(`{"title":"Privacy"}`), "migration", 0); err != nil {
		t.Fatal(err)
	}

	list, err := versions.List(ctx, db, "site_content", "site_content", 50)
	if err != nil {
		t.Fatal(err)
	}
	var got []int64
	for _, v := range list {
		got = append(got, v.ID)
		if v.Snapshot != nil || v.Changed == nil || v.Actor != "owner@example.com" || v.Action != "update" || v.CreatedAt.IsZero() {
			t.Errorf("list row %+v", v)
		}
	}
	slices.Reverse(ids)
	if !slices.Equal(got, ids) {
		t.Errorf("List ids = %v, want newest first %v", got, ids)
	}
	b, _ := json.Marshal(list[0])
	var m map[string]any
	_ = json.Unmarshal(b, &m)
	if _, ok := m["snapshot"]; ok {
		t.Error("List rows carry a snapshot")
	}
	if l, _ := versions.List(ctx, db, "site_content", "site_content", 2); len(l) != 2 {
		t.Errorf("limit 2 gave %d", len(l))
	}

	first, err := versions.Get(ctx, db, "site_content", "site_content", ids[len(ids)-1])
	if err != nil {
		t.Fatal(err)
	}
	if first.Changed == nil || len(first.Changed) != 0 {
		t.Errorf("nil changed = %#v", first.Changed)
	}
	var snap map[string]any
	if err := json.Unmarshal(first.Snapshot, &snap); err != nil || snap["a"] != "x" {
		t.Errorf("snapshot %s: %v", first.Snapshot, err)
	}
	// The hash is over the jsonb text, the same as migrations compute it.
	sum := sha256.Sum256(first.Snapshot)
	if first.SHA256 != hex.EncodeToString(sum[:]) {
		t.Errorf("sha256 %s does not hash the snapshot %s", first.SHA256, first.Snapshot)
	}
	var sqlSum string
	if err := db.QueryRow(ctx, `SELECT encode(sha256(convert_to(snapshot::text, 'UTF8')), 'hex') FROM content_versions WHERE id=$1`, first.ID).Scan(&sqlSum); err != nil || sqlSum != first.SHA256 {
		t.Errorf("sql sha256 %s, %v", sqlSum, err)
	}

	if _, err := versions.Get(ctx, db, "page", "p1", first.ID); !errors.Is(err, versions.ErrNotFound) {
		t.Errorf("other entity: %v", err)
	}
	if _, err := versions.Get(ctx, db, "site_content", "site_content", 1<<40); !errors.Is(err, versions.ErrNotFound) {
		t.Errorf("unknown id: %v", err)
	}
	if _, err := versions.Record(ctx, db, "site_content", "site_content", "delete", nil, []byte(`{}`), "x", 0); err == nil {
		t.Error("unknown action accepted")
	}
	if _, err := versions.Record(ctx, db, "site_content", "site_content", "update", nil, []byte(`not json`), "x", 0); err == nil {
		t.Error("invalid snapshot accepted")
	}

	// keep prunes the oldest rows of the same entity only.
	newest, err := versions.Record(ctx, db, "site_content", "site_content", "restore", []string{"seo"}, []byte(`{"n": 5}`), "x", 2)
	if err != nil {
		t.Fatal(err)
	}
	list, _ = versions.List(ctx, db, "site_content", "site_content", 50)
	if len(list) != 2 || list[0].ID != newest || list[1].ID != ids[0] {
		t.Errorf("after keep=2: %+v", list)
	}
	if l, _ := versions.List(ctx, db, "page", "p1", 50); len(l) != 1 {
		t.Errorf("pruned another entity: %d rows", len(l))
	}
}
