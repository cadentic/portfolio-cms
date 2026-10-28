package api

import (
	"crypto/hmac"
	"crypto/sha256"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/cadentic/portfolio-cms/backend/internal/content"
	"github.com/cadentic/portfolio-cms/backend/internal/email"
	"github.com/cadentic/portfolio-cms/backend/internal/mailer"
	"github.com/cadentic/portfolio-cms/backend/internal/ratelimit"
	"github.com/cadentic/portfolio-cms/backend/internal/subscribers"
)

func (s *Server) publicRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/public/site", s.siteConfig)
	mux.HandleFunc("POST /api/public/subscribe", s.subscribe)
	mux.HandleFunc("POST /api/public/subscribe/resend", s.resend)
	mux.HandleFunc("POST /api/public/subscribe/confirm", s.limited("confirm", 30, time.Hour, s.confirm))
	mux.HandleFunc("GET /api/public/unsubscribe", s.limited("unsub_info", 60, time.Hour, s.unsubscribeInfo))
	mux.HandleFunc("POST /api/public/unsubscribe", s.limited("unsub", 60, time.Hour, s.unsubscribe))
	// RFC 8058 one-click: mailbox providers POST "List-Unsubscribe=One-Click".
	mux.HandleFunc("POST /api/public/unsubscribe/one-click", s.unsubscribeOneClick)
	mux.HandleFunc("POST /api/public/contact", s.contact)
	mux.HandleFunc("POST /api/public/resume/request", s.resumeRequest)
	mux.HandleFunc("POST /api/public/resume/verify", s.limited("resume_verify", 20, time.Hour, s.resumeVerify))
	mux.HandleFunc("GET /api/public/resume/download", s.resumeDownload)

	mux.HandleFunc("GET /api/public/articles", s.listArticles)
	mux.HandleFunc("GET /api/public/articles/{slug}", s.getArticle)
	mux.HandleFunc("GET /api/public/pages/{path...}", s.getPage)
	mux.HandleFunc("GET /api/public/redirect", s.redirectLookup)
	mux.HandleFunc("GET /sitemap.xml", s.sitemap)
	mux.HandleFunc("GET /robots.txt", s.robots)
	mux.HandleFunc("GET /llms.txt", s.llms)
}

func (s *Server) siteConfig(w http.ResponseWriter, r *http.Request) {
	es, _ := s.Settings.Email(r.Context())
	var articles int
	_ = s.DB.QueryRow(r.Context(), `SELECT count(*) FROM cms_pages WHERE kind='article' AND status='published'`).Scan(&articles)
	w.Header().Set("Cache-Control", "public, max-age=60")
	writeJSON(w, 200, map[string]any{
		"site_url":               s.Cfg.PublicSiteURL,
		"blog_enabled":           s.Cfg.BlogEnabled && articles > 0,
		"gtm_id":                 s.Cfg.GTMID,
		"ga4_id":                 s.Cfg.GA4ID,
		"newsletter_topics":      es.Topics,
		"consent_text":           es.ConsentText,
		"privacy_policy_url":     es.PrivacyPolicyURL,
		"privacy_policy_version": es.PrivacyPolicyVersion,
		"resume_available":       s.Cfg.ResumeFilePath != "",
	})
}

// honeypot: bots fill the hidden "website" field.
type subscribeReq struct {
	subscribers.SubscribeInput
	Website string `json:"website"`
}

const genericSubscribeMsg = "Thanks! If this address can receive our newsletter, a confirmation email is on its way. Please click the link inside to finish subscribing."

func (s *Server) subscribe(w http.ResponseWriter, r *http.Request) {
	var req subscribeReq
	if !decode(w, r, &req) {
		return
	}
	if req.Website != "" {
		writeJSON(w, http.StatusAccepted, map[string]string{"message": genericSubscribeMsg})
		return
	}
	addr, err := subscribers.NormalizeEmail(req.Email)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if !req.Consent {
		writeError(w, http.StatusBadRequest, subscribers.ErrConsentMissing.Error())
		return
	}
	ip := s.clientIP(r)
	if !s.allow(r.Context(), map[string]ratelimit.Rule{
		"subscribe:ip:" + ip:      {Limit: 10, Window: time.Hour},
		"subscribe:email:" + addr: {Limit: 5, Window: 24 * time.Hour},
	}) {
		writeError(w, http.StatusTooManyRequests, "too many attempts; please try again later")
		return
	}
	req.IP, req.UserAgent = ip, r.UserAgent()
	if err := s.Subscribers.Subscribe(r.Context(), req.SubscribeInput); err != nil {
		if errors.Is(err, subscribers.ErrInvalidEmail) || errors.Is(err, subscribers.ErrConsentMissing) {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		s.internal(w, r, err)
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]string{"message": genericSubscribeMsg})
}

