package admin

import "oneclickvirt/model/common"

type CreateUserRequest struct {
	Username   string `json:"username" binding:"required"`
	Password   string `json:"password" binding:"required"`
	Nickname   string `json:"nickname"`
	Email      string `json:"email"`
	Phone      string `json:"phone"`
	Telegram   string `json:"telegram"`
	QQ         string `json:"qq"`
	UserType   string `json:"userType" binding:"required"`
	Level      int    `json:"level"`
	TotalQuota int    `json:"totalQuota"`
	Status     int    `json:"status"`
	RoleID     uint   `json:"roleId"`
}

type UpdateUserRequest struct {
	ID         uint   `json:"id" binding:"required"`
	Username   string `json:"username"`
	Password   string `json:"password"`
	Nickname   string `json:"nickname"`
	Email      string `json:"email"`
	Phone      string `json:"phone"`
	Telegram   string `json:"telegram"`
	QQ         string `json:"qq"`
	UserType   string `json:"userType"`
	Level      int    `json:"level"`
	TotalQuota int    `json:"totalQuota"`
	Status     int    `json:"status"`
	RoleID     uint   `json:"roleId"`
}

type UserListRequest struct {
	common.PageInfo
	Username string `json:"username" form:"username"`
	UserType string `json:"userType" form:"userType"`
	Status   *int   `json:"status" form:"status"`
}

type CreateProviderRequest struct {
	Name                  string `json:"name" binding:"required"`
	Type                  string `json:"type" binding:"required"`
	Endpoint              string `json:"endpoint"`
	SSHPort               int    `json:"sshPort"`
	Username              string `json:"username"`
	Password              string `json:"password"`
	Token                 string `json:"token"`
	Config                string `json:"config"`
	Region                string `json:"region"`
	Country               string `json:"country"`
	CountryCode           string `json:"countryCode"`
	Architecture          string `json:"architecture"`
	ContainerEnabled      bool   `json:"container_enabled"`
	VirtualMachineEnabled bool   `json:"vm_enabled"`
	TotalQuota            int    `json:"totalQuota"`
	AllowClaim            bool   `json:"allowClaim"`
	Status                string `json:"status"`
	ExpiresAt             string `json:"expiresAt"`             // 过期时间，格式: "2006-01-02 15:04:05"
	MaxContainerInstances int    `json:"maxContainerInstances"` // 最大容器数量限制
	MaxVMInstances        int    `json:"maxVMInstances"`        // 最大虚拟机数量限制
	AllowConcurrentTasks  bool   `json:"allowConcurrentTasks"`  // 是否允许并发任务，默认false
	MaxConcurrentTasks    int    `json:"maxConcurrentTasks"`    // 最大并发任务数，默认1
	TaskPollInterval      int    `json:"taskPollInterval"`      // 任务轮询间隔（秒），默认60秒
	EnableTaskPolling     bool   `json:"enableTaskPolling"`     // 是否启用任务轮询，默认true
	// 存储配置（ProxmoxVE专用）
	StoragePool string `json:"storagePool"` // 存储池名称，用于存储虚拟机磁盘和容器
	// 操作执行配置
	ExecutionRule string `json:"executionRule" binding:"oneof=auto api_only ssh_only"` // 操作轮转规则：auto(自动切换), api_only(仅API), ssh_only(仅SSH)
	// 端口映射配置
	DefaultPortCount int    `json:"defaultPortCount"`                                                                                // 每个实例默认映射端口数量，默认10
	PortRangeStart   int    `json:"portRangeStart"`                                                                                  // 端口映射范围起始，默认10000
	PortRangeEnd     int    `json:"portRangeEnd"`                                                                                    // 端口映射范围结束，默认65535
	NetworkType      string `json:"networkType" binding:"oneof=nat_ipv4 nat_ipv4_ipv6 dedicated_ipv4 dedicated_ipv4_ipv6 ipv6_only"` // 网络配置类型：nat_ipv4, nat_ipv4_ipv6, dedicated_ipv4, dedicated_ipv4_ipv6, ipv6_only
	// 带宽配置
	DefaultInboundBandwidth  int   `json:"defaultInboundBandwidth"`  // 默认入站带宽限制（Mbps）
	DefaultOutboundBandwidth int   `json:"defaultOutboundBandwidth"` // 默认出站带宽限制（Mbps）
	MaxInboundBandwidth      int   `json:"maxInboundBandwidth"`      // 最大入站带宽限制（Mbps）
	MaxOutboundBandwidth     int   `json:"maxOutboundBandwidth"`     // 最大出站带宽限制（Mbps）
	// 流量管理
	MaxTraffic int64 `json:"maxTraffic"` // 最大流量限制（MB），默认1TB=1048576MB
}

