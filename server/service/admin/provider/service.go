package provider

import (
	"context"
	"errors"
	"fmt"
	"oneclickvirt/global"
	"oneclickvirt/model/admin"
	providerModel "oneclickvirt/model/provider"
	"oneclickvirt/provider/health"
	"oneclickvirt/service/database"
	"oneclickvirt/service/images"
	provider2 "oneclickvirt/service/provider"
	"oneclickvirt/utils"
	"strings"
	"time"

	"go.uber.org/zap"
	"gorm.io/gorm"
)

// Service 管理员Provider管理服务
type Service struct{}

// NewService 创建提供商管理服务
func NewService() *Service {
	return &Service{}
}

// GetProviderList 获取Provider列表
func (s *Service) GetProviderList(req admin.ProviderListRequest) ([]admin.ProviderManageResponse, int64, error) {
	global.APP_LOG.Debug("获取Provider列表",
		zap.String("name", utils.TruncateString(req.Name, 32)),
		zap.String("type", req.Type),
		zap.String("status", req.Status),
		zap.Int("page", req.Page),
		zap.Int("pageSize", req.PageSize))

	var providers []providerModel.Provider
	var total int64

	query := global.APP_DB.Model(&providerModel.Provider{})

	if req.Name != "" {
		query = query.Where("name LIKE ?", "%"+req.Name+"%")
	}
	if req.Type != "" {
		query = query.Where("type = ?", req.Type)
	}
	if req.Status != "" {
		query = query.Where("status = ?", req.Status)
	}

	if err := query.Count(&total).Error; err != nil {
		global.APP_LOG.Error("查询Provider总数失败", zap.Error(err))
		return nil, 0, err
	}

	offset := (req.Page - 1) * req.PageSize
	if err := query.Offset(offset).Limit(req.PageSize).Find(&providers).Error; err != nil {
		global.APP_LOG.Error("查询Provider列表失败", zap.Error(err))
		return nil, 0, err
	}

	var providerResponses []admin.ProviderManageResponse
	for _, provider := range providers {
		var instanceCount int64
		global.APP_DB.Model(&providerModel.Instance{}).Where("provider_id = ?", provider.ID).Count(&instanceCount)

		// 统计正在运行的任务数量
		var runningTasksCount int64
		global.APP_DB.Model(&admin.Task{}).Where("provider_id = ? AND status = ?", provider.ID, "running").Count(&runningTasksCount)

		providerResponse := admin.ProviderManageResponse{
			Provider:          provider,
			InstanceCount:     int(instanceCount),
			HealthStatus:      "healthy",
			RunningTasksCount: int(runningTasksCount),
			// 包含资源信息
			NodeCPUCores:     provider.NodeCPUCores,
			NodeMemoryTotal:  provider.NodeMemoryTotal,
			NodeDiskTotal:    provider.NodeDiskTotal,
			ResourceSynced:   provider.ResourceSynced,
			ResourceSyncedAt: provider.ResourceSyncedAt,
		}
		providerResponses = append(providerResponses, providerResponse)
	}

	global.APP_LOG.Debug("Provider列表查询成功",
		zap.Int64("total", total),
		zap.Int("count", len(providerResponses)))
	return providerResponses, total, nil
}

