package provider

import (
	"context"
	"fmt"
	"oneclickvirt/service/images"
	"oneclickvirt/service/resources"
	"time"

	"oneclickvirt/global"
	imageModel "oneclickvirt/model/image"
	providerModel "oneclickvirt/model/provider"
	"oneclickvirt/provider"

	"go.uber.org/zap"
)

// ProviderApiService 处理Provider API相关的业务逻辑
type ProviderApiService struct{}

// ProviderWithStatus Provider及其状态信息
type ProviderWithStatus struct {
	Provider provider.Provider       // 接口类型
	DBModel  *providerModel.Provider // 数据库模型
}

// ConnectProviderRequest 连接Provider的请求结构
type ConnectProviderRequest struct {
	Name                  string `json:"name" binding:"required"`
	Type                  string `json:"type" binding:"required"`
	Host                  string `json:"host" binding:"required"`
	Port                  int    `json:"port"`    // 兼容旧的port字段
	SSHPort               int    `json:"sshPort"` // 新的sshPort字段
	Username              string `json:"username" binding:"required"`
	Password              string `json:"password" binding:"required"`
	Token                 string `json:"token"` // API Token，用于ProxmoxVE等
	ContainerEnabled      bool   `json:"container_enabled"`
	VirtualMachineEnabled bool   `json:"vm_enabled"`
	CertPath              string `json:"cert_path"`
	KeyPath               string `json:"key_path"`
	NetworkType           string `json:"networkType"` // 网络配置类型
}

// CreateInstanceRequest 创建实例的请求结构
type CreateInstanceRequest struct {
	provider.InstanceConfig
	SystemImageID uint `json:"systemImageId"` // 系统镜像ID
}

// GetProviderByName 从数据库获取Provider配置并创建实例
func (s *ProviderApiService) GetProviderByName(providerName string) (*ProviderWithStatus, error) {
	var dbProvider providerModel.Provider
	if err := global.APP_DB.Where("name = ? AND status = ?", providerName, "active").First(&dbProvider).Error; err != nil {
		return nil, fmt.Errorf("Provider %s 不存在或不可用", providerName)
	}

	// 检查Provider是否过期或冻结
	if dbProvider.IsFrozen {
		return nil, fmt.Errorf("Provider %s 已被冻结", providerName)
	}

	if dbProvider.ExpiresAt != nil && dbProvider.ExpiresAt.Before(time.Now()) {
		return nil, fmt.Errorf("Provider %s 已过期", providerName)
	}

	// 动态创建Provider实例
	prov, err := provider.GetProvider(dbProvider.Type)
	if err != nil {
		return nil, fmt.Errorf("不支持的Provider类型: %s", dbProvider.Type)
	}

	return &ProviderWithStatus{
		Provider: prov,
		DBModel:  &dbProvider,
	}, nil
}

// GetProviderByType 获取指定类型的Provider实例，如果未连接则尝试连接
func (s *ProviderApiService) GetProviderByType(providerType string) (provider.Provider, error) {
	// 首先尝试从已连接的Provider服务中获取
	providerService := GetProviderService()
	if prov, exists := providerService.GetProviderByType(providerType); exists {
		// 检查连接状态
		if prov.IsConnected() {
			return prov, nil
		}
		global.APP_LOG.Info("Provider已存在但未连接，尝试重新连接",
			zap.String("type", providerType))
	}

	// 如果没有找到已连接的Provider，尝试从数据库加载并连接
	var dbProvider providerModel.Provider
	if err := global.APP_DB.Where("type = ? AND status = ?", providerType, "active").First(&dbProvider).Error; err != nil {
		return nil, fmt.Errorf("Provider %s 不存在或不可用", providerType)
	}

	// 检查Provider是否过期或冻结
	if dbProvider.IsFrozen {
		return nil, fmt.Errorf("Provider %s 已被冻结", providerType)
	}

	if dbProvider.ExpiresAt != nil && dbProvider.ExpiresAt.Before(time.Now()) {
		return nil, fmt.Errorf("Provider %s 已过期", providerType)
	}

	// 加载并连接Provider
	if err := providerService.LoadProvider(dbProvider); err != nil {
		global.APP_LOG.Error("加载Provider失败",
			zap.String("type", providerType),
			zap.String("name", dbProvider.Name),
			zap.Error(err))
		return nil, fmt.Errorf("Provider %s 连接失败: %v", providerType, err)
	}

	// 再次从Provider服务中获取已连接的实例
	if prov, exists := providerService.GetProviderByType(providerType); exists {
		return prov, nil
	}

	return nil, fmt.Errorf("Provider %s 加载后仍不可用", providerType)
}