func (s *Server) resend(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Email string `json:"email"`
	}
	if !decode(w, r, &req) {
		return
	}
	addr, err := subscribers.NormalizeEmail(req.Email)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if !s.allow(r.Context(), map[string]ratelimit.Rule{
		"resend:ip:" + s.clientIP(r): {Limit: 5, Window: time.Hour},
		"resend:email:" + addr:       {Limit: 3, Window: time.Hour},
	}) {
		writeError(w, http.StatusTooManyRequests, "please wait a few minutes before requesting another email")
		return
	}
	if err := s.Subscribers.Resend(r.Context(), addr); err != nil {
		s.internal(w, r, err)
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]string{"message": "If a confirmation is pending for this address, a new link is on its way."})
}

func (s *Server) confirm(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Token string `json:"token"`
	}
	if !decode(w, r, &req) {
		return
	}
	res, err := s.Subscribers.Confirm(r.Context(), req.Token, s.clientIP(r), r.UserAgent())
	switch {
	case errors.Is(err, subscribers.ErrTokenExpired):
		writeJSON(w, http.StatusGone, map[string]string{"error": err.Error(), "code": "expired"})
	case errors.Is(err, subscribers.ErrTokenInvalid):
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error(), "code": "invalid"})
	case err != nil:
		s.internal(w, r, err)
	default:
		writeJSON(w, 200, res)
	}
}

func (s *Server) unsubscribeInfo(w http.ResponseWriter, r *http.Request) {
	info, err := s.Subscribers.UnsubscribeInfo(r.Context(), r.URL.Query().Get("token"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "this unsubscribe link is invalid")
		return
	}
	writeJSON(w, 200, info)
}

func (s *Server) unsubscribe(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Token string `json:"token"`
	}
	if !decode(w, r, &req) {
		return
	}
	if err := s.Subscribers.Unsubscribe(r.Context(), req.Token, "unsubscribe_page"); err != nil {
		if errors.Is(err, subscribers.ErrTokenInvalid) {
			writeError(w, http.StatusBadRequest, "this unsubscribe link is invalid")
			return
		}
		s.internal(w, r, err)
		return
	}
	writeJSON(w, 200, map[string]string{"message": "You have been unsubscribed and won't receive further newsletters."})
}

func (s *Server) unsubscribeOneClick(w http.ResponseWriter, r *http.Request) {
	if err := s.Subscribers.Unsubscribe(r.Context(), r.URL.Query().Get("token"), "list_unsubscribe_one_click"); err != nil {
		if errors.Is(err, subscribers.ErrTokenInvalid) {
			writeError(w, http.StatusBadRequest, "invalid token")
			return
		}
		s.internal(w, r, err)
		return
	}
	w.WriteHeader(http.StatusOK)
}