type UpdateProviderRequest struct {
	ID                    uint   `json:"id"`
	Name                  string `json:"name"`
	Type                  string `json:"type"`
	Endpoint              string `json:"endpoint"`
	SSHPort               int    `json:"sshPort"`
	Username              string `json:"username"`
	Password              string `json:"password"`
	Token                 string `json:"token"`
	Config                string `json:"config"`
	Region                string `json:"region"`
	Country               string `json:"country"`
	CountryCode           string `json:"countryCode"`
	Architecture          string `json:"architecture"`
	ContainerEnabled      bool   `json:"container_enabled"`
	VirtualMachineEnabled bool   `json:"vm_enabled"`
	TotalQuota            int    `json:"totalQuota"`
	AllowClaim            bool   `json:"allowClaim"`
	Status                string `json:"status"`
	ExpiresAt             string `json:"expiresAt"`             // 过期时间，格式: "2006-01-02 15:04:05"
	MaxContainerInstances int    `json:"maxContainerInstances"` // 最大容器数量限制
	MaxVMInstances        int    `json:"maxVMInstances"`        // 最大虚拟机数量限制
	AllowConcurrentTasks  bool   `json:"allowConcurrentTasks"`  // 是否允许并发任务，默认false
	MaxConcurrentTasks    int    `json:"maxConcurrentTasks"`    // 最大并发任务数，默认1
	TaskPollInterval      int    `json:"taskPollInterval"`      // 任务轮询间隔（秒），默认60秒
	EnableTaskPolling     bool   `json:"enableTaskPolling"`     // 是否启用任务轮询，默认true
	// 存储配置（ProxmoxVE专用）
	StoragePool string `json:"storagePool"` // 存储池名称，用于存储虚拟机磁盘和容器
	// 操作执行配置
	ExecutionRule string `json:"executionRule" binding:"oneof=auto api_only ssh_only"` // 操作轮转规则：auto(自动切换), api_only(仅API), ssh_only(仅SSH)
	// 端口映射配置
	DefaultPortCount int    `json:"defaultPortCount"`                                                                                // 每个实例默认映射端口数量，默认10
	PortRangeStart   int    `json:"portRangeStart"`                                                                                  // 端口映射范围起始，默认10000
	PortRangeEnd     int    `json:"portRangeEnd"`                                                                                    // 端口映射范围结束，默认65535
	NetworkType      string `json:"networkType" binding:"oneof=nat_ipv4 nat_ipv4_ipv6 dedicated_ipv4 dedicated_ipv4_ipv6 ipv6_only"` // 网络配置类型：nat_ipv4, nat_ipv4_ipv6, dedicated_ipv4, dedicated_ipv4_ipv6, ipv6_only
	// 带宽配置
	DefaultInboundBandwidth  int   `json:"defaultInboundBandwidth"`  // 默认入站带宽限制（Mbps）
	DefaultOutboundBandwidth int   `json:"defaultOutboundBandwidth"` // 默认出站带宽限制（Mbps）
	MaxInboundBandwidth      int   `json:"maxInboundBandwidth"`      // 最大入站带宽限制（Mbps）
	MaxOutboundBandwidth     int   `json:"maxOutboundBandwidth"`     // 最大出站带宽限制（Mbps）
	// 流量管理
	MaxTraffic int64 `json:"maxTraffic"` // 最大流量限制（MB），默认1TB=1048576MB
}

type ProviderListRequest struct {
	common.PageInfo
	Name   string `json:"name"`
	Type   string `json:"type"`
	Status string `json:"status"`
}

type FreezeProviderRequest struct {
	ID uint `json:"id" binding:"required"`
}

type UnfreezeProviderRequest struct {
	ID        uint   `json:"id" binding:"required"`
	ExpiresAt string `json:"expiresAt"` // 新的过期时间，格式: "2006-01-02 15:04:05"
}

type CreateInviteCodeRequest struct {
	Code      string `json:"code"` // 自定义邀请码，如果为空则自动生成
	Count     int    `json:"count" binding:"required,min=1,max=100"`
	MaxUses   int    `json:"maxUses"`
	ExpiresAt string `json:"expiresAt"`
	Remark    string `json:"remark"`
	Length    int    `json:"length"` // 邀请码长度，仅在自动生成时有效
}

type InviteCodeListRequest struct {
	common.PageInfo
	Code   string `json:"code" form:"code"`
	IsUsed *bool  `json:"isUsed" form:"isUsed"` // 是否已使用：true-已使用，false-未使用
	Status int    `json:"status" form:"status"`
}

