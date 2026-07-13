package utils

import "testing"

func TestParseCPUCount(t *testing.T) {
	for input, want := range map[string]int{"4": 4, "0-3": 4, "0,2-3": 3, "1,1,2": 2, "": 0, "50%": 0, "3-1": 0} {
		if got := ParseCPUCount(input); got != want {
			t.Fatalf("ParseCPUCount(%q) = %d, want %d", input, got, want)
		}
	}
}
