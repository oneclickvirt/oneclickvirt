package agent

import "testing"

func TestParseDetectedInterfaceSkipsNoisyLines(t *testing.T) {
	if got, err := parseDetectedInterface("veth1234\r\n"); err != nil || got != "veth1234" {
		t.Fatalf("parseDetectedInterface() = %q, %v", got, err)
	}

	for _, test := range []struct {
		output string
		want   string
	}{
		{output: "Attempting to detect interface...\nveth1234", want: "veth1234"},
		{output: "veth1234\nwarning: fallback used", want: "veth1234"},
	} {
		if got, err := parseDetectedInterface(test.output); err != nil || got != test.want {
			t.Fatalf("parseDetectedInterface(%q) = %q, %v; want %q", test.output, got, err, test.want)
		}
	}

	for _, output := range []string{
		"veth1234 veth5678",
		"ERROR: no interface",
		"veth1234;touch-/tmp/x",
	} {
		if got, err := parseDetectedInterface(output); err == nil {
			t.Fatalf("parseDetectedInterface(%q) = %q, want error", output, got)
		}
	}
}
