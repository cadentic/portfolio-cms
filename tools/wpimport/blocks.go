package main

import (
	"encoding/json"
	"html"
	"regexp"
	"strings"

	xhtml "golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

// Block is one CMS content block (see cms_pages.blocks). Only the fields the
// block type uses are set:
//
//	heading{level,text} paragraph{text} list{style,items} quote{text,cite}
//	code{language,text} divider html{html}
type Block struct {
	Type     string   `json:"type"`
	Level    int      `json:"level,omitempty"`
	Text     string   `json:"text,omitempty"`
	Style    string   `json:"style,omitempty"`
	Items    []string `json:"items,omitempty"`
	Cite     string   `json:"cite,omitempty"`
	Language string   `json:"language,omitempty"`
	HTML     string   `json:"html,omitempty"`
}

// WordPress.com shortcodes that can survive rendering as literal text.
var reShortcode = regexp.MustCompile(`\[/?(?:caption|gallery|embed|video|audio|sourcecode|code|youtube|wpvideo|slideshow|googlemaps|contact-form|contact-field|polldaddy|archives|tagcloud|blip\.tv|vimeo|soundcloud|scribd|slideshare|wufoo)\b[^\]]*\]`)

var reBrush = regexp.MustCompile(`(?:brush|lang(?:uage)?)\s*[:-]\s*([A-Za-z0-9+#_]+)`)

// inline elements kept (without attributes, except a@href) inside html blocks.
var inlineKeep = map[atom.Atom]bool{
	atom.A: true, atom.Strong: true, atom.B: true, atom.Em: true, atom.I: true,
	atom.Code: true, atom.Br: true, atom.Sub: true, atom.Sup: true, atom.U: true,
	atom.Kbd: true, atom.S: true, atom.Del: true,
}

var dropped = map[atom.Atom]bool{
	atom.Script: true, atom.Style: true, atom.Noscript: true, atom.Iframe: true,
	atom.Object: true, atom.Embed: true, atom.Form: true, atom.Input: true,
	atom.Button: true, atom.Select: true, atom.Textarea: true, atom.Svg: true,
	atom.Head: true, atom.Title: true, atom.Meta: true, atom.Link: true,
}

type converter struct {
	rewrite func(string) string // maps media and internal URLs
	blocks  []Block
	inline  []*xhtml.Node // pending inline run, flushed as one paragraph
}

// htmlToBlocks converts WordPress post HTML into CMS blocks, keeping only the
// content: theme markup, classes, inline styles, widgets and shortcodes go.
func htmlToBlocks(src string, rewrite func(string) string) []Block {
	ctx := &xhtml.Node{Type: xhtml.ElementNode, Data: "body", DataAtom: atom.Body}
	nodes, err := xhtml.ParseFragment(strings.NewReader(src), ctx)
	if err != nil {
		return []Block{{Type: "paragraph", Text: cleanText(src)}}
	}
	c := &converter{rewrite: rewrite}
	for _, n := range nodes {
		c.node(n)
	}
	c.flush()
	return c.blocks
}

func (c *converter) add(b Block) {
	c.flush()
	c.blocks = append(c.blocks, b)
}

func (c *converter) node(n *xhtml.Node) {
	switch n.Type {
	case xhtml.TextNode:
		c.inline = append(c.inline, n)
		return
	case xhtml.ElementNode:
	default:
		return
	}
	if dropped[n.DataAtom] || hasClass(n, "sharedaddy", "jp-relatedposts", "wpcnt", "wp-block-buttons") {
		return
	}
	if hasClass(n, "jetpack-slideshow-noscript") {
		return
	}
	// Jetpack slideshows keep their images in a JSON data-gallery attribute.
	if g := attr(n, "data-gallery"); g != "" {
		var imgs []struct{ Src, Alt, Caption string }
		if json.Unmarshal([]byte(g), &imgs) == nil {
			for _, im := range imgs {
				c.addImage(im.Src, im.Alt, cleanText(stripTags(im.Caption)))
			}
			return
		}
	}
	switch n.DataAtom {
	case atom.H1, atom.H2, atom.H3, atom.H4, atom.H5, atom.H6:
		level := int(n.Data[1] - '0')
		if level < 2 {
			level = 2 // the page title is the only h1
		}
		if level > 4 {
			level = 4
		}
		if t := cleanText(textOf(n)); t != "" {
			c.add(Block{Type: "heading", Level: level, Text: t})
		}
		c.images(n)
	case atom.P:
		c.flush()
		c.inline = append(c.inline, childNodes(n)...)
		c.flush()
	case atom.Ul, atom.Ol:
		style := "bullet"
		if n.DataAtom == atom.Ol {
			style = "ordered"
		}
		var items []string
		for li := n.FirstChild; li != nil; li = li.NextSibling {
			if li.DataAtom == atom.Li {
				if t := cleanText(textOf(li)); t != "" {
					items = append(items, t)
				}
			}
		}
		if len(items) > 0 {
			c.add(Block{Type: "list", Style: style, Items: items})
		}
	case atom.Blockquote:
		cite := ""
		for d := range descendants(n) {
			if d.DataAtom == atom.Cite {
				cite = cleanText(textOf(d))
			}
		}
		text := cleanText(textOf(n))
		if cite != "" {
			text = strings.TrimSpace(strings.TrimSuffix(text, cite))
		}
		if text == "" {
			break
		}
		// The old blog used blockquotes as boxes for commands, SQL and
		// output; those become code blocks.
		if cite == "" && !looksLikeProse(text) {
			c.add(Block{Type: "code", Language: guessLanguage(text), Text: codeText(textOf(n))})
			c.images(n)
			break
		}
		c.add(Block{Type: "quote", Text: text, Cite: cite})
	case atom.Pre:
		code := textOf(n)
		code = strings.Trim(strings.ReplaceAll(code, "\r\n", "\n"), "\n")
		if strings.TrimSpace(code) != "" {
			c.add(Block{Type: "code", Language: codeLanguage(n), Text: code})
		}
	case atom.Hr:
		c.add(Block{Type: "divider"})
	case atom.Img:
		c.image(n, "")
	case atom.Figure:
		caption := ""
		for d := range descendants(n) {
			if d.DataAtom == atom.Figcaption {
				caption = cleanText(textOf(d))
			}
		}
		for d := range descendants(n) {
			if d.DataAtom == atom.Img {
				c.image(d, caption)
			}
		}
	case atom.Table:
		if h := c.table(n); h != "" {
			c.add(Block{Type: "html", HTML: h})
		}
	case atom.Br:
		c.inline = append(c.inline, n)
	default:
		if inlineKeep[n.DataAtom] || n.DataAtom == atom.Span || n.DataAtom == atom.Font {
			if containsBlock(n) {
				for ch := n.FirstChild; ch != nil; ch = ch.NextSibling {
					c.node(ch)
				}
				return
			}
			c.inline = append(c.inline, n)
			return
		}
		// Layout containers (div, center, section, dl, ...): unwrap.
		if n.DataAtom == atom.Div && hasClass(n, "wp-caption") {
			caption := ""
			for d := range descendants(n) {
				if hasClass(d, "wp-caption-text") {
					caption = cleanText(textOf(d))
				}
			}
			for d := range descendants(n) {
				if d.DataAtom == atom.Img {
					c.image(d, caption)
				}
			}
			return
		}
		for ch := n.FirstChild; ch != nil; ch = ch.NextSibling {
			c.node(ch)
		}
	}
}

// flush turns the pending inline run into a paragraph (plain text) or, when it
// carries links or emphasis, a sanitized html paragraph. Images found inside
// the run become their own blocks after it.
func (c *converter) flush() {
	run := c.inline
	c.inline = nil
	if len(run) == 0 {
		return
	}
	// A paragraph that is nothing but bold text was the old blog's way of
	// writing a subheading.
	if h := boldOnly(run); h != nil {
		c.blocks = append(c.blocks, Block{Type: "heading", Level: 3, Text: strings.TrimSpace(strings.TrimRight(cleanText(textOf(h)), ":"))})
		c.images(h)
		return
	}
	var imgs []*xhtml.Node
	var b strings.Builder
	rich := false
	for _, n := range run {
		c.inlineHTML(&b, n, &rich, &imgs)
	}
	h := strings.TrimSpace(trimBr(b.String()))
	if cleanText(stripTags(h)) != "" {
		if rich {
			c.blocks = append(c.blocks, Block{Type: "html", HTML: "<p>" + h + "</p>"})
		} else {
			c.blocks = append(c.blocks, Block{Type: "paragraph", Text: paragraphText(h)})
		}
	}
	for _, img := range imgs {
		c.image(img, "")
	}
}

func (c *converter) inlineHTML(b *strings.Builder, n *xhtml.Node, rich *bool, imgs *[]*xhtml.Node) {
	switch n.Type {
	case xhtml.TextNode:
		b.WriteString(html.EscapeString(reShortcode.ReplaceAllString(n.Data, "")))
		return
	case xhtml.ElementNode:
	default:
		return
	}
	if dropped[n.DataAtom] {
		return
	}
	if n.DataAtom == atom.Img {
		*imgs = append(*imgs, n)
		return
	}
	if n.DataAtom == atom.Br {
		b.WriteString("<br>")
		return
	}
	tag := ""
	if inlineKeep[n.DataAtom] {
		tag = n.Data
		switch n.DataAtom {
		case atom.B:
			tag = "strong"
		case atom.I:
			tag = "em"
		}
	}
	if n.DataAtom == atom.A {
		href := c.rewrite(attr(n, "href"))
		onlyImg := strings.TrimSpace(textOf(n)) == ""
		if href == "" || onlyImg || !safeHref(href) {
			tag = "" // anchors around images or with no target are unwrapped
		} else {
			*rich = true
			b.WriteString(`<a href="` + html.EscapeString(href) + `">`)
			for ch := n.FirstChild; ch != nil; ch = ch.NextSibling {
				c.inlineHTML(b, ch, rich, imgs)
			}
			b.WriteString("</a>")
			return
		}
	}
	if tag != "" && strings.TrimSpace(textOf(n)) == "" {
		tag = "" // empty <code></code>, <strong> </strong>, ...
	}
	if tag != "" {
		*rich = true
		b.WriteString("<" + tag + ">")
	}
	for ch := n.FirstChild; ch != nil; ch = ch.NextSibling {
		c.inlineHTML(b, ch, rich, imgs)
	}
	if tag != "" {
		b.WriteString("</" + tag + ">")
	}
}

func (c *converter) image(n *xhtml.Node, caption string) {
	src := attr(n, "data-orig-file")
	if !reMediaURL.MatchString(src) {
		src = attr(n, "src")
	}
	c.addImage(src, attr(n, "alt"), caption)
}

func (c *converter) addImage(src, alt, caption string) {
	src = c.rewrite(src)
	if src == "" || strings.Contains(src, "pixel.wp.com") || strings.Contains(src, "gravatar.com") {
		return
	}
	alt = strings.TrimSpace(alt)
	h := `<figure><img src="` + html.EscapeString(src) + `" alt="` + html.EscapeString(alt) + `" loading="lazy">`
	if caption != "" {
		h += "<figcaption>" + html.EscapeString(caption) + "</figcaption>"
	}
	c.add(Block{Type: "html", HTML: h + "</figure>"})
}

func (c *converter) images(n *xhtml.Node) {
	for d := range descendants(n) {
		if d.DataAtom == atom.Img {
			c.image(d, "")
		}
	}
}

// table keeps only the table structure and cell text (plus links/emphasis).
func (c *converter) table(n *xhtml.Node) string {
	var b strings.Builder
	var walk func(*xhtml.Node)
	walk = func(n *xhtml.Node) {
		for ch := n.FirstChild; ch != nil; ch = ch.NextSibling {
			switch ch.DataAtom {
			case atom.Thead, atom.Tbody, atom.Tfoot, atom.Tr:
				b.WriteString("<" + ch.Data + ">")
				walk(ch)
				b.WriteString("</" + ch.Data + ">")
			case atom.Td, atom.Th:
				b.WriteString("<" + ch.Data + ">")
				var cell strings.Builder
				rich := false
				var imgs []*xhtml.Node
				for cc := ch.FirstChild; cc != nil; cc = cc.NextSibling {
					c.inlineHTML(&cell, cc, &rich, &imgs)
				}
				b.WriteString(strings.TrimSpace(cell.String()))
				b.WriteString("</" + ch.Data + ">")
			}
		}
	}
	walk(n)
	if cleanText(stripTags(b.String())) == "" {
		return ""
	}
	return "<table>" + b.String() + "</table>"
}

func codeLanguage(n *xhtml.Node) string {
	classes := attr(n, "class")
	for d := range descendants(n) {
		if d.DataAtom == atom.Code {
			classes += " " + attr(d, "class")
		}
	}
	if m := reBrush.FindStringSubmatch(classes); m != nil {
		lang := strings.ToLower(m[1])
		switch lang {
		case "plain", "text":
			return "plaintext"
		case "shell", "sh":
			return "bash"
		}
		return lang
	}
	return ""
}

func descendants(n *xhtml.Node) func(func(*xhtml.Node) bool) {
	return func(yield func(*xhtml.Node) bool) {
		var walk func(*xhtml.Node) bool
		walk = func(n *xhtml.Node) bool {
			for ch := n.FirstChild; ch != nil; ch = ch.NextSibling {
				if !yield(ch) || !walk(ch) {
					return false
				}
			}
			return true
		}
		walk(n)
	}
}

func childNodes(n *xhtml.Node) []*xhtml.Node {
	var out []*xhtml.Node
	for ch := n.FirstChild; ch != nil; ch = ch.NextSibling {
		out = append(out, ch)
	}
	return out
}

func containsBlock(n *xhtml.Node) bool {
	for d := range descendants(n) {
		switch d.DataAtom {
		case atom.P, atom.Div, atom.Ul, atom.Ol, atom.Pre, atom.Table, atom.Blockquote,
			atom.H1, atom.H2, atom.H3, atom.H4, atom.H5, atom.H6, atom.Figure, atom.Hr:
			return true
		}
	}
	return false
}

func textOf(n *xhtml.Node) string {
	if n.Type == xhtml.TextNode {
		return n.Data
	}
	var b strings.Builder
	for ch := n.FirstChild; ch != nil; ch = ch.NextSibling {
		if ch.Type == xhtml.ElementNode && dropped[ch.DataAtom] {
			continue
		}
		if ch.DataAtom == atom.Br {
			b.WriteString("\n")
			continue
		}
		b.WriteString(textOf(ch))
	}
	return b.String()
}

func attr(n *xhtml.Node, key string) string {
	for _, a := range n.Attr {
		if a.Key == key {
			return strings.TrimSpace(a.Val)
		}
	}
	return ""
}

func hasClass(n *xhtml.Node, names ...string) bool {
	if n.Type != xhtml.ElementNode {
		return false
	}
	for _, cl := range strings.Fields(attr(n, "class")) {
		for _, name := range names {
			if cl == name {
				return true
			}
		}
	}
	return false
}

func safeHref(h string) bool {
	l := strings.ToLower(h)
	return strings.HasPrefix(l, "http://") || strings.HasPrefix(l, "https://") ||
		strings.HasPrefix(l, "/") || strings.HasPrefix(l, "#") || strings.HasPrefix(l, "mailto:")
}

var reBr = regexp.MustCompile(`^(?:\s*<br>)+|(?:<br>\s*)+$`)

func trimBr(s string) string { return reBr.ReplaceAllString(strings.TrimSpace(s), "") }

func stripTags(s string) string { return html.UnescapeString(reTags.ReplaceAllString(s, " ")) }

// paragraphText turns an attribute-free inline run into plain text, keeping
// <br> as newlines.
func paragraphText(h string) string {
	lines := strings.Split(h, "<br>")
	for i, l := range lines {
		lines[i] = cleanText(html.UnescapeString(l))
	}
	return strings.Trim(strings.Join(lines, "\n"), "\n")
}

// cleanText collapses whitespace (but keeps single newlines) and removes
// leftover shortcodes and non-breaking spaces.
func cleanText(s string) string {
	s = reShortcode.ReplaceAllString(s, "")
	s = strings.ReplaceAll(s, " ", " ")
	lines := strings.Split(s, "\n")
	out := lines[:0]
	for _, l := range lines {
		l = strings.TrimSpace(reSpace.ReplaceAllString(l, " "))
		if l != "" {
			out = append(out, l)
		}
	}
	return strings.Join(out, "\n")
}

var (
	reCodeHint = regexp.MustCompile(`(?im)^\s*(?:usermod|useradd|groupadd|kill |watch |ps |df |du |top|select|insert|update|delete|alter|create|drop|grant|begin|end;|declare|exec|execute|set |spool|connect|startup|shutdown|recover|restore|backup|run\s*\{|crosscheck|rman|sqlplus|srvctl|crsctl|asmcmd|lsnrctl|opatch|adpatch|adadmin|cd |ls |cp |mv |rm |chmod|chown|mkdir|tar |vi |cat |grep |export |echo |su |sudo |yum |rpm |mount|fdisk|service |\./|/u0|/etc/|/usr/|/dev/|[#$>] |\[[a-z0-9_]+@)`)
	reSQLHint  = regexp.MustCompile(`(?i)\b(?:select|alter|create|insert|update|grant|v\$|dba_|begin)\b`)
	reBashHint = regexp.MustCompile(`(?im)^\s*(?:#|\$|\[[a-z0-9_]+@|cd |ls |chmod|chown|mkdir|export |\./|yum |rpm |vi |cat |grep |mount|service )`)
)

// looksLikeProse reports whether quoted text is a real quotation: a few
// sentence-like lines with nothing that looks like a command or output.
func looksLikeProse(text string) bool {
	lines := strings.Split(text, "\n")
	if len(lines) > 4 {
		return false
	}
	for _, l := range lines {
		l = strings.TrimSpace(l)
		if reCodeHint.MatchString(l) || strings.ContainsAny(l, ";{}=<>|\\$#_") || len(strings.Fields(l)) < 3 {
			return false
		}
	}
	return true
}

func guessLanguage(text string) string {
	sql := len(reSQLHint.FindAllString(text, -1))
	sh := len(reBashHint.FindAllString(text, -1))
	switch {
	case sql > sh:
		return "sql"
	case sh > 0:
		return "bash"
	}
	return "plaintext"
}

// codeText keeps line structure but drops non-breaking spaces and the blank
// lines left between WordPress paragraphs.
func codeText(s string) string {
	s = strings.ReplaceAll(strings.ReplaceAll(s, "\r\n", "\n"), "\u00a0", " ")
	s = reShortcode.ReplaceAllString(s, "")
	lines := strings.Split(s, "\n")
	out := lines[:0]
	for _, l := range lines {
		l = strings.TrimRight(l, " \t")
		if strings.TrimSpace(l) != "" {
			out = append(out, l)
		}
	}
	return strings.Join(out, "\n")
}

// boldOnly returns the single <strong>/<b> element that makes up an inline
// run (ignoring whitespace, <br> and empty elements), or nil.
func boldOnly(run []*xhtml.Node) *xhtml.Node {
	var found *xhtml.Node
	for _, n := range run {
		if strings.TrimSpace(textOf(n)) == "" && n.DataAtom != atom.Img {
			continue
		}
		if found != nil || (n.DataAtom != atom.Strong && n.DataAtom != atom.B) {
			return nil
		}
		found = n
	}
	if found == nil {
		return nil
	}
	text := cleanText(textOf(found))
	if len([]rune(text)) > 140 || strings.Contains(text, "\n") {
		return nil
	}
	for d := range descendants(found) {
		if d.DataAtom == atom.A {
			return nil
		}
	}
	return found
}
