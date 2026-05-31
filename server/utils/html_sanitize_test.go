package utils

import (
	"strings"
	"testing"
)

func TestSanitizeHTMLRemovesActiveContent(t *testing.T) {
	input := `<p onclick="alert(1)">ok<script>alert(1)</script><a href="javascript:alert(1)" onmouseover="x()">bad</a><a href="https://example.com" target="_blank">good</a></p>`
	got := SanitizeHTML(input)

	for _, blocked := range []string{"script", "onclick", "onmouseover", "javascript:"} {
		if strings.Contains(strings.ToLower(got), blocked) {
			t.Fatalf("sanitized HTML still contains %q: %s", blocked, got)
		}
	}
	if !strings.Contains(got, `<a href="https://example.com" target="_blank" rel="noopener noreferrer">good</a>`) {
		t.Fatalf("safe link was not preserved correctly: %s", got)
	}
}

func TestSanitizeHTMLPreservesSafeEditorFormatting(t *testing.T) {
	input := `<p class="ql-align-center evil" style="color: rgb(230, 0, 0); background-image: url(javascript:alert(1)); text-align: center"><span style="background-color: #fff">hi</span><sub>2</sub><sup>3</sup><img src="data:image/png;base64,AAAA" width="120" onerror="alert(1)"></p>`
	got := SanitizeHTML(input)

	for _, expected := range []string{
		`class="ql-align-center"`,
		`color: rgb(230, 0, 0)`,
		`text-align: center`,
		`background-color: #fff`,
		`<sub>2</sub>`,
		`<sup>3</sup>`,
		`<img src="data:image/png;base64,AAAA" width="120">`,
	} {
		if !strings.Contains(got, expected) {
			t.Fatalf("expected sanitized HTML to contain %q, got: %s", expected, got)
		}
	}

	for _, blocked := range []string{"evil", "background-image", "javascript:", "onerror"} {
		if strings.Contains(strings.ToLower(got), blocked) {
			t.Fatalf("sanitized HTML still contains %q: %s", blocked, got)
		}
	}
}
