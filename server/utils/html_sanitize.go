package utils

import (
	"bytes"
	stdhtml "html"
	"net/url"
	"strings"

	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

var allowedHTMLTags = map[string]bool{
	"a": true, "b": true, "blockquote": true, "br": true, "code": true,
	"div": true, "em": true, "h1": true, "h2": true, "h3": true,
	"h4": true, "h5": true, "h6": true, "hr": true, "i": true,
	"img": true, "li": true, "ol": true, "p": true, "pre": true,
	"s": true, "span": true, "strike": true, "strong": true,
	"sub": true, "sup": true, "u": true, "ul": true,
}

var blockedHTMLTags = map[string]bool{
	"base": true, "button": true, "embed": true, "form": true,
	"iframe": true, "input": true, "link": true, "meta": true,
	"object": true, "script": true, "style": true, "svg": true,
	"textarea": true,
}

// SanitizeHTML keeps a small, presentation-only HTML subset for admin-authored
// descriptions. It removes scripts, event handlers, inline styles and unsafe URLs.
func SanitizeHTML(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}

	nodes, err := html.ParseFragment(strings.NewReader(raw), &html.Node{
		Type:     html.ElementNode,
		Data:     "body",
		DataAtom: atom.Body,
	})
	if err != nil {
		return stdhtml.EscapeString(raw)
	}

	var buf bytes.Buffer
	for _, node := range nodes {
		renderSanitizedHTML(&buf, node)
	}
	return strings.TrimSpace(buf.String())
}

func renderSanitizedHTML(buf *bytes.Buffer, node *html.Node) {
	switch node.Type {
	case html.TextNode:
		buf.WriteString(stdhtml.EscapeString(node.Data))
	case html.ElementNode:
		tag := strings.ToLower(node.Data)
		if blockedHTMLTags[tag] {
			return
		}
		if !allowedHTMLTags[tag] {
			for child := node.FirstChild; child != nil; child = child.NextSibling {
				renderSanitizedHTML(buf, child)
			}
			return
		}

		buf.WriteByte('<')
		buf.WriteString(tag)
		for _, attr := range sanitizeHTMLAttrs(tag, node.Attr) {
			buf.WriteByte(' ')
			buf.WriteString(attr.Key)
			buf.WriteString(`="`)
			buf.WriteString(stdhtml.EscapeString(attr.Val))
			buf.WriteByte('"')
		}
		if tag == "br" || tag == "hr" || tag == "img" {
			buf.WriteByte('>')
			return
		}
		buf.WriteByte('>')
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			renderSanitizedHTML(buf, child)
		}
		buf.WriteString("</")
		buf.WriteString(tag)
		buf.WriteByte('>')
	default:
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			renderSanitizedHTML(buf, child)
		}
	}
}

func sanitizeHTMLAttrs(tag string, attrs []html.Attribute) []html.Attribute {
	safe := make([]html.Attribute, 0, len(attrs)+1)
	for _, attr := range attrs {
		key := strings.ToLower(strings.TrimSpace(attr.Key))
		val := strings.TrimSpace(attr.Val)
		if key == "" || strings.HasPrefix(key, "on") {
			continue
		}
		switch key {
		case "alt", "title", "aria-label":
			safe = append(safe, html.Attribute{Key: key, Val: val})
		case "class":
			if classValue := sanitizeHTMLClass(val); classValue != "" {
				safe = append(safe, html.Attribute{Key: key, Val: classValue})
			}
		case "style":
			if styleValue := sanitizeHTMLStyle(val); styleValue != "" {
				safe = append(safe, html.Attribute{Key: key, Val: styleValue})
			}
		case "href":
			if tag == "a" && isSafeHTMLURL(val, false) {
				safe = append(safe, html.Attribute{Key: key, Val: val})
			}
		case "src":
			if tag == "img" && isSafeImageURL(val) {
				safe = append(safe, html.Attribute{Key: key, Val: val})
			}
		case "width", "height":
			if tag == "img" && isSafeHTMLDimension(val) {
				safe = append(safe, html.Attribute{Key: key, Val: val})
			}
		case "target":
			if tag == "a" && (val == "_blank" || val == "_self") {
				safe = append(safe, html.Attribute{Key: key, Val: val})
			}
		case "rel":
			if tag == "a" {
				safe = append(safe, html.Attribute{Key: "rel", Val: "noopener noreferrer"})
			}
		}
	}
	if tag == "a" {
		hasRel := false
		for _, attr := range safe {
			if attr.Key == "rel" {
				hasRel = true
				break
			}
		}
		if !hasRel {
			safe = append(safe, html.Attribute{Key: "rel", Val: "noopener noreferrer"})
		}
	}
	return safe
}

