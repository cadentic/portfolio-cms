// Package render turns template/campaign content into branded, email-client
// friendly HTML and plain text, adding the compliance footer (sender identity,
// postal address, unsubscribe link) to every marketing message.
package render

import (
	"bytes"
	"html"
	"html/template"
	"regexp"
	"strings"

	"github.com/microcosm-cc/bluemonday"

	"github.com/cadentic/portfolio-cms/backend/internal/settings"
)

var policy = func() *bluemonday.Policy {
	p := bluemonday.UGCPolicy()
	p.AllowAttrs("style").Globally()
	p.AllowAttrs("align", "width", "height", "bgcolor", "cellpadding", "cellspacing", "border").Globally()
	p.AllowElements("table", "thead", "tbody", "tr", "td", "th", "center", "span", "div", "img", "hr")
	p.AllowAttrs("loading").Matching(regexp.MustCompile(`^(lazy|eager)$`)).OnElements("img")
	p.AllowURLSchemes("https", "http", "mailto")
	p.RequireNoFollowOnLinks(false)
	return p
}()

// Sanitize removes scripts, event handlers and unsafe URLs from author HTML.
func Sanitize(s string) string { return policy.Sanitize(s) }

var placeholder = regexp.MustCompile(`\{\{\s*([a-z_]+)\s*\}\}`)

// Fill replaces {{var}} placeholders with HTML-escaped values.
func Fill(s string, vars map[string]string, escape bool) string {
	return placeholder.ReplaceAllStringFunc(s, func(m string) string {
		k := placeholder.FindStringSubmatch(m)[1]
		v, ok := vars[k]
		if !ok {
			return ""
		}
		if escape {
			return html.EscapeString(v)
		}
		return v
	})
}

type Input struct {
	Marketing      bool
	Subject        string
	Preheader      string
	ContentHTML    string // author HTML, sanitized here
	ContentText    string // optional; derived from HTML when empty
	Vars           map[string]string
	RawVars        map[string]string // trusted HTML fragments (sanitized with the rest)
	UnsubscribeURL string
	PreferencesURL string
	SiteURL        string
	Settings       settings.Email
}

var layout = template.Must(template.New("layout").Parse(`<!doctype html>
<html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1">
<meta name="color-scheme" content="light dark"><title>{{.Subject}}</title></head>
<body style="margin:0;padding:0;background:#f4f6f8;font-family:{{.Font}};color:{{.Ink}}">
<span style="display:none!important;opacity:0;color:transparent;height:0;width:0;overflow:hidden">{{.Preheader}}</span>
<table role="presentation" width="100%" cellpadding="0" cellspacing="0" style="background:#f4f6f8;padding:24px 12px">
<tr><td align="center">
<table role="presentation" width="100%" cellpadding="0" cellspacing="0" style="max-width:620px;background:#ffffff;border-radius:16px;overflow:hidden;border:1px solid #dce2e8">
<tr><td style="background:{{.Dark}};padding:22px 28px">
  <table role="presentation" cellpadding="0" cellspacing="0"><tr>
  {{if .LogoURL}}<td><img src="{{.LogoURL}}" alt="{{.Org}}" height="36" style="display:block;border:0"></td>
  {{else}}<td style="width:36px;height:36px;border-radius:10px;background:{{.Accent}};color:#09291f;font-weight:900;text-align:center;font-size:15px;line-height:36px">{{.LogoText}}</td>{{end}}
  <td style="padding-left:12px;color:#f4f7fa;font-weight:800;font-size:16px">{{.Org}}</td></tr></table>
  {{if .HeaderHTML}}<div style="color:#a6b2bf;font-size:13px;margin-top:10px">{{.HeaderHTML}}</div>{{end}}
</td></tr>
<tr><td style="height:4px;background:{{.Accent}}"></td></tr>
<tr><td style="padding:30px 28px;font-size:16px;line-height:1.65;color:{{.Ink}}">{{.Content}}</td></tr>
<tr><td style="padding:20px 28px;background:#f5f7f8;border-top:1px solid #e3e8ec;font-size:12px;line-height:1.6;color:#596675">
  {{if .FooterHTML}}<div style="margin-bottom:8px">{{.FooterHTML}}</div>{{end}}
  <div>{{.Org}}{{if .Address}} · {{.Address}}{{end}}</div>
  {{if .Marketing}}<div style="margin-top:8px">You are receiving this because you subscribed and confirmed your email at
  <a href="{{.SiteURL}}" style="color:#16865e">{{.SiteHost}}</a>.
  <a href="{{.UnsubscribeURL}}" style="color:#16865e">Unsubscribe</a>{{if .PrivacyURL}} · <a href="{{.PrivacyURL}}" style="color:#16865e">Privacy policy</a>{{end}}</div>{{end}}
</td></tr>
</table></td></tr></table></body></html>`))

