package provider

import (
	"errors"
	"fmt"
	"time"

	"oneclickvirt/global"
	"oneclickvirt/model/admin"
	providerModel "oneclickvirt/model/provider"
	"oneclickvirt/utils"

	"go.uber.org/zap"
	"gorm.io/gorm"
)

// Service 管理员Provider管理服务
type Service struct{}

// CheckProviderOwnership 检查普通管理员是否拥有指定Provider
func CheckProviderOwnership(providerID, ownerAdminID uint) error {
	var count int64
	if err := global.APP_DB.Model(&providerModel.Provider{}).
		Where("id = ? AND owner_admin_id = ?", providerID, ownerAdminID).
		Count(&count).Error; err != nil {
		return fmt.Errorf("检查Provider归属失败: %v", err)
	}
	if count == 0 {
		return fmt.Errorf("无权操作该Provider")
	}
	return nil
}

// NewService 创建提供商管理服务
func NewService() *Service {
	return &Service{}
}

// GetProviderNameByID 根据Provider ID获取名称
func (s *Service) GetProviderNameByID(id uint) (string, error) {
	var provider providerModel.Provider
	if err := global.APP_DB.Select("name").First(&provider, id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return "", fmt.Errorf("Provider不存在")
		}
		return "", fmt.Errorf("查询Provider失败: %v", err)
	}
	return provider.Name, nil
}

