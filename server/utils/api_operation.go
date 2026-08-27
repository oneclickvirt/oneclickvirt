package utils

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
)

// AsyncOperationWaitOptions controls polling of an asynchronous API operation.
// A zero PollInterval deliberately means poll immediately again; callers that
// run against a real provider should set a small positive interval.
type AsyncOperationWaitOptions struct {
	Timeout      time.Duration
	PollInterval time.Duration
}

const (
	defaultAsyncOperationTimeout = 10 * time.Minute
	maxAsyncOperationBody        = 1 << 20
)

// AsyncOperationResponse is the common response envelope used by LXD and
// Incus.  The operation field is a relative /1.0/operations/... URL on most
// installations, but some API proxies return an absolute URL.
type AsyncOperationResponse struct {
	Type       string          `json:"type"`
	Status     string          `json:"status"`
	StatusCode int             `json:"status_code"`
	Operation  string          `json:"operation"`
	Error      string          `json:"error"`
	ErrorCode  int             `json:"error_code"`
	Err        string          `json:"err"`
	ErrCode    int             `json:"err_code"`
	Metadata   json.RawMessage `json:"metadata"`
}

// ParseAsyncOperationResponse validates a successful LXD/Incus mutation
// response and returns its operation URL.  Synchronous 2xx responses are
// reported with asynchronous=false.  A 202 response without an operation is
// rejected because proceeding would reintroduce the create/config race.
func ParseAsyncOperationResponse(body []byte, httpStatus int) (response AsyncOperationResponse, asynchronous bool, err error) {
	if httpStatus < http.StatusOK || httpStatus >= http.StatusMultipleChoices {
		return response, false, fmt.Errorf("API返回错误状态码 %d: %s", httpStatus, truncateAPIResponse(body))
	}

	trimmed := bytes.TrimSpace(body)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		if httpStatus == http.StatusAccepted {
			return response, true, fmt.Errorf("异步 API 响应缺少 operation")
		}
		return response, false, nil
	}
	if err := json.Unmarshal(trimmed, &response); err != nil {
		return response, false, fmt.Errorf("解析异步 API 响应失败: %w", err)
	}

	status := strings.ToLower(strings.TrimSpace(response.Status))
	if response.ErrorCode >= http.StatusBadRequest || response.ErrCode != 0 ||
		response.Error != "" || response.Err != "" ||
		status == "failure" || status == "failed" || status == "error" || status == "cancelled" || status == "canceled" {
		message := strings.TrimSpace(response.Error)
		if message == "" {
			message = strings.TrimSpace(response.Err)
		}
		if message == "" {
			message = strings.TrimSpace(response.Status)
		}
		if message == "" {
			message = "异步操作失败"
		}
		return response, false, fmt.Errorf("%s", message)
	}

	response.Operation = strings.TrimSpace(response.Operation)
	asynchronous = httpStatus == http.StatusAccepted ||
		strings.EqualFold(strings.TrimSpace(response.Type), "async") || response.Operation != ""
	if !asynchronous {
		return response, false, nil
	}
	if response.Operation == "" {
		return response, true, fmt.Errorf("异步 API 响应缺少 operation")
	}
	return response, true, nil
}

// WaitForAsyncOperation waits until an LXD/Incus operation reaches a terminal
// success state.  baseURL is used to resolve relative operation paths.
func WaitForAsyncOperation(ctx context.Context, client *http.Client, operationURL, baseURL string, options AsyncOperationWaitOptions) error {
	if client == nil {
		return fmt.Errorf("异步 API 等待缺少 HTTP 客户端")
	}
	resolvedURL, err := resolveOperationURL(operationURL, baseURL)
	if err != nil {
		return err
	}
	waitTimeout := options.Timeout
	if waitTimeout <= 0 {
		waitTimeout = defaultAsyncOperationTimeout
	}
	waitCtx, cancel := context.WithTimeout(ctx, waitTimeout)
	defer cancel()

	for {
		status, err := pollAsyncOperation(waitCtx, client, resolvedURL)
		if err != nil {
			return err
		}
		if asyncOperationFailed(status) {
			message := strings.TrimSpace(status.Error)
			if message == "" {
				message = strings.TrimSpace(status.Err)
			}
			if message == "" {
				message = strings.TrimSpace(status.Status)
			}
			if message == "" {
				message = "异步操作失败"
			}
			return fmt.Errorf("%s", message)
		}
		if asyncOperationSucceeded(status) {
			return nil
		}

		if options.PollInterval < 0 {
			options.PollInterval = 0
		}
		select {
		case <-waitCtx.Done():
			return fmt.Errorf("等待异步 API 操作完成超时: %w", waitCtx.Err())
		case <-time.After(options.PollInterval):
		}
	}
}

