package provider

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"oneclickvirt/global"
	providerModel "oneclickvirt/model/provider"
	"oneclickvirt/provider"
	"oneclickvirt/utils"

	"go.uber.org/zap"
)

// AgentExecutorFactory 由 service/agent 包在其 init() 中注入，避免循环导入。
// 返回一个基于 AgentHub WebSocket 连接的 ShellExecutor。
var AgentExecutorFactory func(providerID uint) utils.ShellExecutor

// AgentClientCleanupFunc 由 service/agent 包注入，用于Provider删除或重载时清理缓存客户端。
var AgentClientCleanupFunc func(providerID uint)

// AgentConnector 由支持 Agent 模式的 Provider 实现（如 DockerProvider）。
// LoadProvider 在检测到 connection_type=agent 时通过该接口注入执行器。
type AgentConnector interface {
	ConnectAgent(executor utils.ShellExecutor, config provider.NodeConfig) error
}

// ProviderService 管理已配置的Provider实例
type ProviderService struct {
	providers map[uint]provider.Provider // key: provider ID, value: provider instance
	mutex     sync.RWMutex
}

var (
	providerServiceInstance *ProviderService
	providerServiceOnce     sync.Once
)

// GetProviderService 获取Provider服务单例
func GetProviderService() *ProviderService {
	providerServiceOnce.Do(func() {
		providerServiceInstance = &ProviderService{
			providers: make(map[uint]provider.Provider),
		}
	})
	return providerServiceInstance
}

// InitializeProviders 从数据库加载并初始化所有配置的Providers
func (ps *ProviderService) InitializeProviders() error {
	// 检查数据库是否可用
	if global.APP_DB == nil {
		global.APP_LOG.Warn("数据库未初始化，跳过Provider初始化")
		return nil
	}

	// 在初始化Providers之前，先同步配置文件和证书文件
	configService := &ProviderConfigService{}
	if err := configService.SyncConfigsAndCerts(); err != nil {
		global.APP_LOG.Debug("同步配置文件和证书文件失败", zap.String("error", utils.FormatError(err)))
		// 不要因为同步失败而中断初始化过程
	} else {
		global.APP_LOG.Debug("配置文件和证书文件同步完成")
	}

	var dbProviders []providerModel.Provider
	if err := global.APP_DB.Where("status = ?", "active").Find(&dbProviders).Error; err != nil {
		global.APP_LOG.Error("加载Provider配置失败", zap.String("error", utils.FormatError(err)))
		return err
	}

	global.APP_LOG.Debug("开始初始化Providers", zap.Int("count", len(dbProviders)))

	for _, dbProvider := range dbProviders {
		global.APP_LOG.Debug("正在加载Provider", zap.String("name", dbProvider.Name), zap.String("type", dbProvider.Type), zap.String("endpoint", utils.TruncateString(dbProvider.Endpoint, 100)))

		if err := ps.LoadProvider(dbProvider); err != nil {
			global.APP_LOG.Warn("加载Provider失败", zap.String("name", dbProvider.Name), zap.String("type", dbProvider.Type), zap.String("error", utils.FormatError(err)))
			continue
		}
	}

	global.APP_LOG.Info("Providers初始化完成", zap.Int("total", len(dbProviders)), zap.Int("loaded", len(ps.providers)))
	return nil
}

// LoadProvider 加载单个Provider
func (ps *ProviderService) LoadProvider(dbProvider providerModel.Provider) error {
	return ps.LoadProviderWithOptions(dbProvider, false)
}

