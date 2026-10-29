package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/cadentic/portfolio-cms/backend/internal/media"
	"github.com/cadentic/portfolio-cms/backend/internal/settings"
	"github.com/cadentic/portfolio-cms/backend/internal/sitecontent"
	"github.com/cadentic/portfolio-cms/backend/internal/store"
)

// The resume PDF uploaded in the admin lives under private/resume/ in media
// storage, which /media never serves: it is downloaded only after email
// verification (D13). RESUME_FILE_PATH remains a fallback.

const (
	resumeSettingKey = "resume"
	resumeMaxBytes   = 10 << 20
	resumeLockID     = 7262411003 // serialises uploads and deletes
)

func (s *Server) resumeRoutes(m *http.ServeMux) {
	m.HandleFunc("GET /api/admin/resume", s.resumeStatus)
	m.HandleFunc("POST /api/admin/resume", s.uploadResume)
	m.HandleFunc("DELETE /api/admin/resume", s.deleteResume)
	m.HandleFunc("GET /api/admin/resume/file", s.resumeFile)
}

// resumeSetting is the value of the "resume" settings row.
type resumeSetting struct {
	Key       string `json:"key"`
	Filename  string `json:"filename"`
	SizeBytes int64  `json:"size_bytes"`
}

func uploadedResume(ctx context.Context, q store.DB) (resumeSetting, settings.Meta, bool, error) {
	var rs resumeSetting
	raw, meta, found, err := settings.Raw(ctx, q, resumeSettingKey)
	if err != nil || !found {
		return rs, meta, false, err
	}
	if err := json.Unmarshal(raw, &rs); err != nil {
		return rs, meta, false, err
	}
	return rs, meta, rs.Key != "", nil
}

// resumeSource resolves the download: the uploaded file, else
// RESUME_FILE_PATH, else none. key is a media key or a file path.
func (s *Server) resumeSource(ctx context.Context) (kind, key string, err error) {
	rs, _, found, err := uploadedResume(ctx, s.DB)
	if err != nil {
		return "", "", err
	}
	if found && s.Media != nil {
		return "upload", rs.Key, nil
	}
	if s.Cfg.ResumeFilePath != "" {
		return "server_file", s.Cfg.ResumeFilePath, nil
	}
	return "none", "", nil
}

func (s *Server) resumeLimit() int64 { return min(s.Cfg.MediaMaxUploadBytes, resumeMaxBytes) }

func (s *Server) resumeDownloadName(r *http.Request) string {
	return sitecontent.ResumeFilename(s.ownerName(r))
}

// serveResume streams the resolved resume as an attachment, never cached.
func (s *Server) serveResume(w http.ResponseWriter, r *http.Request, kind, key string) {
	var (
		body io.ReadSeeker
		mod  time.Time
	)
	switch kind {
	case "upload":
		obj, err := s.Media.Open(r.Context(), key)
		if errors.Is(err, media.ErrNotFound) {
			writeError(w, http.StatusNotFound, "the resume file is missing; upload it again in Site profile → Resume")
			return
		}
		if err != nil {
			s.internal(w, r, err)
			return
		}
		defer obj.Content.Close()
		body, mod = obj.Content, obj.ModTime
	case "server_file":
		f, err := os.Open(key)
		if err != nil {
			s.internal(w, r, err)
			return
		}
		defer f.Close()
		if st, err := f.Stat(); err == nil {
			mod = st.ModTime()
		}
		body = f
	default:
		writeError(w, http.StatusNotFound, "resume download is not available")
		return
	}
	h := w.Header()
	h.Set("Content-Type", "application/pdf")
	h.Set("Content-Disposition", `attachment; filename="`+s.resumeDownloadName(r)+`"`)
	h.Set("Cache-Control", "private, no-store")
	http.ServeContent(w, r, "", mod, body)
}

type resumeStatusDoc struct {
	Source       string     `json:"source"`
	Filename     *string    `json:"filename"`
	SizeBytes    *int64     `json:"size_bytes"`
	UploadedAt   *time.Time `json:"uploaded_at"`
	UploadedBy   *string    `json:"uploaded_by"`
	DownloadName string     `json:"download_name"`
	MaxBytes     int64      `json:"max_bytes"`
}

func (s *Server) resumeStatus(w http.ResponseWriter, r *http.Request) {
	st := resumeStatusDoc{Source: "none", DownloadName: s.resumeDownloadName(r), MaxBytes: s.resumeLimit()}
	rs, meta, found, err := uploadedResume(r.Context(), s.DB)
	if err != nil {
		s.internal(w, r, err)
		return
	}
	switch {
	case found && s.Media != nil:
		st.Source, st.Filename, st.SizeBytes = "upload", &rs.Filename, &rs.SizeBytes
		st.UploadedAt, st.UploadedBy = &meta.UpdatedAt, nonEmpty(&meta.UpdatedBy)
	case s.Cfg.ResumeFilePath != "":
		name := filepath.Base(s.Cfg.ResumeFilePath)
		st.Source, st.Filename = "server_file", &name
		if fi, err := os.Stat(s.Cfg.ResumeFilePath); err == nil {
			size := fi.Size()
			st.SizeBytes = &size
		}
	}
	writeJSON(w, http.StatusOK, st)
}