// Render produces the final HTML and text bodies.
func Render(in Input) (string, string, error) {
	vars := map[string]string{"site_url": in.SiteURL, "unsubscribe_url": in.UnsubscribeURL, "preferences_url": in.PreferencesURL}
	for k, v := range in.Vars {
		vars[k] = v
	}
	body := in.ContentHTML
	for k, v := range in.RawVars {
		body = regexp.MustCompile(`\{\{\s*`+regexp.QuoteMeta(k)+`\s*\}\}`).ReplaceAllLiteralString(body, v)
	}
	content := Sanitize(Fill(body, vars, true))
	b := in.Settings.Brand
	org := in.Settings.OrganizationName
	if org == "" {
		org = in.Settings.SenderName
	}
	privacy := in.Settings.PrivacyPolicyURL
	if strings.HasPrefix(privacy, "/") {
		privacy = in.SiteURL + privacy
	}
	data := map[string]any{
		"Subject": in.Subject, "Preheader": in.Preheader, "Font": b.FontFamily, "Ink": b.InkColor, "Dark": b.DarkColor,
		"Accent": b.AccentColor, "LogoURL": b.LogoURL, "LogoText": b.LogoText, "Org": org,
		"HeaderHTML": template.HTML(Sanitize(b.HeaderHTML)), "FooterHTML": template.HTML(Sanitize(b.FooterHTML)),
		"Content": template.HTML(content), "Address": in.Settings.PostalAddress, "Marketing": in.Marketing,
		"SiteURL": in.SiteURL, "SiteHost": strings.TrimPrefix(strings.TrimPrefix(in.SiteURL, "https://"), "http://"),
		"UnsubscribeURL": in.UnsubscribeURL, "PrivacyURL": privacy,
	}
	var buf bytes.Buffer
	if err := layout.Execute(&buf, data); err != nil {
		return "", "", err
	}
	text := in.ContentText
	if text == "" {
		text = HTMLToText(content)
	} else {
		text = Fill(text, vars, false)
	}
	footer := "\n\n--\n" + org
	if in.Settings.PostalAddress != "" {
		footer += "\n" + in.Settings.PostalAddress
	}
	if in.Marketing {
		footer += "\nUnsubscribe: " + in.UnsubscribeURL
	}
	return buf.String(), text + footer, nil
}

var (
	tagRe   = regexp.MustCompile(`(?s)<[^>]*>`)
	blockRe = regexp.MustCompile(`(?i)</(p|div|h[1-6]|li|tr|br)\s*>|<br\s*/?>`)
	linkRe  = regexp.MustCompile(`(?is)<a[^>]+href="([^"]+)"[^>]*>(.*?)</a>`)
	spaceRe = regexp.MustCompile(`\n{3,}`)
)

// HTMLToText is a small, dependency-free HTML → text conversion for the
// multipart/alternative plain-text part.
func HTMLToText(s string) string {
	s = linkRe.ReplaceAllString(s, "$2 ($1)")
	s = blockRe.ReplaceAllString(s, "\n")
	s = tagRe.ReplaceAllString(s, "")
	s = html.UnescapeString(s)
	lines := strings.Split(s, "\n")
	for i := range lines {
		lines[i] = strings.TrimSpace(lines[i])
	}
	return strings.TrimSpace(spaceRe.ReplaceAllString(strings.Join(lines, "\n"), "\n\n"))
}
