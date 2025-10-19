package user

import "oneclickvirt/model/common"

type ClaimResourceRequest struct {
	ProviderID   uint   `json:"providerId" binding:"required"`
	InstanceType string `json:"instanceType" binding:"required"`
	Name         string `json:"name" binding:"required"`
	Image        string `json:"image" binding:"required"`
	CPU          int    `json:"cpu"`
	Memory       int64  `json:"memory"`
	Disk         int64  `json:"disk"`
}

type InstanceActionRequest struct {
	InstanceID uint   `json:"instanceId" binding:"required"`
	Action     string `json:"action" binding:"required"`
}

type UserInstanceListRequest struct {
	common.PageInfo
	Name         string `json:"name"`
	Status       string `json:"status"`
	InstanceType string `json:"instanceType"`
	Type         string `json:"type"`         // 实例类型筛选（和instanceType一样，兼容前端）
	ProviderName string `json:"providerName"` // 节点名称搜索
}

type AvailableResourcesRequest struct {
	common.PageInfo
	Country      string `json:"country"`
	InstanceType string `json:"instanceType"`
}

type UpdateProfileRequest struct {
	Nickname string `json:"nickname"`
	Email    string `json:"email"`
	Phone    string `json:"phone"`
	Telegram string `json:"telegram"`
}

type ChangePasswordRequest struct {
	OldPassword string `json:"oldPassword" binding:"required"`
	NewPassword string `json:"newPassword" binding:"required"`
}

// ResetPasswordRequest 用户重置自己密码请求
type ResetPasswordRequest struct {
	// 不需要传递任何参数，由后端自动生成新密码
}

// ResetInstancePasswordRequest 用户重置实例密码请求
type ResetInstancePasswordRequest struct {
	// 不需要传递任何参数，由后端自动生成新密码
}

// UserTasksRequest 用户任务列表请求
type UserTasksRequest struct {
	common.PageInfo
	ProviderId uint   `json:"providerId"`
	TaskType   string `json:"taskType"`
	Status     string `json:"status"`
}

// SystemImagesRequest 获取系统镜像请求
type SystemImagesRequest struct {
	ProviderType string `json:"providerType"`
	Architecture string `json:"architecture"`
	OsType       string `json:"osType"`
	InstanceType string `json:"instanceType"`
}

// CreateInstanceRequest 创建实例请求
// 安全设计：所有参数都是从后端预定义配置中选择的ID，不允许自定义输入
// 实例名称由后端根据provider名称自动生成
type CreateInstanceRequest struct {
	ProviderId  uint   `json:"providerId" binding:"required"`  // 节点ID
	ImageId     uint   `json:"imageId" binding:"required"`     // 镜像ID（从数据库获取）
	CPUId       string `json:"cpuId" binding:"required"`       // CPU规格ID
	MemoryId    string `json:"memoryId" binding:"required"`    // 内存规格ID
	DiskId      string `json:"diskId" binding:"required"`      // 磁盘规格ID
	BandwidthId string `json:"bandwidthId" binding:"required"` // 带宽规格ID
	Description string `json:"description"`                    // 描述信息
}

// QuotaCheckRequest 配额检查请求
type QuotaCheckRequest struct {
	UserID       uint   `json:"userId"`
	InstanceType string `json:"instanceType"`
	CPU          int    `json:"cpu"`
	Memory       int    `json:"memory"`
	Disk         int    `json:"disk"`
	Bandwidth    int    `json:"bandwidth"`
}
