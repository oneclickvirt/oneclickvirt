package provider

import (
	"context"
	"fmt"
	"oneclickvirt/service/images"
	"oneclickvirt/service/resources"

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
	SSHKey                string `json:"sshKey"` // SSH私钥内容，优先于密码使用
	Token                 string `json:"token"`  // API Token，用于ProxmoxVE等
	ContainerEnabled      bool   `json:"container_enabled"`
	VirtualMachineEnabled bool   `json:"vm_enabled"`
	CertPath              string `json:"cert_path"`
	KeyPath               string `json:"key_path"`
	NetworkType           string `json:"networkType"` // 网络配置类型

	// 容器资源限制配置（Provider层面）
	ContainerLimitCPU    bool `json:"containerLimitCpu"`    // 容器是否限制CPU数量
	ContainerLimitMemory bool `json:"containerLimitMemory"` // 容器是否限制内存大小
	ContainerLimitDisk   bool `json:"containerLimitDisk"`   // 容器是否限制硬盘大小

	// 虚拟机资源限制配置（Provider层面）
	VMLimitCPU    bool `json:"vmLimitCpu"`    // 虚拟机是否限制CPU数量
	VMLimitMemory bool `json:"vmLimitMemory"` // 虚拟机是否限制内存大小
	VMLimitDisk   bool `json:"vmLimitDisk"`   // 虚拟机是否限制硬盘大小

	// 节点级别的等级限制配置
	// 用于限制该节点上不同等级用户能创建的最大资源
	LevelLimits map[int]map[string]interface{} `json:"levelLimits"` // 等级限制配置
}

// CreateInstanceRequest 创建实例的请求结构
type CreateInstanceRequest struct {
	provider.InstanceConfig
	SystemImageID uint `json:"systemImageId"` // 系统镜像ID
}

// ConnectProvider 连接Provider（测试连接）
// 此方法仅用于测试连接，会创建临时实例，不会影响已加载的Provider实例
func (s *ProviderApiService) ConnectProvider(ctx context.Context, req ConnectProviderRequest) error {
	// 创建临时Provider实例用于测试连接
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
		PrivateKey:            req.SSHKey,
		Token:                 req.Token,
		ContainerEnabled:      req.ContainerEnabled,
		VirtualMachineEnabled: req.VirtualMachineEnabled,
		CertPath:              req.CertPath,
		KeyPath:               req.KeyPath,
		NetworkType:           req.NetworkType,
		SSHConnectTimeout:     30,  // 默认30秒连接超时
		SSHExecuteTimeout:     300, // 默认300秒执行超时
		// 资源限制配置
		ContainerLimitCPU:    req.ContainerLimitCPU,
		ContainerLimitMemory: req.ContainerLimitMemory,
		ContainerLimitDisk:   req.ContainerLimitDisk,
		VMLimitCPU:           req.VMLimitCPU,
		VMLimitMemory:        req.VMLimitMemory,
		VMLimitDisk:          req.VMLimitDisk,
	}

	// 连接Provider
	if err := prov.Connect(ctx, config); err != nil {
		global.APP_LOG.Error("Provider连接失败", zap.Error(err))
		return fmt.Errorf("Provider连接失败: %v", err)
	}

	global.APP_LOG.Info("Provider连接成功", zap.String("name", req.Name), zap.String("type", req.Type))
	return nil
}

// GetAllProviders 获取所有Provider类型名称列表
// 不再返回Provider实例，因为每次使用都应该创建新实例
func (s *ProviderApiService) GetAllProviders() []string {
	return provider.ListProviders()
}

// CheckProviderConnection 检查Provider连接状态
func CheckProviderConnection(prov provider.Provider) error {
	if !prov.IsConnected() {
		return fmt.Errorf("Provider服务不可用，请先连接Provider")
	}
	return nil
}

// CreateInstanceByProviderID 根据Provider ID创建实例（确保使用正确的Provider）
func (s *ProviderApiService) CreateInstanceByProviderID(ctx context.Context, providerID uint, req CreateInstanceRequest) error {
	// 使用新的GetProviderByID方法
	prov, dbProvider, err := s.GetProviderByID(providerID)
	if err != nil {
		return err
	}

	// 检查连接状态
	if err := CheckProviderConnection(prov); err != nil {
		return err
	}

	config := req.InstanceConfig

	// 验证Provider类型和实例类型兼容性
	resourceService := &resources.ResourceService{}
	if err := resourceService.ValidateInstanceTypeSupport(dbProvider.ID, config.InstanceType); err != nil {
		global.APP_LOG.Error("实例类型不支持", zap.Error(err))
		return err
	}

	// 如果指定了系统镜像ID，获取镜像URL
	if req.SystemImageID > 0 {
		imageService := images.ImageService{}
		downloadReq := imageModel.DownloadImageRequest{
			ImageID:      req.SystemImageID,
			ProviderType: dbProvider.Type,
			InstanceType: config.InstanceType,
			Architecture: dbProvider.Architecture, // 使用指定Provider的架构信息
		}

		imageURL, err := imageService.PrepareImageForInstance(downloadReq)
		if err != nil {
			global.APP_LOG.Error("准备镜像失败", zap.Error(err))
			return fmt.Errorf("准备镜像失败: %v", err)
		}

		config.ImageURL = imageURL
		global.APP_LOG.Info("镜像信息准备完成", zap.String("imageURL", imageURL))
	}

	if err := prov.CreateInstance(ctx, config); err != nil {
		global.APP_LOG.Error("创建实例失败", zap.Error(err))
		return fmt.Errorf("创建实例失败: %v", err)
	}

	global.APP_LOG.Info("实例创建成功", zap.String("name", config.Name), zap.Uint("providerId", providerID))
	return nil
}

