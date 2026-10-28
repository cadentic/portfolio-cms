// Package api wires the HTTP routes: public subscriber/content endpoints,
// provider webhooks, the Firebase-protected admin API, OAuth and MCP.
package api

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"log/slog"
	"net"
	"net/http"
	"runtime/debug"
	"slices"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/cadentic/portfolio-cms/backend/internal/alerts"
	"github.com/cadentic/portfolio-cms/backend/internal/auth"
	"github.com/cadentic/portfolio-cms/backend/internal/campaigns"
	"github.com/cadentic/portfolio-cms/backend/internal/config"
	"github.com/cadentic/portfolio-cms/backend/internal/content"
	"github.com/cadentic/portfolio-cms/backend/internal/email"
	"github.com/cadentic/portfolio-cms/backend/internal/mailer"
	"github.com/cadentic/portfolio-cms/backend/internal/mcp"
	"github.com/cadentic/portfolio-cms/backend/internal/oauth"
	"github.com/cadentic/portfolio-cms/backend/internal/providers"
	"github.com/cadentic/portfolio-cms/backend/internal/ratelimit"
	"github.com/cadentic/portfolio-cms/backend/internal/settings"
	"github.com/cadentic/portfolio-cms/backend/internal/subscribers"
	"github.com/cadentic/portfolio-cms/backend/internal/tokens"
)

type Server struct {
	Cfg         *config.Config
	DB          *pgxpool.Pool
	Log         *slog.Logger
	Verifier    auth.Verifier
	Authorizer  auth.Authorizer
	Settings    *settings.Store
	Providers   *providers.Repo
	Registry    *email.Registry
	Subscribers *subscribers.Service
	Campaigns   *campaigns.Service
	Content     *content.Service
	Mailer      *mailer.Mailer
	Alerts      *alerts.Service
	Limiter     *ratelimit.Limiter
	Signer      tokens.Signer
	OAuth       *oauth.Server
	MCP         *mcp.Server
}

func (s *Server) Routes() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) { writeJSON(w, 200, map[string]string{"status": "ok"}) })
	mux.HandleFunc("GET /readyz", s.ready)
	mux.HandleFunc("GET /metrics", s.metrics)

	s.publicRoutes(mux)
	s.webhookRoutes(mux)

	admin := http.NewServeMux()
	s.adminRoutes(admin)
	mux.Handle("/api/admin/", auth.AdminOnly(s.Verifier, s.Authorizer, writeError, admin))

	// OAuth 2.1 + MCP
	mux.HandleFunc("GET /.well-known/oauth-protected-resource", s.OAuth.ProtectedResourceMetadata)
	mux.HandleFunc("GET /.well-known/oauth-protected-resource/mcp", s.OAuth.ProtectedResourceMetadata)
	mux.HandleFunc("GET /.well-known/oauth-authorization-server", s.OAuth.AuthorizationServerMetadata)
	mux.HandleFunc("POST /oauth/register", s.limited("oauth_register", 20, time.Hour, s.OAuth.Register))
	mux.HandleFunc("GET /oauth/authorize", s.OAuth.Authorize)
	mux.HandleFunc("GET /api/oauth/client", s.OAuth.ClientInfo)
	mux.Handle("POST /api/oauth/approve", auth.AdminOnly(s.Verifier, s.Authorizer, writeError, http.HandlerFunc(s.OAuth.Approve)))
	mux.HandleFunc("POST /oauth/token", s.limited("oauth_token", 60, time.Hour, s.OAuth.Token))
	mux.HandleFunc("POST /oauth/revoke", s.OAuth.Revoke)
	mux.Handle("/mcp", s.OAuth.Protect(s.MCP))

	return s.recoverer(s.requestLog(s.securityHeaders(s.cors(mux))))
}

// --- helpers ------------------------------------------------------------------

type apiError struct {
	Error string `json:"error"`
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, apiError{Error: msg})
}

func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 2<<20))
	if err := dec.Decode(v); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return false
	}
	return true
}

func (s *Server) internal(w http.ResponseWriter, r *http.Request, err error) {
	s.Log.Error("request failed", "path", r.URL.Path, "err", email.RedactString(err.Error()), "request_id", r.Header.Get("X-Request-ID"))
	writeError(w, http.StatusInternalServerError, "something went wrong; please try again")
}

