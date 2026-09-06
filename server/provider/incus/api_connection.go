package incus

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

func (i *IncusProvider) probeAPIConnection(ctx context.Context) error {
	if i.apiClient == nil {
		return fmt.Errorf("Incus API客户端未初始化")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	probeCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(probeCtx, http.MethodGet, i.apiEndpoint("/1.0"), nil)
	if err != nil {
		return err
	}
	resp, err := i.apiClient.Do(req)
	if err != nil {
		return err
	}
	if resp == nil || resp.Body == nil {
		return fmt.Errorf("Incus API返回空响应")
	}
	body, readErr := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	resp.Body.Close()
	if readErr != nil {
		return readErr
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return fmt.Errorf("Incus API返回HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	return nil
}

func (i *IncusProvider) newAPIOnlyHealthChecker() health.HealthChecker {
	config := health.HealthConfig{
		ProviderID: i.config.ID, ProviderName: i.config.Name,
		Host: i.config.Host, Port: i.config.Port,
		APIEnabled: true, APIPort: 8443, APIScheme: "https",
		SSHEnabled: false, ServiceChecks: nil,
		Timeout: 10 * time.Second, CertPath: i.config.CertPath, KeyPath: i.config.KeyPath,
	}
	logger, _ := zap.NewProduction()
	return health.NewIncusHealthChecker(config, logger)
}
