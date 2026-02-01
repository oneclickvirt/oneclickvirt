package interfaces

import (
	"context"

	adminModel "oneclickvirt/model/admin"
	"oneclickvirt/model/auth"
	providerModel "oneclickvirt/model/provider"
	userModel "oneclickvirt/model/user"
)

// InstanceServiceInterface 实例服务接口
type InstanceServiceInterface interface {
	GetUserInstances(userID uint, req userModel.UserInstanceListRequest) ([]userModel.UserInstanceResponse, int64, error)
	InstanceAction(userID uint, req userModel.InstanceActionRequest) error
	GetInstanceDetail(userID, instanceID uint) (*userModel.UserInstanceDetailResponse, error)
	GetInstanceMonitoring(userID, instanceID uint) (*userModel.InstanceMonitoringResponse, error)
	PerformInstanceAction(userID uint, req userModel.InstanceActionRequest) error
}

// ProfileServiceInterface 用户资料服务接口
type ProfileServiceInterface interface {
	UpdateProfile(userID uint, req userModel.UpdateProfileRequest) error
	ChangePassword(userID uint, oldPassword, newPassword string) error
	BatchDeleteUsers(userIDs []uint) (map[string]interface{}, error)
	SearchUsers(req auth.SearchUsersRequest) ([]userModel.User, int64, error)
	GetUserTasks(userID uint, req userModel.UserTasksRequest) ([]userModel.UserTaskResponse, int64, error)
	CancelUserTask(userID, taskID uint) error
}

// NotificationServiceInterface 通知服务接口
type NotificationServiceInterface interface {
	ResetPassword(userID uint) (string, error)
	ResetPasswordAndNotify(userID uint) (string, error)
}

// ResourceServiceInterface 资源服务接口
type ResourceServiceInterface interface {
	GetAvailableResources(req userModel.AvailableResourcesRequest) ([]userModel.AvailableResourceResponse, int64, error)
	ClaimResource(userID uint, req userModel.ClaimResourceRequest) (*providerModel.Instance, error)
}

// ProviderServiceInterface 提供商服务接口
type ProviderServiceInterface interface {
	GetAvailableProviders(userID uint) ([]userModel.AvailableProviderResponse, error)
	GetSystemImages(userID uint, req userModel.SystemImagesRequest) ([]userModel.SystemImageResponse, error)
	GetInstanceConfig(userID uint, providerID uint) (*userModel.InstanceConfigResponse, error)
	GetFilteredSystemImages(userID uint, providerID uint, instanceType string) ([]userModel.SystemImageResponse, error)
	CreateUserInstance(userID uint, req userModel.CreateInstanceRequest) (*adminModel.Task, error)
	GetProviderCapabilities(userID uint, providerID uint) (map[string]interface{}, error)
	GetInstanceTypePermissions(userID uint) (map[string]interface{}, error)
	ProcessCreateInstanceTask(ctx context.Context, task *adminModel.Task) error
}
