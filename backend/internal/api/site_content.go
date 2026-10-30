package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/cadentic/portfolio-cms/backend/internal/email"
	"github.com/cadentic/portfolio-cms/backend/internal/sitecontent"
	"github.com/cadentic/portfolio-cms/backend/internal/versions"
)

func (s *Server) siteContentRoutes(m *http.ServeMux) {
	m.HandleFunc("GET /api/admin/site-content", s.getSiteContent)
	m.HandleFunc("PUT /api/admin/site-content", s.putSiteContent)
	m.HandleFunc("POST /api/admin/site-content/reset", s.resetSiteContent)
	m.HandleFunc("GET /api/admin/site-content/versions", s.siteContentVersions)
	m.HandleFunc("POST /api/admin/site-content/versions/{id}/restore", s.restoreSiteContent)
}

// siteContentPublic serves the public view with a strong ETag (D10). When the
// database fails it serves the last good copy, never the defaults.
func (s *Server) siteContentPublic(w http.ResponseWriter, r *http.Request) {
	body, etag, stale, err := s.SiteContent.Public(r.Context())
	h := w.Header()
	if err != nil {
		s.Log.Error("site content unavailable", "err", email.RedactString(err.Error()))
		h.Set("Cache-Control", "no-store")
		writeError(w, http.StatusServiceUnavailable, "site content is temporarily unavailable")
		return
	}
	h.Set("ETag", etag)
	if stale {
		h.Set("Cache-Control", "no-store")
		h.Set("X-Content-Stale", "1")
	} else {
		h.Set("Cache-Control", "public, max-age=0, must-revalidate")
		if etagMatch(r.Header.Get("If-None-Match"), etag) {
			w.WriteHeader(http.StatusNotModified)
			return
		}
	}
	h.Set("Content-Type", "application/json; charset=utf-8")
	_, _ = w.Write(body)
}

// etagMatch reports whether an If-None-Match list names etag (weak
// comparison, as RFC 9110 asks for If-None-Match).
func etagMatch(header, etag string) bool {
	for _, t := range strings.Split(header, ",") {
		if t = strings.TrimPrefix(strings.TrimSpace(t), "W/"); t == etag || t == "*" {
			return true
		}
	}
	return false
}

// siteDoc is SiteContentAdminDoc.
type siteDoc struct {
	Content   sitecontent.Content          `json:"content"`
	Defaults  sitecontent.Content          `json:"defaults"`
	Limits    map[string]sitecontent.Limit `json:"limits"`
	UpdatedAt *time.Time                   `json:"updated_at"`
	UpdatedBy *string                      `json:"updated_by"`
	Warnings  []sitecontent.FieldError     `json:"warnings"`
}

func writeSiteDoc(w http.ResponseWriter, d sitecontent.Doc, warnings []sitecontent.FieldError) {
	if warnings == nil {
		warnings = []sitecontent.FieldError{}
	}
	writeJSON(w, http.StatusOK, siteDoc{Content: d.Content, Defaults: sitecontent.Default(), Limits: sitecontent.Limits(),
		UpdatedAt: d.UpdatedAt, UpdatedBy: nonEmpty(d.UpdatedBy), Warnings: warnings})
}

func nonEmpty(p *string) *string {
	if p == nil || *p == "" {
		return nil
	}
	return p
}

// decodeStrict decodes a JSON body and refuses unknown keys, so a typo
// cannot answer "Saved." while saving nothing. An empty body is allowed only
// when emptyOK.
func decodeStrict(w http.ResponseWriter, r *http.Request, limit int64, v any, emptyOK bool) bool {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, limit))
	dec.DisallowUnknownFields()
	err := dec.Decode(v)
	if err == nil || emptyOK && errors.Is(err, io.EOF) {
		return true
	}
	var mbe *http.MaxBytesError
	switch msg, unknown := strings.CutPrefix(err.Error(), "json: unknown field "); {
	case errors.As(err, &mbe):
		writeError(w, http.StatusRequestEntityTooLarge, fmt.Sprintf("the request is larger than %d KB", limit>>10))
	case unknown:
		writeError(w, http.StatusBadRequest, "unknown field "+msg)
	default:
		writeError(w, http.StatusBadRequest, "invalid JSON body")
	}
	return false
}

// baseCheck reads base_updated_at: absent means no check, null means no saved
// version is expected, a string is the updated_at the client last read.
func baseCheck(w http.ResponseWriter, raw json.RawMessage) (check bool, base *time.Time, ok bool) {
	if len(raw) == 0 {
		return false, nil, true
	}
	if string(raw) == "null" {
		return true, nil, true
	}
	var v string
	if json.Unmarshal(raw, &v) == nil {
		if t, err := time.Parse(time.RFC3339Nano, v); err == nil {
			return true, &t, true
		}
	}
	writeError(w, http.StatusBadRequest, "base_updated_at must be null or the updated_at value from the last read")
	return false, nil, false
}

