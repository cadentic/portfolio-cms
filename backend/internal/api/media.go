package api

import (
	"errors"
	"fmt"
	"image"
	_ "image/gif" // register decoders for DecodeConfig
	_ "image/jpeg"
	_ "image/png"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/cadentic/portfolio-cms/backend/internal/media"
	"github.com/cadentic/portfolio-cms/backend/internal/store"
)

func (s *Server) mediaRoutes(m *http.ServeMux) {
	m.HandleFunc("GET /api/admin/media", s.listMedia)
	m.HandleFunc("POST /api/admin/media", s.uploadMedia)
	m.HandleFunc("PATCH /api/admin/media/{id}", s.updateMedia)
	m.HandleFunc("DELETE /api/admin/media/{id}", s.deleteMedia)
}

type mediaItem struct {
	ID          string    `json:"id"`
	URL         string    `json:"url"`
	Kind        string    `json:"kind"`
	Alt         string    `json:"alt"`
	Caption     string    `json:"caption"`
	Width       *int      `json:"width"`
	Height      *int      `json:"height"`
	ContentType *string   `json:"content_type"`
	SizeBytes   *int64    `json:"size_bytes"`
	Filename    *string   `json:"filename"`
	Source      *string   `json:"source"`
	CreatedAt   time.Time `json:"created_at"`
}

const mediaCols = `id, url, kind, alt, caption, width, height, content_type, size_bytes, filename, source, created_at`

func scanMedia(row pgx.Row) (mediaItem, error) {
	var m mediaItem
	err := row.Scan(&m.ID, &m.URL, &m.Kind, &m.Alt, &m.Caption, &m.Width, &m.Height, &m.ContentType, &m.SizeBytes, &m.Filename, &m.Source, &m.CreatedAt)
	return m, err
}

// serveMedia streams /media/{key} from the private bucket. ServeContent
// handles Range, If-None-Match and If-Modified-Since.
func (s *Server) serveMedia(w http.ResponseWriter, r *http.Request) {
	key := r.PathValue("key")
	if s.Media == nil || !media.ValidKey(key) {
		http.NotFound(w, r)
		return
	}
	obj, err := s.Media.Open(r.Context(), key)
	if errors.Is(err, media.ErrNotFound) {
		w.Header().Set("Cache-Control", "public, max-age=60")
		http.NotFound(w, r)
		return
	}
	if err != nil {
		s.Log.Error("media open failed", "key", key, "err", err)
		http.Error(w, "temporarily unavailable", http.StatusServiceUnavailable)
		return
	}
	defer obj.Content.Close()
	h := w.Header()
	ct, known := media.ContentType(key)
	h.Set("Content-Type", ct)
	if !known {
		h.Set("Content-Disposition", "attachment")
	}
	// Keys never change content: uploads carry a unique id.
	h.Set("Cache-Control", "public, max-age=31536000, immutable")
	h.Set("ETag", obj.ETag)
	if ct != "application/pdf" { // Chrome's PDF viewer does not load under a strict CSP
		h.Set("Content-Security-Policy", "default-src 'none'; img-src 'self'; media-src 'self'; style-src 'unsafe-inline'")
	}
	http.ServeContent(w, r, "", obj.ModTime, obj.Content)
}

func (s *Server) listMedia(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	kind := q.Get("kind")
	if kind != "" && kind != "image" && kind != "video" && kind != "file" {
		writeError(w, 400, "kind must be image, video or file")
		return
	}
	limit := min(max(atoiDefault(q.Get("limit"), 48), 1), 200)
	offset := max(atoiDefault(q.Get("offset"), 0), 0)
	rows, err := s.DB.Query(r.Context(), `SELECT `+mediaCols+`, count(*) OVER () FROM cms_media
		WHERE ($1 = '' OR kind = $1)
		  AND ($2 = '' OR strpos(lower(url || ' ' || alt || ' ' || caption || ' ' || coalesce(filename, '')), lower($2)) > 0)
		ORDER BY created_at DESC, id LIMIT $3 OFFSET $4`, kind, strings.TrimSpace(q.Get("q")), limit, offset)
	if err != nil {
		s.internal(w, r, err)
		return
	}
	defer rows.Close()
	items := []mediaItem{}
	total := 0
	for rows.Next() {
		var m mediaItem
		if err := rows.Scan(&m.ID, &m.URL, &m.Kind, &m.Alt, &m.Caption, &m.Width, &m.Height, &m.ContentType, &m.SizeBytes,
			&m.Filename, &m.Source, &m.CreatedAt, &total); err != nil {
			s.internal(w, r, err)
			return
		}
		items = append(items, m)
	}
	if err := rows.Err(); err != nil {
		s.internal(w, r, err)
		return
	}
	storage := ""
	if s.Media != nil {
		storage = s.Media.Name()
	}
	writeJSON(w, 200, map[string]any{"items": items, "total": total, "storage": storage, "max_upload_bytes": s.Cfg.MediaMaxUploadBytes})
}

