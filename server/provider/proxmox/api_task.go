package proxmox

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"oneclickvirt/global"
	"oneclickvirt/utils"

	"go.uber.org/zap"
)

// A just-finished PVE task can report terminal status a few seconds before
// its per-guest config lock disappears.  Five bounded attempts cover that
// hand-off without turning a persistent provider fault into an unbounded wait.
const proxmoxAPIConfigLockRetryCount = 5

var (
	proxmoxAPITaskWaitTimeout      = 10 * time.Minute
	proxmoxAPITaskPollInterval     = 2 * time.Second
	proxmoxAPIConfigLockRetryDelay = 2 * time.Second
)

type proxmoxAPIResponseError struct {
	StatusCode int
	Body       string
}

func (e *proxmoxAPIResponseError) Error() string {
	return fmt.Sprintf("status %d, response: %s", e.StatusCode, e.Body)
}

type proxmoxAPITaskStatus struct {
	Status     string `json:"status"`
	ExitStatus string `json:"exitstatus"`
}

// submitProxmoxAPITask sends a PVE mutation request and returns the UPID that
// PVE uses to expose its asynchronous completion state.
func (p *ProxmoxProvider) submitProxmoxAPITask(ctx context.Context, method, endpoint string, payload []byte) (string, error) {
	data, err := p.submitProxmoxAPIRequest(ctx, method, endpoint, payload)
	if err != nil {
		return "", err
	}
	// PVE normally returns an UPID for create requests, but a few compatible
	// API/proxy implementations complete the mutation synchronously and return
	// data:null. Treat that explicit success response as already complete so the
	// API-first path remains compatible with those nodes.
	if bytes.Equal(bytes.TrimSpace(data), []byte("null")) {
		return "", nil
	}

	var upid string
	if err := json.Unmarshal(data, &upid); err != nil {
		return "", fmt.Errorf("PVE API响应未返回任务UPID: %w", err)
	}
	upid = strings.TrimSpace(upid)
	if upid == "" {
		return "", fmt.Errorf("PVE API响应未返回任务UPID")
	}
	return upid, nil
}

// submitProxmoxAPITaskAndWait is the common path for guest mutations that
// PVE executes asynchronously.  A successful HTTP response only confirms
// that PVE accepted the request; the returned UPID owns the guest lock until
// the task reaches a terminal state.  Waiting here keeps lifecycle calls from
// racing a following operation such as stop -> delete or create -> configure.
func (p *ProxmoxProvider) submitProxmoxAPITaskAndWait(ctx context.Context, method, endpoint string, payload []byte, operation string) error {
	upid, err := p.submitProxmoxAPITask(ctx, method, endpoint, payload)
	if err != nil {
		return err
	}
	if upid == "" {
		return nil
	}
	return p.waitForProxmoxAPITask(ctx, upid, operation)
}

// submitProxmoxAPIRequest returns the raw PVE data field.  Create calls need
// an asynchronous UPID, whereas a successful config update is normally
// synchronous and returns data:null.
func (p *ProxmoxProvider) submitProxmoxAPIRequest(ctx context.Context, method, endpoint string, payload []byte) (json.RawMessage, error) {
	if p.apiClient == nil {
		return nil, fmt.Errorf("PVE API客户端未初始化")
	}
	req, err := http.NewRequestWithContext(ctx, method, endpoint, bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	p.setAPIAuth(req)

	resp, err := p.apiClient.Do(req)
	if err != nil {
		return nil, err
	}
	if resp == nil || resp.Body == nil {
		return nil, fmt.Errorf("PVE API返回空响应")
	}
	body, readErr := io.ReadAll(resp.Body)
	resp.Body.Close()
	if readErr != nil {
		return nil, fmt.Errorf("读取PVE API响应失败: %w", readErr)
	}

	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return nil, &proxmoxAPIResponseError{
			StatusCode: resp.StatusCode,
			Body:       utils.TruncateString(strings.TrimSpace(string(body)), 1000),
		}
	}

	var result struct {
		Data json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("解析PVE API响应失败: %w", err)
	}
	if len(result.Data) == 0 {
		return nil, fmt.Errorf("PVE API响应缺少data字段")
	}
	return result.Data, nil
}

