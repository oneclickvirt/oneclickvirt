package console

import "testing"

func TestFilterTerminalInputSuppressesSerialCursorPositionReports(t *testing.T) {
	tests := []struct {
		name     string
		protocol string
		input    string
		want     string
	}{
		{
			name:     "complete report before login input",
			protocol: "serial",
			input:    "\x1b[29;99Rpassword\r",
			want:     "password\r",
		},
		{
			name:     "multiple reports embedded in input",
			protocol: "serial",
			input:    "left\x1b[1;1Rmiddle\x1b[24;80Rright",
			want:     "leftmiddleright",
		},
		{
			name:     "normal terminal escape sequence is preserved",
			protocol: "serial",
			input:    "\x1b[A",
			want:     "\x1b[A",
		},
		{
			name:     "non serial protocol is not changed",
			protocol: "exec",
			input:    "\x1b[29;99R",
			want:     "\x1b[29;99R",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := string(filterTerminalInput(tt.protocol, []byte(tt.input))); got != tt.want {
				t.Fatalf("filterTerminalInput() = %q, want %q", got, tt.want)
			}
		})
	}
}
