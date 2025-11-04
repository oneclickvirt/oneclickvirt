package dashboard

type DashboardStats struct {
	RegionStats   []RegionStat       `json:"regionStats"`
	QuotaStats    QuotaStat          `json:"quotaStats"`
	UserStats     UserStat           `json:"userStats"`
	ResourceUsage ResourceUsageStats `json:"resourceUsage"` // 资源使用统计
}

type RegionStat struct {
	Region string `json:"region"`
	Count  int    `json:"count"`
	Used   int    `json:"used"`
	Total  int    `json:"total"`
}

type QuotaStat struct {
	Used      int `json:"used"`
	Available int `json:"available"`
	Total     int `json:"total"`
}

type UserStat struct {
	TotalUsers  int `json:"totalUsers"`
	ActiveUsers int `json:"activeUsers"`
	AdminUsers  int `json:"adminUsers"`
}

// TaskStatusCount 任务状态统计
type TaskStatusCount struct {
	Status string `json:"status"`
	Count  int64  `json:"count"`
}

// ResourceUsageStats 资源使用统计
type ResourceUsageStats struct {
	VMCount        int64 `json:"vm_count"`
	ContainerCount int64 `json:"container_count"`
	UsedCPUCores   int64 `json:"used_cpu_cores"`
	UsedMemory     int64 `json:"used_memory"`
	UsedDisk       int64 `json:"used_disk"`
}

// InstanceSummary 实例简要信息
type InstanceSummary struct {
	ID     uint   `json:"id"`
	Name   string `json:"name"`
	Status string `json:"status"`
}

// LimitedInstanceSummary 受限实例简要信息
type LimitedInstanceSummary struct {
	ID             uint   `json:"id"`
	Name           string `json:"name"`
	Status         string `json:"status"`
	TrafficLimited bool   `json:"traffic_limited"`
	ProviderID     uint   `json:"provider_id"`
}

// TrafficStats 流量统计
type TrafficStats struct {
	TotalRx    int64 `json:"total_rx"`
	TotalTx    int64 `json:"total_tx"`
	TotalBytes int64 `json:"total_bytes"`
}

// UserCountStats 用户数量统计
type UserCountStats struct {
	TotalUsers   int64 `json:"total_users"`
	LimitedUsers int64 `json:"limited_users"`
}

// ProviderCountStats Provider数量统计
type ProviderCountStats struct {
	TotalProviders   int64 `json:"total_providers"`
	LimitedProviders int64 `json:"limited_providers"`
}
