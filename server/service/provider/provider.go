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

// LocalConnector 由支持本机模式的 Provider 实现。
// 本机模式用于控制端宿主机直接通过本地 shell 管理 libvirt/qemu/lxc，无需 SSH。
type LocalConnector interface {
	ConnectLocal(config provider.NodeConfig) error
}

// ProviderService 管理已配置的Provider实例
type ProviderService struct {
	providers     map[uint]provider.Provider // key: provider ID, value: provider instance
	mutex         sync.RWMutex
	operationLock sync.Map // provider ID -> *sync.Mutex; remote I/O must never hold the global map lock
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

	global.APP_LOG.Info("Providers初始化完成", zap.Int("total", len(dbProviders)), zap.Int("loaded", len(ps.ListProviders())))
	return nil
}

// LoadProvider 加载单个Provider
func (ps *ProviderService) LoadProvider(dbProvider providerModel.Provider) error {
	return ps.LoadProviderWithOptionsContext(context.Background(), dbProvider, false)
}

// LoadProviderWithOptions 加载单个Provider（支持选项）
// allowFrozen: 是否允许加载冻结的Provider（用于删除等特定操作）
func (ps *ProviderService) LoadProviderWithOptions(dbProvider providerModel.Provider, allowFrozen bool) error {
	return ps.LoadProviderWithOptionsContext(context.Background(), dbProvider, allowFrozen)
}

// LoadProviderWithOptionsContext loads a Provider while respecting the caller's
// cancellation and deadline. The provider-scoped lock still serializes reloads
// for one node, while unrelated Providers remain independent.
func (ps *ProviderService) LoadProviderWithOptionsContext(ctx context.Context, dbProvider providerModel.Provider, allowFrozen bool) error {
	if ctx == nil {
		ctx = context.Background()
	}
	operationLock := ps.providerOperationMutex(dbProvider.ID)
	operationLock.Lock()
	defer operationLock.Unlock()
	return ps.loadProviderWithOptionsLocked(ctx, dbProvider, allowFrozen)
}

