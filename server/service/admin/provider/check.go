package provider

import (
	"context"
	"fmt"
	"oneclickvirt/global"
	providerModel "oneclickvirt/model/provider"
	"oneclickvirt/provider/health"
	"oneclickvirt/service/database"
	"oneclickvirt/service/images"
	provider2 "oneclickvirt/service/provider"
	"strings"
	"time"

	"go.uber.org/zap"
	"gorm.io/gorm"
)

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

// CheckProviderHealth 检查Provider健康状态（默认不强制刷新，仅首次同步）
func (s *Service) CheckProviderHealth(providerID uint) error {
	return s.CheckProviderHealthWithOptions(providerID, false)
}

// CheckProviderHealthWithOptions 检查Provider健康状态，支持选择是否强制刷新资源
func (s *Service) CheckProviderHealthWithOptions(providerID uint, forceRefresh bool) error {
	var provider providerModel.Provider
	if err := global.APP_DB.First(&provider, providerID).Error; err != nil {
		return fmt.Errorf("Provider不存在")
	}

	// 复制副本避免共享状态，立即创建所有必要字段的本地副本
	// 这些变量在整个函数执行期间保持不变，确保健康检查使用正确的参数
	localProviderID := provider.ID
	localProviderName := provider.Name
	localProviderType := provider.Type
	localEndpoint := provider.Endpoint
	localUsername := provider.Username
	localPassword := provider.Password
	localSSHKey := provider.SSHKey
	localSSHPort := provider.SSHPort
	if localSSHPort == 0 {
		localSSHPort = 22 // 如果数据库中没有设置SSH端口，使用默认值22
	}
	localAutoConfigured := provider.AutoConfigured
	localAuthConfig := provider.AuthConfig

	now := time.Now()
	ctx := context.Background()

	// 解析endpoint获取主机
	host := strings.Split(localEndpoint, ":")[0]

	global.APP_LOG.Info("开始检查Provider健康状态",
		zap.Uint("providerId", localProviderID),
		zap.String("providerName", localProviderName),
		zap.String("providerType", localProviderType),
		zap.String("endpoint", localEndpoint),
		zap.String("host", host),
		zap.Int("port", localSSHPort))

	// 使用新的健康检查系统
	healthChecker := health.NewProviderHealthChecker(global.APP_LOG)

	var sshStatus, apiStatus, hostName string
	var err error

	// 如果Provider已自动配置，可以尝试进行API检查
	if localAutoConfigured && localAuthConfig != "" {
		configService := &provider2.ProviderConfigService{}
		authConfig, configErr := configService.LoadProviderConfig(localProviderID)
		if configErr == nil {
			// 添加详细日志，确认传入的参数
			global.APP_LOG.Debug("调用CheckProviderHealthWithConfig",
				zap.Uint("providerId", localProviderID),
				zap.String("providerName", localProviderName),
				zap.String("providerType", localProviderType),
				zap.String("host", host),
				zap.Int("sshPort", localSSHPort),
				zap.String("endpoint", localEndpoint))

			// 使用认证配置执行完整健康检查（包含API检查），并获取主机名
			sshStatus, apiStatus, hostName, err = images.CheckProviderHealthWithConfig(
				ctx, localProviderID, localProviderName, localProviderType, host, localUsername, localPassword, localSSHKey, localSSHPort, authConfig)
		} else {
			// 配置加载失败，只进行SSH检查
			global.APP_LOG.Warn("加载Provider配置失败，仅进行SSH检查",
				zap.String("provider", localProviderName),
				zap.Error(configErr))

			if sshErr := healthChecker.CheckSSHConnection(ctx, localProviderID, localProviderName, host, localUsername, localPassword, localSSHKey, localSSHPort); sshErr != nil {
				sshStatus = "offline"
			} else {
				sshStatus = "online"
			}
			apiStatus = "unknown"
		}
	} else {
		// 未自动配置的Provider，只进行SSH检查
		if sshErr := healthChecker.CheckSSHConnection(ctx, localProviderID, localProviderName, host, localUsername, localPassword, localSSHKey, localSSHPort); sshErr != nil {
			sshStatus = "offline"
		} else {
			sshStatus = "online"
		}
		apiStatus = "unknown"
	}

	if err != nil {
		global.APP_LOG.Warn("Health check failed",
			zap.String("provider", localProviderName),
			zap.String("type", localProviderType),
			zap.Error(err))
		// 如果检查失败，设置为offline状态
		if sshStatus == "" {
			sshStatus = "offline"
		}
		if apiStatus == "" {
			apiStatus = "offline"
		}
	}

	// 如果SSH连接成功且（强制刷新或资源信息尚未同步），获取系统资源信息
	shouldSyncResources := sshStatus == "online" && (forceRefresh || !provider.ResourceSynced)
	if shouldSyncResources {
		logMsg := "开始同步节点资源信息"
		if forceRefresh {
			logMsg = "强制刷新节点资源信息"
		}
		global.APP_LOG.Info(logMsg,
			zap.Uint("providerID", localProviderID),
			zap.String("provider", localProviderName),
			zap.String("host", host),
			zap.Int("sshPort", localSSHPort),
			zap.Bool("forceRefresh", forceRefresh))

		resourceInfo, resourceErr := healthChecker.GetSystemResourceInfoWithKey(ctx, localProviderID, localProviderName, host, localUsername, localPassword, localSSHKey, localSSHPort, provider.Type, provider.StoragePool)
		if resourceErr != nil {
			global.APP_LOG.Warn("获取系统资源信息失败",
				zap.String("provider", localProviderName),
				zap.Error(resourceErr))
		} else {
			// 更新Provider的资源信息
			provider.NodeCPUCores = resourceInfo.CPUCores
			provider.NodeMemoryTotal = resourceInfo.MemoryTotal + resourceInfo.SwapTotal
			provider.NodeDiskTotal = resourceInfo.DiskTotal // 直接使用MB值
			provider.StoragePoolPath = resourceInfo.StoragePoolPath // 更新自动检测到的存储池路径
			provider.ResourceSynced = true
			provider.ResourceSyncedAt = resourceInfo.SyncedAt

			// 更新主机名（如果资源信息中包含）
			if resourceInfo.HostName != "" {
				provider.HostName = resourceInfo.HostName
				global.APP_LOG.Info("从资源同步中获取主机名",
					zap.String("provider", localProviderName),
					zap.String("hostName", resourceInfo.HostName))
			}

			global.APP_LOG.Info("节点资源信息同步成功",
				zap.String("provider", localProviderName),
				zap.Int("cpu_cores", resourceInfo.CPUCores),
				zap.Int64("memory_total_mb", resourceInfo.MemoryTotal+resourceInfo.SwapTotal),
				zap.Int64("swap_total_mb", resourceInfo.SwapTotal),
				zap.Int64("disk_total_mb", resourceInfo.DiskTotal),
				zap.String("hostName", resourceInfo.HostName))
		}
	}

	// 更新Provider状态
	provider.SSHStatus = sshStatus
	provider.APIStatus = apiStatus
	provider.LastSSHCheck = &now
	provider.LastAPICheck = &now

	// 更新主机名（如果获取到了）
	if hostName != "" && provider.HostName != hostName {
		global.APP_LOG.Info("更新Provider主机名",
			zap.String("provider", localProviderName),
			zap.String("oldHostName", provider.HostName),
			zap.String("newHostName", hostName))
		provider.HostName = hostName
	}

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

// CheckProviderNameExists 检查Provider名称是否已存在
func (s *Service) CheckProviderNameExists(name string, excludeId *uint) (bool, error) {
	query := global.APP_DB.Model(&providerModel.Provider{}).Where("name = ?", name)

	// 如果提供了excludeId，排除该ID（用于编辑时的检查）
	if excludeId != nil {
		query = query.Where("id != ?", *excludeId)
	}

	var count int64
	if err := query.Count(&count).Error; err != nil {
		return false, err
	}

	return count > 0, nil
}

// CheckProviderEndpointExists 检查Provider SSH地址和端口组合是否已存在
func (s *Service) CheckProviderEndpointExists(endpoint string, sshPort int, excludeId *uint) (bool, error) {
	// 如果端口为0，使用默认值22
	if sshPort == 0 {
		sshPort = 22
	}

	query := global.APP_DB.Model(&providerModel.Provider{}).
		Where("endpoint = ? AND ssh_port = ?", endpoint, sshPort)

	// 如果提供了excludeId，排除该ID（用于编辑时的检查）
	if excludeId != nil {
		query = query.Where("id != ?", *excludeId)
	}

	var count int64
	if err := query.Count(&count).Error; err != nil {
		return false, err
	}

	return count > 0, nil
}
