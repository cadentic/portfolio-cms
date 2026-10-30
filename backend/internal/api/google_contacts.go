package api

import (
	"errors"
	"net/http"
	"net/url"
	"time"

	"github.com/cadentic/portfolio-cms/backend/internal/googlecontacts"
)

const googleContactsAdminPath = "/admin/email/google-contacts"

// googleContactsRoutes registers the admin endpoints. When the feature is off
// (GOOGLE_CONTACTS_ENABLED) only the status call exists, so the admin can hide it.
func (s *Server) googleContactsRoutes(m *http.ServeMux) {
	m.HandleFunc("GET /api/admin/google-contacts", s.googleContactsStatus)
	if !s.googleContactsOn() {
		return
	}
	m.HandleFunc("POST /api/admin/google-contacts/connect", s.googleContactsConnect)
	m.HandleFunc("POST /api/admin/google-contacts/disconnect", s.googleContactsDisconnect)
	m.HandleFunc("GET /api/admin/google-contacts/labels", s.googleContactsLabels)
	m.HandleFunc("PUT /api/admin/google-contacts/label", s.googleContactsSetLabel)
	m.HandleFunc("POST /api/admin/google-contacts/preview", s.googleContactsPreview)
	m.HandleFunc("POST /api/admin/google-contacts/sync", s.googleContactsSync)
	m.HandleFunc("POST /api/admin/google-contacts/pause", s.googleContactsPause)
}

// googleContactsError maps the sync's own messages (all safe to show the
// owner) to 400 and anything else to a logged 500.
func (s *Server) googleContactsError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, googlecontacts.ErrBusy):
		writeError(w, http.StatusConflict, err.Error())
	case googlecontacts.IsUserError(err):
		writeError(w, http.StatusBadRequest, err.Error())
	default:
		s.internal(w, r, err)
	}
}

func (s *Server) googleContactsOn() bool {
	return s.Cfg.GoogleContactsEnabled && s.GoogleContacts != nil
}

func (s *Server) googleContactsStatus(w http.ResponseWriter, r *http.Request) {
	if !s.googleContactsOn() {
		writeJSON(w, 200, map[string]bool{"enabled": false})
		return
	}
	st, err := s.GoogleContacts.Status(r.Context())
	if err != nil {
		s.internal(w, r, err)
		return
	}
	writeJSON(w, 200, st)
}

func (s *Server) googleContactsConnect(w http.ResponseWriter, r *http.Request) {
	if !s.allow(r.Context(), map[string]rateRule{"google_connect:" + actor(r): {Limit: 20, Window: time.Hour}}) {
		writeError(w, 429, "too many attempts; try again later")
		return
	}
	u, err := s.GoogleContacts.StartConnect(r.Context(), actor(r))
	if err != nil {
		s.googleContactsError(w, r, err)
		return
	}
	writeJSON(w, 200, map[string]string{"url": u})
}

// googleOAuthCallback is where Google sends the owner's browser back. It
// carries no admin token, so the single-use state started from the admin is
// what authorises it.
func (s *Server) googleOAuthCallback(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	back := s.Cfg.PublicSiteURL + googleContactsAdminPath
	if e := q.Get("error"); e != "" {
		msg := "Google sign-in was cancelled."
		if e != "access_denied" {
			msg = "Google sign-in failed (" + truncateASCII(e, 60) + ")."
		}
		http.Redirect(w, r, back+"?error="+url.QueryEscape(msg), http.StatusSeeOther)
		return
	}
	actorName, err := s.GoogleContacts.Callback(r.Context(), q.Get("code"), q.Get("state"))
	if err != nil {
		msg := err.Error()
		if !googlecontacts.IsUserError(err) {
			s.Log.Error("google contacts connect failed", "err", err, "actor", actorName)
			msg = "Connecting Google Contacts failed; please try again."
		}
		http.Redirect(w, r, back+"?error="+url.QueryEscape(msg), http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, back+"?connected=1", http.StatusSeeOther)
}

func truncateASCII(s string, n int) string {
	out := make([]byte, 0, n)
	for i := 0; i < len(s) && len(out) < n; i++ {
		if c := s[i]; c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_' {
			out = append(out, c)
		}
	}
	return string(out)
}

func (s *Server) googleContactsDisconnect(w http.ResponseWriter, r *http.Request) {
	if err := s.GoogleContacts.Disconnect(r.Context(), actor(r)); err != nil {
		s.internal(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) googleContactsLabels(w http.ResponseWriter, r *http.Request) {
	labels, err := s.GoogleContacts.Labels(r.Context())
	if err != nil {
		s.googleContactsError(w, r, err)
		return
	}
	writeJSON(w, 200, map[string]any{"items": labels})
}

func (s *Server) googleContactsSetLabel(w http.ResponseWriter, r *http.Request) {
	var in googlecontacts.LabelInput
	if !decode(w, r, &in) {
		return
	}
	l, err := s.GoogleContacts.SetLabel(r.Context(), in, actor(r))
	if err != nil {
		s.googleContactsError(w, r, err)
		return
	}
	writeJSON(w, 200, l)
}

func (s *Server) googleContactsPreview(w http.ResponseWriter, r *http.Request) {
	if !s.allow(r.Context(), map[string]rateRule{"google_sync:" + actor(r): {Limit: 30, Window: time.Hour}}) {
		writeError(w, 429, "too many requests to Google; try again later")
		return
	}
	res, err := s.GoogleContacts.Preview(r.Context())
	if err != nil {
		s.googleContactsError(w, r, err)
		return
	}
	writeJSON(w, 200, res)
}

func (s *Server) googleContactsSync(w http.ResponseWriter, r *http.Request) {
	if !s.allow(r.Context(), map[string]rateRule{"google_sync:" + actor(r): {Limit: 30, Window: time.Hour}}) {
		writeError(w, 429, "too many requests to Google; try again later")
		return
	}
	res, err := s.GoogleContacts.SyncNow(r.Context(), actor(r))
	if err != nil {
		s.googleContactsError(w, r, err)
		return
	}
	writeJSON(w, 200, res)
}

func (s *Server) googleContactsPause(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Paused bool `json:"paused"`
	}
	if !decode(w, r, &in) {
		return
	}
	if err := s.GoogleContacts.SetPaused(r.Context(), in.Paused, actor(r)); err != nil {
		s.internal(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
