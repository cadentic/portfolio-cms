package api

import (
	"context"
	"encoding/csv"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/cadentic/portfolio-cms/backend/internal/alerts"
	"github.com/cadentic/portfolio-cms/backend/internal/audit"
	"github.com/cadentic/portfolio-cms/backend/internal/auth"
	"github.com/cadentic/portfolio-cms/backend/internal/campaigns"
	"github.com/cadentic/portfolio-cms/backend/internal/content"
	"github.com/cadentic/portfolio-cms/backend/internal/email"
	"github.com/cadentic/portfolio-cms/backend/internal/providers"
	"github.com/cadentic/portfolio-cms/backend/internal/queue"
	"github.com/cadentic/portfolio-cms/backend/internal/render"
	"github.com/cadentic/portfolio-cms/backend/internal/settings"
	"github.com/cadentic/portfolio-cms/backend/internal/subscribers"
	"github.com/cadentic/portfolio-cms/backend/internal/suppression"
	"github.com/cadentic/portfolio-cms/backend/internal/templates"
)

func (s *Server) adminRoutes(m *http.ServeMux) {
	m.HandleFunc("GET /api/admin/me", s.me)
	m.HandleFunc("GET /api/admin/dashboard", s.dashboard)

	m.HandleFunc("GET /api/admin/subscribers", s.listSubscribers)
	m.HandleFunc("POST /api/admin/subscribers", s.createSubscriber)
	m.HandleFunc("GET /api/admin/subscribers/export.csv", s.exportSubscribers)
	m.HandleFunc("GET /api/admin/subscribers/stats", s.subscriberStats)
	m.HandleFunc("POST /api/admin/subscribers/import/preview", s.importPreview)
	m.HandleFunc("POST /api/admin/subscribers/import/commit", s.importCommit)
	m.HandleFunc("GET /api/admin/subscribers/{id}", s.getSubscriber)
	m.HandleFunc("PATCH /api/admin/subscribers/{id}", s.updateSubscriber)
	m.HandleFunc("POST /api/admin/subscribers/{id}/unsubscribe", s.adminUnsubscribe)
	m.HandleFunc("DELETE /api/admin/subscribers/{id}", s.eraseSubscriber)
	m.HandleFunc("POST /api/admin/segments/preview", s.segmentPreview)

	m.HandleFunc("GET /api/admin/campaigns", s.listCampaigns)
	m.HandleFunc("POST /api/admin/campaigns", s.createCampaign)
	m.HandleFunc("GET /api/admin/campaigns/{id}", s.getCampaign)
	m.HandleFunc("PATCH /api/admin/campaigns/{id}", s.updateCampaign)
	m.HandleFunc("DELETE /api/admin/campaigns/{id}", s.deleteCampaign)
	m.HandleFunc("GET /api/admin/campaigns/{id}/preview", s.previewCampaign)
	m.HandleFunc("POST /api/admin/campaigns/{id}/audience", s.campaignAudience)
	m.HandleFunc("POST /api/admin/campaigns/{id}/test", s.testCampaign)
	m.HandleFunc("POST /api/admin/campaigns/{id}/send", s.sendCampaign)
	m.HandleFunc("POST /api/admin/campaigns/{id}/cancel", s.cancelCampaign)
	m.HandleFunc("GET /api/admin/campaigns/{id}/recipients", s.campaignRecipients)
	m.HandleFunc("GET /api/admin/campaigns/{id}/report.csv", s.campaignReport)

	m.HandleFunc("GET /api/admin/templates", s.listTemplates)
	m.HandleFunc("POST /api/admin/templates", s.createTemplate)
	m.HandleFunc("GET /api/admin/templates/{id}", s.getTemplate)
	m.HandleFunc("PUT /api/admin/templates/{id}", s.updateTemplate)
	m.HandleFunc("DELETE /api/admin/templates/{id}", s.deleteTemplate)
	m.HandleFunc("GET /api/admin/templates/{id}/preview", s.previewTemplate)

	m.HandleFunc("GET /api/admin/providers", s.listProviders)
	m.HandleFunc("POST /api/admin/providers", s.createProvider)
	m.HandleFunc("POST /api/admin/providers/reorder", s.reorderProviders)
	m.HandleFunc("PATCH /api/admin/providers/{id}", s.updateProvider)
	m.HandleFunc("DELETE /api/admin/providers/{id}", s.deleteProvider)
	m.HandleFunc("POST /api/admin/providers/{id}/health", s.providerHealth)
	m.HandleFunc("POST /api/admin/providers/{id}/test", s.providerTest)

	m.HandleFunc("GET /api/admin/settings/email", s.getEmailSettings)
	m.HandleFunc("PUT /api/admin/settings/email", s.putEmailSettings)
	m.HandleFunc("GET /api/admin/settings/delivery", s.getDeliverySettings)
	m.HandleFunc("PUT /api/admin/settings/delivery", s.putDeliverySettings)

	m.HandleFunc("GET /api/admin/email/monitor", s.monitor)
	m.HandleFunc("GET /api/admin/email/jobs", s.listJobs)
	m.HandleFunc("GET /api/admin/email/jobs/export.csv", s.exportJobs)
	m.HandleFunc("POST /api/admin/email/jobs/replay-dead-letters", s.replayDeadLetters)
	m.HandleFunc("GET /api/admin/email/jobs/{id}", s.getJob)
	m.HandleFunc("POST /api/admin/email/jobs/{id}/retry", s.retryJob)
	m.HandleFunc("POST /api/admin/email/jobs/{id}/cancel", s.cancelJob)
	m.HandleFunc("POST /api/admin/email/jobs/{id}/resolve", s.resolveJob)

	m.HandleFunc("GET /api/admin/suppressions", s.listSuppressions)
	m.HandleFunc("POST /api/admin/suppressions", s.addSuppression)
	m.HandleFunc("POST /api/admin/suppressions/{id}/lift", s.liftSuppression)

	m.HandleFunc("GET /api/admin/alerts", s.listAlerts)
	m.HandleFunc("POST /api/admin/alerts/{id}/ack", s.ackAlert)
	m.HandleFunc("GET /api/admin/audit", s.listAudit)

	m.HandleFunc("GET /api/admin/pages", s.listPages)
	m.HandleFunc("POST /api/admin/pages", s.createPage)
	m.HandleFunc("GET /api/admin/pages/{id}", s.getPageAdmin)
	m.HandleFunc("PATCH /api/admin/pages/{id}", s.updatePage)
	m.HandleFunc("DELETE /api/admin/pages/{id}", s.deletePage)
	m.HandleFunc("POST /api/admin/pages/{id}/publish", s.publishPage)
	m.HandleFunc("POST /api/admin/pages/{id}/unpublish", s.unpublishPage)
	m.HandleFunc("POST /api/admin/pages/markdown", s.markdownToBlocks)

	m.HandleFunc("GET /api/admin/contact-messages", s.listContactMessages)
	m.HandleFunc("GET /api/admin/mcp/connections", s.mcpConnections)
	m.HandleFunc("POST /api/admin/mcp/revoke-all", s.mcpRevokeAll)
}

