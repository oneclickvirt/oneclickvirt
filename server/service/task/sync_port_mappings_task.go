package task

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"oneclickvirt/global"
	adminModel "oneclickvirt/model/admin"
	providerModel "oneclickvirt/model/provider"
	"oneclickvirt/provider"
	"oneclickvirt/service/database"
	provider2 "oneclickvirt/service/provider"
	"oneclickvirt/service/resources"

	"go.uber.org/zap"
	"gorm.io/gorm"
)

// executeSyncPortMappingsTask 执行同步端口映射任务（针对单个Provider）
// 检查数据库中的端口映射对应的实例是否在Provider上实际存在，如果不存在则自动清理
func (s *TaskService) executeSyncPortMappingsTask(ctx context.Context, task *adminModel.Task) error {
	// 初始化进度 (5%)
	s.updateTaskProgress(task.ID, 5, "正在解析任务数据...")

	// 解析任务数据
	var taskReq adminModel.SyncPortMappingsTaskRequest
	if err := json.Unmarshal([]byte(task.TaskData), &taskReq); err != nil {
		return fmt.Errorf("解析任务数据失败: %v", err)
	}

	// 从任务中获取Provider ID
	if task.ProviderID == nil {
		return fmt.Errorf("任务没有关联Provider")
	}
	providerID := *task.ProviderID

	// 更新进度 (10%)
	s.updateTaskProgress(task.ID, 10, "正在获取Provider信息...")

	// 获取Provider
	var prov providerModel.Provider
	if err := global.APP_DB.Where("id = ? AND status = ?", providerID, "active").First(&prov).Error; err != nil {
		return fmt.Errorf("查询Provider失败: %v", err)
	}

	global.APP_LOG.Info("开始同步Provider端口映射",
		zap.Uint("taskId", task.ID),
		zap.Uint("providerId", prov.ID),
		zap.String("providerName", prov.Name))

	// 更新进度 (20%)
	s.updateTaskProgress(task.ID, 20, fmt.Sprintf("正在同步Provider %s 的端口映射...", prov.Name))

	providerApiService := &provider2.ProviderApiService{}

	// 同步Provider的端口映射
	checked, cleaned, instances, ports, instanceNames, err := s.syncProviderPortMappings(ctx, &prov, providerApiService)
	if err != nil {
		return fmt.Errorf("同步Provider端口映射失败: %v", err)
	}

	// 更新进度 (90%)
	s.updateTaskProgress(task.ID, 90, "同步完成，正在生成报告...")

	// 生成完成消息
	var completionMsg strings.Builder
	completionMsg.WriteString(fmt.Sprintf("Provider %s 端口映射同步完成：检查了 %d 个实例", prov.Name, checked))
	if cleaned > 0 {
		completionMsg.WriteString(fmt.Sprintf("，清理了 %d 个孤立实例和 %d 个端口映射。", instances, ports))
		if len(instanceNames) > 0 {
			completionMsg.WriteString(fmt.Sprintf(" 清理的实例：%s", strings.Join(instanceNames, ", ")))
		}
	} else {
		completionMsg.WriteString("，未发现孤立的端口映射。")
	}

	// 标记任务完成
	stateManager := GetTaskStateManager()
	if err := stateManager.CompleteMainTask(task.ID, true, completionMsg.String(), nil); err != nil {
		global.APP_LOG.Error("完成任务失败", zap.Uint("taskId", task.ID), zap.Error(err))
	}

	global.APP_LOG.Info("端口映射同步任务完成",
		zap.Uint("taskId", task.ID),
		zap.Uint("providerId", prov.ID),
		zap.String("providerName", prov.Name),
		zap.Int("checkedInstances", checked),
		zap.Int("cleanedInstances", instances),
		zap.Int("cleanedPorts", ports))

	return nil
}

