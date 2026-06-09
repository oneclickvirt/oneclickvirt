package utils

import (
	"bytes"
	"fmt"
	"regexp"
	"strings"
)

var (
	mdHeadingRE  = regexp.MustCompile(`^(#{1,6})\s+(.+)$`)
	mdOrderedRE  = regexp.MustCompile(`^\d+\.\s+(.+)$`)
	mdLinkRE     = regexp.MustCompile(`\[([^\]]+)\]\(([^\s)]+)\)`)
	mdBoldRE     = regexp.MustCompile(`\*\*([^*]+)\*\*`)
	mdItalicRE   = regexp.MustCompile(`(^|[^*])\*([^*]+)\*`)
	mdInlineCode = regexp.MustCompile("`([^`]+)`")
	mdHrRE       = regexp.MustCompile(`^\s*(-{3,}|_{3,}|\*{3,})\s*$`)
)

// MarkdownToSafeHTML renders a small GitHub-like Markdown subset and then
// sanitizes the result. Admin-authored safe HTML is preserved by SanitizeHTML;
// scripts, unsafe URLs, event handlers and dangerous tags are stripped.
func MarkdownToSafeHTML(raw string) string {
	raw = strings.TrimSpace(strings.ReplaceAll(raw, "\r\n", "\n"))
	if raw == "" {
		return ""
	}

	lines := strings.Split(raw, "\n")
	var buf bytes.Buffer
	inUL := false
	inOL := false
	inCode := false
	var codeBuf bytes.Buffer

	closeLists := func() {
		if inUL {
			buf.WriteString("</ul>")
			inUL = false
		}
		if inOL {
			buf.WriteString("</ol>")
			inOL = false
		}
	}

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "```") {
			if inCode {
				buf.WriteString("<pre><code>")
				buf.WriteString(escapeHTMLText(strings.TrimSuffix(codeBuf.String(), "\n")))
				buf.WriteString("</code></pre>")
				codeBuf.Reset()
				inCode = false
			} else {
				closeLists()
				inCode = true
			}
			continue
		}
		if inCode {
			codeBuf.WriteString(line)
			codeBuf.WriteByte('\n')
			continue
		}
		if trimmed == "" {
			closeLists()
			continue
		}
		if mdHrRE.MatchString(trimmed) {
			closeLists()
			buf.WriteString("<hr>")
			continue
		}
		if m := mdHeadingRE.FindStringSubmatch(trimmed); len(m) == 3 {
			closeLists()
			level := len(m[1])
			buf.WriteString(fmt.Sprintf("<h%d>%s</h%d>", level, renderMarkdownInline(m[2]), level))
			continue
		}
		if strings.HasPrefix(trimmed, "> ") {
			closeLists()
			buf.WriteString("<blockquote>")
			buf.WriteString(renderMarkdownInline(strings.TrimSpace(strings.TrimPrefix(trimmed, ">"))))
			buf.WriteString("</blockquote>")
			continue
		}
		if strings.HasPrefix(trimmed, "- ") || strings.HasPrefix(trimmed, "* ") || strings.HasPrefix(trimmed, "+ ") {
			if inOL {
				buf.WriteString("</ol>")
				inOL = false
			}
			if !inUL {
				buf.WriteString("<ul>")
				inUL = true
			}
			buf.WriteString("<li>")
			buf.WriteString(renderMarkdownInline(strings.TrimSpace(trimmed[2:])))
			buf.WriteString("</li>")
			continue
		}
		if m := mdOrderedRE.FindStringSubmatch(trimmed); len(m) == 2 {
			if inUL {
				buf.WriteString("</ul>")
				inUL = false
			}
			if !inOL {
				buf.WriteString("<ol>")
				inOL = true
			}
			buf.WriteString("<li>")
			buf.WriteString(renderMarkdownInline(m[1]))
			buf.WriteString("</li>")
			continue
		}
		closeLists()
		buf.WriteString("<p>")
		buf.WriteString(renderMarkdownInline(trimmed))
		buf.WriteString("</p>")
	}
	closeLists()
	if inCode {
		buf.WriteString("<pre><code>")
		buf.WriteString(escapeHTMLText(strings.TrimSuffix(codeBuf.String(), "\n")))
		buf.WriteString("</code></pre>")
	}
	return SanitizeHTML(buf.String())
}

func renderMarkdownInline(s string) string {
	s = mdInlineCode.ReplaceAllString(s, "<code>$1</code>")
	s = mdLinkRE.ReplaceAllString(s, `<a href="$2" target="_blank" rel="noopener noreferrer">$1</a>`)
	s = mdBoldRE.ReplaceAllString(s, "<strong>$1</strong>")
	s = mdItalicRE.ReplaceAllString(s, "$1<em>$2</em>")
	return s
}

func escapeHTMLText(s string) string {
	r := strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;", "'", "&#39;")
	return r.Replace(s)
}