func actor(r *http.Request) string { return auth.FromContext(r.Context()).Actor() }

func (s *Server) audit(r *http.Request, action, entityType, entityID string, details map[string]any) {
	if err := audit.Log(r.Context(), s.DB, actor(r), action, entityType, entityID, details, s.clientIP(r)); err != nil {
		s.Log.Error("audit log failed", "err", err)
	}
}

func (s *Server) me(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, auth.FromContext(r.Context()))
}

func (s *Server) dashboard(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	st, err := s.Subscribers.Stats(ctx)
	if err != nil {
		s.internal(w, r, err)
		return
	}
	mon, err := queue.GetMonitor(ctx, s.DB, 24)
	if err != nil {
		s.internal(w, r, err)
		return
	}
	cs, _, _ := s.Campaigns.List(ctx, "", 5, 0)
	ps, _ := s.Providers.List(ctx)
	al, _ := s.Alerts.List(ctx, false, 10)
	var recent []map[string]any
	rows, err := s.DB.Query(ctx, `(SELECT 'subscriber' AS kind, email::text AS label, status AS detail, created_at FROM subscribers ORDER BY created_at DESC LIMIT 5)
		UNION ALL (SELECT 'campaign', name, status, updated_at FROM email_campaigns WHERE status <> 'draft' ORDER BY updated_at DESC LIMIT 5)
		UNION ALL (SELECT 'failover', provider_name, left(failover_reason, 120), started_at FROM email_attempts WHERE failover_reason IS NOT NULL ORDER BY started_at DESC LIMIT 5)
		ORDER BY 4 DESC LIMIT 10`)
	if err == nil {
		for rows.Next() {
			var kind, label, detail string
			var at time.Time
			if rows.Scan(&kind, &label, &detail, &at) == nil {
				if kind == "subscriber" {
					label = email.MaskEmail(label)
				}
				recent = append(recent, map[string]any{"kind": kind, "label": label, "detail": detail, "at": at})
			}
		}
		rows.Close()
	}
	writeJSON(w, 200, map[string]any{"subscribers": st, "monitor": mon, "campaigns": cs, "providers": ps, "alerts": al, "activity": recent})
}

// --- subscribers ------------------------------------------------------------------

func parseFilter(r *http.Request) subscribers.Filter {
	q := r.URL.Query()
	f := subscribers.Filter{Query: strings.TrimSpace(q.Get("q")), Status: q.Get("status"), Source: q.Get("source"),
		Topic: q.Get("topic"), Sort: q.Get("sort"), Page: atoiDefault(q.Get("page"), 1), PageSize: atoiDefault(q.Get("page_size"), 25),
		Eligible: q.Get("eligible") == "true"}
	if t, err := time.Parse("2006-01-02", q.Get("from")); err == nil {
		f.From = &t
	}
	if t, err := time.Parse("2006-01-02", q.Get("to")); err == nil {
		t = t.Add(24 * time.Hour)
		f.To = &t
	}
	return f
}

func (s *Server) subscriberStats(w http.ResponseWriter, r *http.Request) {
	st, err := s.Subscribers.Stats(r.Context())
	if err != nil {
		s.internal(w, r, err)
		return
	}
	writeJSON(w, 200, st)
}

func (s *Server) listSubscribers(w http.ResponseWriter, r *http.Request) {
	f := parseFilter(r)
	items, total, err := s.Subscribers.List(r.Context(), f)
	if err != nil {
		s.internal(w, r, err)
		return
	}
	writeJSON(w, 200, map[string]any{"items": items, "total": total, "page": f.Page, "page_size": f.PageSize})
}

func (s *Server) getSubscriber(w http.ResponseWriter, r *http.Request) {
	d, err := s.Subscribers.Get(r.Context(), r.PathValue("id"))
	if errors.Is(err, subscribers.ErrNotFound) {
		writeError(w, 404, "subscriber not found")
		return
	}
	if err != nil {
		s.internal(w, r, err)
		return
	}
	writeJSON(w, 200, d)
}

func (s *Server) createSubscriber(w http.ResponseWriter, r *http.Request) {
	var in subscribers.ManualInput
	if !decode(w, r, &in) {
		return
	}
	id, err := s.Subscribers.CreateManual(r.Context(), in, actor(r))
	if err != nil {
		writeError(w, 400, err.Error())
		return
	}
	s.audit(r, "subscriber.create_manual", "subscriber", id, map[string]any{"lawful_basis": in.LawfulBasis, "send_confirmation": in.SendConfirmation == nil || *in.SendConfirmation})
	writeJSON(w, 201, map[string]string{"id": id})
}

