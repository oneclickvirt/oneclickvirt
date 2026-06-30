package middleware

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestSanitizeAuditPayloadNormalizesInvalidUTF8(t *testing.T) {
	raw := string([]byte{'{', '"', 'm', '"', ':', '"', 0xe4, 0xba, '"', '}'})

	got := sanitizeAuditPayload(raw)
	if !utf8.ValidString(got) {
		t.Fatalf("sanitizeAuditPayload returned invalid UTF-8: %q", got)
	}
	if !strings.Contains(got, "\uFFFD") {
		t.Fatalf("sanitizeAuditPayload did not replace invalid bytes: %q", got)
	}
}

func TestTruncateAuditPayloadKeepsValidUTF8(t *testing.T) {
	raw := strings.Repeat("测", maxAuditPayloadBytes)

	got := truncateAuditPayload(raw)
	if !utf8.ValidString(got) {
		t.Fatalf("truncateAuditPayload returned invalid UTF-8")
	}
	if !strings.HasSuffix(got, auditTruncatedValue) {
		t.Fatalf("truncateAuditPayload did not mark truncation")
	}
	if len(got) > maxAuditPayloadBytes {
		t.Fatalf("truncateAuditPayload exceeded max bytes: %d", len(got))
	}
}
