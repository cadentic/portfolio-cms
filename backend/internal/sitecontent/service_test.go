package sitecontent

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/url"
	"os"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/cadentic/portfolio-cms/backend/internal/settings"
	"github.com/cadentic/portfolio-cms/backend/internal/store"
	"github.com/cadentic/portfolio-cms/backend/internal/versions"
)

var quiet = slog.New(slog.NewTextHandler(io.Discard, nil))

// newDB creates a migrated throwaway database and returns its URL and a pool.
func newDB(t *testing.T) (string, *pgxpool.Pool) {
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
	name := "pcms_sitecontent_" + hex.EncodeToString(b)
	if _, err := admin.Exec(ctx, "CREATE DATABASE "+name); err != nil {
		t.Fatal(err)
	}
	u, _ := url.Parse(base)
	u.Path = "/" + name
	pool, err := store.Open(ctx, u.String(), 10)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		pool.Close()
		_, _ = admin.Exec(context.Background(), "DROP DATABASE IF EXISTS "+name+" WITH (FORCE)")
		_ = admin.Close(context.Background())
	})
	if err := store.Migrate(ctx, pool, quiet); err != nil {
		t.Fatal(err)
	}
	return u.String(), pool
}

func patch(t *testing.T, s string) map[string]json.RawMessage {
	t.Helper()
	return sections(t, s)
}

func addImage(t *testing.T, db *pgxpool.Pool, u string) {
	t.Helper()
	if _, err := db.Exec(context.Background(), `INSERT INTO cms_media (url, kind) VALUES ($1, 'image')`, u); err != nil {
		t.Fatal(err)
	}
}

func removeImage(t *testing.T, db *pgxpool.Pool, u string) {
	t.Helper()
	if _, err := db.Exec(context.Background(), `DELETE FROM cms_media WHERE url=$1`, u); err != nil {
		t.Fatal(err)
	}
}

func publicContent(t *testing.T, s *Service) Content {
	t.Helper()
	body, _, stale, err := s.Public(context.Background())
	if err != nil || stale {
		t.Fatalf("Public: stale=%v %v", stale, err)
	}
	var c Content
	if err := json.Unmarshal(body, &c); err != nil {
		t.Fatal(err)
	}
	return c
}

func TestSettingsRaw(t *testing.T) {
	_, db := newDB(t)
	ctx := context.Background()
	if _, _, found, err := settings.Raw(ctx, db, "nope"); found || err != nil {
		t.Fatalf("missing row: %v %v", found, err)
	}
	m1, err := settings.PutRaw(ctx, db, "x", []byte(`{"a":1}`), "one@example.com")
	if err != nil || m1.UpdatedBy != "one@example.com" || m1.UpdatedAt.IsZero() {
		t.Fatalf("PutRaw: %+v %v", m1, err)
	}
	raw, meta, found, err := settings.Raw(ctx, db, "x")
	if err != nil || !found || !meta.UpdatedAt.Equal(m1.UpdatedAt) || meta.UpdatedBy != "one@example.com" || !bytes.Contains(raw, []byte(`"a": 1`)) {
		t.Fatalf("Raw: %s %+v %v %v", raw, meta, found, err)
	}
	m2, err := settings.PutRaw(ctx, db, "x", []byte(`{"a":2}`), "two@example.com")
	if err != nil || m2.UpdatedBy != "two@example.com" || !m2.UpdatedAt.After(m1.UpdatedAt) {
		t.Fatalf("PutRaw update: %+v %v", m2, err)
	}
}

