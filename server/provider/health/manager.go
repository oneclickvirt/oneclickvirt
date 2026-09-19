package health

import (
	"context"
	"fmt"
	"sync"
	"time"

	"go.uber.org/zap"
)

// ProviderType 提供商类型
type ProviderType string

const (
	ProviderTypeDocker     ProviderType = "docker"
	ProviderTypeOrbstack   ProviderType = "orbstack"
	ProviderTypeLXD        ProviderType = "lxd"
	ProviderTypeIncus      ProviderType = "incus"
	ProviderTypeProxmox    ProviderType = "proxmox"
	ProviderTypeProxmoxVE  ProviderType = "proxmoxve"
	ProviderTypePodman     ProviderType = "podman"
	ProviderTypeContainerd ProviderType = "containerd"
	ProviderTypeQEMU       ProviderType = "qemu"
	ProviderTypeKubeVirt   ProviderType = "kubevirt"
	ProviderTypeVMware     ProviderType = "vmware"
	ProviderTypeVirtualBox ProviderType = "virtualbox"
	ProviderTypeMultipass  ProviderType = "multipass"
	ProviderTypeVagrant    ProviderType = "vagrant"
)

// HealthManager 健康检查管理器
type HealthManager struct {
	checkers map[string]HealthChecker
	logger   *zap.Logger
	mu       sync.RWMutex
}

// NewHealthManager 创建健康检查管理器
func NewHealthManager(logger *zap.Logger) *HealthManager {
	return &HealthManager{
		checkers: make(map[string]HealthChecker),
		logger:   logger,
	}
}

// RegisterChecker 注册健康检查器
func (hm *HealthManager) RegisterChecker(id string, checker HealthChecker) {
	hm.mu.Lock()
	defer hm.mu.Unlock()
	hm.checkers[id] = checker
}

// CreateChecker 创建指定类型的健康检查器
func (hm *HealthManager) CreateChecker(providerType ProviderType, config HealthConfig) (HealthChecker, error) {
	// 复制副本避免共享状态，创建config的深拷贝，避免并发修改
	configCopy := config.DeepCopy()

	// 设置默认值
	if configCopy.Timeout == 0 {
		configCopy.Timeout = 30 * time.Second
	}
	if configCopy.APIScheme == "" {
		configCopy.APIScheme = "https"
	}

	// 记录创建参数，用于问题排查
	if hm.logger != nil {
		hm.logger.Debug("CreateChecker 开始",
			zap.String("providerType", string(providerType)),
			zap.Uint("providerID", configCopy.ProviderID),
			zap.String("providerName", configCopy.ProviderName),
			zap.String("host", configCopy.Host),
			zap.Int("port", configCopy.Port))
	}

	var checker HealthChecker
	var checkerTypeName string

	switch providerType {
	case ProviderTypeDocker, ProviderTypePodman, ProviderTypeContainerd, ProviderTypeOrbstack:
		if configCopy.APIScheme == "" {
			configCopy.APIScheme = "http"
		}
		if configCopy.APIPort == 0 {
			configCopy.APIPort = 2375
		}
		checker = NewDockerHealthChecker(configCopy, hm.logger)
		checkerTypeName = "DockerHealthChecker"

	case ProviderTypeLXD:
		if configCopy.APIPort == 0 {
			configCopy.APIPort = 8443
		}
		checker = NewLXDHealthChecker(configCopy, hm.logger)
		checkerTypeName = "LXDHealthChecker"

	case ProviderTypeIncus:
		if configCopy.APIPort == 0 {
			configCopy.APIPort = 8443
		}
		checker = NewIncusHealthChecker(configCopy, hm.logger)
		checkerTypeName = "IncusHealthChecker"

	case ProviderTypeProxmox, ProviderTypeProxmoxVE:
		if configCopy.APIPort == 0 {
			configCopy.APIPort = 8006
		}
		checker = NewProxmoxHealthChecker(configCopy, hm.logger)
		checkerTypeName = "ProxmoxHealthChecker"

	case ProviderTypeQEMU, ProviderTypeKubeVirt, ProviderTypeVMware, ProviderTypeVirtualBox, ProviderTypeMultipass, ProviderTypeVagrant:
		// VM CLI providers use SSH-only health checks.
		configCopy.APIEnabled = false
		checker = NewDockerHealthChecker(configCopy, hm.logger)
		checkerTypeName = "SSHHealthChecker"

	default:
		if hm.logger != nil {
			hm.logger.Error("不支持的Provider类型",
				zap.String("providerType", string(providerType)),
				zap.Uint("providerID", configCopy.ProviderID))
		}
		return nil, fmt.Errorf("unsupported provider type: %s", providerType)
	}

	// 验证创建的checker类型是否正确
	if hm.logger != nil {
		hm.logger.Info("成功创建HealthChecker",
			zap.String("expectedType", string(providerType)),
			zap.String("actualCheckerType", checkerTypeName),
			zap.String("checkerPtr", fmt.Sprintf("%p", checker)),
			zap.Uint("providerID", configCopy.ProviderID),
			zap.String("providerName", configCopy.ProviderName),
			zap.String("host", configCopy.Host))
	}

	return checker, nil
}