// uploadResume stores one multipart "file" PDF at private/resume/<uuid>.pdf
// and then removes the previous upload.
func (s *Server) uploadResume(w http.ResponseWriter, r *http.Request) {
	if s.Media == nil {
		writeError(w, http.StatusServiceUnavailable, "media storage is not configured")
		return
	}
	limit := s.resumeLimit()
	_ = http.NewResponseController(w).SetReadDeadline(time.Now().Add(10 * time.Minute))
	r.Body = http.MaxBytesReader(w, r.Body, limit+64<<10)
	mr, err := r.MultipartReader()
	if err != nil {
		writeError(w, http.StatusBadRequest, "send the file as multipart/form-data in the 'file' field")
		return
	}
	var (
		filename string
		tmp      *os.File
		size     int64
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
			writeError(w, http.StatusBadRequest, uploadErr(err, limit))
			return
		}
		if part.FormName() == "file" {
			if tmp != nil {
				writeError(w, http.StatusBadRequest, "upload one file at a time")
				return
			}
			filename = part.FileName()
			if tmp, err = os.CreateTemp("", "resume-upload-*"); err != nil {
				s.internal(w, r, err)
				return
			}
			n, err := io.Copy(tmp, io.LimitReader(part, limit+1))
			if err != nil {
				writeError(w, http.StatusBadRequest, uploadErr(err, limit))
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
		writeError(w, http.StatusBadRequest, "choose a file to upload")
		return
	}
	head := make([]byte, 512)
	n, _ := tmp.ReadAt(head, 0)
	if typ, ok := media.Detect(head[:n]); !ok || typ.MIME != "application/pdf" {
		writeError(w, http.StatusUnsupportedMediaType, "upload a PDF file")
		return
	}

	ctx := r.Context()
	key := "private/resume/" + uuid.NewString() + ".pdf"
	if err := s.Media.Put(ctx, key, "application/pdf", io.NewSectionReader(tmp, 0, size), size); err != nil {
		s.internal(w, r, err)
		return
	}
	rs := resumeSetting{Key: key, Filename: displayName(filename), SizeBytes: size}
	var prev resumeSetting
	var hadPrev bool
	err = store.InTx(ctx, s.DB, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, int64(resumeLockID)); err != nil {
			return err
		}
		var err error
		if prev, _, hadPrev, err = uploadedResume(ctx, tx); err != nil {
			return err
		}
		b, _ := json.Marshal(rs)
		_, err = settings.PutRaw(ctx, tx, resumeSettingKey, b, actor(r))
		return err
	})
	if err != nil {
		if derr := s.Media.Delete(context.WithoutCancel(ctx), key); derr != nil {
			s.Log.Error("resume cleanup failed", "key", key, "err", derr)
		}
		s.internal(w, r, err)
		return
	}
	if hadPrev && prev.Key != key {
		s.deleteResumeObject(ctx, prev.Key)
	}
	s.audit(r, "resume.upload", "settings", resumeSettingKey, map[string]any{"filename": rs.Filename, "bytes": size})
	s.resumeStatus(w, r)
}

// displayName keeps the base name of an uploaded file, at most 255 bytes.
func displayName(name string) string {
	name = path.Base(strings.ReplaceAll(name, `\`, "/"))
	if name == "." || name == "/" {
		return "resume.pdf"
	}
	for len(name) > 255 {
		_, n := utf8.DecodeLastRuneInString(name)
		name = name[:len(name)-n]
	}
	return name
}

func (s *Server) deleteResumeObject(ctx context.Context, key string) {
	if err := s.Media.Delete(context.WithoutCancel(ctx), key); err != nil && !errors.Is(err, media.ErrNotFound) {
		s.Log.Error("resume delete failed", "key", key, "err", err)
	}
}

// deleteResume removes the uploaded file; RESUME_FILE_PATH, if set, applies again.
func (s *Server) deleteResume(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	var rs resumeSetting
	var found bool
	err := store.InTx(ctx, s.DB, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, int64(resumeLockID)); err != nil {
			return err
		}
		var err error
		if rs, _, found, err = uploadedResume(ctx, tx); err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `DELETE FROM app_settings WHERE key=$1`, resumeSettingKey)
		return err
	})
	if err != nil {
		s.internal(w, r, err)
		return
	}
	if found {
		if s.Media != nil {
			s.deleteResumeObject(ctx, rs.Key)
		}
		s.audit(r, "resume.delete", "settings", resumeSettingKey, map[string]any{"filename": rs.Filename})
	}
	s.resumeStatus(w, r)
}

// resumeFile lets the owner check the file visitors will get.
func (s *Server) resumeFile(w http.ResponseWriter, r *http.Request) {
	kind, key, err := s.resumeSource(r.Context())
	if err != nil {
		s.internal(w, r, err)
		return
	}
	s.serveResume(w, r, kind, key)
}