// CreateProvider 创建Provider
func (s *Service) CreateProvider(req admin.CreateProviderRequest) error {
	global.APP_LOG.Debug("开始创建Provider",
		zap.String("name", utils.TruncateString(req.Name, 32)),
		zap.String("type", req.Type),
		zap.String("endpoint", utils.TruncateString(req.Endpoint, 64)))

	// 解析过期时间
	var expiresAt *time.Time
	if req.ExpiresAt != "" {
		// 尝试解析多种时间格式
		var t time.Time
		var err error

		// 首先尝试ISO 8601格式（前端默认格式）
		t, err = time.Parse(time.RFC3339, req.ExpiresAt)
		if err != nil {
			// 尝试标准日期时间格式
			t, err = time.Parse("2006-01-02 15:04:05", req.ExpiresAt)
			if err != nil {
				// 尝试日期格式
				t, err = time.Parse("2006-01-02", req.ExpiresAt)
				if err != nil {
					global.APP_LOG.Warn("Provider创建失败：过期时间格式错误",
						zap.String("name", utils.TruncateString(req.Name, 32)),
						zap.String("expiresAt", utils.TruncateString(req.ExpiresAt, 32)))
					return fmt.Errorf("过期时间格式错误，请使用 'YYYY-MM-DD HH:MM:SS' 或 'YYYY-MM-DD' 格式")
				}
			}
		}
		expiresAt = &t
	} else {
		// 默认31天后过期
		defaultExpiry := time.Now().AddDate(0, 0, 31)
		expiresAt = &defaultExpiry
	}

	provider := providerModel.Provider{
		Name:                  req.Name,
		Type:                  req.Type,
		Endpoint:              req.Endpoint,
		SSHPort:               req.SSHPort,
		Username:              req.Username,
		Password:              req.Password,
		Token:                 req.Token,
		Config:                req.Config,
		Region:                req.Region,
		Country:               req.Country,
		CountryCode:           req.CountryCode,
		Architecture:          req.Architecture,
		ContainerEnabled:      req.ContainerEnabled,
		VirtualMachineEnabled: req.VirtualMachineEnabled,
		TotalQuota:            req.TotalQuota,
		AllowClaim:            req.AllowClaim,
		Status:                "active",
		ExpiresAt:             expiresAt,
		IsFrozen:              false,
		MaxContainerInstances: req.MaxContainerInstances,
		MaxVMInstances:        req.MaxVMInstances,
		AllowConcurrentTasks:  req.AllowConcurrentTasks,
		MaxConcurrentTasks:    req.MaxConcurrentTasks,
		TaskPollInterval:      req.TaskPollInterval,
		EnableTaskPolling:     req.EnableTaskPolling,
		// 存储配置（ProxmoxVE专用）
		StoragePool: req.StoragePool,
		// 操作执行配置
		ExecutionRule: req.ExecutionRule,
		// 端口映射配置
		DefaultPortCount: req.DefaultPortCount,
		PortRangeStart:   req.PortRangeStart,
		PortRangeEnd:     req.PortRangeEnd,
		NetworkType:      req.NetworkType,
		// 带宽配置
		DefaultInboundBandwidth:  req.DefaultInboundBandwidth,
		DefaultOutboundBandwidth: req.DefaultOutboundBandwidth,
		MaxInboundBandwidth:      req.MaxInboundBandwidth,
		MaxOutboundBandwidth:     req.MaxOutboundBandwidth,
		// 流量管理
		MaxTraffic: req.MaxTraffic,
		// SSH连接配置
		SSHConnectTimeout: req.SSHConnectTimeout,
		SSHExecuteTimeout: req.SSHExecuteTimeout,
	}

	// 设置默认值
	// 并发控制默认值：默认不允许并发，最大并发数为1
	if !provider.AllowConcurrentTasks && provider.MaxConcurrentTasks <= 0 {
		provider.MaxConcurrentTasks = 1
	}
	if provider.MaxConcurrentTasks <= 0 {
		provider.MaxConcurrentTasks = 1
	}
	if provider.TaskPollInterval <= 0 {
		provider.TaskPollInterval = 60
	}
	// 操作执行配置默认值
	if provider.ExecutionRule == "" {
		provider.ExecutionRule = "auto"
	}
	// 端口映射默认值
	if provider.DefaultPortCount <= 0 {
		provider.DefaultPortCount = 10
	}
	if provider.PortRangeStart <= 0 {
		provider.PortRangeStart = 10000
	}
	if provider.PortRangeEnd <= 0 {
		provider.PortRangeEnd = 65535
	}
	if provider.NetworkType == "" {
		provider.NetworkType = "nat_ipv4"
	}
	// 带宽配置默认值
	if provider.DefaultInboundBandwidth <= 0 {
		provider.DefaultInboundBandwidth = 300
	}
	if provider.DefaultOutboundBandwidth <= 0 {
		provider.DefaultOutboundBandwidth = 300
	}
	if provider.MaxInboundBandwidth <= 0 {
		provider.MaxInboundBandwidth = 1000
	}
	if provider.MaxOutboundBandwidth <= 0 {
		provider.MaxOutboundBandwidth = 1000
	}
	// 流量限制默认值：1TB
	if provider.MaxTraffic <= 0 {
		provider.MaxTraffic = 1048576 // 1TB = 1048576MB
	}
	// SSH超时默认值
	if provider.SSHConnectTimeout <= 0 {
		provider.SSHConnectTimeout = 30 // 默认30秒连接超时
	}
	if provider.SSHExecuteTimeout <= 0 {
		provider.SSHExecuteTimeout = 300 // 默认300秒执行超时
	}
	provider.NextAvailablePort = provider.PortRangeStart

	// 初始化流量重置时间为下个月的1号
	now := time.Now()
	nextReset := time.Date(now.Year(), now.Month()+1, 1, 0, 0, 0, 0, now.Location())
	provider.TrafficResetAt = &nextReset

	dbService := database.GetDatabaseService()
	if err := dbService.ExecuteTransaction(context.Background(), func(tx *gorm.DB) error {
		return tx.Create(&provider).Error
	}); err != nil {
		global.APP_LOG.Error("Provider创建失败",
			zap.String("name", utils.TruncateString(req.Name, 32)),
			zap.Error(err))
		return err
	}

	global.APP_LOG.Info("Provider创建成功",
		zap.String("name", utils.TruncateString(req.Name, 32)),
		zap.String("type", req.Type),
		zap.String("endpoint", utils.TruncateString(req.Endpoint, 64)))
	return nil
}

