package user

import (
	"context"
	"oneclickvirt/service/resources"
	"oneclickvirt/service/user/instance"
	"oneclickvirt/service/user/notification"
	"oneclickvirt/service/user/profile"
	"oneclickvirt/service/user/provider"
	"oneclickvirt/service/user/resource"

	adminModel "oneclickvirt/model/admin"
	"oneclickvirt/model/auth"
	providerModel "oneclickvirt/model/provider"
	userModel "oneclickvirt/model/user"
)

// Service 用户服务聚合层，维持向后兼容性
type Service struct {
	dashboard    resources.UserDashboardService
	instance     *instance.Service
	profile      *profile.Service
	notification *notification.Service
	resource     *resource.Service
	provider     *provider.Service
}

// NewService 创建用户服务实例
func NewService() *Service {
	return &Service{
		dashboard:    resources.UserDashboardService{},
		instance:     instance.NewService(),
		profile:      profile.NewService(),
		notification: notification.NewService(),
		resource:     resource.NewService(),
		provider:     provider.NewService(),
	}
}

// ===== 仪表板相关方法 =====

// GetUserDashboard 获取用户仪表板数据
func (s *Service) GetUserDashboard(userID uint) (*userModel.UserDashboardResponse, error) {
	return s.dashboard.GetUserDashboard(userID)
}

// GetUserLimits 获取用户资源限制
func (s *Service) GetUserLimits(userID uint) (*userModel.UserLimitsResponse, error) {
	return s.dashboard.GetUserLimits(userID)
}

// ===== 实例管理相关方法 =====

// GetUserInstances 获取用户实例列表
func (s *Service) GetUserInstances(userID uint, req userModel.UserInstanceListRequest) ([]userModel.UserInstanceResponse, int64, error) {
	return s.instance.GetUserInstances(userID, req)
}

// InstanceAction 执行实例操作
func (s *Service) InstanceAction(userID uint, req userModel.InstanceActionRequest) error {
	return s.instance.InstanceAction(userID, req)
}

// GetInstanceDetail 获取实例详情
func (s *Service) GetInstanceDetail(userID, instanceID uint) (*userModel.UserInstanceDetailResponse, error) {
	return s.instance.GetInstanceDetail(userID, instanceID)
}

// GetInstanceMonitoring 获取实例监控数据
func (s *Service) GetInstanceMonitoring(userID, instanceID uint) (*userModel.InstanceMonitoringResponse, error) {
	return s.instance.GetInstanceMonitoring(userID, instanceID)
}

// PerformInstanceAction 执行实例操作（兼容原方法名）
func (s *Service) PerformInstanceAction(userID uint, req userModel.InstanceActionRequest) error {
	return s.instance.PerformInstanceAction(userID, req)
}

// ===== 用户资料管理相关方法 =====

// UpdateProfile 更新用户资料
func (s *Service) UpdateProfile(userID uint, req userModel.UpdateProfileRequest) error {
	return s.profile.UpdateProfile(userID, req)
}

// ChangePassword 修改密码
func (s *Service) ChangePassword(userID uint, oldPassword, newPassword string) error {
	return s.profile.ChangePassword(userID, oldPassword, newPassword)
}

// BatchDeleteUsers 批量删除用户
func (s *Service) BatchDeleteUsers(userIDs []uint) (map[string]interface{}, error) {
	return s.profile.BatchDeleteUsers(userIDs)
}

// SearchUsers 搜索用户
func (s *Service) SearchUsers(req auth.SearchUsersRequest) ([]userModel.User, int64, error) {
	return s.profile.SearchUsers(req)
}

// GetUserTasks 获取用户任务列表
func (s *Service) GetUserTasks(userID uint, req userModel.UserTasksRequest) ([]userModel.UserTaskResponse, int64, error) {
	return s.profile.GetUserTasks(userID, req)
}

// CancelUserTask 取消用户任务
func (s *Service) CancelUserTask(userID, taskID uint) error {
	return s.profile.CancelUserTask(userID, taskID)
}

// ===== 密码重置和通知相关方法 =====

// ResetPassword 用户重置自己的密码
func (s *Service) ResetPassword(userID uint) (string, error) {
	return s.notification.ResetPassword(userID)
}

// ResetPasswordAndNotify 用户重置自己的密码并通过通信渠道发送
func (s *Service) ResetPasswordAndNotify(userID uint) (string, error) {
	return s.notification.ResetPasswordAndNotify(userID)
}

// ===== 资源管理相关方法 =====

// GetAvailableResources 获取可用资源列表
func (s *Service) GetAvailableResources(req userModel.AvailableResourcesRequest) ([]userModel.AvailableResourceResponse, int64, error) {
	return s.resource.GetAvailableResources(req)
}

// ClaimResource 申领资源
func (s *Service) ClaimResource(userID uint, req userModel.ClaimResourceRequest) (*providerModel.Instance, error) {
	return s.resource.ClaimResource(userID, req)
}

// ===== 提供商和配置相关方法 =====

// GetAvailableProviders 获取可用节点列表
func (s *Service) GetAvailableProviders(userID uint) ([]userModel.AvailableProviderResponse, error) {
	return s.provider.GetAvailableProviders(userID)
}

// GetSystemImages 获取系统镜像列表
func (s *Service) GetSystemImages(userID uint, req userModel.SystemImagesRequest) ([]userModel.SystemImageResponse, error) {
	return s.provider.GetSystemImages(userID, req)
}

// GetInstanceConfig 获取实例配置选项
func (s *Service) GetInstanceConfig(userID uint, providerID uint) (*userModel.InstanceConfigResponse, error) {
	return s.provider.GetInstanceConfig(userID, providerID)
}

// GetFilteredSystemImages 根据Provider和实例类型获取过滤后的系统镜像列表
func (s *Service) GetFilteredSystemImages(userID uint, providerID uint, instanceType string) ([]userModel.SystemImageResponse, error) {
	return s.provider.GetFilteredSystemImages(userID, providerID, instanceType)
}

// CreateUserInstance 创建用户实例
func (s *Service) CreateUserInstance(userID uint, req userModel.CreateInstanceRequest) (*adminModel.Task, error) {
	return s.provider.CreateUserInstance(userID, req)
}

// GetProviderCapabilities 获取Provider能力
func (s *Service) GetProviderCapabilities(userID uint, providerID uint) (map[string]interface{}, error) {
	return s.provider.GetProviderCapabilities(userID, providerID)
}

// GetInstanceTypePermissions 获取实例类型权限
func (s *Service) GetInstanceTypePermissions(userID uint) (map[string]interface{}, error) {
	return s.provider.GetInstanceTypePermissions(userID)
}

// ===== 实例创建处理相关方法 =====

// ProcessCreateInstanceTask 处理创建实例的后台任务
func (s *Service) ProcessCreateInstanceTask(ctx context.Context, task *adminModel.Task) error {
	return s.provider.ProcessCreateInstanceTask(ctx, task)
}

// HasInstanceAccess 检查用户是否有权限访问实例
func (s *Service) HasInstanceAccess(userID, instanceID uint) bool {
	return s.instance.HasInstanceAccess(userID, instanceID)
}

// ResetInstancePassword 重置实例密码
func (s *Service) ResetInstancePassword(userID uint, instanceID uint) (uint, error) {
	return s.instance.ResetInstancePassword(userID, instanceID)
}

// GetInstanceNewPassword 获取实例新密码
func (s *Service) GetInstanceNewPassword(userID uint, instanceID uint, taskID uint) (string, int64, error) {
	return s.instance.GetInstanceNewPassword(userID, instanceID, taskID)
}