func (s *Server) updateSubscriber(w http.ResponseWriter, r *http.Request) {
	var in subscribers.UpdateInput
	if !decode(w, r, &in) {
		return
	}
	if err := s.Subscribers.Update(r.Context(), r.PathValue("id"), in); err != nil {
		s.internal(w, r, err)
		return
	}
	s.audit(r, "subscriber.update", "subscriber", r.PathValue("id"), nil)
	writeJSON(w, 200, map[string]string{"status": "ok"})
}

func (s *Server) adminUnsubscribe(w http.ResponseWriter, r *http.Request) {
	if err := s.Subscribers.AdminUnsubscribe(r.Context(), r.PathValue("id"), actor(r)); err != nil {
		writeError(w, 404, err.Error())
		return
	}
	s.audit(r, "subscriber.unsubscribe", "subscriber", r.PathValue("id"), nil)
	writeJSON(w, 200, map[string]string{"status": "unsubscribed"})
}

func (s *Server) eraseSubscriber(w http.ResponseWriter, r *http.Request) {
	if r.URL.Query().Get("confirm") != "true" {
		writeError(w, 400, "add ?confirm=true to permanently erase this subscriber's personal data")
		return
	}
	if err := s.Subscribers.Erase(r.Context(), r.PathValue("id"), actor(r)); err != nil {
		writeError(w, 404, err.Error())
		return
	}
	s.audit(r, "subscriber.erase", "subscriber", r.PathValue("id"), nil)
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) exportSubscribers(w http.ResponseWriter, r *http.Request) {
	f := parseFilter(r)
	if r.URL.Query().Get("eligible") != "false" {
		f.Eligible = true // default: only eligible (verified, consented, unsuppressed) records
	}
	if !s.allow(r.Context(), map[string]rateRule{"export:" + actor(r): {Limit: 20, Window: time.Hour}}) {
		writeError(w, 429, "too many exports")
		return
	}
	s.audit(r, "subscriber.export", "subscriber", "", map[string]any{"eligible_only": f.Eligible})
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="subscribers-%s.csv"`, time.Now().Format("2006-01-02")))
	if err := s.Subscribers.ExportCSV(r.Context(), w, f); err != nil {
		s.Log.Error("export failed", "err", err)
	}
}

func (s *Server) importPreview(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 11<<20)
	file, _, err := r.FormFile("file")
	if err != nil {
		writeError(w, 400, "upload a CSV file in the 'file' field")
		return
	}
	defer file.Close()
	p, err := s.Subscribers.PreviewImport(r.Context(), file, actor(r))
	if err != nil {
		writeError(w, 400, err.Error())
		return
	}
	s.audit(r, "subscriber.import_preview", "subscriber", "", map[string]any{"summary": p.Summary})
	writeJSON(w, 200, p)
}

func (s *Server) importCommit(w http.ResponseWriter, r *http.Request) {
	var in subscribers.CommitInput
	if !decode(w, r, &in) {
		return
	}
	if !s.allow(r.Context(), map[string]rateRule{"import:" + actor(r): {Limit: 10, Window: time.Hour}}) {
		writeError(w, 429, "too many imports; try again later")
		return
	}
	res, err := s.Subscribers.CommitImport(r.Context(), in, actor(r))
	if err != nil {
		writeError(w, 400, err.Error())
		return
	}
	s.audit(r, "subscriber.import_commit", "subscriber", "", map[string]any{"imported": res.Imported, "skipped": res.Skipped,
		"lawful_basis": in.LawfulBasis, "send_confirmation": in.SendConfirmation == nil || *in.SendConfirmation})
	writeJSON(w, 200, res)
}

func (s *Server) segmentPreview(w http.ResponseWriter, r *http.Request) {
	var seg campaigns.Segment
	if !decode(w, r, &seg) {
		return
	}
	a, err := s.Campaigns.AudienceFor(r.Context(), seg)
	if err != nil {
		s.internal(w, r, err)
		return
	}
	writeJSON(w, 200, a)
}

// --- campaigns ---------------------------------------------------------------------

func (s *Server) listCampaigns(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	items, total, err := s.Campaigns.List(r.Context(), q.Get("status"), atoiDefault(q.Get("limit"), 25), atoiDefault(q.Get("offset"), 0))
	if err != nil {
		s.internal(w, r, err)
		return
	}
	writeJSON(w, 200, map[string]any{"items": items, "total": total})
}

func (s *Server) campaignErr(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, campaigns.ErrNotFound):
		writeError(w, 404, err.Error())
	case errors.Is(err, campaigns.ErrNotDraft), errors.Is(err, campaigns.ErrAudienceDrift), errors.Is(err, campaigns.ErrSimilarRecent):
		writeError(w, 409, err.Error())
	default:
		writeError(w, 400, err.Error())
	}
}

func (s *Server) createCampaign(w http.ResponseWriter, r *http.Request) {
	var in campaigns.Input
	if !decode(w, r, &in) {
		return
	}
	c, err := s.Campaigns.Create(r.Context(), in, actor(r))
	if err != nil {
		s.campaignErr(w, r, err)
		return
	}
	s.audit(r, "campaign.create", "campaign", c.ID, map[string]any{"name": c.Name})
	writeJSON(w, 201, c)
}

func (s *Server) getCampaign(w http.ResponseWriter, r *http.Request) {
	c, err := s.Campaigns.Get(r.Context(), r.PathValue("id"))
	if err != nil {
		s.campaignErr(w, r, err)
		return
	}
	writeJSON(w, 200, c)
}

func (s *Server) updateCampaign(w http.ResponseWriter, r *http.Request) {
	var in campaigns.Input
	if !decode(w, r, &in) {
		return
	}
	c, err := s.Campaigns.Update(r.Context(), r.PathValue("id"), in)
	if err != nil {
		s.campaignErr(w, r, err)
		return
	}
	writeJSON(w, 200, c)
}

func (s *Server) deleteCampaign(w http.ResponseWriter, r *http.Request) {
	if err := s.Campaigns.Delete(r.Context(), r.PathValue("id")); err != nil {
		s.campaignErr(w, r, err)
		return
	}
	s.audit(r, "campaign.delete", "campaign", r.PathValue("id"), nil)
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) previewCampaign(w http.ResponseWriter, r *http.Request) {
	p, err := s.Campaigns.Preview(r.Context(), r.PathValue("id"))
	if err != nil {
		s.campaignErr(w, r, err)
		return
	}
	writeJSON(w, 200, p)
}

func (s *Server) campaignAudience(w http.ResponseWriter, r *http.Request) {
	c, err := s.Campaigns.Get(r.Context(), r.PathValue("id"))
	if err != nil {
		s.campaignErr(w, r, err)
		return
	}
	a, err := s.Campaigns.AudienceFor(r.Context(), c.Segment)
	if err != nil {
		s.internal(w, r, err)
		return
	}
	writeJSON(w, 200, a)
}

func (s *Server) adminEmails() []string {
	return s.Authorizer.Emails
}

func (s *Server) testCampaign(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Recipients []string `json:"recipients"`
	}
	if !decode(w, r, &in) {
		return
	}
	if !s.allow(r.Context(), map[string]rateRule{"test_email:" + actor(r): {Limit: 20, Window: time.Hour}}) {
		writeError(w, 429, "test-send limit reached; try again later")
		return
	}
	allowed := append([]string{auth.FromContext(r.Context()).Email}, s.adminEmails()...)
	n, err := s.Campaigns.TestSend(r.Context(), r.PathValue("id"), in.Recipients, allowed)
	if err != nil {
		s.campaignErr(w, r, err)
		return
	}
	s.audit(r, "campaign.test_send", "campaign", r.PathValue("id"), map[string]any{"recipients": len(in.Recipients)})
	writeJSON(w, 202, map[string]int{"queued": n})
}

func (s *Server) sendCampaign(w http.ResponseWriter, r *http.Request) {
	var in campaigns.SendInput
	if !decode(w, r, &in) {
		return
	}
	if !s.allow(r.Context(), map[string]rateRule{"bulk_send:" + actor(r): {Limit: 10, Window: time.Hour}}) {
		writeError(w, 429, "too many send requests")
		return
	}
	c, err := s.Campaigns.Approve(r.Context(), r.PathValue("id"), in, actor(r))
	if err != nil {
		s.campaignErr(w, r, err)
		return
	}
	s.audit(r, "campaign.approve_send", "campaign", c.ID, map[string]any{"audience_count": in.ExpectedCount,
		"scheduled_at": in.ScheduledAt, "allow_similar_recent": in.AllowSimilarRecent})
	writeJSON(w, 200, c)
}

func (s *Server) cancelCampaign(w http.ResponseWriter, r *http.Request) {
	if err := s.Campaigns.Cancel(r.Context(), r.PathValue("id")); err != nil {
		s.campaignErr(w, r, err)
		return
	}
	s.audit(r, "campaign.cancel", "campaign", r.PathValue("id"), nil)
	writeJSON(w, 200, map[string]string{"status": "cancelled"})
}

func (s *Server) campaignRecipients(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	rows, err := s.Campaigns.Recipients(r.Context(), r.PathValue("id"), atoiDefault(q.Get("limit"), 100), atoiDefault(q.Get("offset"), 0))
	if err != nil {
		s.internal(w, r, err)
		return
	}
	writeJSON(w, 200, map[string]any{"items": rows})
}

func (s *Server) campaignReport(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="campaign-`+r.PathValue("id")+`.csv"`)
	if err := s.Campaigns.ReportCSV(r.Context(), r.PathValue("id"), w); err != nil {
		s.Log.Error("campaign report", "err", err)
	}
}

