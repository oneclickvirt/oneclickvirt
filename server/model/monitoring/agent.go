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
	DeletedAt  gorm.DeletedAt `gorm:"index" json:"-"`
	InstanceID uint           `gorm:"uniqueIndex:uk_instance;not null" json:"instance_id"`
	ProviderID uint           `gorm:"index:idx_provider;not null" json:"provider_id"`
	UserID     uint           `gorm:"index:idx_user;not null" json:"user_id"`
	// AgentMonitorID is the ID returned by the agent's /api/v1/add endpoint.
	AgentMonitorID int64  `gorm:"not null" json:"agent_monitor_id"`
	Interfaces     string `gorm:"type:text" json:"interfaces"`
	ProviderKind   string `gorm:"size:32" json:"provider_kind"`
	InstanceName   string `gorm:"size:255" json:"instance_name"`
	// LastTrafficBytes is the last known total_bytes from the agent.
	LastTrafficBytes uint64 `gorm:"not null;default:0" json:"last_traffic_bytes"`
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

// MonitoringConfig stores the monitoring configuration for a provider.
type MonitoringConfig struct {
	ID         uint           `gorm:"primarykey" json:"id"`
	CreatedAt  time.Time      `json:"created_at"`
	UpdatedAt  time.Time      `json:"updated_at"`
	DeletedAt  gorm.DeletedAt `gorm:"index" json:"-"`
	ProviderID uint           `gorm:"uniqueIndex:uk_provider;not null" json:"provider_id"`
	// MonitoringMode: "agent" (default, nft-based) or "pmacct" (legacy fallback)
	MonitoringMode string `gorm:"size:16;not null;default:agent" json:"monitoring_mode"`
	// AgentToken is the API_TOKEN for the agent on this provider host.
	AgentToken string `gorm:"size:255" json:"agent_token"`
	// AgentPort defaults to 23782.
	AgentPort int `gorm:"not null;default:23782" json:"agent_port"`
	// AgentInstalled tracks whether the agent binary has been deployed.
	AgentInstalled bool `gorm:"not null;default:false" json:"agent_installed"`
	// AgentVersion tracks the installed agent version.
	AgentVersion string `gorm:"size:32" json:"agent_version"`
	// CollectInterval in seconds (default 60).
	CollectInterval int `gorm:"not null;default:60" json:"collect_interval"`
	// ResourceCollectInterval in seconds (default 300 = 5 min).
	ResourceCollectInterval int `gorm:"not null;default:300" json:"resource_collect_interval"`
	// ExtraExcludeCIDRsV4 comma-separated additional exclude CIDRs.
	ExtraExcludeCIDRsV4 string `gorm:"type:text" json:"extra_exclude_cidrs_v4"`
	// ExtraExcludeCIDRsV6 comma-separated additional exclude CIDRs.
	ExtraExcludeCIDRsV6 string `gorm:"type:text" json:"extra_exclude_cidrs_v6"`
}

func (MonitoringConfig) TableName() string {
	return "monitoring_configs"
}
