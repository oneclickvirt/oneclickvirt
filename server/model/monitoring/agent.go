package monitoring

import (
	"time"

	"gorm.io/gorm"
)

// AgentMonitor tracks the mapping between an instance and its agent-side monitor ID.
// Each instance on a provider host has a corresponding monitor in the agent's local SQLite.
type AgentMonitor struct {
	ID         uint           `gorm:"primarykey" json:"id"`
	CreatedAt  time.Time      `json:"created_at"`
	UpdatedAt  time.Time      `json:"updated_at"`
	DeletedAt  gorm.DeletedAt `gorm:"index;uniqueIndex:uk_instance,priority:2" json:"-"`
	InstanceID uint           `gorm:"uniqueIndex:uk_instance,priority:1;not null" json:"instance_id"`
	ProviderID uint           `gorm:"index:idx_provider;not null" json:"provider_id"`
	UserID     uint           `gorm:"index:idx_user;not null" json:"user_id"`
	// AgentMonitorID is the ID returned by the agent's /api/v1/add endpoint.
	AgentMonitorID int64  `gorm:"not null" json:"agent_monitor_id"`
	Interfaces     string `gorm:"type:text" json:"interfaces"`
	ProviderKind   string `gorm:"size:32" json:"provider_kind"`
	InstanceName   string `gorm:"size:255" json:"instance_name"`
	// InnerIP is passed to the agent for per-IP filtering on shared bridges/NAT.
	InnerIP string `gorm:"size:64" json:"inner_ip"`
	// LastTrafficBytes is the last known total_bytes from the agent.
	LastTrafficBytes uint64 `gorm:"not null;default:0" json:"last_traffic_bytes"`
	// LastTrafficBytesIn is the last known total_bytes_in (inbound) from the agent.
	LastTrafficBytesIn uint64 `gorm:"not null;default:0" json:"last_traffic_bytes_in"`
	// LastTrafficBytesOut is the last known total_bytes_out (outbound) from the agent.
	LastTrafficBytesOut uint64 `gorm:"not null;default:0" json:"last_traffic_bytes_out"`
	// LastSyncAt tracks when traffic was last synced from the agent.
	LastSyncAt time.Time `gorm:"index" json:"last_sync_at"`
	IsEnabled  bool      `gorm:"not null;default:true" json:"is_enabled"`
}

func (AgentMonitor) TableName() string {
	return "agent_monitors"
}

// ResourceMetric stores resource usage data points synced from the agent.
// Retained for 24 hours.
type ResourceMetric struct {
	ID          uint      `gorm:"primarykey" json:"id"`
	InstanceID  uint      `gorm:"index:idx_resource_instance_ts,priority:1;not null" json:"instance_id"`
	ProviderID  uint      `gorm:"index:idx_resource_provider;not null" json:"provider_id"`
	UserID      uint      `gorm:"index:idx_resource_user;not null" json:"user_id"`
	Timestamp   time.Time `gorm:"index:idx_resource_instance_ts,priority:2;not null" json:"timestamp"`
	CPUPercent  float64   `gorm:"not null;default:0" json:"cpu_percent"`
	MemoryUsed  uint64    `gorm:"not null;default:0" json:"memory_used"`
	MemoryTotal uint64    `gorm:"not null;default:0" json:"memory_total"`
	DiskUsed    uint64    `gorm:"not null;default:0" json:"disk_used"`
	DiskTotal   uint64    `gorm:"not null;default:0" json:"disk_total"`
}

func (ResourceMetric) TableName() string {
	return "resource_metrics"
}

// MonitorSyncTask records long-running provider monitor reconciliation jobs.
// Syncing many instances can require provider-side interface detection and
// agent-side nft/iptables reconciliation, so it must not be tied to a single
// HTTP request lifecycle.
type MonitorSyncTask struct {
	ID           uint       `gorm:"primarykey" json:"id"`
	CreatedAt    time.Time  `json:"created_at"`
	UpdatedAt    time.Time  `json:"updated_at"`
	ProviderID   uint       `gorm:"index:idx_monitor_sync_provider_status,priority:1;not null" json:"provider_id"`
	TaskID       string     `gorm:"size:64;uniqueIndex;not null" json:"task_id"`
	AdminTaskID  uint       `gorm:"index" json:"admin_task_id"`
	Status       string     `gorm:"size:16;index:idx_monitor_sync_provider_status,priority:2;not null;default:pending" json:"status"`
	Total        int        `gorm:"not null;default:0" json:"total"`
	Created      int        `gorm:"not null;default:0" json:"created"`
	Updated      int        `gorm:"not null;default:0" json:"updated"`
	Unchanged    int        `gorm:"not null;default:0" json:"unchanged"`
	Failed       int        `gorm:"not null;default:0" json:"failed"`
	Cleaned      int        `gorm:"not null;default:0" json:"cleaned"`
	ErrorMessage string     `gorm:"type:text" json:"error_message,omitempty"`
	ErrorsJSON   string     `gorm:"type:text" json:"-"`
	StartedAt    *time.Time `json:"started_at,omitempty"`
	FinishedAt   *time.Time `json:"finished_at,omitempty"`
}

func (MonitorSyncTask) TableName() string {
	return "monitor_sync_tasks"
}

// MonitoringConfig stores the monitoring configuration for a provider.
type MonitoringConfig struct {
	ID         uint           `gorm:"primarykey" json:"id"`
	CreatedAt  time.Time      `json:"created_at"`
	UpdatedAt  time.Time      `json:"updated_at"`
	DeletedAt  gorm.DeletedAt `gorm:"index;uniqueIndex:uk_provider,priority:2" json:"-"`
	ProviderID uint           `gorm:"uniqueIndex:uk_provider,priority:1;not null" json:"provider_id"`
	// MonitoringMode: "agent" (default, nft-based) or "pmacct" (legacy fallback)
	MonitoringMode string `gorm:"size:16;not null;default:agent" json:"monitoring_mode"`
	// TrafficCollectMethod: "nft" (default, nftables-based) or "ipt" (iptables-based).
	// Controls which backend the agent uses for traffic counting.
	TrafficCollectMethod string `gorm:"size:8;not null;default:nft" json:"traffic_collect_method"`
	// AgentToken is the API_TOKEN for the agent on this provider host.
	AgentToken string `gorm:"size:255" json:"agent_token"`
	// AgentPort defaults to 23782.
	AgentPort int `gorm:"not null;default:23782" json:"agent_port"`
	// AgentInstalled tracks whether the agent binary has been deployed.
	AgentInstalled bool `gorm:"not null;default:false" json:"agent_installed"`
	// AgentVersion tracks the installed agent version.
	AgentVersion string `gorm:"size:32" json:"agent_version"`
	// CollectInterval in seconds (default 5, agent-side traffic collection).
	CollectInterval int `gorm:"not null;default:5" json:"collect_interval"`
	// ResourceCollectInterval in seconds (default 30, agent-side resource collection).
	ResourceCollectInterval int `gorm:"not null;default:30" json:"resource_collect_interval"`
	// ExtraExcludeCIDRsV4 comma-separated additional exclude CIDRs.
	ExtraExcludeCIDRsV4 string `gorm:"type:text" json:"extra_exclude_cidrs_v4"`
	// ExtraExcludeCIDRsV6 comma-separated additional exclude CIDRs.
	ExtraExcludeCIDRsV6 string `gorm:"type:text" json:"extra_exclude_cidrs_v6"`
}

func (MonitoringConfig) TableName() string {
	return "monitoring_configs"
}