// LoadProviderWithOptions 加载单个Provider（支持选项）
// allowFrozen: 是否允许加载冻结的Provider（用于删除等特定操作）
func (ps *ProviderService) LoadProviderWithOptions(dbProvider providerModel.Provider, allowFrozen bool) error {
	ps.mutex.Lock()
	defer ps.mutex.Unlock()

	// 检查Provider是否过期或冻结
	if dbProvider.IsFrozen && !allowFrozen {
		global.APP_LOG.Debug("Provider已冻结，跳过加载", zap.String("name", dbProvider.Name), zap.Uint("id", dbProvider.ID))
		return nil
	}

	// 如果允许冻结状态，记录日志
	if dbProvider.IsFrozen && allowFrozen {
		global.APP_LOG.Debug("允许加载冻结的Provider用于特定操作",
			zap.String("name", dbProvider.Name),
			zap.Uint("id", dbProvider.ID),
			zap.String("frozen_reason", dbProvider.FrozenReason))
	}

	if dbProvider.ExpiresAt != nil && dbProvider.ExpiresAt.Before(time.Now()) {
		global.APP_LOG.Debug("Provider已过期，跳过加载", zap.String("name", dbProvider.Name), zap.Uint("id", dbProvider.ID), zap.Time("expiresAt", *dbProvider.ExpiresAt))
		return nil
	}

	// 检查Provider是否已加载
	if _, exists := ps.providers[dbProvider.ID]; exists {
		global.APP_LOG.Debug("Provider已加载，跳过重复加载", zap.String("name", dbProvider.Name), zap.Uint("id", dbProvider.ID))
		return nil
	}

	global.APP_LOG.Debug("开始连接Provider", zap.String("name", dbProvider.Name), zap.String("type", dbProvider.Type), zap.String("host", utils.ExtractHost(dbProvider.Endpoint)), zap.Int("port", dbProvider.SSHPort))

	// 创建Provider实例（仅在未加载时创建）
	prov, err := provider.GetProvider(dbProvider.Type)
	if err != nil {
		global.APP_LOG.Error("获取Provider实例失败", zap.String("name", dbProvider.Name), zap.String("type", dbProvider.Type), zap.String("error", utils.FormatError(err)))
		return err
	}

	// 构建NodeConfig
	sshPort := dbProvider.SSHPort
	if sshPort == 0 {
		sshPort = 22 // 默认SSH端口
	}

	config := provider.NodeConfig{
		ID:                    dbProvider.ID, // 传递Provider ID用于资源清理
		Name:                  dbProvider.Name,
		Type:                  dbProvider.Type,
		Host:                  utils.ExtractHost(dbProvider.Endpoint),
		PortIP:                dbProvider.PortIP, // 端口映射使用的公网IP
		Port:                  sshPort,
		Username:              dbProvider.Username,
		Password:              dbProvider.Password,
		PrivateKey:            dbProvider.SSHKey,
		Token:                 dbProvider.Token,
		UUID:                  dbProvider.UUID,
		Country:               dbProvider.Country,
		City:                  dbProvider.City,
		Architecture:          dbProvider.Architecture,
		ContainerEnabled:      dbProvider.ContainerEnabled,
		VirtualMachineEnabled: dbProvider.VirtualMachineEnabled,
		NetworkType:           dbProvider.NetworkType,
		StoragePool:           dbProvider.StoragePool,
		StoragePoolPath:       dbProvider.StoragePoolPath,
		// Proxmox 网桥配置
		NodeInstallType:   dbProvider.NodeInstallType,
		BridgeNAT:         dbProvider.BridgeNAT,
		BridgeDedicatedV4: dbProvider.BridgeDedicatedV4,
		BridgeDedicatedV6: dbProvider.BridgeDedicatedV6,
		NATSubnet:         dbProvider.NATSubnet,
		ExecutionRule:     dbProvider.ExecutionRule,
		SSHConnectTimeout: dbProvider.SSHConnectTimeout,
		SSHExecuteTimeout: dbProvider.SSHExecuteTimeout,
		HostName:          dbProvider.HostName, // 传递数据库中存储的主机名，避免动态获取导致的节点混淆
		// 资源限制配置
		ContainerLimitCPU:    dbProvider.ContainerLimitCPU,
		ContainerLimitMemory: dbProvider.ContainerLimitMemory,
		ContainerLimitDisk:   dbProvider.ContainerLimitDisk,
		VMLimitCPU:           dbProvider.VMLimitCPU,
		VMLimitMemory:        dbProvider.VMLimitMemory,
		VMLimitDisk:          dbProvider.VMLimitDisk,
		// 容器特殊配置选项（仅 LXD/Incus 容器）
		ContainerPrivileged:   dbProvider.ContainerPrivileged,
		ContainerAllowNesting: dbProvider.ContainerAllowNesting,
		ContainerEnableLXCFS:  dbProvider.ContainerEnableLXCFS,
		ContainerCPUAllowance: dbProvider.ContainerCPUAllowance,
		ContainerMemorySwap:   dbProvider.ContainerMemorySwap,
		ContainerMaxProcesses: dbProvider.ContainerMaxProcesses,
		ContainerDiskIOLimit:  dbProvider.ContainerDiskIOLimit,
		GpuEnabled:            dbProvider.GpuEnabled,
		GpuDeviceIds:          dbProvider.GpuDeviceIds,
	}

	// 如果Provider已自动配置，尝试加载完整配置
	if dbProvider.AutoConfigured && dbProvider.AuthConfig != "" {
		configService := &ProviderConfigService{}
		authConfig, err := configService.LoadProviderConfig(dbProvider.ID)
		if err == nil {
			// 使用配置中的信息
			if authConfig.Certificate != nil {
				config.CertPath = authConfig.Certificate.CertPath
				config.KeyPath = authConfig.Certificate.KeyPath
			}
			if authConfig.Token != nil {
				config.Token = fmt.Sprintf("%s=%s", authConfig.Token.TokenID, authConfig.Token.TokenSecret)
			}
		} else {
			global.APP_LOG.Warn("加载Provider配置失败，使用数据库字段",
				zap.String("provider", dbProvider.Name),
				zap.Error(err))
			// 回退到数据库字段
			config.CertPath = dbProvider.CertPath
			config.KeyPath = dbProvider.KeyPath
		}
	} else {
		// 使用数据库字段
		config.CertPath = dbProvider.CertPath
		config.KeyPath = dbProvider.KeyPath
	}

	// 对于Proxmox，设置TokenID
	if (dbProvider.Type == "proxmox" || dbProvider.Type == "proxmoxve") && dbProvider.Username != "" && strings.Contains(dbProvider.Token, "=") {
		config.TokenID = strings.Split(dbProvider.Token, "=")[0]
	}

	// 如果端口为0，使用默认端口
	if config.Port == 0 {
		config.Port = 22
	}

	if dbProvider.ConnectionType == "agent" {
		// 对于支持 Agent 模式的 Provider（如 Docker/Podman/Containerd），
		// 注入基于 AgentHub WebSocket 的执行器代替 SSH。
		if AgentExecutorFactory != nil {
			if ac, ok := prov.(AgentConnector); ok {
				executor := AgentExecutorFactory(dbProvider.ID)
				if err := ac.ConnectAgent(executor, config); err != nil {
					global.APP_LOG.Warn("Agent模式注入执行器失败，使用未连接状态存储",
						zap.String("name", dbProvider.Name),
						zap.Uint("id", dbProvider.ID),
						zap.Error(err))
				}
			}
		}
		ps.providers[dbProvider.ID] = prov
		// 同步检测架构，确保后续实例创建使用正确的架构（ARM 节点不会误用 amd64 镜像）
		detectAndUpdateArchitecture(dbProvider.ID, prov)
		global.APP_LOG.Info("Agent模式节点加载完成",
			zap.String("name", dbProvider.Name),
			zap.Uint("id", dbProvider.ID),
			zap.String("type", dbProvider.Type))
		return nil
	}

	// 连接Provider - 使用较短的超时时间以避免阻塞
	// 如果Provider配置了自定义超时时间，使用自定义值，否则默认10秒
	connectTimeout := 10 * time.Second
	if dbProvider.SSHConnectTimeout > 0 {
		connectTimeout = time.Duration(dbProvider.SSHConnectTimeout) * time.Second
	}

	ctx, cancel := context.WithTimeout(context.Background(), connectTimeout)
	defer cancel()

	if err := prov.Connect(ctx, config); err != nil {
		global.APP_LOG.Error("连接Provider失败",
			zap.String("name", dbProvider.Name),
			zap.Uint("id", dbProvider.ID),
			zap.String("type", dbProvider.Type),
			zap.Error(err))
		return err
	}

	// 连接成功后同步检测节点架构，确保 ARM 节点不会因为默认 amd64 而无法使用
	detectAndUpdateArchitecture(dbProvider.ID, prov)

	// 存储Provider实例（使用ID作为key）
	// 此时已经持有ps.mutex.Lock()，不需要再次加锁
	ps.providers[dbProvider.ID] = prov

	global.APP_LOG.Info("Provider加载成功",
		zap.String("name", dbProvider.Name),
		zap.Uint("id", dbProvider.ID),
		zap.String("type", dbProvider.Type),
		zap.Bool("autoConfigured", dbProvider.AutoConfigured))

	return nil
}