func sanitizeHTMLClass(raw string) string {
	classes := strings.Fields(raw)
	safe := make([]string, 0, len(classes))
	for _, className := range classes {
		if strings.HasPrefix(className, "ql-") && isSafeHTMLToken(className) {
			safe = append(safe, className)
		}
	}
	return strings.Join(safe, " ")
}

func sanitizeHTMLStyle(raw string) string {
	declarations := strings.Split(raw, ";")
	safe := make([]string, 0, len(declarations))
	for _, declaration := range declarations {
		parts := strings.SplitN(declaration, ":", 2)
		if len(parts) != 2 {
			continue
		}
		key := strings.ToLower(strings.TrimSpace(parts[0]))
		val := strings.TrimSpace(parts[1])
		if !allowedHTMLStyleProperty(key) || !isSafeHTMLStyleValue(val) {
			continue
		}
		safe = append(safe, key+": "+val)
	}
	return strings.Join(safe, "; ")
}

func allowedHTMLStyleProperty(key string) bool {
	switch key {
	case "background-color", "color", "font-size", "text-align":
		return true
	default:
		return false
	}
}

func isSafeHTMLStyleValue(value string) bool {
	if value == "" || len(value) > 128 {
		return false
	}
	lower := strings.ToLower(value)
	for _, blocked := range []string{"expression", "javascript:", "url(", "@import", "<", ">"} {
		if strings.Contains(lower, blocked) {
			return false
		}
	}
	for _, r := range value {
		if r < 32 && r != '\t' {
			return false
		}
	}
	return true
}

func isSafeHTMLToken(value string) bool {
	if value == "" || len(value) > 64 {
		return false
	}
	for _, r := range value {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_' {
			continue
		}
		return false
	}
	return true
}

func isSafeHTMLURL(raw string, allowRelative bool) bool {
	parsed, err := url.Parse(raw)
	if err != nil {
		return false
	}
	if parsed.Scheme == "" {
		return allowRelative
	}
	switch strings.ToLower(parsed.Scheme) {
	case "http", "https", "mailto":
		return true
	default:
		return false
	}
}

func isSafeImageURL(raw string) bool {
	parsed, err := url.Parse(raw)
	if err != nil {
		return false
	}
	if parsed.Scheme == "" {
		return true
	}
	switch strings.ToLower(parsed.Scheme) {
	case "http", "https":
		return true
	case "data":
		lower := strings.ToLower(raw)
		return strings.HasPrefix(lower, "data:image/png;base64,") ||
			strings.HasPrefix(lower, "data:image/jpeg;base64,") ||
			strings.HasPrefix(lower, "data:image/jpg;base64,") ||
			strings.HasPrefix(lower, "data:image/gif;base64,") ||
			strings.HasPrefix(lower, "data:image/webp;base64,")
	default:
		return false
	}
}

func isSafeHTMLDimension(raw string) bool {
	raw = strings.TrimSpace(raw)
	if raw == "" || len(raw) > 8 {
		return false
	}
	for i, r := range raw {
		if r >= '0' && r <= '9' {
			continue
		}
		if r == '%' && i == len(raw)-1 {
			continue
		}
		return false
	}
	return true
}
