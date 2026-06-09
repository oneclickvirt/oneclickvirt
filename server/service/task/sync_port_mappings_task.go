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
	"oneclickvirt/provider/incus"
	"oneclickvirt/provider/lxd"
	"oneclickvirt/provider/portmapping"
	"oneclickvirt/service/database"
	provider2 "oneclickvirt/service/provider"
	"oneclickvirt/service/resources"
	"oneclickvirt/utils"

	"go.uber.org/zap"
	"gorm.io/gorm"
)

// executeSyncPortMappingsTask 执行同步端口映射任务（针对单个Provider）
// 检查数据库中的端口映射对应的实例是否在Provider上实际存在，如果不存在则自动清理
func (s *TaskService) executeSyncPortMappingsTask(ctx context.Context, task *adminModel.Task) error {
	// 初始化进度 (5%)
	s.updateTaskProgress(task.ID, 5, "step.parseTaskData")

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
	s.updateTaskProgress(task.ID, 10, "step.getProviderInfo")

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
	s.updateTaskProgress(task.ID, 20, fmt.Sprintf("step.syncProviderPortMappings:%s", prov.Name))

	providerApiService := &provider2.ProviderApiService{}

	// 同步Provider的端口映射
	excludedPortIDs := make(map[uint]bool, len(taskReq.ExcludedPortIDs))
	for _, id := range taskReq.ExcludedPortIDs {
		excludedPortIDs[id] = true
	}
	includedPortIDs := make(map[uint]bool, len(taskReq.IncludedPortIDs))
	for _, id := range taskReq.IncludedPortIDs {
		includedPortIDs[id] = true
	}
	checked, cleaned, instances, ports, instanceNames, err := s.syncProviderPortMappings(ctx, &prov, providerApiService, includedPortIDs, excludedPortIDs)
	if err != nil {
		return fmt.Errorf("同步Provider端口映射失败: %v", err)
	}

	// 更新进度 (90%)
	s.updateTaskProgress(task.ID, 90, "step.generatingReport")

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
func (s *TaskService) syncProviderPortMappings(ctx context.Context, prov *providerModel.Provider, providerApiService *provider2.ProviderApiService, includedPortIDs map[uint]bool, excludedPortIDs map[uint]bool) (int, int, int, int, []string, error) {
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

	var cleanedCount, cleanedInstances, cleanedPorts int

	// 4.1 清理无端口映射模式下的自动端口映射（这些映射本不应被创建）
	// "无端口映射"语义上不应该存在任何自动端口映射记录。
	// 仅清理自动生成的映射（IsAutomatic=true），保留用户手动添加的控制端转发映射。
	if prov.NetworkType == "no_port_mapping" {
		noPmCleanedPorts, noPmErr := s.cleanNoPortMappingAutoPorts(ctx, provInstance, prov, includedPortIDs, excludedPortIDs)
		if noPmErr != nil {
			global.APP_LOG.Warn("清理无端口映射模式的自动端口映射失败",
				zap.Uint("providerId", prov.ID),
				zap.Error(noPmErr))
		} else if noPmCleanedPorts > 0 {
			global.APP_LOG.Info("已清理无端口映射模式下的自动端口映射",
				zap.Uint("providerId", prov.ID),
				zap.Int("cleanedPorts", noPmCleanedPorts))
			cleanedCount += noPmCleanedPorts
			cleanedPorts += noPmCleanedPorts
		}
	}

	if len(orphanedInstances) == 0 {
		global.APP_LOG.Debug("Provider无孤立实例",
			zap.Uint("providerId", prov.ID))
		// 即使无孤立实例，也返回 no_port_mapping 清理的数量（如有）
		return len(dbInstances), cleanedCount, cleanedInstances, cleanedPorts, nil, nil
	}

	global.APP_LOG.Info("发现孤立实例",
		zap.Uint("providerId", prov.ID),
		zap.Int("count", len(orphanedInstances)))

	// 5. 批量清理孤立实例的自动端口映射（使用短事务）。手动端口不由同步任务删除。
	var cleanedInstanceNames []string
	dbService := database.GetDatabaseService()

	for _, orphanInst := range orphanedInstances {
		var syncPorts []providerModel.Port
		if err := global.APP_DB.Where("instance_id = ? AND (is_automatic = ? OR port_type = ?)",
			orphanInst.ID, true, "range_mapped").Find(&syncPorts).Error; err != nil {
			global.APP_LOG.Warn("查询孤立实例自动端口映射失败，跳过",
				zap.Uint("instanceId", orphanInst.ID),
				zap.Error(err))
			continue
		}
		filteredPorts := make([]providerModel.Port, 0, len(syncPorts))
		for _, p := range syncPorts {
			if !shouldDeleteSyncCandidate(p.ID, includedPortIDs, excludedPortIDs) {
				continue
			}
			filteredPorts = append(filteredPorts, p)
		}
		if len(filteredPorts) == 0 {
			global.APP_LOG.Debug("孤立实例没有可由同步任务删除的端口映射，保留实例记录",
				zap.Uint("instanceId", orphanInst.ID),
				zap.String("instanceName", orphanInst.Name))
			continue
		}

		// 5.1 先尝试清理节点侧的实际端口映射规则（尽力而为，不因失败而阻止DB清理）
		s.removePortMappingsFromNode(ctx, provInstance, prov, &orphanInst, filteredPorts)

		// 5.2 使用独立的短事务清理每个孤立实例的数据库记录
		deletedPortsForInstance := 0
		keptInstanceRecord := false
		deletedInstanceRecord := false
		err := dbService.ExecuteTransaction(ctx, func(tx *gorm.DB) error {
			portIDs := make([]uint, 0, len(filteredPorts))
			releasedPorts := make([]int, 0, len(filteredPorts))
			deletedSSHPort := false
			for _, p := range filteredPorts {
				portIDs = append(portIDs, p.ID)
				releasedPorts = append(releasedPorts, p.HostPort)
				if p.IsSSH {
					deletedSSHPort = true
				}
			}

			result := tx.Unscoped().Where("id IN ?", portIDs).Delete(&providerModel.Port{})
			if result.Error != nil {
				return fmt.Errorf("删除孤立实例自动端口映射失败: %w", result.Error)
			}
			deletedPorts := int(result.RowsAffected)
			deletedPortsForInstance = deletedPorts

			if deletedSSHPort {
				if err := tx.Model(&providerModel.Instance{}).Where("id = ?", orphanInst.ID).
					Update("ssh_port", 0).Error; err != nil {
					global.APP_LOG.Warn("清除孤立实例SSH端口引用失败",
						zap.Uint("instanceId", orphanInst.ID),
						zap.Error(err))
				}
			}

			portMappingService := resources.PortMappingService{}
			if len(releasedPorts) > 0 {
				if err := portMappingService.OptimizeNextAvailablePortInTx(tx, prov.ID, releasedPorts); err != nil {
					global.APP_LOG.Warn("回收孤立实例端口失败",
						zap.Uint("providerId", prov.ID),
						zap.Error(err))
				}
			}

			var remainingPorts int64
			if err := tx.Model(&providerModel.Port{}).
				Where("instance_id = ?", orphanInst.ID).
				Count(&remainingPorts).Error; err != nil {
				return err
			}
			if remainingPorts > 0 {
				keptInstanceRecord = true
				global.APP_LOG.Debug("孤立实例仍有手动端口映射，保留实例记录",
					zap.Uint("instanceId", orphanInst.ID),
					zap.String("instanceName", orphanInst.Name),
					zap.Int64("remainingPorts", remainingPorts))
				return nil
			}

			// 软删除实例记录
			if err := tx.Delete(&orphanInst).Error; err != nil {
				return fmt.Errorf("删除孤立实例记录失败: %v", err)
			}

			deletedInstanceRecord = true

			global.APP_LOG.Debug("清理孤立实例成功",
				zap.Uint("instanceId", orphanInst.ID),
				zap.String("instanceName", orphanInst.Name),
				zap.Int("portCount", deletedPorts))

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
		cleanedPorts += deletedPortsForInstance
		if keptInstanceRecord {
			cleanedCount++
		}
		if deletedInstanceRecord {
			cleanedInstances++
			cleanedInstanceNames = append(cleanedInstanceNames, orphanInst.Name)
			cleanedCount++
		}
	}

	return len(dbInstances), cleanedCount, cleanedInstances, cleanedPorts, cleanedInstanceNames, nil
}

// PreviewSyncPortMappings 生成端口映射同步预览，不修改数据库或节点侧规则。
func (s *TaskService) PreviewSyncPortMappings(ctx context.Context, req *adminModel.SyncPortMappingsTaskRequest) (*adminModel.SyncPortMappingsPreviewResponse, error) {
	var providers []providerModel.Provider
	query := global.APP_DB.Where("status = ?", "active")
	if len(req.ProviderIDs) > 0 {
		query = query.Where("id IN ?", req.ProviderIDs)
	}
	if err := query.Find(&providers).Error; err != nil {
		return nil, fmt.Errorf("查询Provider列表失败: %v", err)
	}
	if len(providers) == 0 {
		return nil, fmt.Errorf("Provider不存在")
	}

	providerApiService := &provider2.ProviderApiService{}
	response := &adminModel.SyncPortMappingsPreviewResponse{
		ProviderCount: len(providers),
		Providers:     make([]adminModel.SyncProviderPortMappingsPreview, 0, len(providers)),
	}
	for _, prov := range providers {
		preview := s.previewProviderPortMappings(ctx, &prov, providerApiService)
		response.CandidateCount += preview.CandidateCount
		response.Providers = append(response.Providers, preview)
	}
	return response, nil
}

func (s *TaskService) previewProviderPortMappings(ctx context.Context, prov *providerModel.Provider, providerApiService *provider2.ProviderApiService) adminModel.SyncProviderPortMappingsPreview {
	preview := adminModel.SyncProviderPortMappingsPreview{
		ProviderID:   prov.ID,
		ProviderName: prov.Name,
		Candidates:   []adminModel.SyncPortMappingCandidate{},
	}

	provInstance, _, err := providerApiService.GetProviderByID(prov.ID)
	if err != nil {
		preview.Error = fmt.Sprintf("获取Provider实例失败: %v", err)
		return preview
	}
	if err := provider2.CheckProviderConnection(provInstance); err != nil {
		preview.Error = fmt.Sprintf("Provider连接失败: %v", err)
		return preview
	}
	remoteInstances, err := provInstance.ListInstances(ctx)
	if err != nil {
		preview.Error = fmt.Sprintf("获取Provider实例列表失败: %v", err)
		return preview
	}
	preview.Healthy = true

	remoteInstanceMap := make(map[string]provider.Instance, len(remoteInstances))
	for _, inst := range remoteInstances {
		remoteInstanceMap[inst.Name] = inst
	}

	var dbInstances []providerModel.Instance
	if err := global.APP_DB.Where("provider_id = ? AND status NOT IN ?", prov.ID,
		[]string{"deleted", "deleting"}).Find(&dbInstances).Error; err != nil {
		preview.Error = fmt.Sprintf("查询数据库实例失败: %v", err)
		preview.Healthy = false
		return preview
	}
	preview.Checked = len(dbInstances)

	instanceMap := make(map[uint]providerModel.Instance, len(dbInstances))
	for _, inst := range dbInstances {
		instanceMap[inst.ID] = inst
	}
	seenPortIDs := make(map[uint]bool)
	appendCandidate := func(p providerModel.Port, inst providerModel.Instance, reason string) {
		if seenPortIDs[p.ID] {
			return
		}
		seenPortIDs[p.ID] = true
		preview.Candidates = append(preview.Candidates, adminModel.SyncPortMappingCandidate{
			PortID:       p.ID,
			InstanceID:   p.InstanceID,
			InstanceName: inst.Name,
			ProviderID:   prov.ID,
			ProviderName: prov.Name,
			HostPort:     p.HostPort,
			GuestPort:    p.GuestPort,
			Protocol:     p.Protocol,
			PortType:     p.PortType,
			IsSSH:        p.IsSSH,
			IsAutomatic:  p.IsAutomatic,
			MappingType:  p.MappingType,
			Reason:       reason,
		})
	}

	if prov.NetworkType == "no_port_mapping" {
		var noMappingPorts []providerModel.Port
		if err := global.APP_DB.Where("provider_id = ? AND (is_automatic = ? OR port_type = ?)",
			prov.ID, true, "range_mapped").Find(&noMappingPorts).Error; err == nil {
			for _, p := range noMappingPorts {
				inst := instanceMap[p.InstanceID]
				appendCandidate(p, inst, "no_port_mapping")
			}
		} else {
			preview.Error = fmt.Sprintf("查询no_port_mapping候选失败: %v", err)
			preview.Healthy = false
			return preview
		}
	}

	for _, dbInst := range dbInstances {
		if _, exists := remoteInstanceMap[dbInst.Name]; exists {
			continue
		}
		var orphanPorts []providerModel.Port
		if err := global.APP_DB.Where("instance_id = ? AND (is_automatic = ? OR port_type = ?)",
			dbInst.ID, true, "range_mapped").Find(&orphanPorts).Error; err != nil {
			preview.Error = fmt.Sprintf("查询孤立实例候选失败: %v", err)
			preview.Healthy = false
			return preview
		}
		for _, p := range orphanPorts {
			appendCandidate(p, dbInst, "orphan_instance")
		}
	}

	preview.CandidateCount = len(preview.Candidates)
	return preview
}

func shouldDeleteSyncCandidate(portID uint, includedPortIDs map[uint]bool, excludedPortIDs map[uint]bool) bool {
	if len(includedPortIDs) > 0 && !includedPortIDs[portID] {
		return false
	}
	return !excludedPortIDs[portID]
}

// cleanNoPortMappingAutoPorts 清理无端口映射模式下不应存在的自动端口映射记录。
// "无端口映射"语义上不应该存在任何自动生成的端口映射（IsAutomatic=true / PortType="range_mapped"）。
// 仅清理自动映射，保留用户手动添加的控制端转发端口（PortType="manual"）。
// 同时清理节点侧的实际端口映射规则（iptables / device_proxy 等），确保与数据库状态一致。
// 返回清理的端口数量。
func (s *TaskService) cleanNoPortMappingAutoPorts(ctx context.Context, provInstance provider.Provider, prov *providerModel.Provider, includedPortIDs map[uint]bool, excludedPortIDs map[uint]bool) (int, error) {
	// 查找该Provider下所有自动端口映射（IsAutomatic=true 或 PortType="range_mapped"）
	var autoPorts []providerModel.Port
	if err := global.APP_DB.Where("provider_id = ? AND (is_automatic = ? OR port_type = ?)",
		prov.ID, true, "range_mapped").Find(&autoPorts).Error; err != nil {
		return 0, fmt.Errorf("查询自动端口映射失败: %w", err)
	}

	if len(autoPorts) == 0 {
		return 0, nil
	}
	filteredPorts := make([]providerModel.Port, 0, len(autoPorts))
	for _, p := range autoPorts {
		if !shouldDeleteSyncCandidate(p.ID, includedPortIDs, excludedPortIDs) {
			continue
		}
		filteredPorts = append(filteredPorts, p)
	}
	if len(filteredPorts) == 0 {
		return 0, nil
	}

	global.APP_LOG.Info("发现无端口映射模式下的自动端口映射，准备清理",
		zap.Uint("providerId", prov.ID),
		zap.Int("count", len(filteredPorts)))

	// 预加载关联实例信息（用于节点侧清理）
	instanceIDs := make(map[uint]bool)
	for _, p := range filteredPorts {
		instanceIDs[p.InstanceID] = true
	}
	var instances []providerModel.Instance
	idList := make([]uint, 0, len(instanceIDs))
	for id := range instanceIDs {
		idList = append(idList, id)
	}
	instanceMap := make(map[uint]providerModel.Instance)
	if len(idList) > 0 {
		if err := global.APP_DB.Where("id IN ?", idList).Find(&instances).Error; err == nil {
			for _, inst := range instances {
				instanceMap[inst.ID] = inst
			}
		}
	}

	// 按实例分组，逐实例清理
	portByInstance := make(map[uint][]providerModel.Port)
	for _, p := range filteredPorts {
		portByInstance[p.InstanceID] = append(portByInstance[p.InstanceID], p)
	}

	dbService := database.GetDatabaseService()
	totalCleaned := 0

	for instanceID, ports := range portByInstance {
		// 先清理节点侧的实际端口映射规则
		if inst, ok := instanceMap[instanceID]; ok {
			s.removePortMappingsFromNode(ctx, provInstance, prov, &inst, ports)
		}

		// 停止控制端转发监听（如有）
		for _, p := range ports {
			if p.MappingType == "controller" && resources.StopControllerPortForwardFunc != nil {
				resources.StopControllerPortForwardFunc(p.ID)
			}
		}

		err := dbService.ExecuteTransaction(ctx, func(tx *gorm.DB) error {
			portMappingService := resources.PortMappingService{}
			// 仅删除该实例的自动端口映射，不删除实例本身
			portIDs := make([]uint, 0, len(ports))
			deletedSSHPort := false
			for _, p := range ports {
				portIDs = append(portIDs, p.ID)
				if p.IsSSH {
					deletedSSHPort = true
				}
			}
			result := tx.Unscoped().Where("id IN ?", portIDs).Delete(&providerModel.Port{})
			if result.Error != nil {
				return fmt.Errorf("删除自动端口映射失败: %w", result.Error)
			}
			totalCleaned += int(result.RowsAffected)

			// 清除实例的 ssh_port 引用（如果已失效）
			if deletedSSHPort {
				if err := tx.Model(&providerModel.Instance{}).Where("id = ?", instanceID).
					Update("ssh_port", 0).Error; err != nil {
					global.APP_LOG.Warn("清除实例SSH端口引用失败",
						zap.Uint("instanceId", instanceID),
						zap.Error(err))
				}
			}

			// 回收已释放的端口（更新 next_available_port）
			var releasedPorts []int
			for _, p := range ports {
				releasedPorts = append(releasedPorts, p.HostPort)
			}
			if len(releasedPorts) > 0 {
				if err := portMappingService.OptimizeNextAvailablePortInTx(tx, prov.ID, releasedPorts); err != nil {
					global.APP_LOG.Warn("回收已释放端口失败",
						zap.Uint("providerId", prov.ID),
						zap.Error(err))
				}
			}

			return nil
		})

		if err != nil {
			global.APP_LOG.Error("清理无端口映射自动端口映射事务失败",
				zap.Uint("instanceId", instanceID),
				zap.Error(err))
			continue
		}

		global.APP_LOG.Debug("已清理实例的自动端口映射",
			zap.Uint("instanceId", instanceID),
			zap.Int("portCount", len(ports)))
	}

	return totalCleaned, nil
}

// removeNodeSidePortMappingsBestEffort 尽力清理孤立实例在节点侧的实际端口映射规则。
// 由于实例已不在 Provider 上存在，部分操作（如 LXD device_proxy 删除）可能失败，仅记录日志。
func (s *TaskService) removeNodeSidePortMappingsBestEffort(ctx context.Context, provInstance provider.Provider, prov *providerModel.Provider, instance *providerModel.Instance) {
	if provInstance == nil || instance == nil || instance.Name == "" {
		return
	}

	// 获取该实例的所有端口映射记录
	var ports []providerModel.Port
	if err := global.APP_DB.Where("instance_id = ?", instance.ID).Find(&ports).Error; err != nil {
		global.APP_LOG.Warn("获取孤立实例端口映射失败，跳过节点侧清理",
			zap.Uint("instanceId", instance.ID),
			zap.Error(err))
		return
	}

	s.removePortMappingsFromNode(ctx, provInstance, prov, instance, ports)
}

// removePortMappingsFromNode 从节点侧移除指定实例的端口映射规则。
// 处理逻辑与 executeDeletePortMappingTask 保持一致：
//   - controller 模式：由 StopControllerPortForwardFunc 处理（调用者负责）
//   - 非 controller 模式：通过 portmapping manager 删除，LXD/Incus 额外调用 RemovePortMapping
func (s *TaskService) removePortMappingsFromNode(ctx context.Context, provInstance provider.Provider, prov *providerModel.Provider, instance *providerModel.Instance, ports []providerModel.Port) {
	if provInstance == nil || instance == nil || instance.Name == "" {
		return
	}

	providerType := utils.NormalizeProviderType(prov.Type)
	portMappingType := providerType
	if portMappingType == "proxmox" || portMappingType == "proxmoxve" || portMappingType == "kubevirt" || utils.IsVMOnlyProvider(portMappingType) {
		portMappingType = "iptables"
	}

	// 停止控制端转发（controller 模式端口）
	for _, p := range ports {
		if p.MappingType == "controller" && resources.StopControllerPortForwardFunc != nil {
			resources.StopControllerPortForwardFunc(p.ID)
		}
	}

	// 通过 portmapping manager 删除节点侧规则（处理 Docker/Podman/Containerd/iptables 等）
	manager := portmapping.NewManager(&portmapping.ManagerConfig{
		DefaultMappingMethod: prov.IPv4PortMappingMethod,
	})
	for _, p := range ports {
		if p.MappingType == "controller" {
			continue // controller 模式已在上面处理
		}
		deleteReq := &portmapping.DeletePortMappingRequest{
			ID:         p.ID,
			InstanceID: fmt.Sprintf("%d", instance.ID),
		}
		if err := manager.DeletePortMapping(ctx, portMappingType, deleteReq); err != nil {
			global.APP_LOG.Warn("portmapping manager 删除端口映射失败",
				zap.Uint("portId", p.ID),
				zap.Int("hostPort", p.HostPort),
				zap.Error(err))
		}
	}

	// LXD/Incus：额外通过 SSH 调用 RemovePortMapping 清理 proxy device 规则
	// （Proxmox 的 iptables 规则已由 portmapping manager 的 iptables provider 处理，无需额外调用）
	if providerType == "lxd" || providerType == "incus" {
		for _, p := range ports {
			if p.MappingType == "controller" {
				continue
			}
			var removeErr error
			switch providerType {
			case "lxd":
				if lxdProv, ok := provInstance.(*lxd.LXDProvider); ok {
					removeErr = lxdProv.RemovePortMapping(instance.Name, p.HostPort, p.Protocol, prov.IPv4PortMappingMethod)
				}
			case "incus":
				if incusProv, ok := provInstance.(*incus.IncusProvider); ok {
					removeErr = incusProv.RemovePortMapping(instance.Name, p.HostPort, p.Protocol, prov.IPv4PortMappingMethod)
				}
			}
			if removeErr != nil {
				global.APP_LOG.Warn("节点侧端口映射删除失败（实例可能已不存在）",
					zap.Uint("portId", p.ID),
					zap.String("providerType", providerType),
					zap.String("instanceName", instance.Name),
					zap.Int("hostPort", p.HostPort),
					zap.Error(removeErr))
			}
		}
	}
}