// syncProviderPortMappings 同步单个Provider的端口映射
// 返回：检查数量、清理数量、清理实例数、清理端口数、清理实例名称列表、错误
func (s *TaskService) syncProviderPortMappings(ctx context.Context, prov *providerModel.Provider, providerApiService *provider2.ProviderApiService) (int, int, int, int, []string, error) {
	// 1. 获取Provider实例，检查连接
	provInstance, _, err := providerApiService.GetProviderByID(prov.ID)
	if err != nil {
		return 0, 0, 0, 0, nil, fmt.Errorf("获取Provider实例失败: %v", err)
	}

	// 检查Provider连接状态
	if err := provider2.CheckProviderConnection(provInstance); err != nil {
		return 0, 0, 0, 0, nil, fmt.Errorf("Provider连接失败: %v", err)
	}

	// 2. 批量获取Provider上的所有实例（避免N+1）
	remoteInstances, err := provInstance.ListInstances(ctx)
	if err != nil {
		return 0, 0, 0, 0, nil, fmt.Errorf("获取Provider实例列表失败: %v", err)
	}

	// 构建远程实例名称映射（用于快速查找）
	remoteInstanceMap := make(map[string]provider.Instance)
	for _, inst := range remoteInstances {
		remoteInstanceMap[inst.Name] = inst
	}

	global.APP_LOG.Debug("获取Provider实例列表",
		zap.Uint("providerId", prov.ID),
		zap.Int("remoteCount", len(remoteInstances)))

	// 3. 批量查询数据库中该Provider的所有实例（避免N+1）
	var dbInstances []providerModel.Instance
	if err := global.APP_DB.Where("provider_id = ? AND status NOT IN ?", prov.ID,
		[]string{"deleted", "deleting"}).Find(&dbInstances).Error; err != nil {
		return 0, 0, 0, 0, nil, fmt.Errorf("查询数据库实例失败: %v", err)
	}

	global.APP_LOG.Debug("查询数据库实例",
		zap.Uint("providerId", prov.ID),
		zap.Int("dbCount", len(dbInstances)))

	// 4. 检测孤立实例（数据库有但Provider上不存在）
	var orphanedInstances []providerModel.Instance
	for _, dbInst := range dbInstances {
		if _, exists := remoteInstanceMap[dbInst.Name]; !exists {
			orphanedInstances = append(orphanedInstances, dbInst)
		}
	}

	if len(orphanedInstances) == 0 {
		global.APP_LOG.Debug("Provider无孤立实例",
			zap.Uint("providerId", prov.ID))
		return len(dbInstances), 0, 0, 0, nil, nil
	}

	global.APP_LOG.Info("发现孤立实例",
		zap.Uint("providerId", prov.ID),
		zap.Int("count", len(orphanedInstances)))

	// 5. 批量清理孤立实例和端口映射（使用短事务）
	var cleanedCount, cleanedInstances, cleanedPorts int
	var cleanedInstanceNames []string
	dbService := database.GetDatabaseService()

	for _, orphanInst := range orphanedInstances {
		// 使用独立的短事务清理每个孤立实例
		err := dbService.ExecuteTransaction(ctx, func(tx *gorm.DB) error {
			// 获取该实例的端口映射数量
			var portCount int64
			if err := tx.Model(&providerModel.Port{}).
				Where("instance_id = ?", orphanInst.ID).
				Count(&portCount).Error; err != nil {
				return err
			}

			// 删除实例的端口映射
			portMappingService := resources.PortMappingService{}
			if err := portMappingService.DeleteInstancePortMappingsInTx(tx, orphanInst.ID); err != nil {
				global.APP_LOG.Warn("删除孤立实例端口映射失败",
					zap.Uint("instanceId", orphanInst.ID),
					zap.String("instanceName", orphanInst.Name),
					zap.Error(err))
				// 不返回错误，继续清理实例
			}

			// 软删除实例记录
			if err := tx.Delete(&orphanInst).Error; err != nil {
				return fmt.Errorf("删除孤立实例记录失败: %v", err)
			}

			cleanedInstances++
			cleanedPorts += int(portCount)
			cleanedInstanceNames = append(cleanedInstanceNames, orphanInst.Name)

			global.APP_LOG.Info("清理孤立实例成功",
				zap.Uint("instanceId", orphanInst.ID),
				zap.String("instanceName", orphanInst.Name),
				zap.Int64("portCount", portCount))

			return nil
		})

		if err != nil {
			global.APP_LOG.Error("清理孤立实例事务失败",
				zap.Uint("instanceId", orphanInst.ID),
				zap.String("instanceName", orphanInst.Name),
				zap.Error(err))
			// 继续处理下一个实例
			continue
		}

		cleanedCount++
	}

	return len(dbInstances), cleanedCount, cleanedInstances, cleanedPorts, cleanedInstanceNames, nil
}