// BatchDeleteInviteCodesRequest 批量删除邀请码请求
type BatchDeleteInviteCodesRequest struct {
	IDs []uint `json:"ids" binding:"required,min=1"`
}

type CreateInstanceRequest struct {
	Name         string `json:"name" binding:"required"`
	Provider     string `json:"provider" binding:"required"`
	Image        string `json:"image" binding:"required"`
	CPU          int    `json:"cpu"`
	Memory       int64  `json:"memory"`
	Disk         int64  `json:"disk"`
	InstanceType string `json:"instance_type"`
	UserID       uint   `json:"userId"`
}

type UpdateInstanceRequest struct {
	ID     uint   `json:"id" binding:"required"`
	Name   string `json:"name"`
	CPU    int    `json:"cpu"`
	Memory int64  `json:"memory"`
	Disk   int64  `json:"disk"`
	Status string `json:"status"`
}

type InstanceListRequest struct {
	common.PageInfo
	Name         string `json:"name"`
	Provider     string `json:"provider"`
	Status       string `json:"status"`
	InstanceType string `json:"instance_type"`
	UserID       uint   `json:"userId"`
}

type InstanceActionRequest struct {
	Action string `json:"action" binding:"required"`
}

// ResetInstancePasswordRequest 管理员重置实例密码请求
type ResetInstancePasswordRequest struct {
	// 不需要传递任何参数，由后端自动生成新密码
}

type UpdateSystemConfigRequest struct {
	Key      string `json:"key" binding:"required"`
	Value    string `json:"value"`
	Type     string `json:"type"`
	Category string `json:"category"`
	Remark   string `json:"remark"`
}

// BatchUpdateSystemConfigRequest 批量更新系统配置请求
type BatchUpdateSystemConfigRequest struct {
	Scope  string                 `json:"scope"`  // 配置作用域，如"global"
	Config map[string]interface{} `json:"config"` // 嵌套配置数据
}

type SystemConfigListRequest struct {
	common.PageInfo
	Key      string `json:"key"`
	Category string `json:"category"`
}

type CreateAnnouncementRequest struct {
	Title       string `json:"title" binding:"required"`
	Content     string `json:"content" binding:"required"`
	ContentHTML string `json:"contentHtml"`
	Type        string `json:"type" binding:"required,oneof=homepage topbar"` // 限制类型
	Priority    int    `json:"priority"`
	IsSticky    bool   `json:"isSticky"`
	StartTime   string `json:"startTime"`
	EndTime     string `json:"endTime"`
}

type UpdateAnnouncementRequest struct {
	ID          uint   `json:"id"`
	Title       string `json:"title"`
	Content     string `json:"content"`
	ContentHTML string `json:"contentHtml"`
	Type        string `json:"type" binding:"omitempty,oneof=homepage topbar"`
	Priority    int    `json:"priority"`
	IsSticky    bool   `json:"isSticky"`
	StartTime   string `json:"startTime"`
	EndTime     string `json:"endTime"`
	Status      int    `json:"status"`
}

type AnnouncementListRequest struct {
	common.PageInfo
	Title  string `json:"title" form:"title"`
	Type   string `json:"type" form:"type"`
	Status int    `json:"status" form:"status"` // -1表示获取所有状态，0表示禁用，1表示启用
}

// BatchAnnouncementRequest 批量公告操作请求
type BatchAnnouncementRequest struct {
	IDs []uint `json:"ids" binding:"required"`
}

// BatchUpdateStatusRequest 批量更新状态请求
type BatchUpdateStatusRequest struct {
	IDs    []uint `json:"ids" binding:"required"`
	Status int    `json:"status" binding:"min=0,max=1"`
}

// UpdateUserStatusRequest 更新单个用户状态请求
type UpdateUserStatusRequest struct {
	Status int `json:"status" binding:"min=0,max=1"`
}

// ConfigurationTaskListRequest 配置任务列表请求
type ConfigurationTaskListRequest struct {
	common.PageInfo
	ProviderID uint   `json:"providerId" form:"providerId"`
	TaskType   string `json:"taskType" form:"taskType"`
	Status     string `json:"status" form:"status"`
	ExecutorID uint   `json:"executorId" form:"executorId"`
}

// AutoConfigureRequest 自动配置请求
type AutoConfigureRequest struct {
	ProviderID  uint `json:"providerId" binding:"required"`
	Force       bool `json:"force"`       // 是否强制执行（即使有正在运行的任务）
	ShowHistory bool `json:"showHistory"` // 是否显示历史记录
}

// BatchDeleteUsersRequest 批量删除用户请求
type BatchDeleteUsersRequest struct {
	UserIDs []uint `json:"userIds" binding:"required"`
}

