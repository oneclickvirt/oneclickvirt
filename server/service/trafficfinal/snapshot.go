// Package trafficfinal collects counters before lifecycle operations. It does
// not enforce limits or enqueue more lifecycle tasks while one already owns the guest.
package trafficfinal

import (
	"context"
	"fmt"
	"oneclickvirt/global"
	monitoringModel "oneclickvirt/model/monitoring"
	providerModel "oneclickvirt/model/provider"
	"oneclickvirt/service/agent"
	"oneclickvirt/service/pmacct"
	"time"
)

func Collect(ctx context.Context, instanceID uint) error {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	db := global.APP_DB.WithContext(ctx)
	var instance providerModel.Instance
	if err := db.First(&instance, instanceID).Error; err != nil {
		return err
	}
	var agentCount int64
	if err := db.Model(&monitoringModel.AgentMonitor{}).Where("instance_id = ?", instanceID).Count(&agentCount).Error; err != nil {
		return err
	}
	if agentCount > 0 {
		config, err := agent.GetMonitoringConfig(db, instance.ProviderID)
		if err != nil {
			return err
		}
		if err := agent.NewSyncService(ctx, db).SyncInstanceTraffic(instanceID, config); err != nil {
			return fmt.Errorf("最终 Agent 流量采集失败，已保留记录: %w", err)
		}
	}
	return pmacct.NewServiceWithContext(ctx).CollectInstanceTraffic(instanceID)
}