func pollAsyncOperation(ctx context.Context, client *http.Client, endpoint string) (AsyncOperationResponse, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return AsyncOperationResponse{}, fmt.Errorf("创建异步 API 状态请求失败: %w", err)
	}
	resp, err := client.Do(req)
	if err != nil {
		return AsyncOperationResponse{}, fmt.Errorf("查询异步 API 状态失败: %w", err)
	}
	if resp == nil || resp.Body == nil {
		return AsyncOperationResponse{}, fmt.Errorf("查询异步 API 状态返回空响应")
	}
	body, readErr := io.ReadAll(io.LimitReader(resp.Body, maxAsyncOperationBody))
	resp.Body.Close()
	if readErr != nil {
		return AsyncOperationResponse{}, fmt.Errorf("读取异步 API 状态失败: %w", readErr)
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return AsyncOperationResponse{}, fmt.Errorf("查询异步 API 状态返回 %d: %s", resp.StatusCode, truncateAPIResponse(body))
	}
	var status AsyncOperationResponse
	if err := json.Unmarshal(bytes.TrimSpace(body), &status); err != nil {
		return status, fmt.Errorf("解析异步 API 状态失败: %w", err)
	}
	return status, nil
}

func asyncOperationSucceeded(status AsyncOperationResponse) bool {
	if status.ErrCode != 0 || status.ErrorCode >= http.StatusBadRequest ||
		strings.TrimSpace(status.Err) != "" || strings.TrimSpace(status.Error) != "" {
		return false
	}
	if status.StatusCode == http.StatusOK {
		return true
	}
	switch strings.ToLower(strings.TrimSpace(status.Status)) {
	case "success", "successful", "completed", "complete", "stopped":
		return true
	default:
		return false
	}
}

func asyncOperationFailed(status AsyncOperationResponse) bool {
	if status.StatusCode >= http.StatusBadRequest || status.ErrCode != 0 ||
		status.ErrorCode >= http.StatusBadRequest || strings.TrimSpace(status.Err) != "" ||
		strings.TrimSpace(status.Error) != "" {
		return true
	}
	switch strings.ToLower(strings.TrimSpace(status.Status)) {
	case "failure", "failed", "error", "cancelled", "canceled":
		return true
	default:
		return false
	}
}

func resolveOperationURL(operationURL, baseURL string) (string, error) {
	operationURL = strings.TrimSpace(operationURL)
	if operationURL == "" {
		return "", fmt.Errorf("异步 API 响应缺少 operation URL")
	}
	parsedOperation, err := url.Parse(operationURL)
	if err != nil {
		return "", fmt.Errorf("解析异步 API operation URL 失败: %w", err)
	}
	if parsedOperation.IsAbs() {
		return parsedOperation.String(), nil
	}
	parsedBase, err := url.Parse(strings.TrimSpace(baseURL))
	if err != nil || !parsedBase.IsAbs() {
		return "", fmt.Errorf("解析异步 API 基址失败: %q", baseURL)
	}
	return parsedBase.ResolveReference(parsedOperation).String(), nil
}

func truncateAPIResponse(body []byte) string {
	text := strings.TrimSpace(string(body))
	if len(text) > 1000 {
		return text[:1000] + "..."
	}
	return text
}