// ConnectProvider 连接Provider
func (s *ProviderApiService) ConnectProvider(ctx context.Context, req ConnectProviderRequest) error {
	// 获取Provider实例
	prov, err := provider.GetProvider(req.Type)
	if err != nil {
		global.APP_LOG.Error("获取Provider失败", zap.Error(err))
		return fmt.Errorf("不支持的Provider类型: %s", req.Type)
	}

	// 确定SSH端口：优先使用SSHPort，如果为0则使用Port，最后默认为22
	sshPort := req.SSHPort
	if sshPort == 0 && req.Port != 0 {
		sshPort = req.Port
	}
	if sshPort == 0 {
		sshPort = 22
	}

	// 创建节点配置
	config := provider.NodeConfig{
		Name:                  req.Name,
		Type:                  req.Type,
		Host:                  req.Host,
		Port:                  sshPort,
		Username:              req.Username,
		Password:              req.Password,
		Token:                 req.Token,
		ContainerEnabled:      req.ContainerEnabled,
		VirtualMachineEnabled: req.VirtualMachineEnabled,
		CertPath:              req.CertPath,
		KeyPath:               req.KeyPath,
		NetworkType:           req.NetworkType,
		SSHConnectTimeout:     30,  // 默认30秒连接超时
		SSHExecuteTimeout:     300, // 默认300秒执行超时
	}

	// 连接Provider
	if err := prov.Connect(ctx, config); err != nil {
		global.APP_LOG.Error("Provider连接失败", zap.Error(err))
		return fmt.Errorf("Provider连接失败: %v", err)
	}

	global.APP_LOG.Info("Provider连接成功", zap.String("name", req.Name), zap.String("type", req.Type))
	return nil
}

// GetAllProviders 获取所有Provider
func (s *ProviderApiService) GetAllProviders() map[string]provider.Provider {
	return provider.GetAllProviders()
}

// GetProviderStatus 获取Provider状态
func (s *ProviderApiService) GetProviderStatus(providerType string) (map[string]interface{}, error) {
	prov, err := s.GetProviderByType(providerType)
	if err != nil {
		return nil, fmt.Errorf("Provider不存在")
	}

	// 检查连接状态
	status := "inactive"
	if prov.IsConnected() {
		status = "active"
	}

	return map[string]interface{}{
		"type":                     providerType,
		"status":                   status,
		"supported_instance_types": prov.GetSupportedInstanceTypes(),
	}, nil
}

// GetProviderCapabilities 获取Provider能力
func (s *ProviderApiService) GetProviderCapabilities(providerType string) (map[string]interface{}, error) {
	prov, err := s.GetProviderByType(providerType)
	if err != nil {
		return nil, fmt.Errorf("Provider不存在")
	}

	return map[string]interface{}{
		"type":                     providerType,
		"supported_instance_types": prov.GetSupportedInstanceTypes(),
	}, nil
}

// CheckProviderConnection 检查Provider连接状态
func (s *ProviderApiService) CheckProviderConnection(prov provider.Provider, providerType string) error {
	if !prov.IsConnected() {
		return fmt.Errorf("%s Provider服务不可用，请先连接Provider", providerType)
	}
	return nil
}

// CheckProviderStatus 检查Provider状态（包括连接状态、冻结状态和过期状态）
func (s *ProviderApiService) CheckProviderStatus(prov provider.Provider, providerType string) error {
	// 首先检查连接状态
	if err := s.CheckProviderConnection(prov, providerType); err != nil {
		return err
	}

	// 查询数据库中的Provider状态
	var providerModel providerModel.Provider
	if err := global.APP_DB.Where("type = ?", providerType).First(&providerModel).Error; err != nil {
		return fmt.Errorf("Provider配置不存在")
	}

	// 检查是否冻结
	if providerModel.IsFrozen {
		return fmt.Errorf("Provider已被冻结，无法执行操作")
	}

	// 检查是否过期
	if providerModel.ExpiresAt != nil && providerModel.ExpiresAt.Before(time.Now()) {
		return fmt.Errorf("Provider已过期，无法执行操作")
	}

	return nil
}

