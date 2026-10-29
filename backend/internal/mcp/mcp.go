// Package mcp exposes the CMS as a Model Context Protocol server (Streamable
// HTTP transport, JSON responses) so an AI assistant connected through an MCP
// connector can create and publish pages/articles, edit the site content and
// read dashboard data.
//
// Access requires the OAuth login in package oauth. Tools never send email:
// publishing with notify_subscribers only creates a draft campaign that the
// owner must review and approve in the CMS.
package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/cadentic/portfolio-cms/backend/internal/audit"
	"github.com/cadentic/portfolio-cms/backend/internal/auth"
	"github.com/cadentic/portfolio-cms/backend/internal/campaigns"
	"github.com/cadentic/portfolio-cms/backend/internal/content"
	"github.com/cadentic/portfolio-cms/backend/internal/providers"
	"github.com/cadentic/portfolio-cms/backend/internal/queue"
	"github.com/cadentic/portfolio-cms/backend/internal/sitecontent"
	"github.com/cadentic/portfolio-cms/backend/internal/subscribers"
	"github.com/jackc/pgx/v5/pgxpool"
)

const serverVersion = "1.0.0"

var supportedVersions = []string{"2025-06-18", "2025-03-26", "2024-11-05"}

type Server struct {
	DB          *pgxpool.Pool
	Content     *content.Service
	Campaigns   *campaigns.Service
	Subscribers *subscribers.Service
	Providers   *providers.Repo
	SiteContent *sitecontent.Service
	Log         *slog.Logger
	SiteURL     string
}

type rpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodPost:
	case http.MethodGet:
		// No server-initiated streams are offered.
		w.Header().Set("Allow", "POST, DELETE")
		http.Error(w, "SSE stream not supported; use POST", http.StatusMethodNotAllowed)
		return
	case http.MethodDelete:
		w.WriteHeader(http.StatusNoContent)
		return
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var raw json.RawMessage
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 2<<20)).Decode(&raw); err != nil {
		writeRPC(w, rpcResponse{JSONRPC: "2.0", ID: json.RawMessage("null"), Error: &rpcError{-32700, "parse error"}})
		return
	}
	if len(raw) > 0 && raw[0] == '[' {
		var batch []rpcRequest
		if err := json.Unmarshal(raw, &batch); err != nil {
			writeRPC(w, rpcResponse{JSONRPC: "2.0", ID: json.RawMessage("null"), Error: &rpcError{-32600, "invalid batch"}})
			return
		}
		var out []rpcResponse
		for _, req := range batch {
			if resp, ok := s.handle(r.Context(), req); ok {
				out = append(out, resp)
			}
		}
		if len(out) == 0 {
			w.WriteHeader(http.StatusAccepted)
			return
		}
		writeRPC(w, out)
		return
	}
	var req rpcRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		writeRPC(w, rpcResponse{JSONRPC: "2.0", ID: json.RawMessage("null"), Error: &rpcError{-32600, "invalid request"}})
		return
	}
	resp, ok := s.handle(r.Context(), req)
	if !ok {
		w.WriteHeader(http.StatusAccepted) // notification
		return
	}
	writeRPC(w, resp)
}