// StartInstanceByProviderID 根据Provider ID启动实例（确保使用正确的Provider）
func (s *ProviderApiService) StartInstanceByProviderID(ctx context.Context, providerID uint, instanceID string) error {
	// 使用新的GetProviderByID方法
	prov, _, err := s.GetProviderByID(providerID)
	if err != nil {
		return err
	}

	if err := CheckProviderConnection(prov); err != nil {
		return err
	}

	if err := prov.StartInstance(ctx, instanceID); err != nil {
		global.APP_LOG.Error("启动实例失败",
			zap.Uint("providerId", providerID),
			zap.String("instanceId", instanceID),
			zap.Error(err))
		return fmt.Errorf("启动实例失败: %v", err)
	}

	global.APP_LOG.Info("实例启动成功",
		zap.Uint("providerId", providerID),
		zap.String("instanceId", instanceID))
	return nil
}

// StopInstanceByProviderID 根据Provider ID停止实例（确保使用正确的Provider）
func (s *ProviderApiService) StopInstanceByProviderID(ctx context.Context, providerID uint, instanceID string) error {
	// 使用新的GetProviderByID方法
	prov, _, err := s.GetProviderByID(providerID)
	if err != nil {
		return err
	}

	if err := CheckProviderConnection(prov); err != nil {
		return err
	}

	if err := prov.StopInstance(ctx, instanceID); err != nil {
		global.APP_LOG.Error("停止实例失败",
			zap.Uint("providerId", providerID),
			zap.String("instanceId", instanceID),
			zap.Error(err))
		return fmt.Errorf("停止实例失败: %v", err)
	}

	global.APP_LOG.Info("实例停止成功",
		zap.Uint("providerId", providerID),
		zap.String("instanceId", instanceID))
	return nil
}

// RestartInstanceByProviderID 根据Provider ID重启实例（确保使用正确的Provider）
func (s *ProviderApiService) RestartInstanceByProviderID(ctx context.Context, providerID uint, instanceID string) error {
	// 使用新的GetProviderByID方法
	prov, _, err := s.GetProviderByID(providerID)
	if err != nil {
		return err
	}

	if err := CheckProviderConnection(prov); err != nil {
		return err
	}

	if err := prov.RestartInstance(ctx, instanceID); err != nil {
		global.APP_LOG.Error("重启实例失败",
			zap.Uint("providerId", providerID),
			zap.String("instanceId", instanceID),
			zap.Error(err))
		return fmt.Errorf("重启实例失败: %v", err)
	}

	global.APP_LOG.Info("实例重启成功",
		zap.Uint("providerId", providerID),
		zap.String("instanceId", instanceID))
	return nil
}

// DeleteInstanceByProviderID 根据Provider ID删除实例（确保使用正确的Provider）
// 删除操作允许访问冻结的Provider
func (s *ProviderApiService) DeleteInstanceByProviderID(ctx context.Context, providerID uint, instanceID string) error {
	// 使用GetProviderByIDForOperation方法，允许访问冻结的Provider
	prov, _, err := s.GetProviderByIDForOperation(providerID, "delete")
	if err != nil {
		return err
	}

	if err := CheckProviderConnection(prov); err != nil {
		return err
	}

	if err := prov.DeleteInstance(ctx, instanceID); err != nil {
		global.APP_LOG.Error("删除实例失败",
			zap.Uint("providerId", providerID),
			zap.String("instanceId", instanceID),
			zap.Error(err))
		return fmt.Errorf("删除实例失败: %v", err)
	}

	global.APP_LOG.Info("实例删除成功",
		zap.Uint("providerId", providerID),
		zap.String("instanceId", instanceID))
	return nil
}

// ========================================
// 以下所有使用providerType作为参数的旧方法已全部删除
// 请使用 api_by_id.go 中带有 ByID 后缀的新方法
// ========================================