// uploadMedia accepts one multipart "file" (plus optional "alt" and
// "caption"), checks its real type from its bytes, stores it under
// uploads/YYYY/MM/<id>-<name>.<ext> and records it in cms_media.
func (s *Server) uploadMedia(w http.ResponseWriter, r *http.Request) {
	if s.Media == nil {
		writeError(w, 503, "media storage is not configured")
		return
	}
	limit := s.Cfg.MediaMaxUploadBytes
	// The server's 60s read timeout is too short for a large file on a slow link.
	_ = http.NewResponseController(w).SetReadDeadline(time.Now().Add(10 * time.Minute))
	r.Body = http.MaxBytesReader(w, r.Body, limit+64<<10)
	mr, err := r.MultipartReader()
	if err != nil {
		writeError(w, 400, "send the file as multipart/form-data in the 'file' field")
		return
	}
	var (
		alt, caption, filename string
		tmp                    *os.File
		size                   int64
	)
	defer func() {
		if tmp != nil {
			tmp.Close()
			os.Remove(tmp.Name())
		}
	}()
	for {
		part, err := mr.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			writeError(w, 400, uploadErr(err, limit))
			return
		}
		switch part.FormName() {
		case "alt", "caption":
			b, err := io.ReadAll(io.LimitReader(part, 1001))
			if err != nil || len(b) > 1000 {
				writeError(w, 400, part.FormName()+" must be at most 1000 characters")
				return
			}
			if part.FormName() == "alt" {
				alt = strings.TrimSpace(string(b))
			} else {
				caption = strings.TrimSpace(string(b))
			}
		case "file":
			if tmp != nil {
				writeError(w, 400, "upload one file at a time")
				return
			}
			filename = part.FileName()
			if tmp, err = os.CreateTemp("", "media-upload-*"); err != nil {
				s.internal(w, r, err)
				return
			}
			n, err := io.Copy(tmp, io.LimitReader(part, limit+1))
			if err != nil {
				writeError(w, 400, uploadErr(err, limit))
				return
			}
			if n > limit {
				writeError(w, http.StatusRequestEntityTooLarge, fmt.Sprintf("the file is larger than %d MB", limit>>20))
				return
			}
			size = n
		}
		part.Close()
	}
	if tmp == nil || size == 0 {
		writeError(w, 400, "choose a file to upload")
		return
	}

	head := make([]byte, 512)
	n, _ := tmp.ReadAt(head, 0)
	typ, ok := media.Detect(head[:n])
	if !ok {
		writeError(w, http.StatusUnsupportedMediaType, "this file type is not allowed. Upload a "+media.AcceptedFormats+" file")
		return
	}
	var width, height *int
	if typ.MIME == "image/jpeg" || typ.MIME == "image/png" || typ.MIME == "image/gif" {
		cfg, _, err := image.DecodeConfig(io.NewSectionReader(tmp, 0, size))
		if err != nil {
			writeError(w, http.StatusUnsupportedMediaType, "the image could not be read; it may be damaged")
			return
		}
		width, height = &cfg.Width, &cfg.Height
	}

	id := uuid.New()
	now := time.Now().UTC()
	key := fmt.Sprintf("uploads/%04d/%02d/%s-%s.%s", now.Year(), int(now.Month()), id, media.Slug(filename), typ.Ext)
	if err := s.Media.Put(r.Context(), key, typ.MIME, io.NewSectionReader(tmp, 0, size), size); err != nil {
		s.internal(w, r, err)
		return
	}
	displayName := filename
	if len(displayName) > 255 {
		displayName = displayName[:255]
	}
	m, err := scanMedia(s.DB.QueryRow(r.Context(), `INSERT INTO cms_media
		(id, url, kind, alt, caption, width, height, storage_key, content_type, size_bytes, filename, source, source_id, uploaded_by)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, 'upload', $8, $12)
		RETURNING `+mediaCols, id, "/media/"+key, typ.Kind, alt, caption, width, height, key, typ.MIME, size, displayName, actor(r)))
	if err != nil {
		if derr := s.Media.Delete(r.Context(), key); derr != nil {
			s.Log.Error("media cleanup failed", "key", key, "err", derr)
		}
		s.internal(w, r, err)
		return
	}
	s.audit(r, "media.upload", "media", m.ID, map[string]any{"key": key, "type": typ.MIME, "bytes": size})
	writeJSON(w, http.StatusCreated, m)
}

