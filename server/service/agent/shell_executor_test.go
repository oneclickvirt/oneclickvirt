package agent

import (
	"strings"
	"testing"
	"time"
)

func TestNormalizeConnWaitTimeout(t *testing.T) {
	cases := []struct {
		name   string
		in     time.Duration
		expect time.Duration
	}{
		{name: "zero defaults", in: 0, expect: 30 * time.Second},
		{name: "negative defaults", in: -1 * time.Second, expect: 30 * time.Second},
		{name: "too small clamped", in: 500 * time.Millisecond, expect: 3 * time.Second},
		{name: "normal kept", in: 12 * time.Second, expect: 12 * time.Second},
		{name: "too large clamped", in: 120 * time.Second, expect: 60 * time.Second},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := normalizeConnWaitTimeout(tc.in)
			if got != tc.expect {
				t.Fatalf("normalizeConnWaitTimeout(%v) = %v, want %v", tc.in, got, tc.expect)
			}
		})
	}
}

func TestNormalizeSemaphoreWaitTimeout(t *testing.T) {
	cases := []struct {
		name   string
		in     time.Duration
		expect time.Duration
	}{
		{name: "zero defaults", in: 0, expect: 10 * time.Second},
		{name: "tiny clamped to one second", in: 500 * time.Millisecond, expect: 1 * time.Second},
		{name: "half timeout", in: 6 * time.Second, expect: 3 * time.Second},
		{name: "large capped", in: 60 * time.Second, expect: 10 * time.Second},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := normalizeSemaphoreWaitTimeout(tc.in)
			if got != tc.expect {
				t.Fatalf("normalizeSemaphoreWaitTimeout(%v) = %v, want %v", tc.in, got, tc.expect)
			}
		})
	}
}

func TestGetConnRespectsBoundedWaitTimeout(t *testing.T) {
	hub := &AgentHub{conns: make(map[uint]*AgentConn)}
	exec := NewAgentShellExecutor(42, hub)

	start := time.Now()
	_, err := exec.getConn(2 * time.Second) // clamped to 3s minimum
	elapsed := time.Since(start)

	if err == nil {
		t.Fatalf("expected getConn to fail when no agent connection exists")
	}
	if !strings.Contains(err.Error(), "agent not connected") {
		t.Fatalf("unexpected error: %v", err)
	}
	if elapsed < 2800*time.Millisecond {
		t.Fatalf("getConn returned too early: %v", elapsed)
	}
	if elapsed > 9*time.Second {
		t.Fatalf("getConn waited too long (expected bounded wait): %v", elapsed)
	}
}

func TestExecuteWithTimeoutFailsWithinBoundWhenNoConn(t *testing.T) {
	hub := &AgentHub{conns: make(map[uint]*AgentConn)}
	exec := NewAgentShellExecutor(7, hub)

	start := time.Now()
	_, err := exec.ExecuteWithTimeout("echo test", 4*time.Second)
	elapsed := time.Since(start)

	if err == nil {
		t.Fatalf("expected ExecuteWithTimeout to fail when no agent connection exists")
	}
	if !strings.Contains(err.Error(), "agent not connected") {
		t.Fatalf("unexpected error: %v", err)
	}
	if elapsed < 3500*time.Millisecond {
		t.Fatalf("ExecuteWithTimeout returned too early: %v", elapsed)
	}
	if elapsed > 12*time.Second {
		t.Fatalf("ExecuteWithTimeout exceeded bounded timeout budget: %v", elapsed)
	}
}