// --- templates -------------------------------------------------------------------

func (s *Server) listTemplates(w http.ResponseWriter, r *http.Request) {
	t, err := templates.List(r.Context(), s.DB)
	if err != nil {
		s.internal(w, r, err)
		return
	}
	writeJSON(w, 200, map[string]any{"items": t})
}

func (s *Server) getTemplate(w http.ResponseWriter, r *http.Request) {
	t, err := templates.Get(r.Context(), s.DB, r.PathValue("id"))
	if err != nil {
		writeError(w, 404, "template not found")
		return
	}
	writeJSON(w, 200, t)
}

func (s *Server) createTemplate(w http.ResponseWriter, r *http.Request) {
	var in templates.Input
	if !decode(w, r, &in) {
		return
	}
	in.HTMLBody = render.Sanitize(in.HTMLBody)
	t, err := templates.Create(r.Context(), s.DB, in)
	if err != nil {
		writeError(w, 400, err.Error())
		return
	}
	s.audit(r, "template.create", "template", t.ID, map[string]any{"slug": t.Slug})
	writeJSON(w, 201, t)
}

func (s *Server) updateTemplate(w http.ResponseWriter, r *http.Request) {
	var in templates.Input
	if !decode(w, r, &in) {
		return
	}
	in.HTMLBody = render.Sanitize(in.HTMLBody)
	t, err := templates.Update(r.Context(), s.DB, r.PathValue("id"), in)
	if err != nil {
		writeError(w, 400, err.Error())
		return
	}
	s.audit(r, "template.update", "template", t.ID, nil)
	writeJSON(w, 200, t)
}