func (s *Server) contact(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name    string `json:"name"`
		Email   string `json:"email"`
		Message string `json:"message"`
		Website string `json:"website"`
	}
	if !decode(w, r, &req) {
		return
	}
	ok := map[string]string{"message": "Thanks for your message. I'll get back to you soon."}
	if req.Website != "" {
		writeJSON(w, http.StatusAccepted, ok)
		return
	}
	addr, err := subscribers.NormalizeEmail(req.Email)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	name := strings.TrimSpace(req.Name)
	msg := strings.TrimSpace(req.Message)
	if name == "" || len(name) > 100 || len(msg) < 10 || len(msg) > 5000 {
		writeError(w, http.StatusBadRequest, "please enter your name and a message (10-5000 characters)")
		return
	}
	ip := s.clientIP(r)
	if !s.allow(r.Context(), map[string]ratelimit.Rule{
		"contact:ip:" + ip:      {Limit: 5, Window: time.Hour},
		"contact:email:" + addr: {Limit: 3, Window: 24 * time.Hour},
	}) {
		writeError(w, http.StatusTooManyRequests, "too many messages; please try again later")
		return
	}
	if word, err := s.checkBlockedWords(r.Context(), name, msg); err != nil {
		s.internal(w, r, err)
		return
	} else if word != "" {
		writeError(w, http.StatusUnprocessableEntity, fmt.Sprintf(blockedWordMsg, word))
		return
	}
	var id string
	if err := s.DB.QueryRow(r.Context(), `INSERT INTO contact_messages (name, email, message, ip) VALUES ($1,$2,$3,$4) RETURNING id`,
		name, addr, msg, ipOrNil(ip)).Scan(&id); err != nil {
		s.internal(w, r, err)
		return
	}
	ctx := r.Context()
	if _, err := s.Mailer.Send(ctx, s.DB, mailer.Request{Template: "contact_ack", To: email.Address{Email: addr, Name: name},
		IdempotencyKey: "contact_ack:" + id}); err != nil {
		s.Log.Error("contact ack enqueue", "err", err)
	}
	// The owner is not emailed here: the message waits in the admin until the
	// owner forwards it to their inbox (see forwardContactMessage).
	writeJSON(w, http.StatusAccepted, ok)
}

func (s *Server) ownerEmail(r *http.Request) string {
	if s.Cfg.AdminAlertEmail != "" {
		return s.Cfg.AdminAlertEmail
	}
	es, _ := s.Settings.Email(r.Context())
	return es.ReplyTo
}

func (s *Server) otpHash(emailAddr, code string) []byte {
	m := hmac.New(sha256.New, s.Cfg.AppSecret)
	m.Write([]byte("resume-otp|" + strings.ToLower(emailAddr) + "|" + code))
	return m.Sum(nil)
}

func (s *Server) resumeRequest(w http.ResponseWriter, r *http.Request) {
	if s.Cfg.ResumeFilePath == "" {
		writeError(w, http.StatusNotFound, "resume download is not available")
		return
	}
	var req struct {
		Email string `json:"email"`
	}
	if !decode(w, r, &req) {
		return
	}
	addr, err := subscribers.NormalizeEmail(req.Email)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if !s.allow(r.Context(), map[string]ratelimit.Rule{
		"otp:ip:" + s.clientIP(r): {Limit: 5, Window: time.Hour},
		"otp:email:" + addr:       {Limit: 3, Window: time.Hour},
	}) {
		writeError(w, http.StatusTooManyRequests, "too many code requests; please wait and try again")
		return
	}
	code := tokensNumeric()
	var id string
	if err := s.DB.QueryRow(r.Context(), `INSERT INTO resume_otps (email, code_hash, expires_at) VALUES ($1,$2, now() + interval '10 minutes') RETURNING id`,
		addr, s.otpHash(addr, code)).Scan(&id); err != nil {
		s.internal(w, r, err)
		return
	}
	if _, err := s.Mailer.Send(r.Context(), s.DB, mailer.Request{Template: "resume_otp", To: email.Address{Email: addr},
		IdempotencyKey: "otp:" + id, Priority: 20, Vars: map[string]string{"code": code}}); err != nil {
		s.internal(w, r, err)
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]string{"message": "We've sent a 6-digit code to your email."})
}

func (s *Server) resumeVerify(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Email string `json:"email"`
		Code  string `json:"code"`
	}
	if !decode(w, r, &req) {
		return
	}
	addr, err := subscribers.NormalizeEmail(req.Email)
	if err != nil || len(req.Code) != 6 {
		writeError(w, http.StatusBadRequest, "enter the 6-digit code from the email")
		return
	}
	var id string
	var hash []byte
	err = s.DB.QueryRow(r.Context(), `UPDATE resume_otps SET attempts = attempts + 1
		WHERE id = (SELECT id FROM resume_otps WHERE email=$1 AND used_at IS NULL AND expires_at > now() AND attempts < 5
		            ORDER BY created_at DESC LIMIT 1) RETURNING id, code_hash`, addr).Scan(&id, &hash)
	if err != nil || !hmac.Equal(hash, s.otpHash(addr, req.Code)) {
		writeError(w, http.StatusBadRequest, "that code is incorrect or has expired")
		return
	}
	if _, err := s.DB.Exec(r.Context(), `UPDATE resume_otps SET used_at=now() WHERE id=$1 AND used_at IS NULL`, id); err != nil {
		s.internal(w, r, err)
		return
	}
	tok := s.Signer.Sign("resume", addr, 10*time.Minute)
	writeJSON(w, 200, map[string]string{"download_url": "/api/public/resume/download?token=" + tok})
}