func TestServiceApply(t *testing.T) {
	_, db := newDB(t)
	ctx := context.Background()
	s := &Service{DB: db, Log: quiet}
	const owner = "owner@cadentic.dev"

	doc, err := s.Get(ctx)
	if err != nil || doc.UpdatedAt != nil || doc.UpdatedBy != nil || !reflect.DeepEqual(doc.Content, Default()) {
		t.Fatalf("empty Get: %+v %v", doc, err)
	}
	body, etag, stale, err := s.Public(ctx)
	if err != nil || stale {
		t.Fatal(err)
	}
	golden, _ := os.ReadFile("testdata/public.golden.json")
	var a, b any
	_ = json.Unmarshal(body, &a)
	_ = json.Unmarshal(golden, &b)
	if !reflect.DeepEqual(a, b) {
		t.Error("public body on an empty database differs from public.golden.json")
	}
	if len(etag) != 34 || etag[0] != '"' || etag[33] != '"' {
		t.Errorf("etag = %s", etag)
	}
	if _, etag2, _, _ := s.Public(ctx); etag2 != etag {
		t.Error("etag not stable")
	}

	// The owner's change: years and the featured video as a link.
	p := Patch{Check: true, Sections: patch(t, `{"identity":{"experience_years":12},"videos":{"featured":{"youtube_id":"https://youtu.be/gXT9izr0ljQ?si=XKDH-MSqA-o66lk3","title":"Featured video"}}}`)}
	res, err := s.Apply(ctx, p, owner, false)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(res.Changed, []string{"identity"}) || res.VersionID == 0 || res.Doc.UpdatedAt == nil || *res.Doc.UpdatedBy != owner ||
		res.Doc.Content.Videos.Featured.YouTubeID != "gXT9izr0ljQ" || res.Warnings == nil {
		t.Fatalf("Apply: %+v", res)
	}
	pub := publicContent(t, s)
	for _, got := range []string{pub.Hero.Metrics[0].Value, pub.Identity.Summary, pub.Hero.Lead, pub.FAQ.Items[1].A, pub.SEO.HomeDescription, pub.SEO.LLMsSummary} {
		if !strings.Contains(got, "12") || strings.Contains(got, "{years}") {
			t.Errorf("public text %q", got)
		}
	}
	if pc, err := s.PublicContent(ctx); err != nil || pc.Hero.Metrics[0].Value != "12+ years" {
		t.Errorf("PublicContent: %v %v", pc.Hero.Metrics[0].Value, err)
	}
	doc, _ = s.Get(ctx)
	if !strings.Contains(doc.Content.Hero.Lead, "{years}") || doc.Content.Identity.ExperienceYears != 12 || !doc.UpdatedAt.Equal(*res.Doc.UpdatedAt) {
		t.Errorf("admin doc: %q %d", doc.Content.Hero.Lead, doc.Content.Identity.ExperienceYears)
	}
	saved := *res.Doc.UpdatedAt

	// A save that changes nothing writes nothing.
	p.Base = &saved
	res, err = s.Apply(ctx, p, "someone@else", false)
	if err != nil || res.VersionID != 0 || len(res.Changed) != 0 || !res.Doc.UpdatedAt.Equal(saved) || *res.Doc.UpdatedBy != owner {
		t.Fatalf("no-op: %+v %v", res, err)
	}

	// Stale writes.
	var se *StaleError
	old := saved.Add(-time.Second)
	if _, err := s.Apply(ctx, Patch{Check: true, Base: &old, Sections: patch(t, `{"hero":{"title":"X"}}`)}, owner, false); !errors.As(err, &se) ||
		se.UpdatedBy == nil || *se.UpdatedBy != owner || !se.UpdatedAt.Equal(saved) {
		t.Errorf("stale base: %v", err)
	}
	if _, err := s.Apply(ctx, Patch{Check: true, Sections: patch(t, `{"hero":{"title":"X"}}`)}, owner, false); !errors.As(err, &se) {
		t.Errorf("null base with a row: %v", err)
	}

	// Validation.
	var ve *ValidationError
	for in, path := range map[string]string{
		`{"organizations":{"items":[{"name":"A","logo_url":"https://example.com/l.png"}]}}`:          "organizations.items[0].logo_url",
		`{"organizations":{"items":[{"name":"A","logo_url":"/media/uploads/2026/10/missing.png"}]}}`: "organizations.items[0].logo_url",
		`{"identity":{"linkedin_url":"http://x.com"}}`:                                               "identity.linkedin_url",
		`{"hero":{"lead":"{year}"}}`: "hero.lead",
		`{"hero":{"bar":1}}`:         "hero",
		`{"foo":{}}`:                 "",
	} {
		_, err := s.Apply(ctx, Patch{Sections: patch(t, in)}, owner, false)
		if !errors.As(err, &ve) || ve.Errors[0].Path != path {
			t.Errorf("%s: %v", in, err)
		}
	}
	if _, err := s.Apply(ctx, Patch{Sections: patch(t, `{"organizations":{"items":[{"name":"A","logo_url":"/media/uploads/2026/10/missing.png"}]}}`)}, owner, false); !errors.As(err, &ve) ||
		!strings.Contains(ve.Error(), "media library") {
		t.Errorf("missing media: %v", err)
	}
	// A blank row above is dropped on save, but error paths index the rows as sent.
	if _, err := s.Apply(ctx, Patch{Sections: patch(t, `{"organizations":{"items":[{"name":" "},{"name":"A","logo_url":" /media/uploads/2026/10/missing.png"}]}}`)},
		owner, false); !errors.As(err, &ve) || len(ve.Errors) != 1 || ve.Errors[0].Path != "organizations.items[1].logo_url" {
		t.Errorf("missing media after a blank row: %v", err)
	}
	if _, err := s.Apply(ctx, Patch{Reset: []string{"nope"}}, owner, false); !errors.As(err, &ve) {
		t.Errorf("unknown reset section: %v", err)
	}

	// Dry run reports the change and saves nothing.
	res, err = s.Apply(ctx, Patch{Sections: patch(t, `{"hero":{"title":"Dry"}}`)}, owner, true)
	if err != nil || !slices.Equal(res.Changed, []string{"hero"}) || res.VersionID != 0 || res.Doc.Content.Hero.Title != "Dry" {
		t.Fatalf("dry run: %+v %v", res, err)
	}
	if doc, _ := s.Get(ctx); doc.Content.Hero.Title == "Dry" || !doc.UpdatedAt.Equal(saved) {
		t.Error("dry run saved")
	}

	// Images: a new one must exist; once stored, its loss is a warning.
	photo, logo := "/media/uploads/2026/10/me.png", "/media/uploads/2026/10/acme.png"
	addImage(t, db, photo)
	addImage(t, db, logo)
	res, err = s.Apply(ctx, Patch{Sections: patch(t, `{"identity":{"photo_url":"`+photo+`"},"organizations":{"items":[{"name":"Acme","logo_url":"`+logo+`"}]}}`)}, owner, false)
	if err != nil || !slices.Equal(res.Changed, []string{"identity", "organizations"}) || len(res.Warnings) != 0 {
		t.Fatalf("images: %+v %v", res, err)
	}
	withLogo := res.VersionID
	removeImage(t, db, photo)
	res, err = s.Apply(ctx, Patch{Sections: patch(t, `{"hero":{"title":"Still saves"}}`)}, owner, false)
	if err != nil || len(res.Warnings) != 1 || res.Warnings[0].Path != "identity.photo_url" || res.Doc.Content.Identity.PhotoURL != photo {
		t.Fatalf("stored missing image blocked a save: %+v %v", res, err)
	}
	doc, _ = s.Get(ctx)
	if w, err := s.Warnings(ctx, doc.Content); err != nil || len(w) != 1 || w[0].Path != "identity.photo_url" {
		t.Errorf("Warnings = %v %v", w, err)
	}
	if pub := publicContent(t, s); pub.Identity.PhotoURL != "/images/sayantan.jpg" || pub.Organizations.Items[0].LogoURL != logo {
		t.Errorf("public images %q %q", pub.Identity.PhotoURL, pub.Organizations.Items[0].LogoURL)
	}

	// Restore an older version whose logo has since gone.
	if _, err := s.Apply(ctx, Patch{Sections: patch(t, `{"organizations":{"items":[{"name":"Other"}]}}`)}, owner, false); err != nil {
		t.Fatal(err)
	}
	removeImage(t, db, logo)
	v, err := versions.Get(ctx, db, "site_content", Key, withLogo)
	if err != nil {
		t.Fatal(err)
	}
	var snap map[string]json.RawMessage
	_ = json.Unmarshal(v.Snapshot, &snap)
	res, err = s.Apply(ctx, Patch{Sections: snap, Action: "restore", DropMissingMedia: true}, owner, false)
	if err != nil {
		t.Fatal(err)
	}
	if res.Doc.Content.Organizations.Items[0].Name != "Acme" || res.Doc.Content.Organizations.Items[0].LogoURL != "" ||
		res.Doc.Content.Identity.PhotoURL != "/images/sayantan.jpg" || len(res.Warnings) != 2 {
		t.Errorf("restore: %+v %v", res.Doc.Content.Organizations.Items, res.Warnings)
	}
	list, _ := versions.List(ctx, db, "site_content", Key, 1)
	if list[0].Action != "restore" || list[0].ID != res.VersionID || list[0].Actor != owner {
		t.Errorf("restore version: %+v", list[0])
	}

	// Reset.
	if _, err := s.Apply(ctx, Patch{Sections: patch(t, `{"hero":{"title":"Before reset"}}`)}, owner, false); err != nil {
		t.Fatal(err)
	}
	res, err = s.Apply(ctx, Patch{Reset: []string{"identity", "hero"}, Action: "reset"}, owner, false)
	if err != nil || !slices.Equal(res.Changed, []string{"identity", "hero"}) || !reflect.DeepEqual(res.Doc.Content.Hero, Default().Hero) ||
		res.Doc.Content.Organizations.Items[0].Name != "Acme" {
		t.Fatalf("reset: %+v %v", res.Changed, err)
	}
	if list, _ := versions.List(ctx, db, "site_content", Key, 1); list[0].Action != "reset" || !slices.Equal(list[0].Changed, res.Changed) {
		t.Errorf("reset version: %+v", list[0])
	}
}