func (s *Server) deleteTemplate(w http.ResponseWriter, r *http.Request) {
	if err := templates.Delete(r.Context(), s.DB, r.PathValue("id")); err != nil {
		writeError(w, 400, err.Error())
		return
	}
	s.audit(r, "template.delete", "template", r.PathValue("id"), nil)
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) previewTemplate(w http.ResponseWriter, r *http.Request) {
	t, err := templates.Get(r.Context(), s.DB, r.PathValue("id"))
	if err != nil {
		writeError(w, 404, "template not found")
		return
	}
	es, _ := s.Settings.Email(r.Context())
	vars := map[string]string{"first_name": "Sample", "name": "Sample Reader", "site_name": es.OrganizationName,
		"confirm_url": s.Cfg.PublicSiteURL + "/subscribe/confirm?token=example", "ttl_hours": fmt.Sprint(es.VerificationTTLHours),
		"code": "123456", "title": "Sample article title", "excerpt": "A short summary of the article.",
		"url": s.Cfg.PublicSiteURL + "/blog/sample", "author": es.SenderName, "published_date": time.Now().Format("2 Jan 2006"),
		"subject": t.Subject, "sender_name": "Visitor", "sender_email": "visitor@example.com", "message": "Hello!",
		"kind": "example", "severity": "warning", "admin_url": s.Cfg.PublicSiteURL + "/admin/email/monitor"}
	h, txt, err := render.Render(render.Input{Marketing: t.Category == "marketing", Subject: render.Fill(t.Subject, vars, false),
		Preheader: render.Fill(t.Preheader, vars, false), ContentHTML: t.HTMLBody, Vars: vars,
		RawVars:        map[string]string{"content": "<p>Your campaign content appears here.</p>", "featured_image": ""},
		UnsubscribeURL: s.Cfg.PublicSiteURL + "/unsubscribe?token=example", SiteURL: s.Cfg.PublicSiteURL, Settings: es})
	if err != nil {
		s.internal(w, r, err)
		return
	}
	writeJSON(w, 200, map[string]string{"subject": render.Fill(t.Subject, vars, false), "html": h, "text": txt})
}

// --- providers ------------------------------------------------------------------

func (s *Server) listProviders(w http.ResponseWriter, r *http.Request) {
	ps, err := s.Providers.List(r.Context())
	if err != nil {
		s.internal(w, r, err)
		return
	}
	writeJSON(w, 200, map[string]any{"items": ps, "kinds": s.Registry.Kinds()})
}

func (s *Server) createProvider(w http.ResponseWriter, r *http.Request) {
	var in providers.Input
	if !decode(w, r, &in) {
		return
	}
	p, err := s.Providers.Create(r.Context(), in)
	if err != nil {
		writeError(w, 400, err.Error())
		return
	}
	s.audit(r, "provider.create", "provider", p.ID, map[string]any{"name": p.Name, "kind": p.Kind})
	writeJSON(w, 201, p)
}

func (s *Server) updateProvider(w http.ResponseWriter, r *http.Request) {
	var in providers.Input
	if !decode(w, r, &in) {
		return
	}
	p, err := s.Providers.Update(r.Context(), r.PathValue("id"), in)
	if errors.Is(err, providers.ErrNotFound) {
		writeError(w, 404, err.Error())
		return
	}
	if err != nil {
		writeError(w, 400, err.Error())
		return
	}
	s.audit(r, "provider.update", "provider", p.ID, map[string]any{"enabled": p.Enabled, "priority": p.Priority, "streams": p.Streams})
	writeJSON(w, 200, p)
}

func (s *Server) deleteProvider(w http.ResponseWriter, r *http.Request) {
	if err := s.Providers.Delete(r.Context(), r.PathValue("id")); err != nil {
		s.internal(w, r, err)
		return
	}
	s.audit(r, "provider.delete", "provider", r.PathValue("id"), nil)
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) reorderProviders(w http.ResponseWriter, r *http.Request) {
	var in struct {
		IDs []string `json:"ids"`
	}
	if !decode(w, r, &in) {
		return
	}
	if err := s.Providers.Reorder(r.Context(), in.IDs); err != nil {
		s.internal(w, r, err)
		return
	}
	s.audit(r, "provider.reorder", "provider", "", map[string]any{"order": in.IDs})
	s.listProviders(w, r)
}

func (s *Server) providerHealth(w http.ResponseWriter, r *http.Request) {
	p, err := s.Providers.Get(r.Context(), r.PathValue("id"))
	if err != nil {
		writeError(w, 404, "provider not found")
		return
	}
	a, _ := s.Registry.Get(p.Kind)
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	h := a.HealthCheck(ctx, s.Providers.RuntimeConfig(p))
	_ = s.Providers.RecordHealth(r.Context(), p.ID, h)
	writeJSON(w, 200, h)
}

// providerTest sends a real email directly through one provider (bypassing the
// failover chain) to an allow-listed address, so the owner can verify each
// provider independently.
func (s *Server) providerTest(w http.ResponseWriter, r *http.Request) {
	var in struct {
		To string `json:"to"`
	}
	if !decode(w, r, &in) {
		return
	}
	es, _ := s.Settings.Email(r.Context())
	me := auth.FromContext(r.Context()).Email
	to := strings.ToLower(strings.TrimSpace(in.To))
	if to == "" {
		to = me
	}
	allowed := append(append([]string{me}, s.adminEmails()...), es.TestRecipients...)
	if !slices.ContainsFunc(allowed, func(a string) bool { return strings.EqualFold(a, to) }) {
		writeError(w, 400, "test emails can only go to your own address or configured test recipients")
		return
	}
	if !s.allow(r.Context(), map[string]rateRule{"provider_test:" + actor(r): {Limit: 10, Window: time.Hour}}) {
		writeError(w, 429, "test-send limit reached")
		return
	}
	p, err := s.Providers.Get(r.Context(), r.PathValue("id"))
	if err != nil {
		writeError(w, 404, "provider not found")
		return
	}
	a, _ := s.Registry.Get(p.Kind)
	from := email.Address{Email: firstNonEmpty(p.SenderEmail, es.SenderEmail), Name: firstNonEmpty(p.SenderName, es.SenderName)}
	if from.Email == "" {
		writeError(w, 400, "set a sender email on this provider or in Email settings first")
		return
	}
	h, txt, _ := render.Render(render.Input{Subject: "Provider test: " + p.Name, ContentHTML: fmt.Sprintf(
		"<p>This is a test email sent directly through <strong>%s</strong> (%s) at %s.</p><p>If you can read this, the API key and sender are working. Check SPF/DKIM/DMARC results in the message headers.</p>",
		p.Name, p.Kind, time.Now().UTC().Format(time.RFC1123)), SiteURL: s.Cfg.PublicSiteURL, Settings: es})
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	res := a.Send(ctx, s.Providers.RuntimeConfig(p), email.Message{InternalID: "test-" + fmt.Sprint(time.Now().UnixNano()),
		Category: email.CategoryTransactional, From: from, To: email.Address{Email: to}, Subject: "Provider test: " + p.Name, HTML: h, Text: txt})
	ok := res.Outcome == email.OutcomeAccepted
	_ = s.Providers.RecordTest(r.Context(), p.ID, ok)
	s.audit(r, "provider.test_send", "provider", p.ID, map[string]any{"outcome": res.Outcome, "http_status": res.HTTPStatus})
	writeJSON(w, 200, map[string]any{"ok": ok, "outcome": res.Outcome, "http_status": res.HTTPStatus,
		"provider_message_id": res.ProviderMessageID, "error": firstNonEmpty(res.ErrorMessage, res.ErrorCode),
		"note": "Accepted means the provider took the message; it does not guarantee inbox delivery."})
}

