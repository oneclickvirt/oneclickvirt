package agent

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func TestValidateTunnelTarget(t *testing.T) {
	tests := []struct {
		name       string
		host       string
		port       int
		expectHost string
		expectOK   bool
	}{
		{name: "normal host", host: "10.0.0.5", port: 22, expectHost: "10.0.0.5", expectOK: true},
		{name: "trimmed host", host: "  app.internal  ", port: 8080, expectHost: "app.internal", expectOK: true},
		{name: "empty host", host: "   ", port: 80, expectHost: "", expectOK: false},
		{name: "host with path", host: "10.0.0.5/api", port: 80, expectHost: "10.0.0.5/api", expectOK: false},
		{name: "host with backslash", host: "10.0.0.5\\bad", port: 80, expectHost: "10.0.0.5\\bad", expectOK: false},
		{name: "host with internal space", host: "app internal", port: 80, expectHost: "app internal", expectOK: false},
		{name: "zero port", host: "10.0.0.5", port: 0, expectHost: "10.0.0.5", expectOK: false},
		{name: "negative port", host: "10.0.0.5", port: -1, expectHost: "10.0.0.5", expectOK: false},
		{name: "overflow port", host: "10.0.0.5", port: 70000, expectHost: "10.0.0.5", expectOK: false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			host, ok := validateTunnelTarget(tc.host, tc.port)
			if host != tc.expectHost || ok != tc.expectOK {
				t.Fatalf("validateTunnelTarget(%q, %d) = (%q, %v), want (%q, %v)", tc.host, tc.port, host, ok, tc.expectHost, tc.expectOK)
			}
		})
	}
}

func TestSendTunnelOpenWithRetrySuccessOnSecondAttempt(t *testing.T) {
	ackCh := make(chan tunnelAckPayload, 1)
	attempts := 0

	_, err := sendTunnelOpenWithRetry("a", func() error {
		attempts++
		if attempts == 2 {
			ackCh <- tunnelAckPayload{ConnID: "a", OK: true}
		}
		return nil
	}, ackCh, 2, 20*time.Millisecond)

	if err != nil {
		t.Fatalf("expected success on second attempt, got error: %v", err)
	}
	if attempts != 2 {
		t.Fatalf("expected 2 attempts, got %d", attempts)
	}
}

func TestSendTunnelOpenWithRetryAckFailureStopsImmediately(t *testing.T) {
	ackCh := make(chan tunnelAckPayload, 1)
	attempts := 0

	_, err := sendTunnelOpenWithRetry("a", func() error {
		attempts++
		ackCh <- tunnelAckPayload{ConnID: "a", OK: false, Error: "denied"}
		return nil
	}, ackCh, 2, 20*time.Millisecond)

	if err == nil {
		t.Fatalf("expected error when ack is not ok")
	}
	if !strings.Contains(err.Error(), "denied") {
		t.Fatalf("expected denied reason in error, got: %v", err)
	}
	if attempts != 1 {
		t.Fatalf("expected no retry on explicit ack failure, got %d attempts", attempts)
	}
}

func TestSendTunnelOpenWithRetryAllAttemptsTimeout(t *testing.T) {
	ackCh := make(chan tunnelAckPayload, 1)
	attempts := 0

	_, err := sendTunnelOpenWithRetry("a", func() error {
		attempts++
		return nil
	}, ackCh, 2, 15*time.Millisecond)

	if err == nil {
		t.Fatalf("expected timeout error")
	}
	if !strings.Contains(err.Error(), "超时") {
		t.Fatalf("expected timeout keyword in error, got: %v", err)
	}
	if attempts != 2 {
		t.Fatalf("expected 2 attempts, got %d", attempts)
	}
}

func TestSendTunnelOpenWithRetryWriteErrorThenSuccess(t *testing.T) {
	ackCh := make(chan tunnelAckPayload, 1)
	attempts := 0

	_, err := sendTunnelOpenWithRetry("a", func() error {
		attempts++
		if attempts == 1 {
			return errors.New("temporary write error")
		}
		ackCh <- tunnelAckPayload{ConnID: "a", OK: true}
		return nil
	}, ackCh, 2, 20*time.Millisecond)

	if err != nil {
		t.Fatalf("expected retry to recover after write error, got: %v", err)
	}
	if attempts != 2 {
		t.Fatalf("expected 2 attempts, got %d", attempts)
	}
}

func TestSendTunnelOpenWithRetryDrainsStaleAck(t *testing.T) {
	ackCh := make(chan tunnelAckPayload, 2)
	attempts := 0

	_, err := sendTunnelOpenWithRetry("fresh", func() error {
		attempts++
		if attempts == 1 {
			// 模拟第一次尝试失败后，通道里残留了一个过期失败 ACK。
			ackCh <- tunnelAckPayload{ConnID: "stale", OK: false, Error: "stale-fail"}
			return errors.New("temporary write error")
		}
		ackCh <- tunnelAckPayload{ConnID: "fresh", OK: true}
		return nil
	}, ackCh, 2, 50*time.Millisecond)

	if err != nil {
		t.Fatalf("expected stale ack to be drained, got error: %v", err)
	}
	if attempts != 2 {
		t.Fatalf("expected 2 attempts, got %d", attempts)
	}
}

func TestSendTunnelOpenWithRetryIgnoresMismatchedConnIDAck(t *testing.T) {
	ackCh := make(chan tunnelAckPayload, 2)
	attempts := 0

	_, err := sendTunnelOpenWithRetry("wanted", func() error {
		attempts++
		if attempts == 1 {
			// 先到达错会话 ACK，应被忽略。
			ackCh <- tunnelAckPayload{ConnID: "other", OK: false, Error: "wrong-session"}
			ackCh <- tunnelAckPayload{ConnID: "wanted", OK: true}
		}
		return nil
	}, ackCh, 1, 50*time.Millisecond)

	if err != nil {
		t.Fatalf("expected mismatched connID ack to be ignored, got: %v", err)
	}
	if attempts != 1 {
		t.Fatalf("expected single attempt, got %d", attempts)
	}
}

func TestSendTunnelOpenWithRetryIgnoresEmptyConnIDAck(t *testing.T) {
	ackCh := make(chan tunnelAckPayload, 2)
	attempts := 0

	_, err := sendTunnelOpenWithRetry("wanted", func() error {
		attempts++
		if attempts == 1 {
			// 先到达空 connID ACK，应被忽略。
			ackCh <- tunnelAckPayload{ConnID: "", OK: false, Error: "missing-id"}
			ackCh <- tunnelAckPayload{ConnID: "wanted", OK: true}
		}
		return nil
	}, ackCh, 1, 50*time.Millisecond)

	if err != nil {
		t.Fatalf("expected empty connID ack to be ignored, got: %v", err)
	}
	if attempts != 1 {
		t.Fatalf("expected single attempt, got %d", attempts)
	}
}

func TestOpenTunnelConnRejectsInvalidTargetBeforeHubLookup(t *testing.T) {
	tests := []struct {
		name string
		host string
		port int
	}{
		{name: "empty host", host: " ", port: 22},
		{name: "bad port", host: "127.0.0.1", port: 0},
		{name: "host with path", host: "127.0.0.1/root", port: 22},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			conn, err := OpenTunnelConn(12345, tc.host, tc.port)
			if err == nil {
				if conn != nil {
					_ = conn.Close()
				}
				t.Fatalf("expected invalid tunnel target to fail")
			}
			if !strings.Contains(err.Error(), "无效的 agent 隧道目标") {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}