// UpdateProvider 更新Provider
func (s *Service) UpdateProvider(req admin.UpdateProviderRequest) error {
	global.APP_LOG.Debug("开始更新Provider", zap.Uint("providerID", req.ID))

	var provider providerModel.Provider
	if err := global.APP_DB.First(&provider, req.ID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			global.APP_LOG.Warn("Provider更新失败：Provider不存在", zap.Uint("providerID", req.ID))
		} else {
			global.APP_LOG.Error("查询Provider失败", zap.Uint("providerID", req.ID), zap.Error(err))
		}
		return err
	}

	// 解析过期时间
	if req.ExpiresAt != "" {
		// 尝试解析多种时间格式
		var t time.Time
		var err error

		// 首先尝试ISO 8601格式（前端默认格式）
		t, err = time.Parse(time.RFC3339, req.ExpiresAt)
		if err != nil {
			// 尝试标准日期时间格式
			t, err = time.Parse("2006-01-02 15:04:05", req.ExpiresAt)
			if err != nil {
				// 尝试日期格式
				t, err = time.Parse("2006-01-02", req.ExpiresAt)
				if err != nil {
					return fmt.Errorf("过期时间格式错误，请使用 'YYYY-MM-DD HH:MM:SS' 或 'YYYY-MM-DD' 格式")
				}
			}
		}
		provider.ExpiresAt = &t
	} else {
		// 如果没有指定过期时间，设置为31天后
		defaultExpiry := time.Now().AddDate(0, 0, 31)
		provider.ExpiresAt = &defaultExpiry
	}

	provider.Name = req.Name
	provider.Type = req.Type
	provider.Endpoint = req.Endpoint
	provider.SSHPort = req.SSHPort
	provider.Username = req.Username
	// 只有当传递了非空密码时才更新密码
	if req.Password != "" {
		provider.Password = req.Password
	}
	provider.Token = req.Token
	provider.Config = req.Config
	provider.Region = req.Region
	provider.Country = req.Country
	provider.CountryCode = req.CountryCode
	provider.Architecture = req.Architecture
	provider.ContainerEnabled = req.ContainerEnabled
	provider.VirtualMachineEnabled = req.VirtualMachineEnabled
	provider.TotalQuota = req.TotalQuota
	provider.AllowClaim = req.AllowClaim
	provider.Status = req.Status
	provider.MaxContainerInstances = req.MaxContainerInstances
	provider.MaxVMInstances = req.MaxVMInstances
	provider.AllowConcurrentTasks = req.AllowConcurrentTasks
	provider.MaxConcurrentTasks = req.MaxConcurrentTasks
	provider.TaskPollInterval = req.TaskPollInterval
	provider.EnableTaskPolling = req.EnableTaskPolling
	// 存储配置（ProxmoxVE专用）
	provider.StoragePool = req.StoragePool
	// 操作执行配置更新
	if req.ExecutionRule != "" {
		provider.ExecutionRule = req.ExecutionRule
	}
	// 端口映射配置更新
	if req.DefaultPortCount > 0 {
		provider.DefaultPortCount = req.DefaultPortCount
	}
	if req.PortRangeStart > 0 {
		provider.PortRangeStart = req.PortRangeStart
	}
	if req.PortRangeEnd > 0 {
		provider.PortRangeEnd = req.PortRangeEnd
	}
	if req.NetworkType != "" {
		provider.NetworkType = req.NetworkType
	}
	// 带宽配置更新
	if req.DefaultInboundBandwidth > 0 {
		provider.DefaultInboundBandwidth = req.DefaultInboundBandwidth
	}
	if req.DefaultOutboundBandwidth > 0 {
		provider.DefaultOutboundBandwidth = req.DefaultOutboundBandwidth
	}
	if req.MaxInboundBandwidth > 0 {
		provider.MaxInboundBandwidth = req.MaxInboundBandwidth
	}
	if req.MaxOutboundBandwidth > 0 {
		provider.MaxOutboundBandwidth = req.MaxOutboundBandwidth
	}
	// 流量限制更新
	if req.MaxTraffic > 0 {
		provider.MaxTraffic = req.MaxTraffic
	}
	// SSH超时配置更新
	if req.SSHConnectTimeout > 0 {
		provider.SSHConnectTimeout = req.SSHConnectTimeout
	}
	if req.SSHExecuteTimeout > 0 {
		provider.SSHExecuteTimeout = req.SSHExecuteTimeout
	}

	// 设置默认值
	// 并发控制默认值：确保一致性
	if !provider.AllowConcurrentTasks && provider.MaxConcurrentTasks <= 0 {
		provider.MaxConcurrentTasks = 1
	}
	if provider.MaxConcurrentTasks <= 0 {
		provider.MaxConcurrentTasks = 1
	}
	if provider.TaskPollInterval <= 0 {
		provider.TaskPollInterval = 60
	}

	dbService := database.GetDatabaseService()
	return dbService.ExecuteTransaction(context.Background(), func(tx *gorm.DB) error {
		// 保存Provider更新
		if err := tx.Save(&provider).Error; err != nil {
			return err
		}

		// 同步更新该Provider下所有实例的到期时间
		if provider.ExpiresAt != nil {
			if err := tx.Model(&providerModel.Instance{}).
				Where("provider_id = ? AND status NOT IN (?)", provider.ID, []string{"deleting", "deleted"}).
				Update("expired_at", *provider.ExpiresAt).Error; err != nil {
				global.APP_LOG.Error("同步实例到期时间失败",
					zap.Uint("providerID", provider.ID),
					zap.Time("newExpiresAt", *provider.ExpiresAt),
					zap.Error(err))
				return fmt.Errorf("同步实例到期时间失败: %v", err)
			}
			global.APP_LOG.Info("已同步实例到期时间",
				zap.Uint("providerID", provider.ID),
				zap.Time("newExpiresAt", *provider.ExpiresAt))
		}

		return nil
	})
}