// --- settings ----------------------------------------------------------------------

func (s *Server) getEmailSettings(w http.ResponseWriter, r *http.Request) {
	e, err := s.Settings.Email(r.Context())
	if err != nil {
		s.internal(w, r, err)
		return
	}
	writeJSON(w, 200, e)
}

func (s *Server) putEmailSettings(w http.ResponseWriter, r *http.Request) {
	in := settings.DefaultEmail()
	if !decode(w, r, &in) {
		return
	}
	in.Brand.HeaderHTML = render.Sanitize(in.Brand.HeaderHTML)
	in.Brand.FooterHTML = render.Sanitize(in.Brand.FooterHTML)
	if err := s.Settings.SaveEmail(r.Context(), in, actor(r)); err != nil {
		writeError(w, 400, err.Error())
		return
	}
	s.audit(r, "settings.email", "settings", "email", map[string]any{"double_opt_in": in.DoubleOptIn, "track_opens": in.TrackOpens, "track_clicks": in.TrackClicks})
	s.getEmailSettings(w, r)
}

func (s *Server) getDeliverySettings(w http.ResponseWriter, r *http.Request) {
	d, err := s.Settings.Delivery(r.Context())
	if err != nil {
		s.internal(w, r, err)
		return
	}
	writeJSON(w, 200, d)
}

func (s *Server) putDeliverySettings(w http.ResponseWriter, r *http.Request) {
	in := settings.DefaultDelivery()
	if !decode(w, r, &in) {
		return
	}
	if err := s.Settings.SaveDelivery(r.Context(), in, actor(r)); err != nil {
		writeError(w, 400, err.Error())
		return
	}
	s.audit(r, "settings.delivery", "settings", "delivery", map[string]any{"settings": in})
	s.getDeliverySettings(w, r)
}

// --- monitoring & jobs ---------------------------------------------------------------

func (s *Server) monitor(w http.ResponseWriter, r *http.Request) {
	m, err := queue.GetMonitor(r.Context(), s.DB, atoiDefault(r.URL.Query().Get("hours"), 24))
	if err != nil {
		s.internal(w, r, err)
		return
	}
	ps, _ := s.Providers.List(r.Context())
	al, _ := s.Alerts.List(r.Context(), false, 20)
	writeJSON(w, 200, map[string]any{"monitor": m, "providers": ps, "alerts": al})
}

func (s *Server) jobFilter(r *http.Request) queue.JobFilter {
	q := r.URL.Query()
	return queue.JobFilter{Status: q.Get("status"), Category: q.Get("category"), CampaignID: q.Get("campaign_id"),
		Email: q.Get("email"), Limit: atoiDefault(q.Get("limit"), 50), Offset: atoiDefault(q.Get("offset"), 0)}
}

func (s *Server) listJobs(w http.ResponseWriter, r *http.Request) {
	jobs, total, err := queue.ListJobs(r.Context(), s.DB, s.jobFilter(r))
	if err != nil {
		s.internal(w, r, err)
		return
	}
	writeJSON(w, 200, map[string]any{"items": jobs, "total": total})
}

