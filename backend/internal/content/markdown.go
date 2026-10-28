package content

import (
	"regexp"
	"strings"
)

var (
	orderedRe = regexp.MustCompile(`^\d+[.)]\s+`)
	headingRe = regexp.MustCompile(`^(#{1,4})\s+(.*)$`)
)

// BlocksFromMarkdown converts a small, predictable Markdown subset into
// builder blocks, so pages can be authored from an MCP client or pasted text:
// "#"-headings, paragraphs, "-"/"1." lists, "> " quotes, ``` code fences,
// "---" dividers and "!!! note" callouts. Inline **bold**, *italic*, `code`
// and [links](url) stay in the text and are rendered safely by the frontend.
func BlocksFromMarkdown(md string) []Block {
	lines := strings.Split(strings.ReplaceAll(md, "\r\n", "\n"), "\n")
	var out []Block
	var para []string
	flush := func() {
		if len(para) > 0 {
			out = append(out, Block{Type: "paragraph", Text: strings.Join(para, " ")})
			para = nil
		}
	}
	for i := 0; i < len(lines); i++ {
		line := strings.TrimRight(lines[i], " \t")
		trim := strings.TrimSpace(line)
		switch {
		case trim == "":
			flush()
		case strings.HasPrefix(trim, "```"):
			flush()
			lang := strings.TrimSpace(strings.TrimPrefix(trim, "```"))
			var code []string
			for i++; i < len(lines) && !strings.HasPrefix(strings.TrimSpace(lines[i]), "```"); i++ {
				code = append(code, lines[i])
			}
			out = append(out, Block{Type: "code", Language: lang, Text: strings.Join(code, "\n")})
		case headingRe.MatchString(trim):
			flush()
			m := headingRe.FindStringSubmatch(trim)
			lvl := len(m[1])
			if lvl < 2 {
				lvl = 2 // the page title is the only h1
			}
			out = append(out, Block{Type: "heading", Level: lvl, Text: m[2]})
		case trim == "---" || trim == "***":
			flush()
			out = append(out, Block{Type: "divider"})
		case strings.HasPrefix(trim, ">"):
			flush()
			var q []string
			for ; i < len(lines) && strings.HasPrefix(strings.TrimSpace(lines[i]), ">"); i++ {
				q = append(q, strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(lines[i]), ">")))
			}
			i--
			out = append(out, Block{Type: "quote", Text: strings.Join(q, " ")})
		case strings.HasPrefix(trim, "!!! "):
			flush()
			tone := strings.Fields(strings.TrimPrefix(trim, "!!! "))
			b := Block{Type: "callout", Tone: "info"}
			if len(tone) > 0 && (tone[0] == "warning" || tone[0] == "success") {
				b.Tone = tone[0]
			}
			var body []string
			for i++; i < len(lines) && strings.TrimSpace(lines[i]) != ""; i++ {
				body = append(body, strings.TrimSpace(lines[i]))
			}
			b.Text = strings.Join(body, " ")
			out = append(out, b)
		case strings.HasPrefix(trim, "- ") || strings.HasPrefix(trim, "* ") || orderedRe.MatchString(trim):
			flush()
			b := Block{Type: "list", Style: "bullet"}
			if orderedRe.MatchString(trim) {
				b.Style = "numbered"
			}
			for ; i < len(lines); i++ {
				t := strings.TrimSpace(lines[i])
				switch {
				case strings.HasPrefix(t, "- ") || strings.HasPrefix(t, "* "):
					b.Items = append(b.Items, t[2:])
				case orderedRe.MatchString(t):
					b.Items = append(b.Items, orderedRe.ReplaceAllString(t, ""))
				default:
					goto done
				}
			}
		done:
			i--
			out = append(out, b)
		default:
			para = append(para, trim)
		}
	}
	flush()
	if out == nil {
		out = []Block{}
	}
	return out
}
