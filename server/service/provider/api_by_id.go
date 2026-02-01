package provider

import (
	"context"
	"fmt"
	"oneclickvirt/global"
	providerModel "oneclickvirt/model/provider"
	"oneclickvirt/provider"
	"strconv"
	"time"

	"go.uber.org/zap"
)

// GetProviderByID 根据Provider ID获取Provider实例（如果未连接则尝试连接）
func (s *ProviderApiService) GetProviderByID(providerID uint) (provider.Provider, *providerModel.Provider, error) {
	return s.GetProviderByIDForOperation(providerID, "")
}

// GetProviderByIDForOperation 根据Provider ID和操作类型获取Provider实例
// operationType: 操作类型，如"delete"等，某些操作允许访问冻结的Provider
func (s *ProviderApiService) GetProviderByIDForOperation(providerID uint, operationType string) (provider.Provider, *providerModel.Provider, error) {
	// 从数据库获取Provider配置
	var dbProvider providerModel.Provider
	if err := global.APP_DB.First(&dbProvider, providerID).Error; err != nil {
		return nil, nil, fmt.Errorf("Provider不存在")
	}

	// 检查Provider状态
	if dbProvider.Status != "active" {
		return nil, nil, fmt.Errorf("Provider未激活")
	}

	// 删除操作允许访问冻结的Provider
	allowFrozen := operationType == "delete"
	if dbProvider.IsFrozen && !allowFrozen {
		return nil, nil, fmt.Errorf("Provider已被冻结")
	}

	if dbProvider.ExpiresAt != nil && dbProvider.ExpiresAt.Before(time.Now()) {
		return nil, nil, fmt.Errorf("Provider已过期")
	}

	// 从Provider服务获取已连接的实例（使用ID）
	providerService := GetProviderService()
	if prov, exists := providerService.GetProviderByID(dbProvider.ID); exists {
		if prov.IsConnected() {
			return prov, &dbProvider, nil
		}
		global.APP_LOG.Info("Provider已存在但未连接，尝试重新连接",
			zap.Uint("providerId", providerID),
			zap.String("name", dbProvider.Name))
	}

	// 如果未连接，尝试加载并连接
	// 删除操作允许加载冻结的Provider（使用已定义的allowFrozen变量）
	if err := providerService.LoadProviderWithOptions(dbProvider, allowFrozen); err != nil {
		global.APP_LOG.Error("加载Provider失败",
			zap.Uint("providerId", providerID),
			zap.String("name", dbProvider.Name),
			zap.Error(err))
		return nil, nil, fmt.Errorf("Provider连接失败: %v", err)
	}

	// 再次获取（使用ID）
	if prov, exists := providerService.GetProviderByID(dbProvider.ID); exists {
		return prov, &dbProvider, nil
	}

	return nil, nil, fmt.Errorf("Provider加载后仍不可用")
}

// parseProviderID 解析字符串格式的Provider ID
func parseProviderID(providerIDStr string) (uint, error) {
	id, err := strconv.ParseUint(providerIDStr, 10, 32)
	if err != nil {
		return 0, fmt.Errorf("无效的Provider ID")
	}
	return uint(id), nil
}

// GetProviderStatusByID 根据Provider ID获取状态
func (s *ProviderApiService) GetProviderStatusByID(providerIDStr string) (map[string]interface{}, error) {
	providerID, err := parseProviderID(providerIDStr)
	if err != nil {
		return nil, err
	}

	// 从数据库获取Provider配置
	var dbProvider providerModel.Provider
	if err := global.APP_DB.First(&dbProvider, providerID).Error; err != nil {
		return nil, fmt.Errorf("Provider不存在")
	}

	// Docker 类型固定使用 native 端口映射方式
	ipv4Method := dbProvider.IPv4PortMappingMethod
	ipv6Method := dbProvider.IPv6PortMappingMethod
	if dbProvider.Type == "docker" {
		ipv4Method = "native"
		ipv6Method = "native"
	}

	// 检查Provider是否已连接（不尝试新连接）
	providerService := GetProviderService()
	var connected bool
	var supportedTypes []string

	if prov, exists := providerService.GetProviderByID(dbProvider.ID); exists && prov.IsConnected() {
		connected = true
		supportedTypes = prov.GetSupportedInstanceTypes()
	} else {
		connected = false
		// 根据配置返回支持的实例类型
		if dbProvider.ContainerEnabled && dbProvider.VirtualMachineEnabled {
			supportedTypes = []string{"container", "vm"}
		} else if dbProvider.ContainerEnabled {
			supportedTypes = []string{"container"}
		} else if dbProvider.VirtualMachineEnabled {
			supportedTypes = []string{"vm"}
		}
	}

	status := map[string]interface{}{
		"id":                    dbProvider.ID,
		"name":                  dbProvider.Name,
		"type":                  dbProvider.Type,
		"connected":             connected,
		"status":                dbProvider.Status,
		"supportedTypes":        supportedTypes,
		"containerEnabled":      dbProvider.ContainerEnabled,
		"vmEnabled":             dbProvider.VirtualMachineEnabled,
		"architecture":          dbProvider.Architecture,
		"region":                dbProvider.Region,
		"country":               dbProvider.Country,
		"isFrozen":              dbProvider.IsFrozen,
		"allowClaim":            dbProvider.AllowClaim,
		"cpuCores":              dbProvider.NodeCPUCores,
		"memoryTotal":           dbProvider.NodeMemoryTotal,
		"diskTotal":             dbProvider.NodeDiskTotal,
		"maxContainers":         dbProvider.MaxContainerInstances,
		"maxVMs":                dbProvider.MaxVMInstances,
		"portRangeStart":        dbProvider.PortRangeStart,
		"portRangeEnd":          dbProvider.PortRangeEnd,
		"defaultPortCount":      dbProvider.DefaultPortCount,
		"ipv4PortMappingMethod": ipv4Method,
		"ipv6PortMappingMethod": ipv6Method,
		"maxTraffic":            dbProvider.MaxTraffic,
		"trafficCountMode":      dbProvider.TrafficCountMode,
		"trafficMultiplier":     dbProvider.TrafficMultiplier,
	}

	if dbProvider.ExpiresAt != nil {
		status["expiresAt"] = dbProvider.ExpiresAt
	}

	return status, nil
}

