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
	ps.mutex.Lock()
	defer ps.mutex.Unlock()

	// 检查Provider是否过期或冻结
	if dbProvider.IsFrozen {
		global.APP_LOG.Debug("Provider已冻结，跳过加载", zap.String("name", dbProvider.Name), zap.Uint("id", dbProvider.ID))
		return nil
	}

	if dbProvider.ExpiresAt != nil && dbProvider.ExpiresAt.Before(time.Now()) {
		global.APP_LOG.Debug("Provider已过期，跳过加载", zap.String("name", dbProvider.Name), zap.Uint("id", dbProvider.ID), zap.Time("expiresAt", *dbProvider.ExpiresAt))
		return nil
	}

	global.APP_LOG.Debug("开始连接Provider", zap.String("name", dbProvider.Name), zap.String("type", dbProvider.Type), zap.String("host", extractHost(dbProvider.Endpoint)), zap.Int("port", dbProvider.SSHPort))

	// 创建Provider实例
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
		Name:                  dbProvider.Name,
		Type:                  dbProvider.Type,
		Host:                  extractHost(dbProvider.Endpoint),
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
		ExecutionRule:         dbProvider.ExecutionRule,
		SSHConnectTimeout:     dbProvider.SSHConnectTimeout,
		SSHExecuteTimeout:     dbProvider.SSHExecuteTimeout,
		HostName:              dbProvider.HostName, // 传递数据库中存储的主机名，避免动态获取导致的节点混淆
		// 资源限制配置
		ContainerLimitCPU:    dbProvider.ContainerLimitCPU,
		ContainerLimitMemory: dbProvider.ContainerLimitMemory,
		ContainerLimitDisk:   dbProvider.ContainerLimitDisk,
		VMLimitCPU:           dbProvider.VMLimitCPU,
		VMLimitMemory:        dbProvider.VMLimitMemory,
		VMLimitDisk:          dbProvider.VMLimitDisk,
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
	if dbProvider.Type == "proxmox" && dbProvider.Username != "" && strings.Contains(dbProvider.Token, "=") {
		config.TokenID = strings.Split(dbProvider.Token, "=")[0]
	}

	// 如果端口为0，使用默认端口
	if config.Port == 0 {
		config.Port = 22
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

	// 存储Provider实例（使用ID作为key）
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
// 注意：由于需要遍历，性能不如 GetProviderByID，推荐优先使用 GetProviderByID
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

// GetProviderByType 获取指定类型的第一个Provider
// 【已弃用】此方法存在歧义问题，当有多个相同type的provider时会返回随机结果
// 请使用 GetProvider(name) 或通过 ProviderApiService.GetProviderByID(id) 代替
// Deprecated: Use GetProvider(name) or ProviderApiService.GetProviderByID(id) instead
func (ps *ProviderService) GetProviderByType(providerType string) (provider.Provider, bool) {
	ps.mutex.RLock()
	defer ps.mutex.RUnlock()

	global.APP_LOG.Warn("使用了已弃用的GetProviderByType方法，此方法存在歧义",
		zap.String("type", providerType))

	for _, prov := range ps.providers {
		if prov.GetType() == providerType {
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

// RemoveProvider 移除Provider
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
		global.APP_LOG.Info("Provider已移除",
			zap.Uint("id", providerID),
			zap.String("name", prov.GetName()))
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

// extractHost 从endpoint中提取主机地址
func extractHost(endpoint string) string {
	// 处理各种格式的endpoint
	if endpoint == "" {
		return "127.0.0.1" // 默认本地地址
	}

	// 移除协议前缀
	if idx := strings.Index(endpoint, "://"); idx != -1 {
		endpoint = endpoint[idx+3:]
	}

	// 移除端口
	if idx := strings.Index(endpoint, ":"); idx != -1 {
		host := endpoint[:idx]
		if host == "" {
			return "127.0.0.1" // 如果host为空，返回默认值
		}
		return host
	}

	// 检查是否为空或无效
	if endpoint == "" {
		return "127.0.0.1"
	}

	return endpoint
}