// ListInstances 获取实例列表
func (s *ProviderApiService) ListInstances(ctx context.Context, providerType string) ([]provider.Instance, error) {
	prov, err := s.GetProviderByType(providerType)
	if err != nil {
		return nil, fmt.Errorf("Provider不存在")
	}

	// 检查Provider是否连接
	if err := s.CheckProviderConnection(prov, providerType); err != nil {
		return nil, err
	}

	instances, err := prov.ListInstances(ctx)
	if err != nil {
		global.APP_LOG.Error("获取实例列表失败", zap.Error(err))
		return nil, fmt.Errorf("获取实例列表失败: %v", err)
	}

	return instances, nil
}

// CreateInstance 创建实例
func (s *ProviderApiService) CreateInstance(ctx context.Context, providerType string, req CreateInstanceRequest) error {
	prov, err := s.GetProviderByType(providerType)
	if err != nil {
		return fmt.Errorf("Provider不存在")
	}

	// 检查提供商连接状态、冻结状态和过期状态
	if err := s.CheckProviderStatus(prov, providerType); err != nil {
		return err
	}

	config := req.InstanceConfig

	// 验证Provider类型和实例类型兼容性
	resourceService := &resources.ResourceService{}

	// 首先需要获取Provider ID
	var provider providerModel.Provider
	if err := global.APP_DB.Where("type = ?", providerType).First(&provider).Error; err != nil {
		global.APP_LOG.Error("获取Provider失败", zap.Error(err))
		return fmt.Errorf("Provider不存在")
	}

	// 验证Provider是否支持该实例类型
	if err := resourceService.ValidateInstanceTypeSupport(provider.ID, config.InstanceType); err != nil {
		global.APP_LOG.Error("实例类型不支持", zap.Error(err))
		return err
	}

	// 如果指定了系统镜像ID，获取镜像URL
	if req.SystemImageID > 0 {
		imageService := images.ImageService{}
		downloadReq := imageModel.DownloadImageRequest{
			ImageID:      req.SystemImageID,
			ProviderType: providerType,
			InstanceType: config.InstanceType,
			Architecture: provider.Architecture, // 使用Provider的架构信息
		}

		imageURL, err := imageService.PrepareImageForInstance(downloadReq)
		if err != nil {
			global.APP_LOG.Error("准备镜像失败", zap.Error(err))
			return fmt.Errorf("准备镜像失败: %v", err)
		}

		// 设置镜像URL，让Provider自己处理下载
		config.ImageURL = imageURL
		global.APP_LOG.Info("镜像信息准备完成", zap.String("imageURL", imageURL))
	}

	if err := prov.CreateInstance(ctx, config); err != nil {
		global.APP_LOG.Error("创建实例失败", zap.Error(err))
		return fmt.Errorf("创建实例失败: %v", err)
	}

	global.APP_LOG.Info("实例创建成功", zap.String("name", config.Name))
	return nil
}

// GetInstance 获取实例详情
func (s *ProviderApiService) GetInstance(ctx context.Context, providerType, instanceID string) (interface{}, error) {
	prov, err := s.GetProviderByType(providerType)
	if err != nil {
		return nil, fmt.Errorf("Provider不存在")
	}

	// 检查Provider是否连接
	if err := s.CheckProviderConnection(prov, providerType); err != nil {
		return nil, err
	}

	instance, err := prov.GetInstance(ctx, instanceID)
	if err != nil {
		global.APP_LOG.Error("获取实例失败", zap.Error(err))
		return nil, fmt.Errorf("获取实例失败: %v", err)
	}

	if instance == nil {
		return nil, fmt.Errorf("实例不存在")
	}

	return instance, nil
}

// StartInstance 启动实例
func (s *ProviderApiService) StartInstance(ctx context.Context, providerType, instanceID string) error {
	prov, err := s.GetProviderByType(providerType)
	if err != nil {
		return fmt.Errorf("Provider不存在")
	}

	// 检查Provider是否连接
	if err := s.CheckProviderConnection(prov, providerType); err != nil {
		return err
	}

	if err := prov.StartInstance(ctx, instanceID); err != nil {
		global.APP_LOG.Error("启动实例失败", zap.Error(err))
		return fmt.Errorf("启动实例失败: %v", err)
	}

	global.APP_LOG.Info("实例启动成功", zap.String("id", instanceID))
	return nil
}

