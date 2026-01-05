package user

import (
	"time"

	providerModel "oneclickvirt/model/provider"
)

type UserDashboardResponse struct {
	User       User `json:"user"`
	UsedQuota  int  `json:"usedQuota"`
	TotalQuota int  `json:"totalQuota"`
	Instances  struct {
		Total      int `json:"total"`
		Running    int `json:"running"`
		Stopped    int `json:"stopped"`
		Containers int `json:"containers"`
		VMs        int `json:"vms"`
	} `json:"instances"`
	RecentInstances []providerModel.Instance `json:"recentInstances"`
	ResourceUsage   *ResourceUsageInfo       `json:"resourceUsage,omitempty"`
}

type ResourceUsageInfo struct {
	CPU              int   `json:"cpu"`              // 当前使用的CPU核心数
	Memory           int64 `json:"memory"`           // 当前使用的内存(MB)
	Disk             int64 `json:"disk"`             // 当前使用的磁盘(MB)
	MaxInstances     int   `json:"maxInstances"`     // 最大实例数量
	CurrentInstances int   `json:"currentInstances"` // 当前实例数量
	MaxCPU           int   `json:"maxCpu"`           // 最大CPU核心数
	MaxMemory        int64 `json:"maxMemory"`        // 最大内存(MB)
	MaxDisk          int64 `json:"maxDisk"`          // 最大磁盘(MB)
}

type AvailableResourceResponse struct {
	ID                    uint   `json:"id"`
	Name                  string `json:"name"`
	Type                  string `json:"type"`
	Region                string `json:"region"`
	Country               string `json:"country"`
	CountryCode           string `json:"countryCode"`
	City                  string `json:"city"`
	ContainerEnabled      bool   `json:"containerEnabled"`
	VirtualMachineEnabled bool   `json:"vmEnabled"`
	AvailableQuota        int    `json:"availableQuota"`
	Status                string `json:"status"`
}

type UserInstanceResponse struct {
	providerModel.Instance
	CanStart       bool                     `json:"canStart"`
	CanStop        bool                     `json:"canStop"`
	CanRestart     bool                     `json:"canRestart"`
	CanDelete      bool                     `json:"canDelete"`
	PortMappings   []map[string]interface{} `json:"portMappings"`   // 端口映射列表
	PublicIP       string                   `json:"publicIP"`       // 纯净的公网IP（不含端口）
	ProviderType   string                   `json:"providerType"`   // Provider虚拟化类型：docker, lxd, incus, proxmox
	ProviderStatus string                   `json:"providerStatus"` // Provider状态：active, inactive, partial
}

// UserLimitsResponse 用户配额限制响应
type UserLimitsResponse struct {
	Level          int   `json:"level"`
	MaxInstances   int   `json:"maxInstances"`
	UsedInstances  int   `json:"usedInstances"`
	ContainerCount int   `json:"containerCount"` // 容器数量
	VMCount        int   `json:"vmCount"`        // 虚拟机数量
	MaxCpu         int   `json:"maxCpu"`         // 最大CPU核心数
	UsedCpu        int   `json:"usedCpu"`        // 已使用CPU核心数
	MaxMemory      int   `json:"maxMemory"`      // 最大内存(MB)
	UsedMemory     int   `json:"usedMemory"`     // 已使用内存(MB)
	MaxDisk        int   `json:"maxDisk"`        // 最大磁盘(MB)
	UsedDisk       int   `json:"usedDisk"`       // 已使用磁盘(MB)
	MaxBandwidth   int   `json:"maxBandwidth"`   // 最大带宽(Mbps)
	UsedBandwidth  int   `json:"usedBandwidth"`  // 已使用带宽(Mbps)
	MaxTraffic     int64 `json:"maxTraffic"`     // 最大流量(MB)
	UsedTraffic    int64 `json:"usedTraffic"`    // 已使用流量(MB)
}