// DeleteProvider 删除Provider
func (s *Service) DeleteProvider(providerID uint) error {
	global.APP_LOG.Debug("开始删除Provider", zap.Uint("providerID", providerID))

	// 检查是否还有相关的实例
	var instanceCount int64
	global.APP_DB.Model(&providerModel.Instance{}).Where("provider_id = ?", providerID).Count(&instanceCount)
	if instanceCount > 0 {
		global.APP_LOG.Warn("Provider删除失败：Provider还有实例",
			zap.Uint("providerID", providerID),
			zap.Int64("instanceCount", instanceCount))
		return errors.New("提供商还有实例，无法删除")
	}

	// 检查是否还有相关的端口映射
	var portCount int64
	global.APP_DB.Model(&providerModel.Port{}).Where("provider_id = ?", providerID).Count(&portCount)
	if portCount > 0 {
		global.APP_LOG.Warn("Provider删除失败：Provider还有端口映射",
			zap.Uint("providerID", providerID),
			zap.Int64("portCount", portCount))
		return errors.New("提供商还有端口映射，无法删除")
	}

	dbService := database.GetDatabaseService()
	if err := dbService.ExecuteTransaction(context.Background(), func(tx *gorm.DB) error {
		return tx.Delete(&providerModel.Provider{}, providerID).Error
	}); err != nil {
		global.APP_LOG.Error("Provider删除失败", zap.Uint("providerID", providerID), zap.Error(err))
		return err
	}

	global.APP_LOG.Info("Provider删除成功", zap.Uint("providerID", providerID))
	return nil
}