// siteContentError maps Apply errors: invalid content 400, a stale base 409.
func (s *Server) siteContentError(w http.ResponseWriter, r *http.Request, err error) {
	var ve *sitecontent.ValidationError
	var se *sitecontent.StaleError
	switch {
	case errors.As(err, &ve):
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": ve.Error(), "errors": ve.Errors})
	case errors.As(err, &se):
		msg := "Site content was changed elsewhere. Reload to see it; your edits are still in the form."
		if se.UpdatedAt != nil {
			by := "someone"
			if se.UpdatedBy != nil && *se.UpdatedBy != "" {
				by = *se.UpdatedBy
			}
			msg = fmt.Sprintf("Site content was changed elsewhere (by %s at %s). Reload to see it; your edits are still in the form.",
				by, se.UpdatedAt.UTC().Format("2 Jan 2006 15:04 UTC"))
		}
		writeJSON(w, http.StatusConflict, map[string]any{"error": msg, "updated_at": se.UpdatedAt, "updated_by": nonEmpty(se.UpdatedBy)})
	default:
		s.internal(w, r, err)
	}
}

func (s *Server) getSiteContent(w http.ResponseWriter, r *http.Request) {
	d, err := s.SiteContent.Get(r.Context())
	if err != nil {
		s.internal(w, r, err)
		return
	}
	warnings, err := s.SiteContent.Warnings(r.Context(), d.Content)
	if err != nil {
		s.internal(w, r, err)
		return
	}
	writeSiteDoc(w, d, warnings)
}

// putSiteContent merges the sections sent over the stored document (D5).
func (s *Server) putSiteContent(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Content map[string]json.RawMessage `json:"content"`
		Base    json.RawMessage            `json:"base_updated_at"`
	}
	if !decodeStrict(w, r, 512<<10, &in, false) {
		return
	}
	if len(in.Content) == 0 {
		writeError(w, http.StatusBadRequest, "content is required")
		return
	}
	check, base, ok := baseCheck(w, in.Base)
	if !ok {
		return
	}
	res, err := s.SiteContent.Apply(r.Context(), sitecontent.Patch{Sections: in.Content, Check: check, Base: base}, actor(r), false)
	if err != nil {
		s.siteContentError(w, r, err)
		return
	}
	if res.VersionID != 0 {
		s.audit(r, "site_content.update", "settings", sitecontent.Key, map[string]any{"sections": res.Changed, "version_id": res.VersionID})
	}
	writeSiteDoc(w, res.Doc, res.Warnings)
}

func (s *Server) resetSiteContent(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Sections []string        `json:"sections"`
		All      bool            `json:"all"`
		Base     json.RawMessage `json:"base_updated_at"`
	}
	if !decodeStrict(w, r, 64<<10, &in, false) {
		return
	}
	keys := in.Sections
	if in.All {
		keys = sitecontent.Sections
	}
	if len(keys) == 0 {
		writeError(w, http.StatusBadRequest, "choose sections to reset")
		return
	}
	check, base, ok := baseCheck(w, in.Base)
	if !ok {
		return
	}
	res, err := s.SiteContent.Apply(r.Context(), sitecontent.Patch{Reset: keys, Check: check, Base: base, Action: "reset"}, actor(r), false)
	if err != nil {
		s.siteContentError(w, r, err)
		return
	}
	if res.VersionID != 0 {
		s.audit(r, "site_content.reset", "settings", sitecontent.Key, map[string]any{"sections": res.Changed, "version_id": res.VersionID})
	}
	writeSiteDoc(w, res.Doc, res.Warnings)
}

func (s *Server) siteContentVersions(w http.ResponseWriter, r *http.Request) {
	items, err := versions.List(r.Context(), s.DB, "site_content", sitecontent.Key, atoiDefault(r.URL.Query().Get("limit"), 50))
	if err != nil {
		s.internal(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

// restoreSiteContent saves an earlier version as a new one. Images deleted
// since then are cleared and reported as warnings rather than refused.
func (s *Server) restoreSiteContent(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusNotFound, versions.ErrNotFound.Error())
		return
	}
	var in struct {
		Base json.RawMessage `json:"base_updated_at"`
	}
	if !decodeStrict(w, r, 64<<10, &in, true) {
		return
	}
	check, base, ok := baseCheck(w, in.Base)
	if !ok {
		return
	}
	v, err := versions.Get(r.Context(), s.DB, "site_content", sitecontent.Key, id)
	if errors.Is(err, versions.ErrNotFound) {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	if err != nil {
		s.internal(w, r, err)
		return
	}
	var secs map[string]json.RawMessage
	if err := json.Unmarshal(v.Snapshot, &secs); err != nil {
		s.internal(w, r, err)
		return
	}
	for k := range secs {
		if !slices.Contains(sitecontent.Sections, k) {
			delete(secs, k)
		}
	}
	res, err := s.SiteContent.Apply(r.Context(), sitecontent.Patch{Sections: secs, Check: check, Base: base, Action: "restore",
		DropMissingMedia: true}, actor(r), false)
	if err != nil {
		s.siteContentError(w, r, err)
		return
	}
	if res.VersionID != 0 {
		s.audit(r, "site_content.restore", "settings", sitecontent.Key, map[string]any{"version_id": id, "sections": res.Changed})
	}
	writeSiteDoc(w, res.Doc, res.Warnings)
}

// ownerName is identity.name for bylines and file names, with the original
// owner's name when the site content cannot be read.
func (s *Server) ownerName(r *http.Request) string {
	if s.SiteContent != nil {
		if c, err := s.SiteContent.PublicContent(r.Context()); err == nil && c.Identity.Name != "" {
			return c.Identity.Name
		}
	}
	return "Sayantan Chakraborty"
}