// GetProviderCapabilitiesByID 根据Provider ID获取能力
func (s *ProviderApiService) GetProviderCapabilitiesByID(providerIDStr string) (map[string]interface{}, error) {
	providerID, err := parseProviderID(providerIDStr)
	if err != nil {
		return nil, err
	}

	// 从数据库获取Provider配置
	var dbProvider providerModel.Provider
	if err := global.APP_DB.First(&dbProvider, providerID).Error; err != nil {
		return nil, fmt.Errorf("Provider不存在")
	}

	// Docker 类型固定使用 native 端口映射方式
	ipv4Method := dbProvider.IPv4PortMappingMethod
	ipv6Method := dbProvider.IPv6PortMappingMethod
	if dbProvider.Type == "docker" {
		ipv4Method = "native"
		ipv6Method = "native"
	}

	// 检查Provider是否已连接（不尝试新连接）
	providerService := GetProviderService()
	var supportedTypes []string

	if prov, exists := providerService.GetProviderByID(dbProvider.ID); exists && prov.IsConnected() {
		supportedTypes = prov.GetSupportedInstanceTypes()
	} else {
		// 根据配置返回支持的实例类型
		if dbProvider.ContainerEnabled && dbProvider.VirtualMachineEnabled {
			supportedTypes = []string{"container", "vm"}
		} else if dbProvider.ContainerEnabled {
			supportedTypes = []string{"container"}
		} else if dbProvider.VirtualMachineEnabled {
			supportedTypes = []string{"vm"}
		}
	}

	capabilities := map[string]interface{}{
		"id":                    dbProvider.ID,
		"name":                  dbProvider.Name,
		"type":                  dbProvider.Type,
		"supportedTypes":        supportedTypes,
		"containerEnabled":      dbProvider.ContainerEnabled,
		"vmEnabled":             dbProvider.VirtualMachineEnabled,
		"architecture":          dbProvider.Architecture,
		"maxCpu":                dbProvider.NodeCPUCores,
		"maxMemory":             dbProvider.NodeMemoryTotal,
		"maxDisk":               dbProvider.NodeDiskTotal,
		"region":                dbProvider.Region,
		"country":               dbProvider.Country,
		"status":                dbProvider.Status,
		"ipv4PortMappingMethod": ipv4Method,
		"ipv6PortMappingMethod": ipv6Method,
		"maxContainerInstances": dbProvider.MaxContainerInstances,
		"maxVMInstances":        dbProvider.MaxVMInstances,
		"allowConcurrentTasks":  dbProvider.AllowConcurrentTasks,
		"maxConcurrentTasks":    dbProvider.MaxConcurrentTasks,
		// 流量配置
		"maxTraffic":        dbProvider.MaxTraffic,
		"trafficCountMode":  dbProvider.TrafficCountMode,
		"trafficMultiplier": dbProvider.TrafficMultiplier,
	}

	return capabilities, nil
}

// ListInstancesByProviderID 根据Provider ID获取实例列表
func (s *ProviderApiService) ListInstancesByProviderID(ctx context.Context, providerIDStr string) ([]provider.Instance, error) {
	providerID, err := parseProviderID(providerIDStr)
	if err != nil {
		return nil, err
	}

	prov, _, err := s.GetProviderByID(providerID)
	if err != nil {
		return nil, err
	}

	instances, err := prov.ListInstances(ctx)
	if err != nil {
		global.APP_LOG.Error("获取实例列表失败",
			zap.Uint("providerId", providerID),
			zap.Error(err))
		return nil, fmt.Errorf("获取实例列表失败: %v", err)
	}

	return instances, nil
}

