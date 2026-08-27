package incus

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"time"

	"oneclickvirt/utils"
)

var (
	incusAPIOperationWaitTimeout  = 10 * time.Minute
	incusAPIOperationPollInterval = 500 * time.Millisecond
)

func (i *IncusProvider) apiEndpoint(path string) string {
	return utils.BuildEndpointURL("https", i.config.Host, 8443, path)
}

// waitForAPIMutation consumes an Incus response and waits for its asynchronous
// operation before another mutation is sent to the same instance.
func (i *IncusProvider) waitForAPIMutation(ctx context.Context, resp *http.Response, operation string) error {
	if resp == nil {
		return fmt.Errorf("Incus %s响应为空", operation)
	}
	if resp.Body == nil {
		return fmt.Errorf("Incus %s响应缺少响应体", operation)
	}
	body, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil {
		return fmt.Errorf("读取Incus %s响应失败: %w", operation, err)
	}
	parsed, asynchronous, err := utils.ParseAsyncOperationResponse(body, resp.StatusCode)
	if err != nil {
		return fmt.Errorf("Incus %s失败: %w", operation, err)
	}
	if !asynchronous {
		return nil
	}
	baseURL := i.apiEndpoint("/")
	if baseURL == "" {
		return fmt.Errorf("Incus %s无法构造API基址", operation)
	}
	if err := utils.WaitForAsyncOperation(ctx, i.apiClient, parsed.Operation, baseURL, utils.AsyncOperationWaitOptions{
		Timeout:      incusAPIOperationWaitTimeout,
		PollInterval: incusAPIOperationPollInterval,
	}); err != nil {
		return fmt.Errorf("等待Incus %s完成失败: %w", operation, err)
	}
	return nil
}