// GetProviderList 获取Provider列表
func (s *Service) GetProviderList(req admin.ProviderListRequest, ownerAdminID uint) ([]admin.ProviderManageResponse, int64, error) {
	global.APP_LOG.Debug("获取Provider列表",
		zap.String("name", utils.TruncateString(req.Name, 32)),
		zap.String("type", req.Type),
		zap.String("status", req.Status),
		zap.Int("page", req.Page),
		zap.Int("pageSize", req.PageSize))

	var providers []providerModel.Provider
	var total int64

	query := global.APP_DB.Model(&providerModel.Provider{})

	// 普通管理员数据隔离：只能看到自己归属的Provider
	if ownerAdminID > 0 {
		query = query.Where("owner_admin_id = ?", ownerAdminID)
	}

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

	// 批量查询统计数据
	var providerIDs []uint
	for _, provider := range providers {
		providerIDs = append(providerIDs, provider.ID)
	}

	// 批量统计实例数量（总数、容器、虚拟机）
	type InstanceCountResult struct {
		ProviderID     uint
		TotalCount     int64
		ContainerCount int64
		VMCount        int64
	}
	var instanceCounts []InstanceCountResult
	if len(providerIDs) > 0 {
		global.APP_DB.Model(&providerModel.Instance{}).
			Select(`provider_id,
				COUNT(*) as total_count,
				SUM(CASE WHEN instance_type = 'container' THEN 1 ELSE 0 END) as container_count,
				SUM(CASE WHEN instance_type = 'vm' THEN 1 ELSE 0 END) as vm_count`).
			Where("provider_id IN ?", providerIDs).
			Group("provider_id").
			Scan(&instanceCounts)
	}

	// 批量统计运行中的任务数量
	type TaskCountResult struct {
		ProviderID        uint
		RunningTasksCount int64
	}
	var taskCounts []TaskCountResult
	if len(providerIDs) > 0 {
		global.APP_DB.Model(&admin.Task{}).
			Select("provider_id, COUNT(*) as running_tasks_count").
			Where("provider_id IN ? AND status = ?", providerIDs, "running").
			Group("provider_id").
			Scan(&taskCounts)
	}

	// 批量查询Provider本月流量使用情况
	// 直接使用provider_traffic_histories聚合表，避免复杂的实时计算
	type TrafficUsageResult struct {
		ProviderID  uint
		UsedTraffic float64
	}
	var trafficUsages []TrafficUsageResult
	if len(providerIDs) > 0 {
		now := time.Now()
		year, month := now.Year(), int(now.Month())

		// 使用聚合表查询，性能大幅提升
		// day=0,hour=0 表示月度汇总数据
		global.APP_DB.Raw(`
			SELECT 
				pth.provider_id,
				CASE 
					WHEN p.traffic_count_mode = 'out' THEN pth.traffic_out * COALESCE(p.traffic_multiplier, 1.0)
					WHEN p.traffic_count_mode = 'in' THEN pth.traffic_in * COALESCE(p.traffic_multiplier, 1.0)
					ELSE pth.total_used * COALESCE(p.traffic_multiplier, 1.0)
				END as used_traffic
			FROM provider_traffic_histories pth
			INNER JOIN providers p ON pth.provider_id = p.id
			WHERE pth.provider_id IN ?
			  AND pth.year = ?
			  AND pth.month = ?
			  AND pth.day = 0
			  AND pth.hour = 0
			  AND p.enable_traffic_control = true
			  AND pth.deleted_at IS NULL
		`, providerIDs, year, month).Scan(&trafficUsages)
	}

	// 构建映射表
	instanceCountMap := make(map[uint]InstanceCountResult)
	for _, count := range instanceCounts {
		instanceCountMap[count.ProviderID] = count
	}

	taskCountMap := make(map[uint]int64)
	for _, count := range taskCounts {
		taskCountMap[count.ProviderID] = count.RunningTasksCount
	}

	trafficUsageMap := make(map[uint]int64)
	for _, usage := range trafficUsages {
		trafficUsageMap[usage.ProviderID] = int64(usage.UsedTraffic)
	}

	var providerResponses []admin.ProviderManageResponse
	for _, provider := range providers {
		// 从映射表中获取统计数据
		instanceCount := instanceCountMap[provider.ID]
		runningTasksCount := taskCountMap[provider.ID]
		usedTraffic := trafficUsageMap[provider.ID]

		// Docker/Podman/Containerd 类型固定使用 native 端口映射方式
		if provider.Type == "docker" || provider.Type == "podman" || provider.Type == "containerd" {
			provider.IPv4PortMappingMethod = "native"
			provider.IPv6PortMappingMethod = "native"
		}

		// 计算已分配资源（基于实例配置和limit配置）
		// UsedCPUCores, UsedMemory, UsedDisk 已经在数据库中按照limit配置计算好了
		// 这些值在创建/删除实例时由 AllocateResourcesInTx / ReleaseResourcesInTx 维护
		allocatedCPU := provider.UsedCPUCores
		allocatedMemory := provider.UsedMemory
		allocatedDisk := provider.UsedDisk

		providerResponse := admin.ProviderManageResponse{
			Provider:          provider,
			InstanceCount:     int(instanceCount.TotalCount),
			HealthStatus:      "healthy",
			RunningTasksCount: int(runningTasksCount),
			// 包含资源信息
			NodeCPUCores:     provider.NodeCPUCores,
			NodeMemoryTotal:  provider.NodeMemoryTotal,
			NodeDiskTotal:    provider.NodeDiskTotal,
			ResourceSynced:   provider.ResourceSynced,
			ResourceSyncedAt: provider.ResourceSyncedAt,
			// 认证方式标识
			AuthMethod: provider.GetAuthMethod(),
			// 资源占用情况（已分配/总量）
			AllocatedCPUCores: allocatedCPU,
			AllocatedMemory:   allocatedMemory,
			AllocatedDisk:     allocatedDisk,
			// 实例数量统计
			CurrentContainerCount: int(instanceCount.ContainerCount),
			CurrentVMCount:        int(instanceCount.VMCount),
			// 流量使用情况
			UsedTraffic: usedTraffic,
		}
		providerResponses = append(providerResponses, providerResponse)
	}

	global.APP_LOG.Debug("Provider列表查询成功",
		zap.Int64("total", total),
		zap.Int("count", len(providerResponses)))
	return providerResponses, total, nil
}