func uploadErr(err error, limit int64) string {
	var mbe *http.MaxBytesError
	if errors.As(err, &mbe) || errors.Is(err, multipart.ErrMessageTooLarge) {
		return fmt.Sprintf("the file is larger than %d MB", limit>>20)
	}
	return "the upload was interrupted or malformed; please try again"
}

func (s *Server) updateMedia(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		writeError(w, 404, "media not found")
		return
	}
	var in struct {
		Alt     *string `json:"alt"`
		Caption *string `json:"caption"`
	}
	if !decode(w, r, &in) {
		return
	}
	if (in.Alt != nil && len(*in.Alt) > 1000) || (in.Caption != nil && len(*in.Caption) > 1000) {
		writeError(w, 400, "alt text and caption must be at most 1000 characters")
		return
	}
	m, err := scanMedia(s.DB.QueryRow(r.Context(), `UPDATE cms_media SET alt = coalesce($2, alt), caption = coalesce($3, caption)
		WHERE id = $1 RETURNING `+mediaCols, id, trimPtr(in.Alt), trimPtr(in.Caption)))
	if errors.Is(err, pgx.ErrNoRows) {
		writeError(w, 404, "media not found")
		return
	}
	if err != nil {
		s.internal(w, r, err)
		return
	}
	s.audit(r, "media.update", "media", m.ID, nil)
	writeJSON(w, 200, m)
}

func trimPtr(p *string) *string {
	if p == nil {
		return nil
	}
	v := strings.TrimSpace(*p)
	return &v
}

// deleteMedia removes the record and its stored object. A file still used by
// a page is kept unless ?force=true.
func (s *Server) deleteMedia(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		writeError(w, 404, "media not found")
		return
	}
	ctx := r.Context()
	var url string
	var key *string
	if err := s.DB.QueryRow(ctx, `SELECT url, storage_key FROM cms_media WHERE id = $1`, id).Scan(&url, &key); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, 404, "media not found")
			return
		}
		s.internal(w, r, err)
		return
	}
	if r.URL.Query().Get("force") != "true" {
		var titles []string
		rows, err := s.DB.Query(ctx, `SELECT title FROM cms_pages
			WHERE strpos(hero::text, $1) > 0 OR strpos(blocks::text, $1) > 0 OR featured_image_url = $1 OR seo->>'og_image' = $1
			ORDER BY updated_at DESC LIMIT 5`, url)
		if err != nil {
			s.internal(w, r, err)
			return
		}
		for rows.Next() {
			var t string
			if rows.Scan(&t) == nil {
				titles = append(titles, t)
			}
		}
		rows.Close()
		if len(titles) > 0 {
			writeJSON(w, http.StatusConflict, map[string]any{"error": "this file is used on: " + strings.Join(titles, ", "), "pages": titles})
			return
		}
	}
	err = store.InTx(ctx, s.DB, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `DELETE FROM cms_media WHERE id = $1`, id); err != nil {
			return err
		}
		if key == nil || s.Media == nil {
			return nil
		}
		if err := s.Media.Delete(ctx, *key); err != nil && !errors.Is(err, media.ErrNotFound) {
			return err
		}
		return nil
	})
	if err != nil {
		s.internal(w, r, err)
		return
	}
	s.audit(r, "media.delete", "media", id.String(), map[string]any{"url": url})
	w.WriteHeader(http.StatusNoContent)
}