// Saves take their own advisory lock, not the one provider seeding holds
// while an API instance starts.
func TestServiceLockID(t *testing.T) {
	_, db := newDB(t)
	ctx := context.Background()
	ids := []int64{store.LockMigrate, store.LockProviders, store.LockResume, store.LockSiteContent}
	if u := slices.Compact(slices.Sorted(slices.Values(ids))); len(u) != len(ids) {
		t.Fatalf("advisory lock ids repeat: %v", ids)
	}
	conn, err := db.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Release()
	if _, err := conn.Exec(ctx, `SELECT pg_advisory_lock($1)`, store.LockProviders); err != nil {
		t.Fatal(err)
	}
	defer conn.Exec(context.Background(), `SELECT pg_advisory_unlock($1)`, store.LockProviders) //nolint:errcheck
	short, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	s := &Service{DB: db, Log: quiet}
	if _, err := s.Apply(short, Patch{Sections: patch(t, `{"hero":{"title":"Not blocked"}}`)}, "w", false); err != nil {
		t.Fatalf("save waited on the provider seeding lock: %v", err)
	}
}

// A database that hangs (Neon resuming, a black-holed connection) delays
// Public by at most the refresh timeout once a good copy exists, however many
// requests are waiting: they get the last good copy instead of queueing.
func TestServiceSlowDatabase(t *testing.T) {
	_, db := newDB(t)
	ctx := context.Background()
	s := &Service{DB: db, Log: quiet, refresh: 300 * time.Millisecond}
	good, etag, _, err := s.Public(ctx)
	if err != nil {
		t.Fatal(err)
	}
	good = bytes.Clone(good)

	// Hold a lock that every read of app_settings waits for.
	tx, err := db.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(context.Background()) //nolint:errcheck
	if _, err := tx.Exec(ctx, `LOCK TABLE app_settings IN ACCESS EXCLUSIVE MODE`); err != nil {
		t.Fatal(err)
	}
	const n = 6
	start := time.Now()
	var wg sync.WaitGroup
	for range n {
		wg.Go(func() {
			cctx, cancel := context.WithTimeout(ctx, 10*time.Second)
			defer cancel()
			body, tag, stale, err := s.Public(cctx)
			if err != nil || !stale || !bytes.Equal(body, good) || tag != etag {
				t.Errorf("during the outage: stale=%v err=%v", stale, err)
			}
		})
	}
	wg.Wait()
	if d := time.Since(start); d > 2*time.Second {
		t.Errorf("%d requests took %v; they queued on the database", n, d)
	}
	// The admin read waits for the database rather than failing fast.
	short, cancel := context.WithTimeout(ctx, 500*time.Millisecond)
	defer cancel()
	if _, err := s.Get(short); err == nil {
		t.Error("Get returned while the table was locked")
	}

	if err := tx.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, _, stale, err := s.Public(ctx); err == nil && !stale {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("still stale after the database came back")
		}
		time.Sleep(50 * time.Millisecond)
	}

	// A save proves the database is back: the next read goes to it at once.
	if _, err := s.Apply(ctx, Patch{Sections: patch(t, `{"hero":{"title":"Back"}}`)}, "w", false); err != nil {
		t.Fatal(err)
	}
	if c := publicContent(t, s); c.Hero.Title != "Back" {
		t.Errorf("after a save: %q", c.Hero.Title)
	}
}

