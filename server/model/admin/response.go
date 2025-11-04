package admin

import (
	"time"

	"oneclickvirt/model/provider"
	"oneclickvirt/model/system"
	"oneclickvirt/model/user"
)

type AdminDashboardResponse struct {
	Statistics struct {
		TotalUsers       int `json:"totalUsers"`
		TotalProviders   int `json:"totalProviders"`
		TotalVMs         int `json:"totalVMs"`
		TotalContainers  int `json:"totalContainers"`
		ActiveUsers      int `json:"activeUsers"`
		TotalInstances   int `json:"totalInstances"`
		RunningInstances int `json:"runningInstances"`
		ActiveProviders  int `json:"activeProviders"`
	} `json:"statistics"`
	RecentUsers     []user.User         `json:"recentUsers"`
	RecentInstances []provider.Instance `json:"recentInstances"`
	SystemStatus    struct {
		CPUUsage    float64 `json:"cpuUsage"`
		MemoryUsage float64 `json:"memoryUsage"`
		DiskUsage   float64 `json:"diskUsage"`
		Uptime      string  `json:"uptime"`
	} `json:"systemStatus"`
}

type UserManageResponse struct {
	user.User
	InstanceCount int       `json:"instanceCount"`
	LastLoginAt   time.Time `json:"lastLoginAt"`
}

type ProviderManageResponse struct {
	provider.Provider
	InstanceCount int    `json:"instanceCount"`
	HealthStatus  string `json:"healthStatus"`
	// 节点资源信息
	NodeCPUCores     int        `json:"nodeCpuCores"`
	NodeMemoryTotal  int64      `json:"nodeMemoryTotal"`
	NodeDiskTotal    int64      `json:"nodeDiskTotal"`
	ResourceSynced   bool       `json:"resourceSynced"`
	ResourceSyncedAt *time.Time `json:"resourceSyncedAt"`
	// 当前运行任务数
	RunningTasksCount int `json:"runningTasksCount"`
	// 当前使用的认证方式
	AuthMethod string `json:"authMethod"` // "password" 或 "sshKey"
	// 资源占用情况（已分配/总量）- 基于实例配置计算
	AllocatedCPUCores int   `json:"allocatedCpuCores"` // 已分配的CPU核心数（考虑limit配置）
	AllocatedMemory   int64 `json:"allocatedMemory"`   // 已分配的内存（MB）（考虑limit配置）
	AllocatedDisk     int64 `json:"allocatedDisk"`     // 已分配的磁盘（MB）（考虑limit配置）
}

type InviteCodeResponse struct {
	system.InviteCode
	CreatedByUser string `json:"createdByUser"`
}

type InstanceManageResponse struct {
	provider.Instance
	UserName     string `json:"userName"`
	ProviderName string `json:"providerName"`
	ProviderType string `json:"providerType"`
	HealthStatus string `json:"healthStatus"`
}

type SystemConfigResponse struct {
	SystemConfig
}

type AnnouncementResponse struct {
	system.Announcement
	CreatedByUser string `json:"createdByUser"`
}

type ProviderStatusResponse struct {
	ID              uint       `json:"id"`
	UUID            string     `json:"uuid"`
	Name            string     `json:"name"`
	Type            string     `json:"type"`
	Status          string     `json:"status"`
	APIStatus       string     `json:"apiStatus"`
	SSHStatus       string     `json:"sshStatus"`
	LastAPICheck    *time.Time `json:"lastApiCheck"`
	LastSSHCheck    *time.Time `json:"lastSshCheck"`
	CertPath        string     `json:"certPath"`
	KeyPath         string     `json:"keyPath"`
	CertFingerprint string     `json:"certFingerprint"`
	// 节点资源信息
	NodeCPUCores     int        `json:"nodeCpuCores"`
	NodeMemoryTotal  int64      `json:"nodeMemoryTotal"`
	NodeDiskTotal    int64      `json:"nodeDiskTotal"`
	ResourceSynced   bool       `json:"resourceSynced"`
	ResourceSyncedAt *time.Time `json:"resourceSyncedAt"`
}

