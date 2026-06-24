package maintenance

import (
	"context"
	"fmt"
	"time"

	"oneclickvirt/config"
	"oneclickvirt/global"

	"go.uber.org/zap"
)

const maxCleanupBatchesPerTable = 100

type DataCleanupService struct{}

type DataCleanupStats struct {
	AuditLogs                int64 `json:"auditLogs"`
	PmacctTrafficRecords     int64 `json:"pmacctTrafficRecords"`
	InstanceTrafficHistories int64 `json:"instanceTrafficHistories"`
	ProviderTrafficHistories int64 `json:"providerTrafficHistories"`
	UserTrafficHistories     int64 `json:"userTrafficHistories"`
}

func NewDataCleanupService() *DataCleanupService {
	return &DataCleanupService{}
}

func NormalizeMaintenanceConfig(cfg config.Maintenance) config.Maintenance {
	if cfg.DataCleanupIntervalHours <= 0 {
		cfg.DataCleanupIntervalHours = 24
	}
	if cfg.AuditLogRetentionDays <= 0 {
		cfg.AuditLogRetentionDays = 30
	}
	if cfg.PmacctRawRetentionDays <= 0 {
		cfg.PmacctRawRetentionDays = 45
	}
	if cfg.TrafficHistoryRetentionDays <= 0 {
		cfg.TrafficHistoryRetentionDays = 180
	}
	if cfg.CleanupBatchSize <= 0 {
		cfg.CleanupBatchSize = 5000
	}
	if cfg.CleanupBatchSize > 50000 {
		cfg.CleanupBatchSize = 50000
	}
	return cfg
}

func (s *DataCleanupService) Run(ctx context.Context) (DataCleanupStats, error) {
	var stats DataCleanupStats
	if global.APP_DB == nil {
		return stats, nil
	}

	cfg := NormalizeMaintenanceConfig(global.GetAppConfig().Maintenance)
	if !cfg.EnableDataCleanup {
		return stats, nil
	}

	now := time.Now()
	auditCutoff := now.AddDate(0, 0, -cfg.AuditLogRetentionDays)
	rawCutoff := now.AddDate(0, 0, -cfg.PmacctRawRetentionDays)
	historyCutoff := now.AddDate(0, 0, -cfg.TrafficHistoryRetentionDays)

	var err error
	if stats.AuditLogs, err = s.deleteOldRows(ctx, "audit_logs", "created_at", auditCutoff, cfg.CleanupBatchSize); err != nil {
		return stats, fmt.Errorf("清理审计日志失败: %w", err)
	}
	if stats.PmacctTrafficRecords, err = s.deleteOldRows(ctx, "pmacct_traffic_records", "timestamp", rawCutoff, cfg.CleanupBatchSize); err != nil {
		return stats, fmt.Errorf("清理pmacct原始流量记录失败: %w", err)
	}
	if stats.InstanceTrafficHistories, err = s.deleteOldRows(ctx, "instance_traffic_histories", "record_time", historyCutoff, cfg.CleanupBatchSize); err != nil {
		return stats, fmt.Errorf("清理实例流量历史失败: %w", err)
	}
	if stats.ProviderTrafficHistories, err = s.deleteOldRows(ctx, "provider_traffic_histories", "record_time", historyCutoff, cfg.CleanupBatchSize); err != nil {
		return stats, fmt.Errorf("清理节点流量历史失败: %w", err)
	}
	if stats.UserTrafficHistories, err = s.deleteOldRows(ctx, "user_traffic_histories", "record_time", historyCutoff, cfg.CleanupBatchSize); err != nil {
		return stats, fmt.Errorf("清理用户流量历史失败: %w", err)
	}

	if cfg.OptimizeAfterCleanup && global.APP_DB.Dialector.Name() == "mysql" && stats.totalDeleted() > 0 {
		s.optimizeTables(ctx)
	}

	if stats.totalDeleted() > 0 {
		global.APP_LOG.Info("数据库保留策略清理完成",
			zap.Int64("auditLogs", stats.AuditLogs),
			zap.Int64("pmacctTrafficRecords", stats.PmacctTrafficRecords),
			zap.Int64("instanceTrafficHistories", stats.InstanceTrafficHistories),
			zap.Int64("providerTrafficHistories", stats.ProviderTrafficHistories),
			zap.Int64("userTrafficHistories", stats.UserTrafficHistories))
	}

	return stats, nil
}

func (s *DataCleanupService) deleteOldRows(ctx context.Context, tableName string, timeColumn string, cutoff time.Time, batchSize int) (int64, error) {
	query := fmt.Sprintf(
		"DELETE FROM `%s` WHERE `id` IN (SELECT `id` FROM (SELECT `id` FROM `%s` WHERE `%s` < ? ORDER BY `id` LIMIT ?) AS stale_rows)",
		tableName,
		tableName,
		timeColumn,
	)

	var total int64
	for batch := 0; batch < maxCleanupBatchesPerTable; batch++ {
		select {
		case <-ctx.Done():
			return total, ctx.Err()
		default:
		}

		result := global.APP_DB.WithContext(ctx).Exec(query, cutoff, batchSize)
		if result.Error != nil {
			return total, result.Error
		}
		total += result.RowsAffected
		if result.RowsAffected < int64(batchSize) {
			return total, nil
		}
	}

	global.APP_LOG.Warn("数据库清理达到单表批次数上限，本轮保守停止",
		zap.String("table", tableName),
		zap.Int("maxBatches", maxCleanupBatchesPerTable),
		zap.Int("batchSize", batchSize),
		zap.Int64("deleted", total))
	return total, nil
}

func (s *DataCleanupService) optimizeTables(ctx context.Context) {
	for _, tableName := range []string{
		"audit_logs",
		"pmacct_traffic_records",
		"instance_traffic_histories",
		"provider_traffic_histories",
		"user_traffic_histories",
	} {
		select {
		case <-ctx.Done():
			return
		default:
		}
		if err := global.APP_DB.WithContext(ctx).Exec("OPTIMIZE TABLE `" + tableName + "`").Error; err != nil {
			global.APP_LOG.Warn("数据库表空间优化失败",
				zap.String("table", tableName),
				zap.Error(err))
		}
	}
}

func (s DataCleanupStats) totalDeleted() int64 {
	return s.AuditLogs +
		s.PmacctTrafficRecords +
		s.InstanceTrafficHistories +
		s.ProviderTrafficHistories +
		s.UserTrafficHistories
}