func writeRPC(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

func (s *Server) handle(ctx context.Context, req rpcRequest) (rpcResponse, bool) {
	if len(req.ID) == 0 {
		return rpcResponse{}, false // notifications (e.g. notifications/initialized)
	}
	resp := rpcResponse{JSONRPC: "2.0", ID: req.ID}
	switch req.Method {
	case "initialize":
		var p struct {
			ProtocolVersion string `json:"protocolVersion"`
		}
		_ = json.Unmarshal(req.Params, &p)
		version := supportedVersions[0]
		for _, v := range supportedVersions {
			if v == p.ProtocolVersion {
				version = v
			}
		}
		resp.Result = map[string]any{
			"protocolVersion": version,
			"capabilities":    map[string]any{"tools": map[string]any{"listChanged": false}},
			"serverInfo":      map[string]any{"name": "portfolio-cms", "title": "Portfolio CMS", "version": serverVersion},
			"instructions": "Tools for the owner's portfolio CMS at " + s.SiteURL + ". Pages and articles are created as drafts; " +
				"publish_page makes them public. Email is never sent from here: notify_subscribers only prepares a draft campaign " +
				"that the owner approves in the CMS. " +
				"Site content (home page text, profile, organisations, videos) is read with get_site_content and changed with " +
				"update_site_content. Send only the fields you change; any list or nested object you send replaces the whole list " +
				"or object. Changes are live within a minute, versioned and audited. Legal pages can only be edited by the owner.",
		}
	case "ping":
		resp.Result = map[string]any{}
	case "tools/list":
		resp.Result = map[string]any{"tools": toolDefs}
	case "tools/call":
		var p struct {
			Name      string          `json:"name"`
			Arguments json.RawMessage `json:"arguments"`
		}
		if err := json.Unmarshal(req.Params, &p); err != nil {
			resp.Error = &rpcError{-32602, "invalid params"}
			break
		}
		result, err := s.call(ctx, p.Name, p.Arguments)
		if err != nil {
			resp.Result = map[string]any{"isError": true, "content": []map[string]any{{"type": "text", "text": err.Error()}}}
			break
		}
		b, _ := json.MarshalIndent(result, "", "  ")
		resp.Result = map[string]any{"content": []map[string]any{{"type": "text", "text": string(b)}}, "structuredContent": result}
	default:
		resp.Error = &rpcError{-32601, "method not found: " + req.Method}
	}
	return resp, true
}

func schema(props map[string]any, required ...string) map[string]any {
	if required == nil {
		required = []string{}
	}
	return map[string]any{"type": "object", "properties": props, "required": required, "additionalProperties": false}
}

var heroSchema = map[string]any{
	"type": "object",
	"description": "Top section. type 'youtube' takes a YouTube URL/ID (rendered as a privacy-friendly click-to-play placeholder); " +
		"'image' needs url + alt; 'video' needs an https mp4/webm url.",
	"properties": map[string]any{
		"type":       map[string]any{"type": "string", "enum": []string{"none", "image", "video", "youtube"}},
		"url":        map[string]any{"type": "string"},
		"youtube_id": map[string]any{"type": "string"},
		"alt":        map[string]any{"type": "string"},
		"caption":    map[string]any{"type": "string"},
		"poster":     map[string]any{"type": "string"},
		"heading":    map[string]any{"type": "string"},
		"subheading": map[string]any{"type": "string"},
	},
	"required": []string{"type"},
}

var pageProps = map[string]any{
	"title":              map[string]any{"type": "string"},
	"slug":               map[string]any{"type": "string", "description": "URL slug; derived from the title when omitted"},
	"excerpt":            map[string]any{"type": "string", "description": "Summary used for listings, SEO description and Open Graph"},
	"hero":               heroSchema,
	"body_markdown":      map[string]any{"type": "string", "description": "Body in Markdown (#/## headings, lists, > quotes, ``` code, ---)"},
	"featured_image_url": map[string]any{"type": "string"},
	"tags":               map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
	"categories":         map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
	"seo_title":          map[string]any{"type": "string"},
	"seo_description":    map[string]any{"type": "string"},
	"parent_id":          map[string]any{"type": "string", "description": "Parent page ID (pages only)"},
}

func withKind(p map[string]any) map[string]any {
	out := map[string]any{"kind": map[string]any{"type": "string", "enum": []string{"article", "page"}, "description": "article = blog post at /blog/{slug}; page = /{slug}"}}
	for k, v := range p {
		out[k] = v
	}
	return out
}

func withID(p map[string]any) map[string]any {
	out := map[string]any{"id": map[string]any{"type": "string"}}
	for k, v := range p {
		out[k] = v
	}
	return out
}

var readOnly = map[string]any{"readOnlyHint": true}

var toolDefs = []map[string]any{
	{"name": "list_pages", "title": "List pages and articles", "annotations": readOnly,
		"description": "List CMS pages/articles with status and URL.",
		"inputSchema": schema(map[string]any{
			"kind": map[string]any{"type": "string", "enum": []string{"article", "page"}}, "status": map[string]any{"type": "string", "enum": []string{"draft", "published", "archived"}},
			"query": map[string]any{"type": "string"}, "limit": map[string]any{"type": "integer", "minimum": 1, "maximum": 100}})},
	{"name": "get_page", "title": "Get a page", "annotations": readOnly, "description": "Get one page/article including its blocks.",
		"inputSchema": schema(map[string]any{"id": map[string]any{"type": "string"}}, "id")},
	{"name": "create_page", "title": "Create a page or article (draft)",
		"description": "Create a new draft page or blog article. Optionally add an image/video/YouTube hero section.",
		"inputSchema": schema(withKind(pageProps), "kind", "title")},
	{"name": "update_page", "title": "Update a page", "description": "Update fields of an existing page/article. body_markdown replaces the body.",
		"inputSchema": schema(withID(pageProps), "id")},
	{"name": "publish_page", "title": "Publish a page",
		"description": "Publish a page/article. For a first-time article publish, notify_subscribers=true prepares a DRAFT newsletter campaign for the owner to approve; nothing is emailed.",
		"inputSchema": schema(map[string]any{"id": map[string]any{"type": "string"}, "notify_subscribers": map[string]any{"type": "boolean"}}, "id")},
	{"name": "unpublish_page", "title": "Unpublish a page", "description": "Move a published page/article back to draft.",
		"inputSchema": schema(map[string]any{"id": map[string]any{"type": "string"}}, "id")},
	{"name": "subscriber_stats", "title": "Subscriber statistics", "annotations": readOnly,
		"description": "Totals for verified, pending, unsubscribed, bounced and suppressed subscribers.", "inputSchema": schema(map[string]any{})},
	{"name": "email_health", "title": "Email delivery health", "annotations": readOnly,
		"description": "Queue depth, failovers, dead letters, acceptance-unknown messages and provider circuit/health status.", "inputSchema": schema(map[string]any{})},
	{"name": "list_campaigns", "title": "List campaigns", "annotations": readOnly, "description": "Recent newsletter campaigns and their delivery stats.",
		"inputSchema": schema(map[string]any{"status": map[string]any{"type": "string"}})},
	{"name": "get_site_content", "title": "Get site content", "annotations": readOnly,
		"description": "Read the public site content (home page text, profile and links, organisations, videos, FAQ, SEO text) as " +
			"stored, with {years} placeholders kept. Pass sections to read only some. Send updated_at back as base_updated_at " +
			"when updating.",
		"inputSchema": schema(map[string]any{"sections": sectionList})},
	{"name": "update_site_content", "title": "Update site content",
		"description": "Change the public site content (home page text, profile and links, organisations with logos, videos, FAQ, " +
			"SEO text). Send only the fields you want to change inside a section; a list or nested object you send replaces the " +
			"whole list or object, so send lists complete; null empties a field. Use {years} where the years of experience " +
			"should appear; it is replaced by identity.experience_years. YouTube links are converted to video IDs. Images " +
			"must already be in the media library (/media/…); external image URLs are refused. Use dry_run to validate without saving, and " +
			"base_updated_at from get_site_content to avoid overwriting the owner's edits. Changes are live within a minute, " +
			"versioned and recorded in the audit log.",
		"inputSchema": schema(map[string]any{
			"sections": map[string]any{"type": "object", "propertyNames": map[string]any{"enum": sitecontent.Sections},
				"additionalProperties": map[string]any{"type": "object"}},
			"reset_sections":  sectionList,
			"base_updated_at": map[string]any{"type": []string{"string", "null"}},
			"dry_run":         map[string]any{"type": "boolean"},
		})},
}

var sectionList = map[string]any{"type": "array", "items": map[string]any{"type": "string", "enum": sitecontent.Sections}}

var errLegalPage = errors.New("Legal pages (privacy, terms, ai-policy) can only be edited by the owner in the admin.")

type pageArgs struct {
	ID               string        `json:"id"`
	Kind             string        `json:"kind"`
	Title            *string       `json:"title"`
	Slug             *string       `json:"slug"`
	Excerpt          *string       `json:"excerpt"`
	Hero             *content.Hero `json:"hero"`
	BodyMarkdown     *string       `json:"body_markdown"`
	FeaturedImageURL *string       `json:"featured_image_url"`
	Tags             []string      `json:"tags"`
	Categories       []string      `json:"categories"`
	SEOTitle         *string       `json:"seo_title"`
	SEODescription   *string       `json:"seo_description"`
	ParentID         *string       `json:"parent_id"`
}

func (a pageArgs) input() content.Input {
	in := content.Input{Title: a.Title, Slug: a.Slug, Excerpt: a.Excerpt, Hero: a.Hero, FeaturedImageURL: a.FeaturedImageURL,
		Tags: a.Tags, Categories: a.Categories, ParentID: a.ParentID}
	if a.Kind != "" {
		k := a.Kind
		in.Kind = &k
	}
	if a.BodyMarkdown != nil {
		in.Blocks = content.BlocksFromMarkdown(*a.BodyMarkdown)
	}
	if a.SEOTitle != nil || a.SEODescription != nil {
		in.SEO = &content.SEO{Title: deref(a.SEOTitle), Description: deref(a.SEODescription)}
	}
	return in
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func (s *Server) call(ctx context.Context, name string, args json.RawMessage) (any, error) {
	id := auth.FromContext(ctx)
	if id == nil || (id.Email == "" && id.UID == "") {
		return nil, errors.New("not authorised")
	}
	actor := "mcp:" + id.Actor()
	if len(args) == 0 {
		args = json.RawMessage("{}")
	}
	var a pageArgs
	_ = json.Unmarshal(args, &a)
	logAudit := func(action, entity string, details map[string]any) {
		if err := audit.Log(ctx, s.DB, actor, action, "page", entity, details, ""); err != nil {
			s.Log.Error("audit", "err", err)
		}
	}
	summary := func(p *content.Page) map[string]any {
		return map[string]any{"id": p.ID, "kind": p.Kind, "title": p.Title, "slug": p.Slug, "status": p.Status,
			"url": s.SiteURL + p.Path, "hero": p.Hero.Type, "blocks": len(p.Blocks), "updated_at": p.UpdatedAt}
	}
	switch name {
	case "list_pages":
		var f struct {
			Kind, Status, Query string
			Limit               int
		}
		_ = json.Unmarshal(args, &f)
		pages, total, err := s.Content.List(ctx, content.ListFilter{Kind: f.Kind, Status: f.Status, Query: f.Query, Limit: f.Limit})
		if err != nil {
			return nil, err
		}
		items := make([]map[string]any, 0, len(pages))
		for _, p := range pages {
			items = append(items, summary(p))
		}
		return map[string]any{"total": total, "items": items}, nil
	case "get_page":
		return s.Content.Get(ctx, a.ID)
	case "create_page":
		if a.Kind != "page" {
			a.Kind = "article"
		}
		in := a.input()
		if in.AuthorName == nil {
			author := s.ownerName(ctx)
			in.AuthorName = &author
		}
		p, err := s.Content.Create(ctx, in, actor)
		if err != nil {
			return nil, err
		}
		logAudit("page.create", p.ID, map[string]any{"title": p.Title, "via": "mcp"})
		return map[string]any{"page": summary(p), "next": "Review it in the CMS, then call publish_page when ready."}, nil
	case "update_page":
		if err := s.guardLegal(ctx, a.ID); err != nil {
			return nil, err
		}
		a.Kind = ""
		p, changed, err := s.Content.Update(ctx, a.ID, a.input(), actor)
		if err != nil {
			return nil, err
		}
		if len(changed) > 0 {
			logAudit("page.update", p.ID, map[string]any{"changed": changed, "via": "mcp"})
		}
		return summary(p), nil
	case "publish_page":
		var x struct {
			ID     string `json:"id"`
			Notify bool   `json:"notify_subscribers"`
		}
		_ = json.Unmarshal(args, &x)
		if err := s.guardLegal(ctx, x.ID); err != nil {
			return nil, err
		}
		res, err := s.Content.Publish(ctx, x.ID)
		if err != nil {
			return nil, err
		}
		logAudit("page.publish", x.ID, map[string]any{"via": "mcp", "notify": x.Notify})
		out := map[string]any{"page": summary(res.Page)}
		if x.Notify && res.FirstPublish && res.Page.Kind == "article" {
			c, created, err := s.Campaigns.CreateBlogNotification(ctx, campaigns.BlogPost{
				ID: res.Page.ID, Title: res.Page.Title, Excerpt: res.Page.Excerpt, URL: s.SiteURL + res.Page.Path,
				FeaturedImageURL: absolute(s.SiteURL, res.Page.FeaturedImageURL), Author: res.Page.AuthorName,
				PublishedAt: *res.Page.PublishedAt}, actor)
			if err != nil {
				return nil, err
			}
			out["campaign"] = map[string]any{"id": c.ID, "status": c.Status, "created": created,
				"note": "Draft campaign prepared. The owner must preview, test and approve it in Subscribers & Email → Campaigns."}
		} else if x.Notify {
			out["campaign"] = map[string]any{"note": "No notification prepared: only the first publish of a native article can notify subscribers."}
		}
		return out, nil
	case "unpublish_page":
		if err := s.guardLegal(ctx, a.ID); err != nil {
			return nil, err
		}
		p, err := s.Content.Unpublish(ctx, a.ID)
		if err != nil {
			return nil, err
		}
		logAudit("page.unpublish", p.ID, map[string]any{"via": "mcp"})
		return summary(p), nil
	case "subscriber_stats":
		return s.Subscribers.Stats(ctx)
	case "email_health":
		m, err := queue.GetMonitor(ctx, s.DB, 24)
		if err != nil {
			return nil, err
		}
		ps, err := s.Providers.List(ctx)
		if err != nil {
			return nil, err
		}
		prov := []map[string]any{}
		for _, p := range ps {
			prov = append(prov, map[string]any{"name": p.Name, "kind": p.Kind, "enabled": p.Enabled, "priority": p.Priority,
				"circuit": p.CircuitState, "configured": p.CredentialsConfigured, "last_success_at": p.LastSuccessAt,
				"last_error": p.LastError, "last_health_ok": p.LastHealthOK})
		}
		return map[string]any{"monitor": m, "providers": prov}, nil
	case "list_campaigns":
		var f struct{ Status string }
		_ = json.Unmarshal(args, &f)
		cs, total, err := s.Campaigns.List(ctx, f.Status, 20, 0)
		if err != nil {
			return nil, err
		}
		return map[string]any{"total": total, "items": cs}, nil
	case "get_site_content":
		return s.getSiteContent(ctx, args)
	case "update_site_content":
		return s.updateSiteContent(ctx, args, actor)
	}
	return nil, fmt.Errorf("unknown tool %q", name)
}

// guardLegal refuses changes to the protected legal pages.
func (s *Server) guardLegal(ctx context.Context, id string) error {
	protected, err := s.Content.IsProtected(ctx, id)
	if err != nil {
		return err
	}
	if protected {
		return errLegalPage
	}
	return nil
}

// ownerName is identity.name from the site content, for default bylines.
func (s *Server) ownerName(ctx context.Context) string {
	if s.SiteContent != nil {
		if c, err := s.SiteContent.PublicContent(ctx); err == nil && c.Identity.Name != "" {
			return c.Identity.Name
		}
	}
	return "Sayantan Chakraborty"
}

// strict decodes tool arguments and refuses unknown keys.
func strict(args json.RawMessage, v any) error {
	dec := json.NewDecoder(bytes.NewReader(args))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return fmt.Errorf("invalid arguments: %s", strings.TrimPrefix(err.Error(), "json: "))
	}
	return nil
}

// sectionsOf returns the given sections of c as raw JSON, all when keys is empty.
func sectionsOf(c sitecontent.Content, keys []string) (map[string]json.RawMessage, error) {
	b, err := json.Marshal(c)
	if err != nil {
		return nil, err
	}
	all := map[string]json.RawMessage{}
	if err := json.Unmarshal(b, &all); err != nil {
		return nil, err
	}
	if len(keys) == 0 {
		return all, nil
	}
	out := map[string]json.RawMessage{}
	for _, k := range keys {
		v, ok := all[k]
		if !ok {
			return nil, fmt.Errorf("unknown section %q; sections are %s", k, strings.Join(sitecontent.Sections, ", "))
		}
		out[k] = v
	}
	return out, nil
}

func (s *Server) getSiteContent(ctx context.Context, args json.RawMessage) (any, error) {
	var in struct {
		Sections []string `json:"sections"`
	}
	if err := strict(args, &in); err != nil {
		return nil, err
	}
	d, err := s.SiteContent.Get(ctx)
	if err != nil {
		return nil, err
	}
	secs, err := sectionsOf(d.Content, in.Sections)
	if err != nil {
		return nil, err
	}
	return map[string]any{"content": secs, "updated_at": d.UpdatedAt, "updated_by": d.UpdatedBy, "sections": sitecontent.Sections,
		"placeholders": map[string]string{"{years}": "identity.experience_years"}}, nil
}

func (s *Server) updateSiteContent(ctx context.Context, args json.RawMessage, actor string) (any, error) {
	var in struct {
		Sections      map[string]json.RawMessage `json:"sections"`
		ResetSections []string                   `json:"reset_sections"`
		Base          json.RawMessage            `json:"base_updated_at"`
		DryRun        bool                       `json:"dry_run"`
	}
	if err := strict(args, &in); err != nil {
		return nil, err
	}
	if len(in.Sections) == 0 && len(in.ResetSections) == 0 {
		return nil, errors.New("send sections to change, or reset_sections")
	}
	p := sitecontent.Patch{Sections: in.Sections, Reset: in.ResetSections}
	switch {
	case len(in.Base) == 0:
	case string(in.Base) == "null":
		p.Check = true
	default:
		errBase := errors.New("base_updated_at must be null or the updated_at value from get_site_content")
		var v string
		if json.Unmarshal(in.Base, &v) != nil {
			return nil, errBase
		}
		t, err := time.Parse(time.RFC3339Nano, v)
		if err != nil {
			return nil, errBase
		}
		p.Check, p.Base = true, &t
	}
	if len(in.Sections) == 0 {
		p.Action = "reset"
	}
	res, err := s.SiteContent.Apply(ctx, p, actor, in.DryRun)
	var ve *sitecontent.ValidationError
	var se *sitecontent.StaleError
	switch {
	case errors.As(err, &ve):
		lines := make([]string, len(ve.Errors))
		for i, fe := range ve.Errors {
			lines[i] = "- " + fe.String()
		}
		return nil, errors.New("Site content was not saved:\n" + strings.Join(lines, "\n"))
	case errors.As(err, &se):
		since := "it was read"
		if se.UpdatedAt != nil {
			since = se.UpdatedAt.Format(time.RFC3339Nano)
		}
		return nil, fmt.Errorf("Site content changed since %s; call get_site_content again.", since)
	case err != nil:
		return nil, err
	}
	if res.VersionID != 0 {
		details := map[string]any{"sections": res.Changed, "via": "mcp", "version_id": res.VersionID}
		if len(in.ResetSections) > 0 {
			details["reset_sections"] = in.ResetSections
		}
		if err := audit.Log(ctx, s.DB, actor, "site_content.update", "settings", sitecontent.Key, details, ""); err != nil {
			s.Log.Error("audit", "err", err)
		}
	}
	changed, err := sectionsOf(res.Doc.Content, res.Changed)
	if err != nil {
		return nil, err
	}
	return map[string]any{"changed_sections": res.Changed, "updated_at": res.Doc.UpdatedAt, "dry_run": in.DryRun,
		"warnings": res.Warnings, "content": changed}, nil
}

func absolute(site, u string) string {
	if len(u) > 0 && u[0] == '/' {
		return site + u
	}
	return u
}
