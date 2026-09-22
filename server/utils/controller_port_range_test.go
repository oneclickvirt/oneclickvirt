package utils

import "testing"

func TestResolveControllerPortRangeDefaultsForBareMetal(t *testing.T) {
	t.Setenv(ControllerPortRangeStartEnv, "")
	t.Setenv(ControllerPortRangeEndEnv, "")
	rangeConfig, err := ResolveControllerPortRange()
	if err != nil {
		t.Fatal(err)
	}
	if rangeConfig.Configured || rangeConfig.Start != 10000 || rangeConfig.End != 65535 {
		t.Fatalf("default controller range = %#v", rangeConfig)
	}
}

func TestResolveControllerPortRangeRequiresCompleteValidRange(t *testing.T) {
	for _, tc := range []struct {
		name  string
		start string
		end   string
	}{
		{name: "missing end", start: "10000"},
		{name: "not numeric", start: "ten", end: "10999"},
		{name: "reversed", start: "11000", end: "10999"},
		{name: "overflow", start: "10000", end: "65536"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv(ControllerPortRangeStartEnv, tc.start)
			t.Setenv(ControllerPortRangeEndEnv, tc.end)
			if _, err := ResolveControllerPortRange(); err == nil {
				t.Fatal("invalid controller range was accepted")
			}
		})
	}
}

func TestControllerPortRangeContainsWholeSpan(t *testing.T) {
	t.Setenv(ControllerPortRangeStartEnv, "12000")
	t.Setenv(ControllerPortRangeEndEnv, "12003")
	rangeConfig, err := ResolveControllerPortRange()
	if err != nil {
		t.Fatal(err)
	}
	if !rangeConfig.Configured || !rangeConfig.Contains(12000, 4) {
		t.Fatalf("configured controller range = %#v", rangeConfig)
	}
	for _, candidate := range []struct{ start, count int }{{11999, 1}, {12003, 2}, {12000, 0}} {
		if rangeConfig.Contains(candidate.start, candidate.count) {
			t.Fatalf("range unexpectedly contains start=%d count=%d", candidate.start, candidate.count)
		}
	}
}