// GetProviderByID 根据ID获取已加载的Provider（推荐使用）
func (ps *ProviderService) GetProviderByID(id uint) (provider.Provider, bool) {
	ps.mutex.RLock()
	defer ps.mutex.RUnlock()

	prov, exists := ps.providers[id]
	return prov, exists
}

// GetProvider 根据名称获取已加载的Provider（通过遍历查找）
// 由于需要遍历，性能不如 GetProviderByID，推荐优先使用 GetProviderByID
func (ps *ProviderService) GetProvider(name string) (provider.Provider, bool) {
	ps.mutex.RLock()
	defer ps.mutex.RUnlock()

	for _, prov := range ps.providers {
		if prov.GetName() == name {
			return prov, true
		}
	}
	return nil, false
}

// ReloadProvider 重新加载指定的Provider
func (ps *ProviderService) ReloadProvider(providerID uint) error {
	var dbProvider providerModel.Provider
	if err := global.APP_DB.First(&dbProvider, providerID).Error; err != nil {
		return err
	}

	// 断开旧连接
	ps.RemoveProvider(providerID)

	// 重新加载
	return ps.LoadProvider(dbProvider)
}

// RemoveProvider 移除Provider并清理资源
func (ps *ProviderService) RemoveProvider(providerID uint) {
	ps.mutex.Lock()
	defer ps.mutex.Unlock()

	if prov, exists := ps.providers[providerID]; exists {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		if err := prov.Disconnect(ctx); err != nil {
			global.APP_LOG.Warn("断开Provider连接失败",
				zap.Uint("id", providerID),
				zap.String("name", prov.GetName()),
				zap.Error(err))
		}

		delete(ps.providers, providerID)

		global.APP_LOG.Info("Provider已移除并清理资源",
			zap.Uint("id", providerID),
			zap.String("name", prov.GetName()))
	}

	cleanupProviderRuntimeResources(providerID)
}