func (s *Server) resumeDownload(w http.ResponseWriter, r *http.Request) {
	if _, err := s.Signer.Verify("resume", r.URL.Query().Get("token")); err != nil || s.Cfg.ResumeFilePath == "" {
		writeError(w, http.StatusForbidden, "this download link has expired; please verify your email again")
		return
	}
	f, err := os.Open(s.Cfg.ResumeFilePath)
	if err != nil {
		s.internal(w, r, err)
		return
	}
	defer f.Close()
	st, _ := f.Stat()
	w.Header().Set("Content-Type", "application/pdf")
	w.Header().Set("Content-Disposition", `attachment; filename="Sayantan-Chakraborty-Resume.pdf"`)
	w.Header().Set("Cache-Control", "private, no-store")
	http.ServeContent(w, r, filepath.Base(s.Cfg.ResumeFilePath), st.ModTime(), f)
}

// --- content --------------------------------------------------------------------

func (s *Server) listArticles(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	pages, total, err := s.Content.List(r.Context(), content.ListFilter{Kind: "article", Status: "published",
		Tag: q.Get("tag"), Limit: atoiDefault(q.Get("limit"), 12), Offset: atoiDefault(q.Get("offset"), 0)})
	if err != nil {
		s.internal(w, r, err)
		return
	}
	for _, p := range pages {
		p.Blocks = nil // listings don't need bodies
	}
	w.Header().Set("Cache-Control", "public, max-age=60")
	writeJSON(w, 200, map[string]any{"items": pages, "total": total})
}

func (s *Server) getArticle(w http.ResponseWriter, r *http.Request) {
	p, err := s.Content.PublishedArticle(r.Context(), r.PathValue("slug"))
	if errors.Is(err, content.ErrNotFound) {
		writeError(w, 404, "article not found")
		return
	}
	if err != nil {
		s.internal(w, r, err)
		return
	}
	w.Header().Set("Cache-Control", "public, max-age=60")
	writeJSON(w, 200, p)
}

func (s *Server) getPage(w http.ResponseWriter, r *http.Request) {
	p, err := s.Content.PublishedPage(r.Context(), r.PathValue("path"))
	if errors.Is(err, content.ErrNotFound) {
		writeError(w, 404, "page not found")
		return
	}
	if err != nil {
		s.internal(w, r, err)
		return
	}
	w.Header().Set("Cache-Control", "public, max-age=60")
	writeJSON(w, 200, p)
}

// redirectLookup lets the SSR server issue 301s for legacy URLs, but only
// when the target is live.
func (s *Server) redirectLookup(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Query().Get("path")
	to, code, ok := s.Content.Redirect(r.Context(), path)
	if !ok || !s.targetLive(r, to) {
		writeError(w, 404, "no redirect")
		return
	}
	w.Header().Set("Cache-Control", "public, max-age=300")
	writeJSON(w, 200, map[string]any{"to": to, "status": code})
}

func (s *Server) targetLive(r *http.Request, to string) bool {
	if strings.HasPrefix(to, "https://") {
		return true
	}
	p := strings.TrimSuffix(to, "/")
	if p == "" || p == "/blog" || p == "/privacy" || p == "/terms" || p == "/ai-policy" || p == "/videos" {
		return true
	}
	if strings.HasPrefix(p, "/blog/") {
		_, err := s.Content.PublishedArticle(r.Context(), strings.TrimPrefix(p, "/blog/"))
		return err == nil
	}
	_, err := s.Content.PublishedPage(r.Context(), p)
	return err == nil
}

var staticPaths = []string{"/", "/blog", "/videos", "/privacy", "/terms", "/ai-policy"}

