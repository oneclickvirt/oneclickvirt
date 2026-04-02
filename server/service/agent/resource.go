package agent

import (
	"context"
	"fmt"
	"time"

	"oneclickvirt/global"
	monitoringModel "oneclickvirt/model/monitoring"

	"go.uber.org/zap"
	"gorm.io/gorm"
)

// ResourceSyncService synchronizes resource metrics from the agent into MySQL.
type ResourceSyncService struct {
	db  *gorm.DB
	ctx context.Context
}

// NewResourceSyncService creates a new resource sync service.
func NewResourceSyncService(ctx context.Context, db *gorm.DB) *ResourceSyncService {
	return &ResourceSyncService{db: db, ctx: ctx}
}

// SyncProviderResources collects resource metrics from the agent for all monitors under a provider.
func (s *ResourceSyncService) SyncProviderResources(providerID uint, config *monitoringModel.MonitoringConfig) error {
	// Get the provider endpoint
	var endpoint string
	if err := s.db.Raw("SELECT endpoint FROM providers WHERE id = ? AND deleted_at IS NULL", providerID).Scan(&endpoint).Error; err != nil || endpoint == "" {
		return fmt.Errorf("no endpoint for provider %d", providerID)
	}

	// Get all active monitors for this provider
	var monitors []monitoringModel.AgentMonitor
	if err := s.db.Where("provider_id = ? AND is_enabled = ?", providerID, true).Find(&monitors).Error; err != nil {
		return fmt.Errorf("list monitors: %w", err)
	}
	if len(monitors) == 0 {
		return nil
	}

	port := config.AgentPort
	if port == 0 {
		port = AgentPort
	}
	client := GetClient(providerID, endpoint, port, config.AgentToken)

	for i := range monitors {
		monitor := &monitors[i]

		// Fetch latest resource data from agent (limit 1 for latest only)
		resp, err := client.GetResources(monitor.AgentMonitorID, 1)
		if err != nil {
			if global.APP_LOG != nil {
				global.APP_LOG.Warn("fetch resources from agent failed",
					zap.Uint("instance_id", monitor.InstanceID),
					zap.Int64("agent_monitor_id", monitor.AgentMonitorID),
					zap.Error(err))
			}
			continue
		}

		if len(resp.Data) == 0 {
			continue
		}

		// Store the latest data point
		dp := resp.Data[0]
		metric := monitoringModel.ResourceMetric{
			InstanceID:  monitor.InstanceID,
			ProviderID:  monitor.ProviderID,
			UserID:      monitor.UserID,
			Timestamp:   time.Unix(dp.Timestamp, 0),
			CPUPercent:  dp.CPUPercent,
			MemoryUsed:  dp.MemoryUsed,
			MemoryTotal: dp.MemoryTotal,
			DiskUsed:    dp.DiskUsed,
			DiskTotal:   dp.DiskTotal,
		}

		// Avoid duplicate timestamps
		var count int64
		s.db.Model(&monitoringModel.ResourceMetric{}).
			Where("instance_id = ? AND timestamp = ?", monitor.InstanceID, metric.Timestamp).
			Count(&count)
		if count > 0 {
			continue
		}

		if err := s.db.Create(&metric).Error; err != nil {
			if global.APP_LOG != nil {
				global.APP_LOG.Warn("save resource metric failed",
					zap.Uint("instance_id", monitor.InstanceID),
					zap.Error(err))
			}
		}
	}

	return nil
}

// CleanupOldResourceMetrics removes resource metrics older than 24 hours.
func (s *ResourceSyncService) CleanupOldResourceMetrics() error {
	cutoff := time.Now().Add(-24 * time.Hour)
	result := s.db.Where("timestamp < ?", cutoff).Delete(&monitoringModel.ResourceMetric{})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected > 0 && global.APP_LOG != nil {
		global.APP_LOG.Info("cleaned up old resource metrics",
			zap.Int64("deleted", result.RowsAffected))
	}
	return nil
}

// GetInstanceResources returns resource metrics for an instance within the last N hours.
func (s *ResourceSyncService) GetInstanceResources(instanceID uint, hours int) ([]monitoringModel.ResourceMetric, error) {
	if hours <= 0 {
		hours = 24
	}
	since := time.Now().Add(-time.Duration(hours) * time.Hour)

	var metrics []monitoringModel.ResourceMetric
	if err := s.db.Where("instance_id = ? AND timestamp >= ?", instanceID, since).
		Order("timestamp ASC").Find(&metrics).Error; err != nil {
		return nil, err
	}
	return metrics, nil
}

// GetProviderResourceSummary returns the latest resource metrics for all instances of a provider.
func (s *ResourceSyncService) GetProviderResourceSummary(providerID uint) ([]monitoringModel.ResourceMetric, error) {
	var metrics []monitoringModel.ResourceMetric
	// Get latest metric per instance using subquery
	if err := s.db.Raw(`
		SELECT rm.* FROM resource_metrics rm
		INNER JOIN (
			SELECT instance_id, MAX(timestamp) as max_ts
			FROM resource_metrics
			WHERE provider_id = ?
			GROUP BY instance_id
		) latest ON rm.instance_id = latest.instance_id AND rm.timestamp = latest.max_ts
		WHERE rm.provider_id = ?
	`, providerID, providerID).Scan(&metrics).Error; err != nil {
		return nil, err
	}
	return metrics, nil
}