// CreateInstanceByProviderIDFromString 根据字符串Provider ID创建实例
func (s *ProviderApiService) CreateInstanceByProviderIDFromString(ctx context.Context, providerIDStr string, req CreateInstanceRequest) error {
	providerID, err := parseProviderID(providerIDStr)
	if err != nil {
		return err
	}

	return s.CreateInstanceByProviderID(ctx, providerID, req)
}

// GetInstanceByProviderID 根据Provider ID获取实例详情
func (s *ProviderApiService) GetInstanceByProviderID(ctx context.Context, providerIDStr string, instanceName string) (interface{}, error) {
	providerID, err := parseProviderID(providerIDStr)
	if err != nil {
		return nil, err
	}

	prov, _, err := s.GetProviderByID(providerID)
	if err != nil {
		return nil, err
	}

	instance, err := prov.GetInstance(ctx, instanceName)
	if err != nil {
		global.APP_LOG.Error("获取实例失败",
			zap.Uint("providerId", providerID),
			zap.String("instanceName", instanceName),
			zap.Error(err))
		return nil, fmt.Errorf("获取实例失败: %v", err)
	}

	if instance == nil {
		return nil, fmt.Errorf("实例不存在")
	}

	return instance, nil
}

// StartInstanceByProviderIDFromString 根据字符串Provider ID启动实例
func (s *ProviderApiService) StartInstanceByProviderIDFromString(ctx context.Context, providerIDStr string, instanceName string) error {
	providerID, err := parseProviderID(providerIDStr)
	if err != nil {
		return err
	}

	return s.StartInstanceByProviderID(ctx, providerID, instanceName)
}

// StopInstanceByProviderIDFromString 根据字符串Provider ID停止实例
func (s *ProviderApiService) StopInstanceByProviderIDFromString(ctx context.Context, providerIDStr string, instanceName string) error {
	providerID, err := parseProviderID(providerIDStr)
	if err != nil {
		return err
	}

	return s.StopInstanceByProviderID(ctx, providerID, instanceName)
}

// DeleteInstanceByProviderIDFromString 根据字符串Provider ID删除实例
func (s *ProviderApiService) DeleteInstanceByProviderIDFromString(ctx context.Context, providerIDStr string, instanceName string) error {
	providerID, err := parseProviderID(providerIDStr)
	if err != nil {
		return err
	}

	return s.DeleteInstanceByProviderID(ctx, providerID, instanceName)
}

// ListImagesByProviderID 根据Provider ID获取镜像列表
func (s *ProviderApiService) ListImagesByProviderID(ctx context.Context, providerIDStr string) ([]interface{}, error) {
	providerID, err := parseProviderID(providerIDStr)
	if err != nil {
		return nil, err
	}

	prov, _, err := s.GetProviderByID(providerID)
	if err != nil {
		return nil, err
	}

	images, err := prov.ListImages(ctx)
	if err != nil {
		global.APP_LOG.Error("获取镜像列表失败",
			zap.Uint("providerId", providerID),
			zap.Error(err))
		return nil, fmt.Errorf("获取镜像列表失败: %v", err)
	}

	// 转换为interface{}数组
	result := make([]interface{}, len(images))
	for i, img := range images {
		result[i] = img
	}

	return result, nil
}

// PullImageByProviderID 根据Provider ID拉取镜像
func (s *ProviderApiService) PullImageByProviderID(ctx context.Context, providerIDStr string, imageName string) error {
	providerID, err := parseProviderID(providerIDStr)
	if err != nil {
		return err
	}

	prov, _, err := s.GetProviderByID(providerID)
	if err != nil {
		return err
	}

	if err := prov.PullImage(ctx, imageName); err != nil {
		global.APP_LOG.Error("拉取镜像失败",
			zap.Uint("providerId", providerID),
			zap.String("imageName", imageName),
			zap.Error(err))
		return fmt.Errorf("拉取镜像失败: %v", err)
	}

	global.APP_LOG.Info("镜像拉取成功",
		zap.Uint("providerId", providerID),
		zap.String("imageName", imageName))
	return nil
}

// DeleteImageByProviderID 根据Provider ID删除镜像
func (s *ProviderApiService) DeleteImageByProviderID(ctx context.Context, providerIDStr string, imageName string) error {
	providerID, err := parseProviderID(providerIDStr)
	if err != nil {
		return err
	}

	prov, _, err := s.GetProviderByID(providerID)
	if err != nil {
		return err
	}

	if err := prov.DeleteImage(ctx, imageName); err != nil {
		global.APP_LOG.Error("删除镜像失败",
			zap.Uint("providerId", providerID),
			zap.String("imageName", imageName),
			zap.Error(err))
		return fmt.Errorf("删除镜像失败: %v", err)
	}

	global.APP_LOG.Info("镜像删除成功",
		zap.Uint("providerId", providerID),
		zap.String("imageName", imageName))
	return nil
}
