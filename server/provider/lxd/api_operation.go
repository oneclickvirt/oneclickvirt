package lxd

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"time"

	"oneclickvirt/utils"
)

var (
	lxdAPIOperationWaitTimeout  = 10 * time.Minute
	lxdAPIOperationPollInterval = 500 * time.Millisecond
)

func (l *LXDProvider) apiEndpoint(path string) string {
	return utils.BuildEndpointURL("https", l.config.Host, 8443, path)
}

// waitForAPIMutation consumes an LXD response and waits for its asynchronous
// operation before the caller performs another mutation on the same instance.
// LXD and compatible API proxies may return a synchronous 200, so that form is
// accepted without requiring an operation URL.
func (l *LXDProvider) waitForAPIMutation(ctx context.Context, resp *http.Response, operation string) error {
	if resp == nil {
		return fmt.Errorf("LXD %s响应为空", operation)
	}
	if resp.Body == nil {
		return fmt.Errorf("LXD %s响应缺少响应体", operation)
	}
	body, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil {
		return fmt.Errorf("读取LXD %s响应失败: %w", operation, err)
	}
	parsed, asynchronous, err := utils.ParseAsyncOperationResponse(body, resp.StatusCode)
	if err != nil {
		return fmt.Errorf("LXD %s失败: %w", operation, err)
	}
	if !asynchronous {
		return nil
	}
	baseURL := l.apiEndpoint("/")
	if baseURL == "" {
		return fmt.Errorf("LXD %s无法构造API基址", operation)
	}
	if err := utils.WaitForAsyncOperation(ctx, l.apiClient, parsed.Operation, baseURL, utils.AsyncOperationWaitOptions{
		Timeout:      lxdAPIOperationWaitTimeout,
		PollInterval: lxdAPIOperationPollInterval,
	}); err != nil {
		return fmt.Errorf("等待LXD %s完成失败: %w", operation, err)
	}
	return nil
}