// UserTaskResponse 用户任务响应
type UserTaskResponse struct {
	ID               uint       `json:"id"`
	UUID             string     `json:"uuid"`
	TaskType         string     `json:"taskType"`
	Status           string     `json:"status"`
	Progress         int        `json:"progress"`
	ErrorMessage     string     `json:"errorMessage"`
	CancelReason     string     `json:"cancelReason"` // 取消原因
	CreatedAt        time.Time  `json:"createdAt"`
	UpdatedAt        time.Time  `json:"updatedAt"`
	StartedAt        *time.Time `json:"startedAt"`
	CompletedAt      *time.Time `json:"completedAt"`
	ProviderId       uint       `json:"providerId"`
	ProviderName     string     `json:"providerName"`
	InstanceID       *uint      `json:"instanceId"`
	InstanceName     string     `json:"instanceName"`
	InstanceType     string     `json:"instanceType"`     // 实例类型：container 或 vm
	TimeoutDuration  int        `json:"timeoutDuration"`  // 超时时间（秒）
	RemainingTime    int        `json:"remainingTime"`    // 剩余时间（秒）
	StatusMessage    string     `json:"statusMessage"`    // 状态描述
	CanCancel        bool       `json:"canCancel"`        // 是否可以取消
	IsForceStoppable bool       `json:"isForceStoppable"` // 是否允许强制停止
	// 排队信息
	QueuePosition     int `json:"queuePosition"`     // 排队位置（0表示正在执行，>0表示前面有n个任务）
	EstimatedWaitTime int `json:"estimatedWaitTime"` // 预计等待时间（秒）
	// 预分配的实例配置信息
	PreallocatedCPU       int `json:"preallocatedCpu"`       // 预分配的CPU核心数
	PreallocatedMemory    int `json:"preallocatedMemory"`    // 预分配的内存(MB)
	PreallocatedDisk      int `json:"preallocatedDisk"`      // 预分配的磁盘(MB)
	PreallocatedBandwidth int `json:"preallocatedBandwidth"` // 预分配的带宽(Mbps)
}

// TaskResponse 通用任务响应（向后兼容）
type TaskResponse = UserTaskResponse

// UserInstanceDetailResponse 用户实例详情响应
type UserInstanceDetailResponse struct {
	ID              uint       `json:"id"`
	Name            string     `json:"name"`
	Type            string     `json:"type"`
	Status          string     `json:"status"`
	CPU             int        `json:"cpu"`
	Memory          int        `json:"memory"`
	Disk            int        `json:"disk"`
	Bandwidth       int        `json:"bandwidth"`
	OsType          string     `json:"osType"`
	PrivateIP       string     `json:"privateIP"`   // 内网IPv4地址
	PublicIP        string     `json:"publicIP"`    // 公网IPv4地址
	IPv6Address     string     `json:"ipv6Address"` // 内网IPv6地址
	PublicIPv6      string     `json:"publicIPv6"`  // 公网IPv6地址
	SSHPort         int        `json:"sshPort"`
	Username        string     `json:"username"`
	Password        string     `json:"password"`
	ProviderName    string     `json:"providerName"`
	ProviderType    string     `json:"providerType"`    // Provider虚拟化类型：docker, lxd, incus, proxmox
	ProviderStatus  string     `json:"providerStatus"`  // Provider状态：active, inactive, partial
	PortRangeStart  int        `json:"portRangeStart"`  // 端口范围起始
	PortRangeEnd    int        `json:"portRangeEnd"`    // 端口范围结束
	IPv4MappingType string     `json:"ipv4MappingType"` // IPv4映射类型：nat(NAT共享IP), dedicated(独立IPv4地址) (已弃用，保留向后兼容)
	NetworkType     string     `json:"networkType"`     // 网络配置类型：nat_ipv4, nat_ipv4_ipv6, dedicated_ipv4, dedicated_ipv4_ipv6, ipv6_only
	CreatedAt       time.Time  `json:"createdAt"`
	ExpiresAt       *time.Time `json:"expiresAt"` // 实例过期时间
	// 关联任务信息
	RelatedTask *UserTaskResponse `json:"relatedTask,omitempty"` // 关联的最新任务（如果有）
}

// InstanceMonitoringResponse 实例监控数据响应
type InstanceMonitoringResponse struct {
	// CPUUsage    float64     `json:"cpuUsage"`    // 已移除：硬件资源使用率监控
	// MemoryUsage float64     `json:"memoryUsage"` // 已移除：硬件资源使用率监控
	// DiskUsage   float64     `json:"diskUsage"`   // 已移除：硬件资源使用率监控
	TrafficData TrafficData `json:"trafficData"` // 流量详细数据（基于pmacct）
}

// TrafficData 流量数据结构
type TrafficData struct {
	CurrentMonth int64                `json:"currentMonth"` // 当月已使用流量（MB）
	TotalLimit   int64                `json:"totalLimit"`   // 流量限制（MB）
	UsagePercent float64              `json:"usagePercent"` // 使用百分比
	IsLimited    bool                 `json:"isLimited"`    // 是否因流量超限被限制
	LimitType    string               `json:"limitType"`    // 流量限制类型: user, provider, both, unknown
	LimitReason  string               `json:"limitReason"`  // 流量限制原因描述
	History      []TrafficHistoryItem `json:"history"`      // 历史流量数据
}