func cleanupProviderRuntimeResources(providerID uint) {
	// 清理SSH连接池中的连接
	if global.APP_SSH_POOL != nil {
		if pool, ok := global.APP_SSH_POOL.(interface{ RemoveProvider(uint) }); ok {
			pool.RemoveProvider(providerID)
		}
	}

	provider.GetTransportCleanupManager().CleanupProvider(providerID)

	if AgentClientCleanupFunc != nil {
		AgentClientCleanupFunc(providerID)
	}
}

// ListProviders 列出所有已加载的Providers的ID
func (ps *ProviderService) ListProviders() []uint {
	ps.mutex.RLock()
	defer ps.mutex.RUnlock()

	var ids []uint
	for id := range ps.providers {
		ids = append(ids, id)
	}
	return ids
}

// SetInstancePassword 设置实例密码
func (ps *ProviderService) SetInstancePassword(ctx context.Context, providerID uint, instanceName, password string) error {
	// 获取Provider信息
	var dbProvider providerModel.Provider
	if err := global.APP_DB.First(&dbProvider, providerID).Error; err != nil {
		return fmt.Errorf("获取Provider信息失败: %v", err)
	}

	// 获取Provider实例，如果不存在则尝试连接
	ps.mutex.RLock()
	prov, exists := ps.providers[dbProvider.ID]
	ps.mutex.RUnlock()

	if !exists {
		// 如果Provider未连接，尝试动态加载
		global.APP_LOG.Info("Provider未连接，尝试动态加载",
			zap.Uint("id", dbProvider.ID),
			zap.String("name", dbProvider.Name))
		if err := ps.LoadProvider(dbProvider); err != nil {
			global.APP_LOG.Error("动态加载Provider失败",
				zap.Uint("id", dbProvider.ID),
				zap.String("name", dbProvider.Name),
				zap.Error(err))
			return fmt.Errorf("Provider ID %d 连接失败: %v", dbProvider.ID, err)
		}

		// 重新获取Provider实例
		ps.mutex.RLock()
		prov, exists = ps.providers[dbProvider.ID]
		ps.mutex.RUnlock()

		if !exists {
			return fmt.Errorf("Provider ID %d 连接后仍然不可用", dbProvider.ID)
		}
	}

	// 调用Provider的密码设置方法
	return prov.SetInstancePassword(ctx, instanceName, password)
}

