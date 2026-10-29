package sitecontent

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"log/slog"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/cadentic/portfolio-cms/backend/internal/settings"
	"github.com/cadentic/portfolio-cms/backend/internal/store"
	"github.com/cadentic/portfolio-cms/backend/internal/versions"
)

// Doc is the raw stored document with its row meta. UpdatedAt is nil when
// nothing was ever saved; clients echo it back verbatim as base_updated_at.
type Doc struct {
	Content   Content    `json:"content"`
	UpdatedAt *time.Time `json:"updated_at"`
	UpdatedBy *string    `json:"updated_by"`
}

type Patch struct {
	Sections         map[string]json.RawMessage // merged per D5
	Reset            []string                   // section keys copied from Default() first
	Check            bool                       // compare Base with the stored updated_at
	Base             *time.Time                 // nil + Check = expect no stored row
	Action           string                     // "update" (default), "reset", "restore"
	DropMissingMedia bool                       // restore: clear missing images instead of rejecting them
}

type Result struct {
	Doc       Doc
	Changed   []string
	VersionID int64        // 0 when nothing was saved
	Warnings  []FieldError // never nil
}

// Service loads, caches and saves the document.
type Service struct {
	DB  *pgxpool.Pool
	TTL time.Duration // 5s in production, 0 in tests
	Log *slog.Logger

	mu   sync.Mutex
	cur  *snapshot // cache; nil when expired by a write
	last *snapshot // last good load, served when the database fails
}

type snapshot struct {
	doc  Doc
	pub  Content // public view: missing media dropped, {years} expanded
	body []byte  // json of pub
	etag string
	at   time.Time
}

const (
	lockID         = 7262411002
	keepVersions   = 200
	msgMediaAbsent = "choose an image from the media library (this file is not in it)"
)

var errNoWrite = errors.New("sitecontent: nothing to write")

func (s *Service) log() *slog.Logger {
	if s.Log != nil {
		return s.Log
	}
	return slog.Default()
}

// load returns the cached snapshot or reads a fresh one. Holding mu across
// the read means a write's cache clear can never be overtaken by an older read.
func (s *Service) load(ctx context.Context) (*snapshot, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cur != nil && s.TTL > 0 && time.Since(s.cur.at) < s.TTL {
		return s.cur, nil
	}
	raw, meta, found, err := settings.Raw(ctx, s.DB, Key)
	if err != nil {
		return nil, err
	}
	c, err := FromStored(raw)
	if err != nil {
		return nil, err
	}
	missing, err := missingMedia(ctx, s.DB, LocalMedia(c))
	if err != nil {
		return nil, err
	}
	pub := clone(c)
	DropMedia(&pub, missing)
	pub = Expand(pub)
	body, err := json.Marshal(pub)
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256(body)
	snap := &snapshot{doc: Doc{Content: c}, pub: pub, body: body, etag: `"` + hex.EncodeToString(sum[:])[:32] + `"`, at: time.Now()}
	if found {
		snap.doc.UpdatedAt, snap.doc.UpdatedBy = &meta.UpdatedAt, &meta.UpdatedBy
	}
	s.cur, s.last = snap, snap
	return snap, nil
}

func (s *Service) invalidate() {
	s.mu.Lock()
	s.cur = nil
	s.mu.Unlock()
}

// Get returns the raw stored document ({years} kept, missing media kept).
func (s *Service) Get(ctx context.Context) (Doc, error) {
	snap, err := s.load(ctx)
	if err != nil {
		return Doc{}, err
	}
	return copyDoc(snap.doc), nil
}

// Public returns the public JSON body and its strong ETag (quoted, ready for
// the header). When the database fails it serves the last good body with
// stale=true and logs the error; with none it returns the error. The body is
// shared: callers must not modify it.
func (s *Service) Public(ctx context.Context) (body []byte, etag string, stale bool, err error) {
	snap, err := s.load(ctx)
	if err == nil {
		return snap.body, snap.etag, false, nil
	}
	s.mu.Lock()
	last := s.last
	s.mu.Unlock()
	if last == nil {
		return nil, "", false, err
	}
	s.log().Error("site content: serving the last good copy", "err", err)
	return last.body, last.etag, true, nil
}

// PublicContent is the public view as a value, for llms.txt, the resume
// file name and the default author.
func (s *Service) PublicContent(ctx context.Context) (Content, error) {
	snap, err := s.load(ctx)
	if err != nil {
		return Content{}, err
	}
	return clone(snap.pub), nil
}