// FreezeProvider 冻结Provider
func (s *Service) FreezeProvider(req admin.FreezeProviderRequest) error {
	var provider providerModel.Provider
	if err := global.APP_DB.First(&provider, req.ID).Error; err != nil {
		return fmt.Errorf("Provider不存在")
	}

	provider.IsFrozen = true
	dbService := database.GetDatabaseService()
	return dbService.ExecuteTransaction(context.Background(), func(tx *gorm.DB) error {
		return tx.Save(&provider).Error
	})
}

// UnfreezeProvider 解冻Provider
func (s *Service) UnfreezeProvider(req admin.UnfreezeProviderRequest) error {
	var provider providerModel.Provider
	if err := global.APP_DB.First(&provider, req.ID).Error; err != nil {
		return fmt.Errorf("Provider不存在")
	}

	// 解析新的过期时间
	if req.ExpiresAt != "" {
		// 尝试解析多种时间格式
		var t time.Time
		var err error

		// 首先尝试ISO 8601格式（前端默认格式）
		t, err = time.Parse(time.RFC3339, req.ExpiresAt)
		if err != nil {
			// 尝试标准日期时间格式
			t, err = time.Parse("2006-01-02 15:04:05", req.ExpiresAt)
			if err != nil {
				// 尝试日期格式
				t, err = time.Parse("2006-01-02", req.ExpiresAt)
				if err != nil {
					return fmt.Errorf("过期时间格式错误，请使用 'YYYY-MM-DD HH:MM:SS' 或 'YYYY-MM-DD' 格式")
				}
			}
		}
		// 检查新的过期时间必须是未来时间
		if t.Before(time.Now()) {
			return fmt.Errorf("过期时间必须是未来时间")
		}
		provider.ExpiresAt = &t
	} else {
		// 如果没有指定新的过期时间，设置为31天后
		defaultExpiry := time.Now().AddDate(0, 0, 31)
		provider.ExpiresAt = &defaultExpiry
	}

	provider.IsFrozen = false
	dbService := database.GetDatabaseService()
	return dbService.ExecuteTransaction(context.Background(), func(tx *gorm.DB) error {
		// 保存Provider更新
		if err := tx.Save(&provider).Error; err != nil {
			return err
		}

		// 同步更新该Provider下所有实例的到期时间
		if provider.ExpiresAt != nil {
			if err := tx.Model(&providerModel.Instance{}).
				Where("provider_id = ? AND status NOT IN (?)", provider.ID, []string{"deleting", "deleted"}).
				Update("expired_at", *provider.ExpiresAt).Error; err != nil {
				global.APP_LOG.Error("同步实例到期时间失败",
					zap.Uint("providerID", provider.ID),
					zap.Time("newExpiresAt", *provider.ExpiresAt),
					zap.Error(err))
				return fmt.Errorf("同步实例到期时间失败: %v", err)
			}
			global.APP_LOG.Info("已同步实例到期时间",
				zap.Uint("providerID", provider.ID),
				zap.Time("newExpiresAt", *provider.ExpiresAt))
		}

		return nil
	})
}

// GenerateProviderCert 为Provider生成证书配置
func (s *Service) GenerateProviderCert(providerID uint) (string, error) {
	var provider providerModel.Provider
	if err := global.APP_DB.First(&provider, providerID).Error; err != nil {
		return "", fmt.Errorf("Provider不存在")
	}

	// 支持LXD、Incus和Proxmox
	if provider.Type != "lxd" && provider.Type != "incus" && provider.Type != "proxmox" {
		return "", fmt.Errorf("只支持为LXD、Incus和Proxmox生成配置")
	}

	certService := &provider2.CertService{}

	// 执行自动配置（现在包含完整的数据库和文件保存）
	err := certService.AutoConfigureProvider(&provider)
	if err != nil {
		return "", fmt.Errorf("自动配置失败: %w", err)
	}

	// 根据类型返回不同的成功消息
	var message string
	switch provider.Type {
	case "proxmox":
		message = "Proxmox VE API 自动配置成功，认证配置已保存到数据库和文件"
	case "lxd":
		message = "LXD 自动配置成功，证书已安装并保存到数据库和文件"
	case "incus":
		message = "Incus 自动配置成功，证书已安装并保存到数据库和文件"
	}

	return message, nil
}

