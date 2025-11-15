package health

import (
	"context"
	"crypto/tls"
	"fmt"
	"net/http"
	"sync"
	"time"

	"go.uber.org/zap"
)

// BaseHealthChecker 基础健康检查器
type BaseHealthChecker struct {
	config     HealthConfig
	httpClient *http.Client
	logger     *zap.Logger
	// 追踪字段
	instanceID string       // 实例唯一标识，用于日志追踪
	mu         sync.RWMutex // 保护 config 字段的并发访问
}

// NewBaseHealthChecker 创建基础健康检查器
func NewBaseHealthChecker(config HealthConfig, logger *zap.Logger) *BaseHealthChecker {
	// 生成实例ID用于追踪
	instanceID := fmt.Sprintf("provider_%d_%s", config.ProviderID, config.ProviderName)

	// 创建HTTP客户端，根据配置决定是否跳过TLS验证
	transport := &http.Transport{}

	// 如果使用HTTPS且配置了跳过TLS验证，则设置InsecureSkipVerify
	if config.APIScheme == "https" && config.SkipTLSVerify {
		transport.TLSClientConfig = &tls.Config{
			InsecureSkipVerify: true,
		}
	}

	checker := &BaseHealthChecker{
		config:     config,
		instanceID: instanceID,
		httpClient: &http.Client{
			Timeout:   config.Timeout,
			Transport: transport,
		},
		logger: logger,
	}

	if logger != nil {
		logger.Debug("创建BaseHealthChecker",
			zap.String("instanceID", instanceID),
			zap.Uint("providerID", config.ProviderID),
			zap.String("providerName", config.ProviderName),
			zap.String("host", config.Host),
			zap.Int("port", config.Port))
	}

	return checker
}

// SetConfig 设置配置
func (b *BaseHealthChecker) SetConfig(config HealthConfig) {
	b.mu.Lock()
	defer b.mu.Unlock()

	b.config = config.DeepCopy() // 使用深拷贝避免外部修改
	b.instanceID = fmt.Sprintf("provider_%d_%s", config.ProviderID, config.ProviderName)

	// 重新配置HTTP客户端
	transport := &http.Transport{}

	// 如果使用HTTPS且配置了跳过TLS验证，则设置InsecureSkipVerify
	if config.APIScheme == "https" && config.SkipTLSVerify {
		transport.TLSClientConfig = &tls.Config{
			InsecureSkipVerify: true,
		}
	}

	b.httpClient = &http.Client{
		Timeout:   config.Timeout,
		Transport: transport,
	}
}

// GetConfig 获取配置的只读副本（线程安全）
func (b *BaseHealthChecker) GetConfig() HealthConfig {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.config.DeepCopy()
}

// GetHealthStatus 获取健康状态（默认实现）
func (b *BaseHealthChecker) GetHealthStatus() HealthStatus {
	return HealthStatusUnknown
}

// executeChecks 执行多个检查并合并结果
func (b *BaseHealthChecker) executeChecks(ctx context.Context, checks []func(context.Context) CheckResult) *HealthResult {
	startTime := time.Now()
	result := &HealthResult{
		Timestamp: startTime,
		Details:   make(map[string]interface{}),
		Errors:    []string{},
	}

	var sshOk, apiOk, serviceOk bool

	for _, check := range checks {
		checkResult := check(ctx)

		// 记录检查详情
		result.Details[string(checkResult.Type)] = checkResult

		// 如果有错误，记录到错误列表
		if !checkResult.Success && checkResult.Error != "" {
			result.Errors = append(result.Errors, fmt.Sprintf("%s: %s", checkResult.Type, checkResult.Error))
		}

		// 更新各种状态
		switch checkResult.Type {
		case CheckTypeSSH:
			result.SSHStatus = b.getStatusString(checkResult.Success)
			sshOk = checkResult.Success
		case CheckTypeAPI:
			result.APIStatus = b.getStatusString(checkResult.Success)
			apiOk = checkResult.Success
		case CheckTypeService:
			result.ServiceStatus = b.getStatusString(checkResult.Success)
			serviceOk = checkResult.Success
		}
	}

	// 计算总体状态
	result.Status = b.calculateOverallStatus(sshOk, apiOk, serviceOk)
	result.Duration = time.Since(startTime)

	return result
}

// getStatusString 将布尔值转换为状态字符串
func (b *BaseHealthChecker) getStatusString(success bool) string {
	if success {
		return "online"
	}
	return "offline"
}

// calculateOverallStatus 计算总体健康状态
func (b *BaseHealthChecker) calculateOverallStatus(sshOk, apiOk, serviceOk bool) HealthStatus {
	// 计算成功的检查数量
	successCount := 0
	totalCount := 0

	if b.config.SSHEnabled {
		totalCount++
		if sshOk {
			successCount++
		}
	}

	if b.config.APIEnabled {
		totalCount++
		if apiOk {
			successCount++
		}
	}

	if len(b.config.ServiceChecks) > 0 {
		totalCount++
		if serviceOk {
			successCount++
		}
	}

	// 根据成功率确定状态
	if totalCount == 0 {
		return HealthStatusUnknown
	}

	if successCount == totalCount {
		return HealthStatusHealthy
	} else if successCount > 0 {
		return HealthStatusPartial
	} else {
		return HealthStatusUnhealthy
	}
}

// createCheckFunc 创建检查函数的辅助方法
func (b *BaseHealthChecker) createCheckFunc(checkType CheckType, checkFunc func(context.Context) error) func(context.Context) CheckResult {
	return func(ctx context.Context) CheckResult {
		startTime := time.Now()
		err := checkFunc(ctx)

		result := CheckResult{
			Type:     checkType,
			Success:  err == nil,
			Duration: time.Since(startTime),
		}

		if err != nil {
			result.Error = err.Error()
			if b.logger != nil {
				b.logger.Debug("Health check failed",
					zap.String("type", string(checkType)),
					zap.Error(err))
			}
		}

		return result
	}
}
