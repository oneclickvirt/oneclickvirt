package utils

import "testing"

func TestParseContainerInspectOutputAllowsFourFields(t *testing.T) {
	output := "warning: PTY allocation changed\r\n" +
		"/example|running|docker.io/library/debian:12|0123456789abcdef\n"
	record, err := ParseContainerInspectOutput(output)
	if err != nil {
		t.Fatalf("ParseContainerInspectOutput() error = %v", err)
	}
	if record.Name != "/example" || record.Status != "running" ||
		record.Image != "docker.io/library/debian:12" || record.ID != "0123456789abcdef" ||
		record.Created != "" {
		t.Fatalf("record = %#v", record)
	}
}

func TestParseContainerInspectOutputAllowsCreatedTimestampWhitespace(t *testing.T) {
	output := "warning: PTY allocation changed\r\n" +
		"/example|running|docker.io/library/debian:12|0123456789abcdef|2026-07-30 07:19:45.123456789 +0800 CST\n"
	record, err := ParseContainerInspectOutput(output)
	if err != nil {
		t.Fatalf("ParseContainerInspectOutput() error = %v", err)
	}
	if record.Name != "/example" || record.Status != "running" ||
		record.Image != "docker.io/library/debian:12" || record.ID != "0123456789abcdef" ||
		record.Created != "2026-07-30 07:19:45.123456789 +0800 CST" {
		t.Fatalf("record = %#v", record)
	}
}

func TestParseContainerInspectOutputRejectsAmbiguousOrMalformedRecords(t *testing.T) {
	valid := "/one|running|debian:12|abc123"
	for _, output := range []string{
		"/missing-id|running|debian:12",
		"/bad name|running|debian:12|abc123|2026-07-30 07:19:45 +0800 CST",
		"/bad|running|debian:12|abc123|2026-07-30\t07:19:45",
		"/bad|running|debian:12|<no value>",
		"/example|running|debian:12|/example|running|debian:12|abc123|2026-07-30T07:19:45Z",
		valid + "\n/second|exited|alpine:latest|def456",
	} {
		if _, err := ParseContainerInspectOutput(output); err == nil {
			t.Fatalf("ParseContainerInspectOutput(%q) unexpectedly succeeded", output)
		}
	}
}

func TestParseContainerInspectOutputSkipsPipeDelimitedDiagnostics(t *testing.T) {
	output := "warning: retry|status|image|id|created at\n" +
		"/example|exited|alpine:latest|def456|2026-07-30 07:20:45 +0800 CST"
	record, err := ParseContainerInspectOutput(output)
	if err != nil || record.ID != "def456" {
		t.Fatalf("ParseContainerInspectOutput() = %#v, %v", record, err)
	}
}