// AutoConfigureProviderWithStream 带实时输出的自动配置Provider
func (s *Service) AutoConfigureProviderWithStream(providerID uint, outputChan chan<- string) error {
	var provider providerModel.Provider
	if err := global.APP_DB.First(&provider, providerID).Error; err != nil {
		outputChan <- fmt.Sprintf("错误: Provider不存在 (ID: %d)", providerID)
		return fmt.Errorf("Provider不存在")
	}

	// 支持LXD、Incus和Proxmox
	if provider.Type != "lxd" && provider.Type != "incus" && provider.Type != "proxmox" {
		outputChan <- fmt.Sprintf("错误: 不支持的Provider类型: %s (只支持LXD、Incus和Proxmox)", provider.Type)
		return fmt.Errorf("只支持为LXD、Incus和Proxmox生成配置")
	}

	outputChan <- fmt.Sprintf("=== 开始自动配置 %s Provider: %s ===", strings.ToUpper(provider.Type), provider.Name)
	outputChan <- fmt.Sprintf("Provider地址: %s", provider.Endpoint)
	outputChan <- fmt.Sprintf("SSH用户: %s", provider.Username)

	certService := &provider2.CertService{}

	// 执行自动配置（现在包含完整的配置保存）
	err := certService.AutoConfigureProviderWithStream(&provider, outputChan)
	if err != nil {
		outputChan <- fmt.Sprintf("自动配置失败: %s", err.Error())
		return fmt.Errorf("自动配置失败: %w", err)
	}

	// 根据类型返回不同的成功消息
	var message string
	switch provider.Type {
	case "proxmox":
		message = "Proxmox VE API 自动配置成功，认证配置已保存到数据库和文件"
	case "lxd":
		message = "LXD 自动配置成功，证书已安装并保存到数据库和文件"
	case "incus":
		message = "Incus 自动配置成功，证书已安装并保存到数据库和文件"
	}

	outputChan <- fmt.Sprintf("✅ %s", message)
	outputChan <- "✅ 自动配置流程完成，配置信息已统一管理"

	return nil
}

// CheckProviderHealthAsync 异步检查Provider健康状态
func (s *Service) CheckProviderHealthAsync(providerID uint) {
	go func() {
		if err := s.CheckProviderHealth(providerID); err != nil {
			global.APP_LOG.Warn("异步健康检查失败",
				zap.Uint("providerID", providerID),
				zap.Error(err))
		}
	}()
}