// clientIP honours X-Forwarded-For only for the configured number of trusted
// proxy hops (Cloudflare / Cloud Run / Container Apps ingress).
func (s *Server) clientIP(r *http.Request) string {
	if s.Cfg.TrustedProxyHops > 0 {
		if h := r.Header.Get("CF-Connecting-IP"); h != "" && net.ParseIP(h) != nil {
			return h
		}
		parts := strings.Split(r.Header.Get("X-Forwarded-For"), ",")
		idx := len(parts) - s.Cfg.TrustedProxyHops
		if idx >= 0 && idx < len(parts) {
			if ip := strings.TrimSpace(parts[idx]); net.ParseIP(ip) != nil {
				return ip
			}
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// limited applies a per-IP rate limit to a handler.
func (s *Server) limited(name string, limit int, window time.Duration, h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ok, err := s.Limiter.Allow(r.Context(), name+":ip:"+s.clientIP(r), limit, window)
		if err != nil || !ok {
			w.Header().Set("Retry-After", "60")
			writeError(w, http.StatusTooManyRequests, "too many requests; please wait a moment and try again")
			return
		}
		h(w, r)
	}
}

func (s *Server) allow(ctx context.Context, rules map[string]ratelimit.Rule) bool {
	ok, err := s.Limiter.AllowAll(ctx, rules)
	return err == nil && ok
}

// --- middleware -----------------------------------------------------------------

type statusWriter struct {
	http.ResponseWriter
	status int
}

func (w *statusWriter) WriteHeader(code int) {
	w.status = code
	w.ResponseWriter.WriteHeader(code)
}

func (w *statusWriter) Flush() {
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func (s *Server) requestLog(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.Header.Get("X-Request-ID")
		if id == "" || len(id) > 64 {
			b := make([]byte, 8)
			_, _ = rand.Read(b)
			id = hex.EncodeToString(b)
			r.Header.Set("X-Request-ID", id)
		}
		w.Header().Set("X-Request-ID", id)
		start := time.Now()
		sw := &statusWriter{ResponseWriter: w, status: 200}
		next.ServeHTTP(sw, r)
		if r.URL.Path == "/healthz" || r.URL.Path == "/readyz" {
			return
		}
		// Query strings are not logged: they carry tokens.
		s.Log.Info("http", "method", r.Method, "path", r.URL.Path, "status", sw.status,
			"duration_ms", time.Since(start).Milliseconds(), "request_id", id)
	})
}

func (s *Server) recoverer(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if rec := recover(); rec != nil {
				s.Log.Error("panic", "err", rec, "stack", string(debug.Stack()))
				writeError(w, http.StatusInternalServerError, "internal error")
			}
		}()
		next.ServeHTTP(w, r)
	})
}

func (s *Server) securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "strict-origin-when-cross-origin")
		h.Set("X-Frame-Options", "DENY")
		if strings.HasPrefix(r.URL.Path, "/api/") {
			h.Set("Cache-Control", "no-store")
		}
		if s.Cfg.IsProduction() {
			h.Set("Strict-Transport-Security", "max-age=63072000; includeSubDomains")
		}
		next.ServeHTTP(w, r)
	})
}

// cors allows only configured origins. Admin calls use Authorization bearer
// tokens (never cookies), so cross-site request forgery cannot ride on
// ambient credentials.
func (s *Server) cors(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		if origin != "" && (slices.Contains(s.Cfg.CORSOrigins, origin) || origin == s.Cfg.PublicSiteURL) {
			h := w.Header()
			h.Set("Access-Control-Allow-Origin", origin)
			h.Set("Vary", "Origin")
			h.Set("Access-Control-Allow-Headers", "Authorization, Content-Type, X-Request-ID, Mcp-Protocol-Version, Mcp-Session-Id")
			h.Set("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS")
			h.Set("Access-Control-Max-Age", "600")
		}
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) ready(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()
	if err := s.DB.Ping(ctx); err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"status": "database unavailable"})
		return
	}
	writeJSON(w, 200, map[string]string{"status": "ready"})
}

// metrics exposes a few Prometheus gauges for queue/provider monitoring.
func (s *Server) metrics(w http.ResponseWriter, r *http.Request) {
	var depth, unknown, dead, processing int
	var oldest float64
	err := s.DB.QueryRow(r.Context(), `SELECT count(*) FILTER (WHERE status IN ('queued','retry_scheduled')),
		count(*) FILTER (WHERE status='acceptance_unknown'), count(*) FILTER (WHERE status='dead_letter'),
		count(*) FILTER (WHERE status='processing'),
		coalesce(extract(epoch FROM now() - min(created_at) FILTER (WHERE status IN ('queued','retry_scheduled'))),0)
		FROM email_jobs`).Scan(&depth, &unknown, &dead, &processing, &oldest)
	if err != nil {
		http.Error(w, "unavailable", 503)
		return
	}
	w.Header().Set("Content-Type", "text/plain; version=0.0.4")
	var b strings.Builder
	write := func(name, help string, v any) {
		b.WriteString("# HELP " + name + " " + help + "\n# TYPE " + name + " gauge\n")
		b.WriteString(name + " " + toString(v) + "\n")
	}
	write("email_queue_depth", "Jobs waiting to be sent", depth)
	write("email_jobs_processing", "Jobs currently leased by a worker", processing)
	write("email_acceptance_unknown", "Jobs with unknown provider acceptance", unknown)
	write("email_dead_letter", "Jobs in the dead-letter state", dead)
	write("email_oldest_pending_seconds", "Age of the oldest pending job", oldest)
	if ps, err := s.Providers.List(r.Context()); err == nil {
		b.WriteString("# HELP email_provider_circuit_open Provider circuit breaker open (1) or closed (0)\n# TYPE email_provider_circuit_open gauge\n")
		for _, p := range ps {
			v := 0
			if p.CircuitState != "closed" {
				v = 1
			}
			b.WriteString(`email_provider_circuit_open{provider="` + p.Name + `"} ` + toString(v) + "\n")
		}
	}
	_, _ = w.Write([]byte(b.String()))
}

func toString(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}

type rateRule = ratelimit.Rule
