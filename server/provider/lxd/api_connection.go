package lxd

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"go.uber.org/zap"
	"oneclickvirt/provider/health"
)

func (l *LXDProvider) probeAPIConnection(ctx context.Context) error {
	if l.apiClient == nil {
		return fmt.Errorf("LXD API客户端未初始化")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	probeCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(probeCtx, http.MethodGet, l.apiEndpoint("/1.0"), nil)
	if err != nil {
		return err
	}
	resp, err := l.apiClient.Do(req)
	if err != nil {
		return err
	}
	if resp == nil || resp.Body == nil {
		return fmt.Errorf("LXD API返回空响应")
	}
	body, readErr := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	resp.Body.Close()
	if readErr != nil {
		return readErr
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return fmt.Errorf("LXD API返回HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	return nil
}

func (l *LXDProvider) newAPIOnlyHealthChecker() health.HealthChecker {
	config := health.HealthConfig{
		ProviderID: l.config.ID, ProviderName: l.config.Name,
		Host: l.config.Host, Port: l.config.Port,
		APIEnabled: true, APIPort: 8443, APIScheme: "https",
		SSHEnabled: false, ServiceChecks: nil,
		Timeout: 10 * time.Second, CertPath: l.config.CertPath, KeyPath: l.config.KeyPath,
	}
	logger, _ := zap.NewProduction()
	return health.NewLXDHealthChecker(config, logger)
}