// BatchUpdateUserStatusRequest 批量更新用户状态请求
type BatchUpdateUserStatusRequest struct {
	UserIDs []uint `json:"userIds" binding:"required"`
	Status  int    `json:"status" binding:"min=0,max=1"`
}

// BatchUpdateUserLevelRequest 批量更新用户等级请求
type BatchUpdateUserLevelRequest struct {
	UserIDs []uint `json:"userIds" binding:"required"`
	Level   int    `json:"level" binding:"min=1,max=5"`
}

// UpdateUserLevelRequest 更新单个用户等级请求
type UpdateUserLevelRequest struct {
	Level int `json:"level" binding:"min=1,max=5"`
}

// ResetUserPasswordRequest 管理员强制重置用户密码请求
type ResetUserPasswordRequest struct {
	// 不再需要前端传递密码，由后端自动生成
}

// UpdateInstanceTypePermissionsRequest 更新实例类型权限配置请求
type UpdateInstanceTypePermissionsRequest struct {
	MinLevelForContainer int `json:"minLevelForContainer" binding:"min=1,max=5"`
	MinLevelForVM        int `json:"minLevelForVM" binding:"min=1,max=5"`
	MinLevelForDelete    int `json:"minLevelForDelete" binding:"min=1,max=5"`
}

// 端口映射管理相关请求

// PortMappingListRequest 端口映射列表请求
type PortMappingListRequest struct {
	common.PageInfo
	ProviderID uint   `json:"providerId" form:"providerId"`
	InstanceID uint   `json:"instanceId" form:"instanceId"`
	Protocol   string `json:"protocol" form:"protocol"`
	Status     string `json:"status" form:"status"`
}

// CreatePortMappingRequest 创建端口映射请求
type CreatePortMappingRequest struct {
	InstanceID  uint   `json:"instanceId" binding:"required"`
	GuestPort   int    `json:"guestPort" binding:"required,min=1,max=65535"`
	Protocol    string `json:"protocol" binding:"required,oneof=tcp udp"`
	Description string `json:"description"`
	HostPort    int    `json:"hostPort"` // 可选，不指定则自动分配
}

// UpdatePortMappingRequest 更新端口映射请求
type UpdatePortMappingRequest struct {
	ID          uint   `json:"id" binding:"required"`
	GuestPort   int    `json:"guestPort" binding:"required,min=1,max=65535"`
	Protocol    string `json:"protocol" binding:"required,oneof=tcp udp"`
	Description string `json:"description"`
	Status      string `json:"status" binding:"required,oneof=active inactive"`
}

// BatchDeletePortMappingRequest 批量删除端口映射请求
type BatchDeletePortMappingRequest struct {
	IDs []uint `json:"ids" binding:"required"`
}

// ProviderPortConfigRequest Provider端口配置请求
type ProviderPortConfigRequest struct {
	DefaultPortCount int    `json:"defaultPortCount" binding:"min=1,max=50"`                                                         // 每个实例默认映射端口数量
	PortRangeStart   int    `json:"portRangeStart" binding:"min=1024,max=65535"`                                                     // 端口映射范围起始
	PortRangeEnd     int    `json:"portRangeEnd" binding:"min=1024,max=65535"`                                                       // 端口映射范围结束
	NetworkType      string `json:"networkType" binding:"oneof=nat_ipv4 nat_ipv4_ipv6 dedicated_ipv4 dedicated_ipv4_ipv6 ipv6_only"` // 网络配置类型
}

// CreateInstanceTaskRequest 创建实例任务数据结构
type CreateInstanceTaskRequest struct {
	ProviderId  uint   `json:"providerId"`
	ImageId     uint   `json:"imageId"`
	CPUId       string `json:"cpuId"`
	MemoryId    string `json:"memoryId"`
	DiskId      string `json:"diskId"`
	BandwidthId string `json:"bandwidthId"`
	Description string `json:"description"`
	SessionId   string `json:"sessionId"` // 新增：会话ID，用于新的资源预留机制
}

// InstanceOperationTaskRequest 实例操作任务数据结构（启动、停止、重启、重置）
type InstanceOperationTaskRequest struct {
	InstanceId uint `json:"instanceId"`
	ProviderId uint `json:"providerId"`
}

// DeleteInstanceTaskRequest 删除实例任务数据结构
type DeleteInstanceTaskRequest struct {
	InstanceId     uint `json:"instanceId"`
	ProviderId     uint `json:"providerId"`
	AdminOperation bool `json:"adminOperation,omitempty"` // 是否为管理员操作
}

// ResetPasswordTaskRequest 重置密码任务数据结构
type ResetPasswordTaskRequest struct {
	InstanceId uint `json:"instanceId"`
	ProviderId uint `json:"providerId"`
}