// ResetInstancePassword 重置实例密码
func (ps *ProviderService) ResetInstancePassword(ctx context.Context, providerID uint, instanceName string) (string, error) {
	// 获取Provider信息
	var dbProvider providerModel.Provider
	if err := global.APP_DB.First(&dbProvider, providerID).Error; err != nil {
		return "", fmt.Errorf("获取Provider信息失败: %v", err)
	}

	// 获取Provider实例，如果不存在则尝试连接
	ps.mutex.RLock()
	prov, exists := ps.providers[dbProvider.ID]
	ps.mutex.RUnlock()

	if !exists {
		// 如果Provider未连接，尝试动态加载
		global.APP_LOG.Info("Provider未连接，尝试动态加载",
			zap.Uint("id", dbProvider.ID),
			zap.String("name", dbProvider.Name))
		if err := ps.LoadProvider(dbProvider); err != nil {
			global.APP_LOG.Error("动态加载Provider失败",
				zap.Uint("id", dbProvider.ID),
				zap.String("name", dbProvider.Name),
				zap.Error(err))
			return "", fmt.Errorf("Provider ID %d 连接失败: %v", dbProvider.ID, err)
		}

		// 重新获取Provider实例
		ps.mutex.RLock()
		prov, exists = ps.providers[dbProvider.ID]
		ps.mutex.RUnlock()

		if !exists {
			return "", fmt.Errorf("Provider ID %d 连接后仍然不可用", dbProvider.ID)
		}
	}

	// 调用Provider的密码重置方法
	return prov.ResetInstancePassword(ctx, instanceName)
}

// detectAndUpdateArchitecture 在 Provider 连接成功后同步检测节点 CPU 架构，
// 如果检测值与数据库记录不一致则自动更新（同步执行，5s 超时）。
// 解决 ARM 节点因默认 amd64 架构导致镜像筛选错误、无法开设实例的问题。
func detectAndUpdateArchitecture(providerID uint, prov provider.Provider) {
	defer func() {
		if r := recover(); r != nil {
			global.APP_LOG.Warn("架构检测发生panic",
				zap.Uint("providerID", providerID),
				zap.Any("panic", r))
		}
	}()

	// uname -m 是瞬时命令，5s 足够；使用同步调用确保架构在 Provider 可用前已纠正
	detectCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	output, err := prov.ExecuteSSHCommand(detectCtx, "uname -m")
	if err != nil {
		global.APP_LOG.Debug("架构检测失败（非关键错误）",
			zap.Uint("providerID", providerID),
			zap.Error(err))
		return
	}

	arch := strings.TrimSpace(output)
	var detectedArch string
	switch arch {
	case "x86_64", "amd64":
		detectedArch = "amd64"
	case "aarch64", "arm64", "armv8l", "armv8", "armv7l", "armv7", "armv6l", "armv6", "armv5tel", "armv5te", "armv5t":
		detectedArch = "arm64"
	case "s390x":
		detectedArch = "s390x"
	default:
		global.APP_LOG.Debug("未知架构，跳过自动更新",
			zap.Uint("providerID", providerID),
			zap.String("arch", arch))
		return
	}

	// 只在检测值与数据库不一致时才更新
	var dbProvider providerModel.Provider
	if err := global.APP_DB.Select("id, architecture").First(&dbProvider, providerID).Error; err != nil {
		return
	}

	if dbProvider.Architecture == detectedArch {
		return // 无需更新
	}

	if err := global.APP_DB.Model(&providerModel.Provider{}).
		Where("id = ?", providerID).
		Update("architecture", detectedArch).Error; err != nil {
		global.APP_LOG.Warn("更新Provider架构失败",
			zap.Uint("providerID", providerID),
			zap.String("detected", detectedArch),
			zap.Error(err))
		return
	}

	global.APP_LOG.Info("自动检测并更新Provider架构",
		zap.Uint("providerID", providerID),
		zap.String("oldArch", dbProvider.Architecture),
		zap.String("newArch", detectedArch))
}