func TestServiceConcurrentWrites(t *testing.T) {
	_, db := newDB(t)
	ctx := context.Background()
	s := &Service{DB: db, Log: quiet}
	const n = 8
	var wg sync.WaitGroup
	errs := make([]error, n)
	for i := range n {
		wg.Go(func() {
			_, errs[i] = s.Apply(ctx, Patch{Check: true, Sections: patch(t, `{"hero":{"title":"Writer `+string(rune('A'+i))+`"}}`)}, "w", false)
		})
	}
	wg.Wait()
	ok, stale := 0, 0
	for _, err := range errs {
		var se *StaleError
		switch {
		case err == nil:
			ok++
		case errors.As(err, &se):
			stale++
		default:
			t.Errorf("unexpected: %v", err)
		}
	}
	if ok != 1 || stale != n-1 {
		t.Errorf("%d saved and %d stale, want 1 and %d", ok, stale, n-1)
	}
}

func TestServiceCache(t *testing.T) {
	dsn, db := newDB(t)
	ctx := context.Background()

	// A write clears the cache even with a long TTL.
	s := &Service{DB: db, TTL: time.Hour, Log: quiet}
	before, _, _, _ := s.Public(ctx)
	if _, err := s.Apply(ctx, Patch{Sections: patch(t, `{"hero":{"title":"Cached"}}`)}, "w", false); err != nil {
		t.Fatal(err)
	}
	after, _, _, _ := s.Public(ctx)
	if bytes.Equal(before, after) || !bytes.Contains(after, []byte("Cached")) {
		t.Error("cache not cleared by a write")
	}

	// Stale-if-error: the last good body survives a database failure.
	pool, err := store.Open(ctx, dsn, 2)
	if err != nil {
		t.Fatal(err)
	}
	s2 := &Service{DB: pool, Log: quiet}
	body, etag, _, err := s2.Public(ctx)
	if err != nil {
		t.Fatal(err)
	}
	pool.Close()
	got, etag2, stale, err := s2.Public(ctx)
	if err != nil || !stale || !bytes.Equal(got, body) || etag2 != etag {
		t.Errorf("stale-if-error: stale=%v err=%v", stale, err)
	}
	if _, err := s2.Get(ctx); err == nil {
		t.Error("Get on a closed pool succeeded")
	}
	fresh := &Service{DB: pool, Log: quiet}
	if _, _, _, err := fresh.Public(ctx); err == nil {
		t.Error("a fresh Service on a closed pool returned a body")
	}
}