// ConfigurationTaskResponse 配置任务响应
type ConfigurationTaskResponse struct {
	ID           uint       `json:"id"`
	ProviderID   uint       `json:"providerId"`
	ProviderName string     `json:"providerName"`
	ProviderType string     `json:"providerType"`
	TaskType     string     `json:"taskType"`
	Status       string     `json:"status"`
	Progress     int        `json:"progress"`
	StartedAt    *time.Time `json:"startedAt"`
	CompletedAt  *time.Time `json:"completedAt"`
	ExecutorID   uint       `json:"executorId"`
	ExecutorName string     `json:"executorName"`
	Success      bool       `json:"success"`
	ErrorMessage string     `json:"errorMessage"`
	LogSummary   string     `json:"logSummary"`
	Duration     string     `json:"duration"` // 格式化的时长，如 "2m30s"
	CreatedAt    time.Time  `json:"createdAt"`
	UpdatedAt    time.Time  `json:"updatedAt"`
}

// ConfigurationTaskListResponse 配置任务列表响应
type ConfigurationTaskListResponse struct {
	List  []ConfigurationTaskResponse `json:"list"`
	Total int64                       `json:"total"`
}

// ConfigurationTaskDetailResponse 配置任务详情响应
type ConfigurationTaskDetailResponse struct {
	ConfigurationTaskResponse
	LogOutput  string                 `json:"logOutput"`  // 完整日志
	ResultData map[string]interface{} `json:"resultData"` // 结果数据
}

// AutoConfigureResponse 自动配置响应
type AutoConfigureResponse struct {
	TaskID       uint                        `json:"taskId"`
	Status       string                      `json:"status"`
	Message      string                      `json:"message"`
	CanProceed   bool                        `json:"canProceed"`
	RunningTask  *ConfigurationTaskResponse  `json:"runningTask,omitempty"`  // 当前运行的任务
	HistoryTasks []ConfigurationTaskResponse `json:"historyTasks,omitempty"` // 历史任务
	StreamURL    string                      `json:"streamUrl,omitempty"`    // 实时流URL
}

// ResetUserPasswordResponse 重置用户密码响应
type ResetUserPasswordResponse struct {
	NewPassword string `json:"newPassword"` // 生成的新密码
}

// ResetInstancePasswordResponse 管理员重置实例密码响应
type ResetInstancePasswordResponse struct {
	TaskID uint `json:"taskId"` // 异步任务ID
}

// GetInstancePasswordResponse 获取实例新密码响应
type GetInstancePasswordResponse struct {
	NewPassword string `json:"newPassword"`
	ResetTime   int64  `json:"resetTime"`
}

// ResetPasswordTaskResult 重置密码任务结果结构
type ResetPasswordTaskResult struct {
	InstanceID  uint   `json:"instanceId"`
	ProviderID  uint   `json:"providerId"`
	NewPassword string `json:"newPassword"`
	ResetTime   int64  `json:"resetTime"`
}

// TestSSHConnectionResponse 测试SSH连接响应
type TestSSHConnectionResponse struct {
	Success            bool   `json:"success"`                // 测试是否成功
	MinLatency         int64  `json:"minLatency"`             // 最小延迟（毫秒）
	MaxLatency         int64  `json:"maxLatency"`             // 最大延迟（毫秒）
	AvgLatency         int64  `json:"avgLatency"`             // 平均延迟（毫秒）
	RecommendedTimeout int    `json:"recommendedTimeout"`     // 推荐的超时时间（秒），最大延迟*2
	TestCount          int    `json:"testCount"`              // 测试次数
	ErrorMessage       string `json:"errorMessage,omitempty"` // 错误信息（如果失败）
}
