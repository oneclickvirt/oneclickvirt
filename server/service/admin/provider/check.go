package provider

import (
	"context"
	"fmt"
	"oneclickvirt/global"
	providerModel "oneclickvirt/model/provider"
	"oneclickvirt/provider/health"
	agentService "oneclickvirt/service/agent"
	"oneclickvirt/service/database"
	"oneclickvirt/service/images"
	provider2 "oneclickvirt/service/provider"
	"oneclickvirt/utils"
	"strings"
	"time"

	"go.uber.org/zap"
	"gorm.io/gorm"
)

const agentReconnectGraceWindow = 2 * time.Minute

func isLXDOrIncusProvider(providerType string) bool {
	pt := strings.ToLower(providerType)
	return pt == "lxd" || pt == "incus"
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
	originalStoragePool := provider.StoragePool
	originalStoragePoolPath := provider.StoragePoolPath

	now := time.Now()
	ctx := context.Background()

	// Agent 模式：仅按 Agent WebSocket 链路判断；与 SSH 节点使用不同健康检测语义。
	if provider.ConnectionType == "agent" {
		hub := agentService.GetHub()
		conn, ok := hub.GetConn(provider.ID)
		agentStatus := "offline"
		// 默认保持原有状态，避免主控重启后健康检查将正常节点误判为 inactive
		generalStatus := provider.Status
		if generalStatus == "" {
			generalStatus = "partial"
		}
		var agentHostName, agentVersion string

		if ok && conn != nil {
			// Agent 节点健康仅以反向 WebSocket 在线为准，不复用 SSH 风格命令探测。
			agentStatus = "online"
			generalStatus = "active"
		} else if provider.AgentLastSeen != nil && now.Sub(*provider.AgentLastSeen) <= agentReconnectGraceWindow {
			// 控制端重启后的短窗口内，避免把刚刚在线的 agent 立即判为掉线。
			agentStatus = "online"
			generalStatus = "partial"
			global.APP_LOG.Debug("Agent 处于重连宽限期，暂不降级为离线",
				zap.Uint("providerID", localProviderID),
				zap.String("provider", localProviderName),
				zap.Time("agentLastSeen", *provider.AgentLastSeen),
				zap.Duration("graceWindow", agentReconnectGraceWindow))
		} else if agentService.IsInStartupGracePeriod() {
			// 主控刚启动不久（2分钟宽限期内），Agent 可能尚未完成重连，暂不降级为 inactive。
			// 保留 Provider 当前 status，仅更新 agent_status = offline，避免触发连续失败计数。
			agentStatus = "offline"
			// generalStatus 已设置为 provider.Status（保持原有状态不变）
			global.APP_LOG.Debug("Agent 处于主控启动宽限期，暂不降级 Provider 状态",
				zap.Uint("providerID", localProviderID),
				zap.String("provider", localProviderName),
				zap.String("currentStatus", provider.Status))
		} else {
			// 宽限期已过且 Agent 未重连：渐进式降级
			// active → partial (首次离线), partial → inactive (持续离线)
			switch provider.Status {
			case "active":
				generalStatus = "partial"
				global.APP_LOG.Debug("Agent 离线，状态从 active 降级为 partial",
					zap.Uint("providerID", localProviderID),
					zap.String("provider", localProviderName))
			case "partial":
				generalStatus = "inactive"
				global.APP_LOG.Info("Agent 持续离线，状态从 partial 降级为 inactive",
					zap.Uint("providerID", localProviderID),
					zap.String("provider", localProviderName))
			default:
				// 已经是 inactive 或未知状态，保持不变
				generalStatus = provider.Status
				if generalStatus == "" {
					generalStatus = "inactive"
				}
			}
		}

		updates := map[string]interface{}{
			"agent_status": agentStatus,
			"status":       generalStatus,
			"updated_at":   now,
		}
		if agentHostName != "" {
			updates["host_name"] = agentHostName
		}
		if agentVersion != "" {
			updates["version"] = agentVersion
		}
		global.APP_DB.Model(&providerModel.Provider{}).Where("id = ?", localProviderID).Updates(updates)
		return nil
	}

	// 解析endpoint获取主机
	host := utils.ExtractHost(localEndpoint)

	global.APP_LOG.Debug("开始检查Provider健康状态",
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
	// LXD/Incus 的 storage pool 名称直接参与创建实例。即使资源已同步，也要在健康检查中轻量校验并纠正，
	// 防止历史默认值 default/local 或人工录入错误导致后续创建实例报 Storage pool not found。
	shouldSyncResources := sshStatus == "online" && (forceRefresh || !provider.ResourceSynced || isLXDOrIncusProvider(provider.Type))
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
			provider.NodeDiskTotal = resourceInfo.DiskTotal         // 直接使用MB值
			provider.StoragePoolPath = resourceInfo.StoragePoolPath // 更新自动检测到的存储池路径
			provider.ResourceSynced = true
			provider.ResourceSyncedAt = resourceInfo.SyncedAt

			// 自动检测到的存储池名称（LXD/Incus）
			if resourceInfo.StoragePoolName != "" && provider.StoragePool != resourceInfo.StoragePoolName {
				oldPool := provider.StoragePool
				provider.StoragePool = resourceInfo.StoragePoolName
				global.APP_LOG.Info("自动更新存储池名称",
					zap.Uint("providerID", localProviderID),
					zap.String("provider", localProviderName),
					zap.String("oldPool", oldPool),
					zap.String("newPool", resourceInfo.StoragePoolName))
			}

			// profile root 设备修复日志
			if resourceInfo.ProfileRootDeviceFixed {
				global.APP_LOG.Info("已自动修复default profile的root设备",
					zap.Uint("providerID", localProviderID),
					zap.String("provider", localProviderName))
			}

			// 更新主机名（如果资源信息中包含）
			if resourceInfo.HostName != "" {
				provider.HostName = resourceInfo.HostName
				global.APP_LOG.Debug("从资源同步中获取主机名",
					zap.String("provider", localProviderName),
					zap.String("hostName", resourceInfo.HostName))
			}

			global.APP_LOG.Debug("节点资源信息同步成功",
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
		global.APP_LOG.Debug("更新Provider主机名",
			zap.String("provider", localProviderName),
			zap.String("oldHostName", provider.HostName),
			zap.String("newHostName", hostName))
		provider.HostName = hostName
	}

	// 如果SSH在线，尝试从Provider实例获取版本信息
	if sshStatus == "online" {
		providerSvc := provider2.GetProviderService()
		if providerInstance, exists := providerSvc.GetProviderByID(localProviderID); exists {
			version := providerInstance.GetVersion()
			if version != "" && provider.Version != version {
				global.APP_LOG.Debug("更新Provider版本信息",
					zap.String("provider", localProviderName),
					zap.String("oldVersion", provider.Version),
					zap.String("newVersion", version))
				provider.Version = version
			}
		}
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

	storageChanged := provider.StoragePool != originalStoragePool || provider.StoragePoolPath != originalStoragePoolPath
	refreshRuntimeProviderAfterHealthCheck(localProviderID, localProviderName, provider.ConnectionType, provider.Status, provider.SSHStatus, forceRefresh, storageChanged, originalStoragePool, provider.StoragePool, originalStoragePoolPath, provider.StoragePoolPath)

	// 如果健康检查有错误，返回该错误（这样前端可以获取具体错误信息）
	return err
}

// refreshRuntimeProviderAfterHealthCheck keeps the in-memory ProviderService cache consistent
// with an explicit health check. Health checks use independent SSH/Agent probes and update DB
// state first; without this reload, stale cached providers can remain disconnected and keep
// returning "Provider不可用" even after the node has passed a manual health check.
func refreshRuntimeProviderAfterHealthCheck(providerID uint, providerName, connectionType, status, sshStatus string, forceRefresh, storageChanged bool, oldPool, newPool, oldPoolPath, newPoolPath string) {
	providerSvc := provider2.GetProviderService()

	if status == "inactive" {
		providerSvc.RemoveProvider(providerID)
		global.APP_LOG.Info("Provider健康检查为离线，已清理运行时缓存",
			zap.Uint("providerID", providerID),
			zap.String("provider", providerName))
		return
	}

	shouldReload := storageChanged || forceRefresh
	if connectionType == "agent" {
		shouldReload = true
	}
	if !shouldReload {
		return
	}

	// SSH 类型只有在 SSH 在线时才重载，避免把一次 API/网络抖动变成缓存清空。
	if connectionType != "agent" && sshStatus != "online" {
		return
	}

	if reloadErr := providerSvc.ReloadProvider(providerID); reloadErr != nil {
		global.APP_LOG.Warn("Provider健康检查后运行时缓存刷新失败，新状态将在下次重连后生效",
			zap.Uint("providerID", providerID),
			zap.String("provider", providerName),
			zap.String("status", status),
			zap.String("sshStatus", sshStatus),
			zap.Bool("forceRefresh", forceRefresh),
			zap.Bool("storageChanged", storageChanged),
			zap.String("oldPool", oldPool),
			zap.String("newPool", newPool),
			zap.String("oldPoolPath", oldPoolPath),
			zap.String("newPoolPath", newPoolPath),
			zap.Error(reloadErr))
		return
	}

	global.APP_LOG.Info("Provider健康检查后运行时缓存已刷新",
		zap.Uint("providerID", providerID),
		zap.String("provider", providerName),
		zap.String("status", status),
		zap.Bool("forceRefresh", forceRefresh),
		zap.Bool("storageChanged", storageChanged),
		zap.String("oldPool", oldPool),
		zap.String("newPool", newPool),
		zap.String("oldPoolPath", oldPoolPath),
		zap.String("newPoolPath", newPoolPath))
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
	endpoint, sshPort = normalizeProviderEndpointAndPort(endpoint, sshPort)
	if endpoint == "" {
		return false, nil
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