// StopInstance 停止实例
func (s *ProviderApiService) StopInstance(ctx context.Context, providerType, instanceID string) error {
	prov, err := s.GetProviderByType(providerType)
	if err != nil {
		return fmt.Errorf("Provider不存在")
	}

	// 检查Provider是否连接
	if err := s.CheckProviderConnection(prov, providerType); err != nil {
		return err
	}

	if err := prov.StopInstance(ctx, instanceID); err != nil {
		global.APP_LOG.Error("停止实例失败", zap.Error(err))
		return fmt.Errorf("停止实例失败: %v", err)
	}

	global.APP_LOG.Info("实例停止成功", zap.String("id", instanceID))
	return nil
}

// RestartInstance 重启实例
func (s *ProviderApiService) RestartInstance(ctx context.Context, providerType, instanceID string) error {
	prov, err := s.GetProviderByType(providerType)
	if err != nil {
		return fmt.Errorf("Provider不存在")
	}

	// 检查提供商连接状态
	if err := s.CheckProviderConnection(prov, providerType); err != nil {
		return err
	}

	if err := prov.RestartInstance(ctx, instanceID); err != nil {
		global.APP_LOG.Error("重启实例失败", zap.Error(err))
		return fmt.Errorf("重启实例失败: %v", err)
	}

	global.APP_LOG.Info("实例重启成功", zap.String("id", instanceID))
	return nil
}

// DeleteInstance 删除实例
func (s *ProviderApiService) DeleteInstance(ctx context.Context, providerType, instanceID string) error {
	prov, err := s.GetProviderByType(providerType)
	if err != nil {
		return fmt.Errorf("Provider不存在")
	}

	// 检查提供商连接状态
	if err := s.CheckProviderConnection(prov, providerType); err != nil {
		return err
	}

	if err := prov.DeleteInstance(ctx, instanceID); err != nil {
		global.APP_LOG.Error("删除实例失败", zap.Error(err))
		return fmt.Errorf("删除实例失败: %v", err)
	}

	global.APP_LOG.Info("实例删除成功", zap.String("id", instanceID))
	return nil
}

// ListImages 获取镜像列表
func (s *ProviderApiService) ListImages(ctx context.Context, providerType string) ([]provider.Image, error) {
	prov, err := s.GetProviderByType(providerType)
	if err != nil {
		return nil, fmt.Errorf("Provider不存在")
	}

	// 检查Provider是否连接
	if err := s.CheckProviderConnection(prov, providerType); err != nil {
		return nil, err
	}

	images, err := prov.ListImages(ctx)
	if err != nil {
		global.APP_LOG.Error("获取镜像列表失败", zap.Error(err))
		return nil, fmt.Errorf("获取镜像列表失败: %v", err)
	}

	return images, nil
}

// PullImage 拉取镜像
func (s *ProviderApiService) PullImage(ctx context.Context, providerType, image string) error {
	prov, err := s.GetProviderByType(providerType)
	if err != nil {
		return fmt.Errorf("Provider不存在")
	}

	// 检查提供商连接状态
	if err := s.CheckProviderConnection(prov, providerType); err != nil {
		return err
	}

	if err := prov.PullImage(ctx, image); err != nil {
		global.APP_LOG.Error("镜像拉取失败", zap.Error(err))
		return fmt.Errorf("镜像拉取失败: %v", err)
	}

	global.APP_LOG.Info("镜像拉取成功", zap.String("image", image))
	return nil
}

// DeleteImage 删除镜像
func (s *ProviderApiService) DeleteImage(ctx context.Context, providerType, imageID string) error {
	prov, err := s.GetProviderByType(providerType)
	if err != nil {
		return fmt.Errorf("Provider不存在")
	}

	// 检查提供商连接状态
	if err := s.CheckProviderConnection(prov, providerType); err != nil {
		return err
	}

	if err := prov.DeleteImage(ctx, imageID); err != nil {
		global.APP_LOG.Error("镜像删除失败", zap.Error(err))
		return fmt.Errorf("镜像删除失败: %v", err)
	}

	global.APP_LOG.Info("镜像删除成功", zap.String("id", imageID))
	return nil
}
