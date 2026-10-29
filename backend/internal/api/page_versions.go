package api

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/cadentic/portfolio-cms/backend/internal/content"
	"github.com/cadentic/portfolio-cms/backend/internal/versions"
)

// Version history of the protected legal pages (D12, D21).
func (s *Server) pageVersionRoutes(m *http.ServeMux) {
	m.HandleFunc("GET /api/admin/pages/{id}/versions", s.listPageVersions)
	m.HandleFunc("GET /api/admin/pages/{id}/versions/{vid}", s.getPageVersion)
	m.HandleFunc("POST /api/admin/pages/{id}/versions/{vid}/restore", s.restorePageVersion)
}

func (s *Server) listPageVersions(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if _, err := s.Content.Get(r.Context(), id); err != nil {
		writeError(w, http.StatusNotFound, "page not found")
		return
	}
	items, err := versions.List(r.Context(), s.DB, "page", id, atoiDefault(r.URL.Query().Get("limit"), 50))
	if err != nil {
		s.internal(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (s *Server) getPageVersion(w http.ResponseWriter, r *http.Request) {
	vid, err := strconv.ParseInt(r.PathValue("vid"), 10, 64)
	if err != nil {
		writeError(w, http.StatusNotFound, versions.ErrNotFound.Error())
		return
	}
	v, err := versions.Get(r.Context(), s.DB, "page", r.PathValue("id"), vid)
	if errors.Is(err, versions.ErrNotFound) {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	if err != nil {
		s.internal(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, v)
}

func (s *Server) restorePageVersion(w http.ResponseWriter, r *http.Request) {
	vid, err := strconv.ParseInt(r.PathValue("vid"), 10, 64)
	if err != nil {
		writeError(w, http.StatusNotFound, versions.ErrNotFound.Error())
		return
	}
	p, changed, err := s.Content.RestoreVersion(r.Context(), r.PathValue("id"), vid, actor(r))
	var invalid *content.InvalidError
	switch {
	case errors.Is(err, versions.ErrNotFound), errors.Is(err, content.ErrNotFound):
		writeError(w, http.StatusNotFound, err.Error())
		return
	case errors.Is(err, content.ErrProtected):
		writeError(w, http.StatusConflict, err.Error())
		return
	case errors.As(err, &invalid):
		writeError(w, http.StatusBadRequest, err.Error())
		return
	case err != nil:
		s.internal(w, r, err)
		return
	}
	// Restoring the text the page already has writes no version; nor is it audited.
	if len(changed) > 0 {
		s.audit(r, "page.restore", "page", p.ID, map[string]any{"version_id": vid, "changed": changed})
	}
	writeJSON(w, http.StatusOK, p)
}