func (s *Server) exportJobs(w http.ResponseWriter, r *http.Request) {
	f := s.jobFilter(r)
	f.Limit = 200
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="delivery-report-%s.csv"`, time.Now().Format("2006-01-02")))
	cw := csv.NewWriter(w)
	_ = cw.Write([]string{"job_id", "created_at", "category", "kind", "to", "subject", "status", "attempts", "failovers", "provider_message_id", "accepted_at", "delivered_at", "last_error"})
	for off := 0; off < 100000; off += f.Limit {
		f.Offset = off
		jobs, _, err := queue.ListJobs(r.Context(), s.DB, f)
		if err != nil || len(jobs) == 0 {
			break
		}
		for _, j := range jobs {
			_ = cw.Write([]string{j.ID, j.CreatedAt.UTC().Format(time.RFC3339), string(j.Category), j.Kind, j.ToEmail, j.Subject, j.Status,
				fmt.Sprint(j.AttemptCount), fmt.Sprint(j.FailoverCount), j.ProviderMessageID, tsp(j.AcceptedAt), tsp(j.DeliveredAt), j.LastError})
		}
	}
	cw.Flush()
}

func (s *Server) getJob(w http.ResponseWriter, r *http.Request) {
	j, err := queue.GetJob(r.Context(), s.DB, r.PathValue("id"))
	if err != nil {
		writeError(w, 404, "job not found")
		return
	}
	attempts, _ := queue.ListAttempts(r.Context(), s.DB, j.ID)
	events, _ := queue.ListJobEvents(r.Context(), s.DB, j.ID)
	writeJSON(w, 200, map[string]any{"job": j, "attempts": attempts, "events": events, "html": j.HTMLBody})
}

func (s *Server) jobAction(w http.ResponseWriter, r *http.Request, action string, fn func() error) {
	if err := fn(); err != nil {
		if errors.Is(err, queue.ErrInvalidState) {
			writeError(w, 409, err.Error())
			return
		}
		writeError(w, 400, err.Error())
		return
	}
	s.audit(r, "email_job."+action, "email_job", r.PathValue("id"), nil)
	writeJSON(w, 200, map[string]string{"status": "ok"})
}

func (s *Server) retryJob(w http.ResponseWriter, r *http.Request) {
	s.jobAction(w, r, "retry", func() error { return queue.Retry(r.Context(), s.DB, r.PathValue("id")) })
}

func (s *Server) cancelJob(w http.ResponseWriter, r *http.Request) {
	s.jobAction(w, r, "cancel", func() error { return queue.Cancel(r.Context(), s.DB, r.PathValue("id")) })
}

func (s *Server) resolveJob(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Resolution string `json:"resolution"`
		Confirm    bool   `json:"confirm"`
	}
	if !decode(w, r, &in) {
		return
	}
	if in.Resolution == "resend" && !in.Confirm {
		writeError(w, 400, "resending an email whose acceptance is unknown may create a duplicate; confirm explicitly")
		return
	}
	s.jobAction(w, r, "resolve_"+in.Resolution, func() error { return queue.ResolveUnknown(r.Context(), s.DB, r.PathValue("id"), in.Resolution) })
}

func (s *Server) replayDeadLetters(w http.ResponseWriter, r *http.Request) {
	var in struct {
		IDs     []string `json:"ids"`
		Confirm bool     `json:"confirm"`
	}
	if !decode(w, r, &in) {
		return
	}
	if !in.Confirm {
		writeError(w, 400, "explicit confirmation is required to replay dead-letter jobs")
		return
	}
	if !s.allow(r.Context(), map[string]rateRule{"replay:" + actor(r): {Limit: 10, Window: time.Hour}}) {
		writeError(w, 429, "too many replays")
		return
	}
	if in.IDs == nil {
		in.IDs = []string{}
	}
	n, err := queue.ReplayDeadLetters(r.Context(), s.DB, in.IDs)
	if err != nil {
		s.internal(w, r, err)
		return
	}
	s.audit(r, "email_job.replay_dead_letters", "email_job", "", map[string]any{"count": n, "ids": in.IDs})
	writeJSON(w, 200, map[string]int64{"replayed": n})
}

// --- suppressions, alerts, audit -------------------------------------------------------

func (s *Server) listSuppressions(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	items, total, err := suppression.List(r.Context(), s.DB, q.Get("q"), q.Get("reason"), q.Get("all") != "true",
		atoiDefault(q.Get("limit"), 50), atoiDefault(q.Get("offset"), 0))
	if err != nil {
		s.internal(w, r, err)
		return
	}
	writeJSON(w, 200, map[string]any{"items": items, "total": total})
}

func (s *Server) addSuppression(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Email string `json:"email"`
		Scope string `json:"scope"`
		Note  string `json:"note"`
	}
	if !decode(w, r, &in) {
		return
	}
	addr, err := subscribers.NormalizeEmail(in.Email)
	if err != nil {
		writeError(w, 400, err.Error())
		return
	}
	if in.Scope != suppression.ScopeAll {
		in.Scope = suppression.ScopeMarketing
	}
	if err := suppression.Add(r.Context(), s.DB, addr, in.Scope, "manual", "admin", nil, in.Note); err != nil {
		s.internal(w, r, err)
		return
	}
	s.audit(r, "suppression.add", "suppression", addr, map[string]any{"scope": in.Scope})
	writeJSON(w, 201, map[string]string{"status": "suppressed"})
}

func (s *Server) liftSuppression(w http.ResponseWriter, r *http.Request) {
	if err := suppression.Lift(r.Context(), s.DB, r.PathValue("id"), actor(r)); err != nil {
		s.internal(w, r, err)
		return
	}
	s.audit(r, "suppression.lift", "suppression", r.PathValue("id"), nil)
	writeJSON(w, 200, map[string]string{"status": "lifted"})
}

func (s *Server) listAlerts(w http.ResponseWriter, r *http.Request) {
	a, err := s.Alerts.List(r.Context(), r.URL.Query().Get("all") == "true", 100)
	if err != nil {
		s.internal(w, r, err)
		return
	}
	writeJSON(w, 200, map[string]any{"items": a})
}

func (s *Server) ackAlert(w http.ResponseWriter, r *http.Request) {
	if err := s.Alerts.Ack(r.Context(), r.PathValue("id"), actor(r)); err != nil {
		s.internal(w, r, err)
		return
	}
	writeJSON(w, 200, map[string]string{"status": "acknowledged"})
}

func (s *Server) listAudit(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	items, err := audit.List(r.Context(), s.DB, q.Get("entity_type"), atoiDefault(q.Get("limit"), 50), atoiDefault(q.Get("offset"), 0))
	if err != nil {
		s.internal(w, r, err)
		return
	}
	writeJSON(w, 200, map[string]any{"items": items})
}

// --- pages & articles -----------------------------------------------------------------

func (s *Server) listPages(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	items, total, err := s.Content.List(r.Context(), content.ListFilter{Kind: q.Get("kind"), Status: q.Get("status"),
		Query: q.Get("q"), Limit: atoiDefault(q.Get("limit"), 25), Offset: atoiDefault(q.Get("offset"), 0)})
	if err != nil {
		s.internal(w, r, err)
		return
	}
	writeJSON(w, 200, map[string]any{"items": items, "total": total})
}

func (s *Server) getPageAdmin(w http.ResponseWriter, r *http.Request) {
	p, err := s.Content.Get(r.Context(), r.PathValue("id"))
	if err != nil {
		writeError(w, 404, "page not found")
		return
	}
	writeJSON(w, 200, p)
}

func (s *Server) createPage(w http.ResponseWriter, r *http.Request) {
	var in content.Input
	if !decode(w, r, &in) {
		return
	}
	if in.AuthorName == nil {
		es, _ := s.Settings.Email(r.Context())
		in.AuthorName = &es.SenderName
	}
	p, err := s.Content.Create(r.Context(), in, actor(r))
	if err != nil {
		writeError(w, 400, err.Error())
		return
	}
	s.audit(r, "page.create", "page", p.ID, map[string]any{"title": p.Title, "kind": p.Kind})
	writeJSON(w, 201, p)
}

func (s *Server) updatePage(w http.ResponseWriter, r *http.Request) {
	var in content.Input
	if !decode(w, r, &in) {
		return
	}
	p, err := s.Content.Update(r.Context(), r.PathValue("id"), in)
	if errors.Is(err, content.ErrNotFound) {
		writeError(w, 404, err.Error())
		return
	}
	if err != nil {
		writeError(w, 400, err.Error())
		return
	}
	s.audit(r, "page.update", "page", p.ID, nil)
	writeJSON(w, 200, p)
}

func (s *Server) deletePage(w http.ResponseWriter, r *http.Request) {
	if err := s.Content.Delete(r.Context(), r.PathValue("id")); err != nil {
		s.internal(w, r, err)
		return
	}
	s.audit(r, "page.delete", "page", r.PathValue("id"), nil)
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) publishPage(w http.ResponseWriter, r *http.Request) {
	var in struct {
		NotifySubscribers bool `json:"notify_subscribers"`
	}
	if r.ContentLength > 0 && !decode(w, r, &in) {
		return
	}
	res, err := s.Content.Publish(r.Context(), r.PathValue("id"))
	if errors.Is(err, content.ErrNotFound) {
		writeError(w, 404, err.Error())
		return
	}
	if err != nil {
		s.internal(w, r, err)
		return
	}
	out := map[string]any{"page": res.Page}
	es, _ := s.Settings.Email(r.Context())
	if res.Page.Kind == "article" && res.FirstPublish && (in.NotifySubscribers || es.AutoNotifyOnPublish) {
		c, created, err := s.Campaigns.CreateBlogNotification(r.Context(), campaigns.BlogPost{
			ID: res.Page.ID, Title: res.Page.Title, Excerpt: res.Page.Excerpt, URL: s.Cfg.PublicSiteURL + res.Page.Path,
			FeaturedImageURL: absURL(s.Cfg.PublicSiteURL, firstNonEmpty(res.Page.FeaturedImageURL, res.Page.Hero.URL)),
			Author:           res.Page.AuthorName, PublishedAt: *res.Page.PublishedAt}, actor(r))
		if err != nil {
			s.Log.Error("create blog notification", "err", err)
		} else {
			out["campaign"] = c
			out["campaign_created"] = created
		}
	}
	s.audit(r, "page.publish", "page", res.Page.ID, map[string]any{"notify": in.NotifySubscribers, "first_publish": res.FirstPublish})
	writeJSON(w, 200, out)
}

func (s *Server) unpublishPage(w http.ResponseWriter, r *http.Request) {
	p, err := s.Content.Unpublish(r.Context(), r.PathValue("id"))
	if err != nil {
		writeError(w, 404, "page not found")
		return
	}
	s.audit(r, "page.unpublish", "page", p.ID, nil)
	writeJSON(w, 200, p)
}

func (s *Server) markdownToBlocks(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Markdown string `json:"markdown"`
	}
	if !decode(w, r, &in) {
		return
	}
	writeJSON(w, 200, map[string]any{"blocks": content.BlocksFromMarkdown(in.Markdown)})
}

func (s *Server) listContactMessages(w http.ResponseWriter, r *http.Request) {
	rows, err := s.DB.Query(r.Context(), `SELECT id, name, email::text, message, status, created_at FROM contact_messages
		ORDER BY created_at DESC LIMIT $1 OFFSET $2`, atoiDefault(r.URL.Query().Get("limit"), 50), atoiDefault(r.URL.Query().Get("offset"), 0))
	if err != nil {
		s.internal(w, r, err)
		return
	}
	defer rows.Close()
	items := []map[string]any{}
	for rows.Next() {
		var id, name, mail, msg, status string
		var at time.Time
		if err := rows.Scan(&id, &name, &mail, &msg, &status, &at); err != nil {
			s.internal(w, r, err)
			return
		}
		items = append(items, map[string]any{"id": id, "name": name, "email": mail, "message": msg, "status": status, "created_at": at})
	}
	writeJSON(w, 200, map[string]any{"items": items})
}

func (s *Server) mcpConnections(w http.ResponseWriter, r *http.Request) {
	c, err := s.OAuth.Connections(r.Context())
	if err != nil {
		s.internal(w, r, err)
		return
	}
	writeJSON(w, 200, map[string]any{"items": c, "mcp_url": s.OAuth.ResourceURL()})
}

func (s *Server) mcpRevokeAll(w http.ResponseWriter, r *http.Request) {
	if err := s.OAuth.RevokeAll(r.Context()); err != nil {
		s.internal(w, r, err)
		return
	}
	s.audit(r, "mcp.revoke_all", "oauth", "", nil)
	writeJSON(w, 200, map[string]string{"status": "revoked"})
}

func firstNonEmpty(v ...string) string {
	for _, s := range v {
		if s != "" {
			return s
		}
	}
	return ""
}

func absURL(site, u string) string {
	if strings.HasPrefix(u, "/") {
		return site + u
	}
	return u
}

func tsp(t *time.Time) string {
	if t == nil {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}

var _ = alerts.Info
