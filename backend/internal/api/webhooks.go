package api

import (
	"io"
	"net"
	"net/http"
	"strconv"
	"time"

	"github.com/cadentic/portfolio-cms/backend/internal/queue"
	"github.com/cadentic/portfolio-cms/backend/internal/tokens"
)

// Events older than this are rejected as possible replays; providers retry
// failed webhook deliveries for at most a day or so.
const webhookMaxAge = 7 * 24 * time.Hour

func (s *Server) webhookRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/webhooks/{providerID}", s.webhook)
}

func (s *Server) webhook(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	p, err := s.Providers.GetByRef(ctx, r.PathValue("providerID"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	adapter, ok := s.Registry.Get(p.Kind)
	if !ok {
		http.NotFound(w, r)
		return
	}
	cfg := s.Providers.RuntimeConfig(p)
	if err := adapter.VerifyWebhook(r, cfg); err != nil {
		s.Log.Warn("webhook rejected: bad credentials", "provider", p.Name, "ip", s.clientIP(r))
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 5<<20))
	if err != nil {
		writeError(w, http.StatusRequestEntityTooLarge, "payload too large")
		return
	}
	events, err := adapter.ParseWebhook(body)
	if err != nil {
		writeError(w, http.StatusBadRequest, "unrecognised payload")
		return
	}
	fresh := events[:0]
	for _, e := range events {
		if time.Since(e.OccurredAt) > webhookMaxAge || e.OccurredAt.After(time.Now().Add(time.Hour)) {
			continue // stale or implausible timestamp: treat as replay
		}
		fresh = append(fresh, e)
	}
	n, err := queue.IngestEvents(ctx, s.DB, p.ID, p.Kind, fresh)
	if err != nil {
		s.internal(w, r, err)
		return
	}
	// Apply right away; anything blocked by an in-flight job is retried by the worker.
	if err := queue.ProcessPendingEvents(ctx, s.DB, n+5); err != nil {
		s.Log.Error("process webhook events", "err", err)
	}
	s.Log.Info("webhook accepted", "provider", p.Name, "events", len(events), "new", n)
	w.WriteHeader(http.StatusOK)
}

func ipOrNil(ip string) any {
	if net.ParseIP(ip) == nil {
		return nil
	}
	return ip
}

func tokensNumeric() string { return tokens.NumericCode(6) }

func atoiDefault(s string, def int) int {
	if v, err := strconv.Atoi(s); err == nil && v >= 0 {
		return v
	}
	return def
}