// CheckProviderHealth 检查Provider健康状态
func (s *Service) CheckProviderHealth(providerID uint) error {
	var provider providerModel.Provider
	if err := global.APP_DB.First(&provider, providerID).Error; err != nil {
		return fmt.Errorf("Provider不存在")
	}

	now := time.Now()
	ctx := context.Background()

	// 解析endpoint获取主机，使用数据库中存储的SSH端口
	host := strings.Split(provider.Endpoint, ":")[0]
	sshPort := provider.SSHPort
	if sshPort == 0 {
		sshPort = 22 // 如果数据库中没有设置SSH端口，使用默认值22
	}

	// 使用新的健康检查系统
	healthChecker := health.NewProviderHealthChecker(global.APP_LOG)

	var sshStatus, apiStatus string
	var err error

	// 如果Provider已自动配置，可以尝试进行API检查
	if provider.AutoConfigured && provider.AuthConfig != "" {
		configService := &provider2.ProviderConfigService{}
		authConfig, configErr := configService.LoadProviderConfig(provider.ID)
		if configErr == nil {
			// 使用认证配置执行完整健康检查（包含API检查）
			sshStatus, apiStatus, err = images.CheckProviderHealthWithConfig(
				ctx, provider.Type, host, provider.Username, provider.Password, sshPort, authConfig)
		} else {
			// 配置加载失败，只进行SSH检查
			global.APP_LOG.Warn("加载Provider配置失败，仅进行SSH检查",
				zap.String("provider", provider.Name),
				zap.Error(configErr))

			if sshErr := healthChecker.CheckSSHConnection(ctx, host, provider.Username, provider.Password, sshPort); sshErr != nil {
				sshStatus = "offline"
			} else {
				sshStatus = "online"
			}
			apiStatus = "unknown"
		}
	} else {
		// 未自动配置的Provider，只进行SSH检查
		if sshErr := healthChecker.CheckSSHConnection(ctx, host, provider.Username, provider.Password, sshPort); sshErr != nil {
			sshStatus = "offline"
		} else {
			sshStatus = "online"
		}
		apiStatus = "unknown"
	}

	if err != nil {
		global.APP_LOG.Warn("Health check failed",
			zap.String("provider", provider.Name),
			zap.String("type", provider.Type),
			zap.Error(err))
		// 如果检查失败，设置为offline状态
		if sshStatus == "" {
			sshStatus = "offline"
		}
		if apiStatus == "" {
			apiStatus = "offline"
		}
	}

	// 如果SSH连接成功且资源信息尚未同步，获取系统资源信息
	if sshStatus == "online" && !provider.ResourceSynced {
		global.APP_LOG.Info("开始同步节点资源信息", zap.String("provider", provider.Name))

		resourceInfo, resourceErr := healthChecker.GetSystemResourceInfo(ctx, host, provider.Username, provider.Password, sshPort)
		if resourceErr != nil {
			global.APP_LOG.Warn("获取系统资源信息失败",
				zap.String("provider", provider.Name),
				zap.Error(resourceErr))
		} else {
			// 更新Provider的资源信息
			provider.NodeCPUCores = resourceInfo.CPUCores
			provider.NodeMemoryTotal = resourceInfo.MemoryTotal + resourceInfo.SwapTotal
			provider.NodeDiskTotal = resourceInfo.DiskTotal // 直接使用MB值
			provider.ResourceSynced = true
			provider.ResourceSyncedAt = resourceInfo.SyncedAt

			global.APP_LOG.Info("节点资源信息同步成功",
				zap.String("provider", provider.Name),
				zap.Int("cpu_cores", resourceInfo.CPUCores),
				zap.Int64("memory_total_mb", resourceInfo.MemoryTotal+resourceInfo.SwapTotal),
				zap.Int64("swap_total_mb", resourceInfo.SwapTotal),
				zap.Int64("disk_total_mb", resourceInfo.DiskTotal))
		}
	}

	// 更新Provider状态
	provider.SSHStatus = sshStatus
	provider.APIStatus = apiStatus
	provider.LastSSHCheck = &now
	provider.LastAPICheck = &now

	// 更新整体状态
	if sshStatus == "online" && (apiStatus == "online" || apiStatus == "N/A" || apiStatus == "unknown") {
		provider.Status = "active"
	} else if sshStatus == "offline" && apiStatus == "offline" {
		provider.Status = "inactive"
	} else {
		provider.Status = "partial" // 部分连接正常
	}

	// 先保存状态到数据库
	dbService := database.GetDatabaseService()
	if dbErr := dbService.ExecuteTransaction(context.Background(), func(tx *gorm.DB) error {
		return tx.Save(&provider).Error
	}); dbErr != nil {
		return fmt.Errorf("保存Provider状态失败: %w", dbErr)
	}

	// 如果健康检查有错误，返回该错误（这样前端可以获取具体错误信息）
	return err
}

// GetProviderStatus 获取Provider状态详情
func (s *Service) GetProviderStatus(providerID uint) (*admin.ProviderStatusResponse, error) {
	var provider providerModel.Provider
	if err := global.APP_DB.First(&provider, providerID).Error; err != nil {
		return nil, fmt.Errorf("Provider不存在")
	}

	response := &admin.ProviderStatusResponse{
		ID:              provider.ID,
		UUID:            provider.UUID,
		Name:            provider.Name,
		Type:            provider.Type,
		Status:          provider.Status,
		APIStatus:       provider.APIStatus,
		SSHStatus:       provider.SSHStatus,
		LastAPICheck:    provider.LastAPICheck,
		LastSSHCheck:    provider.LastSSHCheck,
		CertPath:        provider.CertPath,
		KeyPath:         provider.KeyPath,
		CertFingerprint: provider.CertFingerprint,
		// 添加资源信息
		NodeCPUCores:     provider.NodeCPUCores,
		NodeMemoryTotal:  provider.NodeMemoryTotal,
		NodeDiskTotal:    provider.NodeDiskTotal,
		ResourceSynced:   provider.ResourceSynced,
		ResourceSyncedAt: provider.ResourceSyncedAt,
	}

	return response, nil
}