// Warnings reports images in c that are no longer in the media library.
// It is never nil.
func (s *Service) Warnings(ctx context.Context, c Content) ([]FieldError, error) {
	missing, err := missingMedia(ctx, s.DB, LocalMedia(c))
	if err != nil {
		return nil, err
	}
	c = clone(c)
	return DropMedia(&c, missing), nil
}

// Apply resets, merges, validates and saves in one transaction behind an
// advisory lock. A change that alters nothing, or a dry run, writes nothing.
// Errors are *ValidationError (400), *StaleError (409) or internal.
func (s *Service) Apply(ctx context.Context, p Patch, actor string, dryRun bool) (Result, error) {
	action := p.Action
	if action == "" {
		action = "update"
	}
	var res Result
	err := store.InTx(ctx, s.DB, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, int64(lockID)); err != nil {
			return err
		}
		raw, meta, found, err := settings.Raw(ctx, tx, Key)
		if err != nil {
			return err
		}
		cur, err := FromStored(raw)
		if err != nil {
			return err
		}
		if p.Check && (p.Base == nil && found || p.Base != nil && (!found || !meta.UpdatedAt.Equal(*p.Base))) {
			se := &StaleError{}
			if found {
				se.UpdatedAt, se.UpdatedBy = &meta.UpdatedAt, &meta.UpdatedBy
			}
			return se
		}

		next := cur
		if len(p.Reset) > 0 {
			if next, err = Reset(next, p.Reset); err != nil {
				return err
			}
		}
		next, errs := ApplySections(next, p.Sections)
		if len(errs) > 0 {
			return &ValidationError{Errors: errs}
		}
		if errs := Normalize(&next); len(errs) > 0 {
			return &ValidationError{Errors: errs}
		}

		// Only images new to the document must exist; stored ones that have
		// since gone are warnings (D8). A restore clears every missing one.
		missing, err := missingMedia(ctx, tx, LocalMedia(next))
		if err != nil {
			return err
		}
		if p.DropMissingMedia {
			res.Warnings = DropMedia(&next, missing)
		} else {
			stored := map[string]bool{}
			for _, u := range LocalMedia(cur) {
				stored[u] = true
			}
			for _, im := range images(&next) {
				if missing[*im.value] && !stored[*im.value] {
					errs = append(errs, FieldError{Path: im.path, Message: msgMediaAbsent})
				}
			}
			if len(errs) > 0 {
				return &ValidationError{Errors: errs}
			}
			tmp := clone(next)
			res.Warnings = DropMedia(&tmp, missing)
		}

		res.Changed = Changed(cur, next)
		res.Doc = Doc{Content: next}
		if found {
			res.Doc.UpdatedAt, res.Doc.UpdatedBy = &meta.UpdatedAt, &meta.UpdatedBy
		}
		if len(res.Changed) == 0 || dryRun {
			return errNoWrite
		}
		b, err := json.Marshal(next)
		if err != nil {
			return err
		}
		if meta, err = settings.PutRaw(ctx, tx, Key, b, actor); err != nil {
			return err
		}
		res.Doc.UpdatedAt, res.Doc.UpdatedBy = &meta.UpdatedAt, &meta.UpdatedBy
		res.VersionID, err = versions.Record(ctx, tx, "site_content", Key, action, res.Changed, b, actor, keepVersions)
		return err
	})
	if errors.Is(err, errNoWrite) {
		err = nil
	}
	if err != nil {
		return Result{}, err
	}
	if res.Warnings == nil {
		res.Warnings = []FieldError{}
	}
	if res.VersionID != 0 {
		s.invalidate()
	}
	return res, nil
}

// missingMedia returns the urls that have no image row in cms_media.
func missingMedia(ctx context.Context, q store.DB, urls []string) (map[string]bool, error) {
	missing := map[string]bool{}
	if len(urls) == 0 {
		return missing, nil
	}
	for _, u := range urls {
		missing[u] = true
	}
	rows, err := q.Query(ctx, `SELECT url FROM cms_media WHERE kind='image' AND url = ANY($1)`, urls)
	if err != nil {
		return nil, err
	}
	found, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return nil, err
	}
	for _, u := range found {
		delete(missing, u)
	}
	return missing, nil
}

func copyDoc(d Doc) Doc {
	out := Doc{Content: clone(d.Content)}
	if d.UpdatedAt != nil {
		t := *d.UpdatedAt
		out.UpdatedAt = &t
	}
	if d.UpdatedBy != nil {
		b := *d.UpdatedBy
		out.UpdatedBy = &b
	}
	return out
}