func (s *Server) sitemap(w http.ResponseWriter, r *http.Request) {
	entries, err := s.Content.Sitemap(r.Context())
	if err != nil {
		s.internal(w, r, err)
		return
	}
	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="UTF-8"?>` + "\n" + `<urlset xmlns="http://www.sitemaps.org/schemas/sitemap/0.9">` + "\n")
	for _, p := range staticPaths {
		fmt.Fprintf(&b, "  <url><loc>%s%s</loc></url>\n", s.Cfg.PublicSiteURL, p)
	}
	for _, e := range entries {
		fmt.Fprintf(&b, "  <url><loc>%s%s</loc><lastmod>%s</lastmod></url>\n", s.Cfg.PublicSiteURL, xmlEscape(e.Path), e.Updated.UTC().Format("2006-01-02"))
	}
	b.WriteString("</urlset>\n")
	w.Header().Set("Content-Type", "application/xml; charset=utf-8")
	w.Header().Set("Cache-Control", "public, max-age=600")
	_, _ = w.Write([]byte(b.String()))
}

// robots allows search and answer engines (consistent with /ai-policy) and
// keeps private areas out of indexes.
func (s *Server) robots(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Cache-Control", "public, max-age=3600")
	fmt.Fprintf(w, `User-agent: *
Allow: /
Disallow: /admin
Disallow: /api/
Disallow: /subscribe/confirm
Disallow: /unsubscribe

# Answer engines and AI assistants may read public pages (see /ai-policy)
User-agent: GPTBot
Allow: /
User-agent: OAI-SearchBot
Allow: /
User-agent: ChatGPT-User
Allow: /
User-agent: ClaudeBot
Allow: /
User-agent: Claude-SearchBot
Allow: /
User-agent: PerplexityBot
Allow: /
User-agent: Google-Extended
Allow: /

Sitemap: %s/sitemap.xml
`, s.Cfg.PublicSiteURL)
}

// llms implements the llms.txt convention: a concise, link-rich summary for
// answer engines.
func (s *Server) llms(w http.ResponseWriter, r *http.Request) {
	site := s.Cfg.PublicSiteURL
	var b strings.Builder
	b.WriteString("# Sayantan Chakraborty — Oracle DBA & Database Reliability Engineer\n\n")
	b.WriteString("> Portfolio of Sayantan Chakraborty, an Oracle Database Administrator and Database Reliability Engineer (11+ years, Bangalore, India) " +
		"specialising in Oracle RAC, Data Guard, GoldenGate, RMAN, OEM, Oracle EBS, PostgreSQL/Patroni, high availability, disaster recovery and automation with Ansible and Terraform.\n\n")
	b.WriteString("## Key pages\n")
	fmt.Fprintf(&b, "- [Home and profile](%s/): experience, projects, expertise and contact\n", site)
	fmt.Fprintf(&b, "- [Articles](%s/blog): technical writing on Oracle and database reliability\n", site)
	fmt.Fprintf(&b, "- [Videos](%s/videos): YouTube channel @csayantan\n", site)
	fmt.Fprintf(&b, "- [AI policy](%s/ai-policy): how AI systems may use this content\n", site)
	fmt.Fprintf(&b, "- [Privacy (DPDP)](%s/privacy) and [Terms](%s/terms)\n", site, site)
	b.WriteString("\n## Profiles\n- LinkedIn: https://www.linkedin.com/in/sayantan-chakraborty-y2022\n- GitHub: https://github.com/ust-sayantan\n- YouTube: https://www.youtube.com/@csayantan\n")
	if pages, _, err := s.Content.List(r.Context(), content.ListFilter{Kind: "article", Status: "published", Limit: 50}); err == nil && len(pages) > 0 {
		b.WriteString("\n## Articles\n")
		for _, p := range pages {
			fmt.Fprintf(&b, "- [%s](%s%s): %s\n", p.Title, site, p.Path, strings.ReplaceAll(p.Excerpt, "\n", " "))
		}
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Cache-Control", "public, max-age=3600")
	_, _ = w.Write([]byte(b.String()))
}

func xmlEscape(s string) string {
	r := strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;", "'", "&apos;")
	return r.Replace(s)
}