// TrafficHistoryItem 流量历史项
type TrafficHistoryItem struct {
	Year       int        `json:"year"`       // 年份
	Month      int        `json:"month"`      // 月份
	TrafficIn  int64      `json:"trafficIn"`  // 入站流量（MB）
	TrafficOut int64      `json:"trafficOut"` // 出站流量（MB）
	TotalUsed  int64      `json:"totalUsed"`  // 总使用流量（MB）
	LastSync   *time.Time `json:"lastSync"`   // 最后同步时间
}

// ResetPasswordResponse 用户重置密码响应
type ResetPasswordResponse struct {
	NewPassword string `json:"newPassword"` // 生成的新密码
}

// AvailableProviderResponse 可用服务器响应
type AvailableProviderResponse struct {
	ID                      uint    `json:"id"`
	Name                    string  `json:"name"`
	Type                    string  `json:"type"`
	Region                  string  `json:"region"`
	Country                 string  `json:"country"`
	CountryCode             string  `json:"countryCode"`
	City                    string  `json:"city"`
	Status                  string  `json:"status"`
	CPU                     int     `json:"cpu"`
	Memory                  int     `json:"memory"`                  // 总内存(MB)
	Disk                    int     `json:"disk"`                    // 总硬盘空间(MB)
	AvailableContainerSlots int     `json:"availableContainerSlots"` // 可用容器槽位数，-1表示不限制
	AvailableVMSlots        int     `json:"availableVMSlots"`        // 可用虚拟机槽位数，-1表示不限制
	MaxContainerInstances   int     `json:"maxContainerInstances"`   // 最大容器数量，0表示不限制
	MaxVMInstances          int     `json:"maxVMInstances"`          // 最大虚拟机数量，0表示不限制
	CPUUsage                float64 `json:"cpuUsage"`
	MemoryUsage             float64 `json:"memoryUsage"`
	ContainerEnabled        bool    `json:"containerEnabled"`
	VmEnabled               bool    `json:"vmEnabled"`
}

// SystemImageResponse 系统镜像响应
type SystemImageResponse struct {
	ID           uint   `json:"id"`
	Name         string `json:"name"`
	DisplayName  string `json:"displayName"`
	Version      string `json:"version"`
	Architecture string `json:"architecture"`
	OsType       string `json:"osType"`
	ProviderType string `json:"providerType"`
	InstanceType string `json:"instanceType"`
	ImageURL     string `json:"imageUrl"`
	Description  string `json:"description"`
	IsActive     bool   `json:"isActive"`
	MinMemoryMB  int    `json:"minMemoryMB"`
	MinDiskMB    int    `json:"minDiskMB"`
	UseCDN       bool   `json:"useCdn"`
}

// InstanceConfigResponse 实例配置响应
type InstanceConfigResponse struct {
	Images         []SystemImageResponse   `json:"images"`         // 可用镜像列表（从数据库获取）
	CPUSpecs       []CPUSpecResponse       `json:"cpuSpecs"`       // 可用CPU规格列表
	MemorySpecs    []MemorySpecResponse    `json:"memorySpecs"`    // 可用内存规格列表
	DiskSpecs      []DiskSpecResponse      `json:"diskSpecs"`      // 可用磁盘规格列表
	BandwidthSpecs []BandwidthSpecResponse `json:"bandwidthSpecs"` // 可用带宽规格列表
}

// 规格响应结构
type CPUSpecResponse struct {
	ID    string `json:"id"`
	Cores int    `json:"cores"`
	Name  string `json:"name"`
}

type MemorySpecResponse struct {
	ID     string `json:"id"`
	SizeMB int    `json:"sizeMB"`
	Name   string `json:"name"`
}

type DiskSpecResponse struct {
	ID     string `json:"id"`
	SizeMB int    `json:"sizeMB"`
	Name   string `json:"name"`
}

type BandwidthSpecResponse struct {
	ID        string `json:"id"`
	SpeedMbps int    `json:"speedMbps"`
	Name      string `json:"name"`
}

// ResourceReservationResponse 资源预留响应
type ResourceReservationResponse struct {
	ID           uint      `json:"id"`
	TaskID       uint      `json:"taskId"`
	ProviderName string    `json:"providerName"`
	InstanceType string    `json:"instanceType"`
	CPU          int       `json:"cpu"`
	Memory       int64     `json:"memory"`
	Disk         int64     `json:"disk"`
	Bandwidth    int       `json:"bandwidth"`
	Status       string    `json:"status"`
	ExpiresAt    time.Time `json:"expiresAt"`
	CreatedAt    time.Time `json:"createdAt"`
}

// ResetInstancePasswordResponse 用户重置实例密码响应
type ResetInstancePasswordResponse struct {
	TaskID uint `json:"taskId"`
}

// GetInstancePasswordResponse 获取实例新密码响应
type GetInstancePasswordResponse struct {
	NewPassword string `json:"newPassword"`
	ResetTime   int64  `json:"resetTime"`
}