// loadProviderWithOptionsLocked performs a load while the provider-scoped
// operation lock is held. Network connection and architecture detection happen
// outside the global providers-map lock so one unreachable node cannot block
// every other Provider or API lookup.
func (ps *ProviderService) loadProviderWithOptionsLocked(ctx context.Context, dbProvider providerModel.Provider, allowFrozen bool) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return err
	}

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
	ps.mutex.RLock()
	_, exists := ps.providers[dbProvider.ID]
	ps.mutex.RUnlock()
	if exists {
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
		CACertPath:            dbProvider.CACertPath,
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
		ContainerLimitCPU:     dbProvider.ContainerLimitCPU,
		ContainerLimitMemory:  dbProvider.ContainerLimitMemory,
		ContainerLimitDisk:    dbProvider.ContainerLimitDisk,
		VMLimitCPU:            dbProvider.VMLimitCPU,
		VMLimitMemory:         dbProvider.VMLimitMemory,
		VMLimitDisk:           dbProvider.VMLimitDisk,
		ContainerReadIOLimit:  dbProvider.ContainerReadIOLimit,
		ContainerWriteIOLimit: dbProvider.ContainerWriteIOLimit,
		VMReadIOLimit:         dbProvider.VMReadIOLimit,
		VMWriteIOLimit:        dbProvider.VMWriteIOLimit,
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

	// API credentials must be reconstructed from the durable row for every
	// loading path.  A reboot recovery can run before a legacy config backup is
	// available, while token/certificate fields on the Provider row remain the
	// authoritative fallbacks.
	authConfig := ProviderAuthConfigFromRecord(dbProvider)
	certificateConfig := authConfig.Certificate
	if authConfig.Token != nil && strings.TrimSpace(authConfig.Token.TokenID) != "" {
		config.Token = fmt.Sprintf("%s=%s", authConfig.Token.TokenID, authConfig.Token.TokenSecret)
	}
	if certificateConfig != nil {
		if certificateConfig.CertContent == "" {
			certificateConfig.CertContent = dbProvider.CertContent
		}
		if certificateConfig.KeyContent == "" {
			certificateConfig.KeyContent = dbProvider.KeyContent
		}
		configService := &ProviderConfigService{}
		if materializeErr := configService.ensureCertificateFiles(dbProvider.UUID, certificateConfig); materializeErr != nil {
			// Leave both paths empty so the provider cleanly uses SSH and does not
			// emit a second misleading "certificate file not found" warning.
			global.APP_LOG.Warn("恢复Provider证书文件失败，将仅使用SSH",
				zap.String("provider", dbProvider.Name),
				zap.Error(materializeErr))
		} else {
			config.CertPath = certificateConfig.CertPath
			config.KeyPath = certificateConfig.KeyPath
		}
	}

	// 对于Proxmox，TokenID 与 TokenSecret 需要分别保留。优先采用结构化
	// 凭据，兼容旧行上的 "id=secret" 格式。
	if dbProvider.Type == "proxmox" || dbProvider.Type == "proxmoxve" || dbProvider.Type == "pve" {
		if authConfig.Token != nil {
			config.TokenID = authConfig.Token.TokenID
		}
		if config.TokenID == "" && strings.Contains(config.Token, "=") {
			config.TokenID = strings.SplitN(config.Token, "=", 2)[0]
		}
	}

	// 如果端口为0，使用默认端口
	if config.Port == 0 {
		config.Port = 22
	}

	isAPIOnly := strings.EqualFold(strings.TrimSpace(dbProvider.ExecutionRule), "api_only")
	if dbProvider.ConnectionType == "agent" && !isAPIOnly {
		// 对于支持 Agent 模式的 Provider（如 Docker/Podman/Containerd），
		// 注入基于 AgentHub WebSocket 的执行器代替 SSH。
		if AgentExecutorFactory == nil {
			return fmt.Errorf("Agent executor factory is unavailable for provider %d", dbProvider.ID)
		}
		ac, ok := prov.(AgentConnector)
		if !ok {
			return fmt.Errorf("provider %s does not support Agent connection mode", dbProvider.Type)
		}
		executor := AgentExecutorFactory(dbProvider.ID)
		if executor == nil {
			return fmt.Errorf("Agent executor is unavailable for provider %d", dbProvider.ID)
		}
		if err := ac.ConnectAgent(executor, config); err != nil {
			return fmt.Errorf("connect provider through Agent: %w", err)
		}
		ps.mutex.Lock()
		ps.providers[dbProvider.ID] = prov
		ps.mutex.Unlock()
		// Agent 可能尚未建立 WebSocket 连接。不能在加载路径中调用 IsConnected
		// 或同步执行 uname：两者都会等待 Agent 重连并阻塞 Provider 更新接口。
		// 这里保留数据库中的架构，待 Agent 上线后由正常的同步/部署流程刷新。
		global.APP_LOG.Info("Agent模式节点加载完成",
			zap.String("name", dbProvider.Name),
			zap.Uint("id", dbProvider.ID),
			zap.String("type", dbProvider.Type))
		return nil
	}

	if dbProvider.ConnectionType == "local" {
		lc, ok := prov.(LocalConnector)
		if !ok {
			return fmt.Errorf("provider %s does not support local connection mode", dbProvider.Type)
		}
		if err := lc.ConnectLocal(config); err != nil {
			global.APP_LOG.Error("本机模式Provider连接失败",
				zap.String("name", dbProvider.Name),
				zap.Uint("id", dbProvider.ID),
				zap.String("type", dbProvider.Type),
				zap.Error(err))
			return err
		}
		detectAndUpdateArchitecture(dbProvider.ID, prov)
		ps.mutex.Lock()
		ps.providers[dbProvider.ID] = prov
		ps.mutex.Unlock()
		global.APP_LOG.Info("本机模式Provider加载成功",
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

	connectCtx, cancel := context.WithTimeout(ctx, connectTimeout)
	defer cancel()

	if err := prov.Connect(connectCtx, config); err != nil {
		global.APP_LOG.Error("连接Provider失败",
			zap.String("name", dbProvider.Name),
			zap.Uint("id", dbProvider.ID),
			zap.String("type", dbProvider.Type),
			zap.Error(err))
		return err
	}

	// api_only Provider must never turn a successful API connection into an SSH
	// probe just to run uname. Its persisted architecture is refreshed through
	// its API discovery path instead.
	if !isAPIOnly {
		detectAndUpdateArchitecture(dbProvider.ID, prov)
	}

	// 存储Provider实例（使用ID作为key）。远端连接阶段没有持有全局锁。
	ps.mutex.Lock()
	ps.providers[dbProvider.ID] = prov
	ps.mutex.Unlock()

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
	return ps.ReloadProviderContext(context.Background(), providerID)
}

// ReloadProviderContext refreshes a Provider while respecting the task
// deadline. Individual Provider Connect implementations use SSHConnectTimeout,
// so clamp that value to the remaining context budget before dialing.
func (ps *ProviderService) ReloadProviderContext(ctx context.Context, providerID uint) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	var dbProvider providerModel.Provider
	if err := global.APP_DB.WithContext(ctx).First(&dbProvider, providerID).Error; err != nil {
		return err
	}
	if deadline, ok := ctx.Deadline(); ok {
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return context.DeadlineExceeded
		}
		remainingSeconds := int((remaining + time.Second - 1) / time.Second)
		if remainingSeconds < 1 {
			remainingSeconds = 1
		}
		if dbProvider.SSHConnectTimeout <= 0 || dbProvider.SSHConnectTimeout > remainingSeconds {
			dbProvider.SSHConnectTimeout = remainingSeconds
		}
	}

	operationLock := ps.providerOperationMutex(providerID)
	operationLock.Lock()
	defer operationLock.Unlock()

	// 断开旧连接
	ps.removeProviderLockedContext(ctx, providerID)
	if err := ctx.Err(); err != nil {
		return err
	}

	// 重新加载
	return ps.loadProviderWithOptionsLocked(ctx, dbProvider, false)
}