func (p *ProxmoxProvider) getProxmoxAPITaskStatus(ctx context.Context, upid string) (proxmoxAPITaskStatus, error) {
	if p.apiClient == nil {
		return proxmoxAPITaskStatus{}, fmt.Errorf("PVE API客户端未初始化")
	}
	endpoint := p.apiEndpoint(fmt.Sprintf(
		"/api2/json/nodes/%s/tasks/%s/status",
		url.PathEscape(strings.TrimSpace(p.node)),
		url.PathEscape(strings.TrimSpace(upid)),
	))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return proxmoxAPITaskStatus{}, err
	}
	p.setAPIAuth(req)

	resp, err := p.apiClient.Do(req)
	if err != nil {
		return proxmoxAPITaskStatus{}, err
	}
	if resp == nil || resp.Body == nil {
		return proxmoxAPITaskStatus{}, fmt.Errorf("PVE任务状态返回空响应")
	}
	body, readErr := io.ReadAll(resp.Body)
	resp.Body.Close()
	if readErr != nil {
		return proxmoxAPITaskStatus{}, fmt.Errorf("读取PVE任务状态失败: %w", readErr)
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return proxmoxAPITaskStatus{}, &proxmoxAPIResponseError{
			StatusCode: resp.StatusCode,
			Body:       utils.TruncateString(strings.TrimSpace(string(body)), 1000),
		}
	}

	var result struct {
		Data proxmoxAPITaskStatus `json:"data"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return proxmoxAPITaskStatus{}, fmt.Errorf("解析PVE任务状态失败: %w", err)
	}
	result.Data.Status = strings.TrimSpace(result.Data.Status)
	result.Data.ExitStatus = strings.TrimSpace(result.Data.ExitStatus)
	if result.Data.Status == "" {
		return proxmoxAPITaskStatus{}, fmt.Errorf("PVE任务状态响应缺少status")
	}
	return result.Data, nil
}

// waitForProxmoxAPITask prevents a follow-up mutation from racing the PVE
// worker which owns the guest configuration lock until its UPID is terminal.
func (p *ProxmoxProvider) waitForProxmoxAPITask(ctx context.Context, upid, operation string) error {
	if strings.TrimSpace(upid) == "" {
		return fmt.Errorf("等待PVE %s任务时缺少UPID", operation)
	}

	waitCtx, cancel := context.WithTimeout(ctx, proxmoxAPITaskWaitTimeout)
	defer cancel()

	for {
		status, err := p.getProxmoxAPITaskStatus(waitCtx, upid)
		if err != nil {
			return fmt.Errorf("查询PVE %s任务状态失败: %w", operation, err)
		}

		switch strings.ToLower(status.Status) {
		case "stopped", "completed", "success":
			if status.ExitStatus != "" && !strings.EqualFold(status.ExitStatus, "OK") {
				return fmt.Errorf("PVE %s任务失败: %s", operation, status.ExitStatus)
			}
			return nil
		case "error", "failed":
			if status.ExitStatus == "" {
				return fmt.Errorf("PVE %s任务失败: %s", operation, status.Status)
			}
			return fmt.Errorf("PVE %s任务失败: %s", operation, status.ExitStatus)
		}

		select {
		case <-waitCtx.Done():
			return fmt.Errorf("等待PVE %s任务完成超时: %w", operation, waitCtx.Err())
		case <-time.After(proxmoxAPITaskPollInterval):
		}
	}
}

func isProxmoxConfigLockError(err error) bool {
	if err == nil {
		return false
	}
	message := strings.ToLower(err.Error())
	lockMessage := strings.Contains(message, "can't lock file") ||
		strings.Contains(message, "cannot lock file") ||
		strings.Contains(message, "lock file")
	timeoutMessage := strings.Contains(message, "timeout") ||
		strings.Contains(message, "timed out")
	return lockMessage && timeoutMessage
}

func (p *ProxmoxProvider) submitProxmoxConfigTask(ctx context.Context, endpoint string, payload []byte) (string, error) {
	data, err := p.submitProxmoxAPIRequest(ctx, http.MethodPut, endpoint, payload)
	if err != nil {
		return "", err
	}
	if bytes.Equal(bytes.TrimSpace(data), []byte("null")) {
		return "", nil
	}

	var upid string
	if err := json.Unmarshal(data, &upid); err != nil {
		return "", fmt.Errorf("解析PVE配置任务响应失败: %w", err)
	}
	upid = strings.TrimSpace(upid)
	if upid == "" {
		return "", fmt.Errorf("PVE配置任务响应未返回UPID")
	}
	return upid, nil
}

func (p *ProxmoxProvider) submitProxmoxConfigTaskWithRetry(ctx context.Context, endpoint string, payload []byte, operation string) error {
	return p.retryProxmoxGuestLock(ctx, operation, func() error {
		upid, err := p.submitProxmoxConfigTask(ctx, endpoint, payload)
		if err == nil && upid != "" {
			err = p.waitForProxmoxAPITask(ctx, upid, operation)
		}
		return err
	})
}

// retryProxmoxGuestLock retries a mutation only when PVE explicitly reports
// its short-lived per-guest config lock.  Create, network, and start tasks can
// all cross this hand-off, while validation and permission failures must still
// return immediately.
func (p *ProxmoxProvider) retryProxmoxGuestLock(ctx context.Context, operation string, mutation func() error) error {
	var lastErr error
	for attempt := 1; attempt <= proxmoxAPIConfigLockRetryCount; attempt++ {
		err := mutation()
		if err == nil {
			return nil
		}
		lastErr = err
		if !isProxmoxConfigLockError(err) || attempt == proxmoxAPIConfigLockRetryCount {
			return err
		}

		global.APP_LOG.Warn("PVE配置锁暂时繁忙，等待后重试",
			zap.String("operation", operation),
			zap.Int("attempt", attempt),
			zap.Error(err))
		retryDelay := time.Duration(attempt) * proxmoxAPIConfigLockRetryDelay
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(retryDelay):
		}
	}
	return lastErr
}

func (p *ProxmoxProvider) submitProxmoxAPITaskAndWaitWithLockRetry(ctx context.Context, method, endpoint string, payload []byte, operation string) error {
	return p.retryProxmoxGuestLock(ctx, operation, func() error {
		return p.submitProxmoxAPITaskAndWait(ctx, method, endpoint, payload, operation)
	})
}