// CheckHealth 执行健康检查
func (hm *HealthManager) CheckHealth(ctx context.Context, id string) (*HealthResult, error) {
	hm.mu.RLock()
	checker, exists := hm.checkers[id]
	hm.mu.RUnlock()
	if !exists {
		return nil, fmt.Errorf("health checker not found for ID: %s", id)
	}
	if checker == nil {
		return nil, fmt.Errorf("health checker is nil for ID: %s", id)
	}

	return checker.CheckHealth(ctx)
}

// CheckAllHealth 检查所有注册的健康检查器
func (hm *HealthManager) CheckAllHealth(ctx context.Context) (map[string]*HealthResult, error) {
	results := make(map[string]*HealthResult)
	hm.mu.RLock()
	checkers := make(map[string]HealthChecker, len(hm.checkers))
	for id, checker := range hm.checkers {
		checkers[id] = checker
	}
	hm.mu.RUnlock()

	for id, checker := range checkers {
		if checker == nil {
			results[id] = &HealthResult{
				Status:    HealthStatusUnhealthy,
				Timestamp: time.Now(),
				Errors:    []string{"health checker is nil"},
			}
			continue
		}
		result, err := checker.CheckHealth(ctx)
		if err != nil {
			if hm.logger != nil {
				hm.logger.Error("Health check failed",
					zap.String("checker_id", id),
					zap.Error(err))
			}
			// 创建错误结果
			result = &HealthResult{
				Status:    HealthStatusUnhealthy,
				Timestamp: time.Now(),
				Errors:    []string{err.Error()},
			}
		} else if hm.logger != nil {
			hm.logger.Debug("Health check succeeded",
				zap.String("checker_id", id),
				zap.String("status", string(result.Status)))
		}
		results[id] = result
	}

	return results, nil
}

// RemoveChecker 移除健康检查器
func (hm *HealthManager) RemoveChecker(id string) {
	hm.mu.Lock()
	if checker, exists := hm.checkers[id]; exists {
		delete(hm.checkers, id)
		hm.mu.Unlock()
		// 如果检查器实现了Close方法，则调用它
		if closer, ok := checker.(interface{ Close() error }); ok {
			if err := closer.Close(); err != nil {
				if hm.logger != nil {
					hm.logger.Warn("Failed to close health checker",
						zap.String("checker_id", id),
						zap.Error(err))
				}
			}
		}
		return
	}
	hm.mu.Unlock()
}

// GetChecker 获取健康检查器
func (hm *HealthManager) GetChecker(id string) (HealthChecker, bool) {
	hm.mu.RLock()
	defer hm.mu.RUnlock()
	checker, exists := hm.checkers[id]
	return checker, exists
}

// ListCheckers 列出所有注册的检查器ID
func (hm *HealthManager) ListCheckers() []string {
	hm.mu.RLock()
	defer hm.mu.RUnlock()
	ids := make([]string, 0, len(hm.checkers))
	for id := range hm.checkers {
		ids = append(ids, id)
	}
	return ids
}

// Close 关闭所有健康检查器
func (hm *HealthManager) Close() error {
	hm.mu.Lock()
	checkers := make(map[string]HealthChecker, len(hm.checkers))
	for id, checker := range hm.checkers {
		checkers[id] = checker
	}
	hm.checkers = make(map[string]HealthChecker)
	hm.mu.Unlock()

	for id, checker := range checkers {
		if closer, ok := checker.(interface{ Close() error }); ok {
			if err := closer.Close(); err != nil {
				if hm.logger != nil {
					hm.logger.Warn("Failed to close health checker",
						zap.String("checker_id", id),
						zap.Error(err))
				}
			}
		}
	}
	return nil
}