// RemoveProvider 移除Provider并清理资源
func (ps *ProviderService) RemoveProvider(providerID uint) {
	ps.RemoveProviderContext(context.Background(), providerID)
}

// RemoveProviderContext removes a cached Provider without letting a stale
// disconnect outlive the caller's shutdown or task deadline.
func (ps *ProviderService) RemoveProviderContext(ctx context.Context, providerID uint) {
	if ctx == nil {
		ctx = context.Background()
	}
	operationLock := ps.providerOperationMutex(providerID)
	operationLock.Lock()
	defer operationLock.Unlock()
	ps.removeProviderLockedContext(ctx, providerID)
}

func (ps *ProviderService) providerOperationMutex(providerID uint) *sync.Mutex {
	value, _ := ps.operationLock.LoadOrStore(providerID, &sync.Mutex{})
	return value.(*sync.Mutex)
}

// removeProviderLocked removes the map entry before performing remote cleanup.
// The caller holds only the provider-scoped operation lock, so a slow
// Disconnect cannot stall unrelated Provider lookups or reloads.
func (ps *ProviderService) removeProviderLocked(providerID uint) {
	ps.removeProviderLockedContext(context.Background(), providerID)
}

func (ps *ProviderService) removeProviderLockedContext(ctx context.Context, providerID uint) {
	if ctx == nil {
		ctx = context.Background()
	}
	ps.mutex.Lock()
	prov, exists := ps.providers[providerID]
	if exists {
		delete(ps.providers, providerID)
	}
	ps.mutex.Unlock()

	if exists {
		disconnectCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		if err := prov.Disconnect(disconnectCtx); err != nil {
			global.APP_LOG.Warn("断开Provider连接失败",
				zap.Uint("id", providerID),
				zap.String("name", prov.GetName()),
				zap.Error(err))
		}
		cancel()

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
