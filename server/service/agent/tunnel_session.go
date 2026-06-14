package agent

// tunnel_session.go — 隧道协议帧定义及会话辅助函数。

import (
	"fmt"
	"strings"
	"time"
)

// ──────────────────────────────────────────────────────────────────────────────
// 协议帧定义
// ──────────────────────────────────────────────────────────────────────────────

const (
	msgTypeTunnelOpen        = "tunnel_open"  // 控制端 → Agent: 请求打开隧道
	msgTypeTunnelAck         = "tunnel_ack"   // Agent → 控制端: 确认或拒绝
	msgTypeTunnelClose       = "tunnel_close" // 双向: 关闭隧道
	msgTypeTunnelKeepalive   = "tunnel_keepalive"
	msgTypeTunnelData        = "tunnel_data" // 已废弃，使用二进制帧
	tunnelSessionIdleTimeout = 5 * time.Minute
	tunnelKeepaliveInterval  = 30 * time.Second
	tunnelOpenAckAttempts    = 2
	tunnelOpenAckTimeout     = 20 * time.Second
	tunnelOpenRetryBackoff   = 200 * time.Millisecond
)

type tunnelOpenPayload struct {
	ConnID string `json:"id"`
	Host   string `json:"host"`
	Port   int    `json:"port"`
}

type tunnelAckPayload struct {
	ConnID string `json:"id"`
	OK     bool   `json:"ok"`
	Error  string `json:"error,omitempty"`
}

type tunnelClosePayload struct {
	ConnID string `json:"id"`
}

type tunnelKeepalivePayload struct {
	ConnID string `json:"id"`
}

func validateTunnelTarget(targetHost string, targetPort int) (string, bool) {
	host := strings.TrimSpace(targetHost)
	if host == "" || targetPort <= 0 || targetPort > 65535 {
		return host, false
	}
	// host 仅允许常规主机标识，拒绝路径/控制字符，避免异常输入导致隧道建立行为不可预期。
	if strings.ContainsAny(host, "/\\\r\n\t") {
		return host, false
	}
	if strings.Contains(host, " ") {
		return host, false
	}
	return host, true
}

func sendTunnelOpenWithRetry(expectedConnID string, sendOpen func() error, ackCh <-chan tunnelAckPayload, maxAttempts int, perAttemptTimeout time.Duration) (tunnelAckPayload, error) {
	if maxAttempts <= 0 {
		maxAttempts = 1
	}
	if perAttemptTimeout <= 0 {
		perAttemptTimeout = 5 * time.Second
	}
	drainAck := func() {
		for {
			select {
			case <-ackCh:
			default:
				return
			}
		}
	}

	var lastErr error
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		// 清空上一次尝试遗留的 ACK，避免陈旧响应干扰当前重试。
		drainAck()

		if err := sendOpen(); err != nil {
			lastErr = fmt.Errorf("发送 tunnel_open 失败: %w", err)
			if attempt < maxAttempts {
				time.Sleep(tunnelOpenRetryBackoff)
			}
			continue
		}

		timer := time.NewTimer(perAttemptTimeout)
		for {
			select {
			case ack := <-ackCh:
				// 仅接收当前 connID 的 ACK，忽略错会话或乱序 ACK。
				if expectedConnID != "" && ack.ConnID != expectedConnID {
					continue
				}
				if !timer.Stop() {
					select {
					case <-timer.C:
					default:
					}
				}
				if ack.OK {
					return ack, nil
				}
				reason := strings.TrimSpace(ack.Error)
				if reason == "" {
					reason = "unknown"
				}
				return ack, fmt.Errorf("tunnel_ack 返回失败: %s", reason)
			case <-timer.C:
				lastErr = fmt.Errorf("等待 tunnel_ack 超时（第 %d/%d 次）", attempt, maxAttempts)
				if attempt < maxAttempts {
					time.Sleep(tunnelOpenRetryBackoff)
				}
				goto nextAttempt
			}
		}
	nextAttempt:
	}

	if lastErr == nil {
		lastErr = fmt.Errorf("tunnel_open 握手失败")
	}
	return tunnelAckPayload{}, lastErr
}
