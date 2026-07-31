package task

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"oneclickvirt/constant"
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

const (
	syncOrphanGracePeriod   = 10 * time.Minute
	syncPortDeleteBatchSize = 200
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
	if len(taskReq.IncludedPortIDs) == 0 {
		return fmt.Errorf("同步任务缺少预览确认的端口映射")
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
	checked, ports, err := s.syncProviderPortMappings(ctx, &prov, providerApiService, includedPortIDs, excludedPortIDs)
	if err != nil {
		return fmt.Errorf("同步Provider端口映射失败: %v", err)
	}

	// 更新进度 (90%)
	s.updateTaskProgress(task.ID, 90, "step.generatingReport")

	// 生成完成消息
	var completionMsg strings.Builder
	completionMsg.WriteString(fmt.Sprintf("Provider %s 端口映射同步完成：检查了 %d 个实例", prov.Name, checked))
	if ports > 0 {
		completionMsg.WriteString(fmt.Sprintf("，清理了 %d 个孤立端口映射。", ports))
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
		zap.Int("cleanedPorts", ports))

	return nil
}

// syncProviderPortMappings 同步单个Provider的端口映射
func (s *TaskService) syncProviderPortMappings(ctx context.Context, prov *providerModel.Provider, providerApiService *provider2.ProviderApiService, includedPortIDs map[uint]bool, excludedPortIDs map[uint]bool) (int, int, error) {
	// 1. 获取Provider实例，检查连接
	provInstance, _, err := providerApiService.GetProviderByID(prov.ID)
	if err != nil {
		return 0, 0, fmt.Errorf("获取Provider实例失败: %v", err)
	}

	// 检查Provider连接状态
	if err := provider2.CheckProviderConnection(provInstance); err != nil {
		return 0, 0, fmt.Errorf("Provider连接失败: %v", err)
	}

	// 2. 批量获取Provider上的所有实例（避免N+1）
	remoteInstances, err := provInstance.ListInstances(ctx)
	if err != nil {
		return 0, 0, fmt.Errorf("获取Provider实例列表失败: %v", err)
	}

	remoteInstanceMap := buildRemoteInstanceIdentitySet(remoteInstances)

	global.APP_LOG.Debug("获取Provider实例列表",
		zap.Uint("providerId", prov.ID),
		zap.Int("remoteCount", len(remoteInstances)))

	// 3. 批量查询数据库中该Provider的所有实例（避免N+1）
	var dbInstances []providerModel.Instance
	if err := global.APP_DB.Where("provider_id = ? AND status NOT IN ?", prov.ID,
		[]string{"deleted", "deleting"}).Find(&dbInstances).Error; err != nil {
		return 0, 0, fmt.Errorf("查询数据库实例失败: %v", err)
	}

	global.APP_LOG.Debug("查询数据库实例",
		zap.Uint("providerId", prov.ID),
		zap.Int("dbCount", len(dbInstances)))

	// 4. 检测孤立实例（数据库有但Provider上不存在）
	var orphanedInstances []providerModel.Instance
	now := time.Now()
	for _, dbInst := range dbInstances {
		if !remoteInstanceMatchesDBInstance(prov.Type, remoteInstanceMap, dbInst) && isSafeSyncOrphanCandidate(dbInst, now) {
			orphanedInstances = append(orphanedInstances, dbInst)
		}
	}
	orphanedInstances, err = s.excludeInstancesWithActiveTasks(orphanedInstances)
	if err != nil {
		return 0, 0, fmt.Errorf("查询孤立实例活动任务失败: %v", err)
	}

	cleanedPorts := 0

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
			cleanedPorts += noPmCleanedPorts
		}
		return len(dbInstances), cleanedPorts, nil
	}

	if len(orphanedInstances) == 0 {
		global.APP_LOG.Debug("Provider无孤立实例",
			zap.Uint("providerId", prov.ID))
		return len(dbInstances), cleanedPorts, nil
	}

	global.APP_LOG.Info("发现孤立实例",
		zap.Uint("providerId", prov.ID),
		zap.Int("count", len(orphanedInstances)))

	// 5. 批量清理孤立实例的自动端口映射（使用短事务）。手动端口不由同步任务删除。
	orphanInstanceIDs := make(map[uint]struct{}, len(orphanedInstances))
	for _, orphanInst := range orphanedInstances {
		orphanInstanceIDs[orphanInst.ID] = struct{}{}
	}
	var syncPorts []providerModel.Port
	if len(orphanInstanceIDs) > 0 {
		if err := global.APP_DB.Where("provider_id = ? AND (is_automatic = ? OR port_type = ?)",
			prov.ID, true, "range_mapped").Find(&syncPorts).Error; err != nil {
			return 0, 0, fmt.Errorf("查询孤立实例自动端口映射失败: %v", err)
		}
	}
	portsByInstance := make(map[uint][]providerModel.Port, len(orphanedInstances))
	for _, port := range syncPorts {
		if _, orphaned := orphanInstanceIDs[port.InstanceID]; orphaned && shouldDeleteSyncCandidate(port.ID, includedPortIDs, excludedPortIDs) {
			portsByInstance[port.InstanceID] = append(portsByInstance[port.InstanceID], port)
		}
	}

	instanceByID := make(map[uint]providerModel.Instance, len(orphanedInstances))
	for _, inst := range orphanedInstances {
		instanceByID[inst.ID] = inst
	}
	allSelectedPorts := make([]providerModel.Port, 0, len(syncPorts))
	for instanceID, ports := range portsByInstance {
		inst := instanceByID[instanceID]
		s.removePortMappingsFromNode(ctx, provInstance, prov, &inst, ports)
		allSelectedPorts = append(allSelectedPorts, ports...)
	}

	if len(allSelectedPorts) > 0 {
		deletedPortsForProvider, err := s.deleteSyncPortsInShortTransaction(ctx, prov.ID, allSelectedPorts)
		if err != nil {
			return 0, 0, err
		}
		cleanedPorts = deletedPortsForProvider
	}

	return len(dbInstances), cleanedPorts, nil
}

type remoteInstanceIdentitySet struct {
	ids   map[string]struct{}
	names map[string]struct{}
}

func buildRemoteInstanceIdentitySet(remoteInstances []provider.Instance) remoteInstanceIdentitySet {
	identities := remoteInstanceIdentitySet{
		ids:   make(map[string]struct{}, len(remoteInstances)),
		names: make(map[string]struct{}, len(remoteInstances)),
	}
	for _, instance := range remoteInstances {
		if identity := strings.TrimSpace(instance.ID); identity != "" {
			identities.ids[identity] = struct{}{}
		}
		if name := strings.TrimSpace(instance.Name); name != "" {
			identities.names[name] = struct{}{}
		}
	}
	return identities
}

func remoteInstanceMatchesDBInstance(providerType string, remoteIdentities remoteInstanceIdentitySet, instance providerModel.Instance) bool {
	providerType = strings.ToLower(strings.TrimSpace(providerType))
	providerID := strings.TrimSpace(instance.ProviderVMID)
	if providerID != "" {
		if _, ok := remoteIdentities.ids[providerID]; ok {
			return true
		}
		if providerType == "proxmox" || providerType == "proxmoxve" || providerType == "pve" {
			return false
		}
	}
	_, ok := remoteIdentities.names[strings.TrimSpace(instance.Name)]
	return ok
}

func isSafeSyncOrphanCandidate(instance providerModel.Instance, now time.Time) bool {
	if constant.IsBusyStatus(instance.Status) {
		return false
	}
	return instance.CreatedAt.IsZero() || now.Sub(instance.CreatedAt) >= syncOrphanGracePeriod
}

func (s *TaskService) excludeInstancesWithActiveTasks(instances []providerModel.Instance) ([]providerModel.Instance, error) {
	if len(instances) == 0 {
		return instances, nil
	}
	instanceIDs := make([]uint, 0, len(instances))
	for _, instance := range instances {
		instanceIDs = append(instanceIDs, instance.ID)
	}
	var activeInstanceIDs []uint
	if err := global.APP_DB.Model(&adminModel.Task{}).
		Where("instance_id IN ? AND status IN ?", instanceIDs, []string{"pending", "processing", "running", "cancelling"}).
		Distinct().Pluck("instance_id", &activeInstanceIDs).Error; err != nil {
		return nil, err
	}
	active := make(map[uint]struct{}, len(activeInstanceIDs))
	for _, id := range activeInstanceIDs {
		active[id] = struct{}{}
	}
	filtered := make([]providerModel.Instance, 0, len(instances))
	for _, instance := range instances {
		if _, ok := active[instance.ID]; !ok {
			filtered = append(filtered, instance)
		}
	}
	return filtered, nil
}

func (s *TaskService) deleteSyncPortsInShortTransaction(ctx context.Context, providerID uint, ports []providerModel.Port) (int, error) {
	totalDeleted := 0
	for start := 0; start < len(ports); start += syncPortDeleteBatchSize {
		end := start + syncPortDeleteBatchSize
		if end > len(ports) {
			end = len(ports)
		}
		deleted, err := s.deleteSyncPortBatchInShortTransaction(ctx, providerID, ports[start:end])
		if err != nil {
			return totalDeleted, err
		}
		totalDeleted += deleted
	}
	return totalDeleted, nil
}

func (s *TaskService) deleteSyncPortBatchInShortTransaction(ctx context.Context, providerID uint, ports []providerModel.Port) (int, error) {
	portIDs := make([]uint, 0, len(ports))
	releasedPorts := make([]int, 0, len(ports))
	sshInstanceIDs := make(map[uint]struct{})
	for _, port := range ports {
		portIDs = append(portIDs, port.ID)
		releasedPorts = append(releasedPorts, port.HostPort)
		if port.IsSSH {
			sshInstanceIDs[port.InstanceID] = struct{}{}
		}
	}

	result := 0
	err := database.GetDatabaseService().ExecuteTransaction(ctx, func(tx *gorm.DB) error {
		deleteResult := tx.Unscoped().Where("id IN ?", portIDs).Delete(&providerModel.Port{})
		if deleteResult.Error != nil {
			return fmt.Errorf("删除同步端口映射失败: %w", deleteResult.Error)
		}
		result = int(deleteResult.RowsAffected)
		if len(sshInstanceIDs) > 0 {
			ids := make([]uint, 0, len(sshInstanceIDs))
			for id := range sshInstanceIDs {
				ids = append(ids, id)
			}
			if err := tx.Model(&providerModel.Instance{}).Where("id IN ?", ids).Update("ssh_port", 0).Error; err != nil {
				return fmt.Errorf("清除同步实例SSH端口引用失败: %w", err)
			}
		}
		if len(releasedPorts) > 0 {
			portMappingService := resources.PortMappingService{}
			if err := portMappingService.OptimizeNextAvailablePortInTx(tx, providerID, releasedPorts); err != nil {
				return fmt.Errorf("回收同步端口失败: %w", err)
			}
		}
		return nil
	})
	if err != nil {
		return 0, err
	}
	return result, nil
}

// PreviewSyncPortMappings 生成端口映射同步预览，不修改数据库或节点侧规则。
func (s *TaskService) PreviewSyncPortMappings(ctx context.Context, req *adminModel.SyncPortMappingsTaskRequest, ownerAdminID uint) (*adminModel.SyncPortMappingsPreviewResponse, error) {
	var providers []providerModel.Provider
	query := global.APP_DB.Where("status = ?", "active")
	if ownerAdminID > 0 {
		query = query.Where("owner_admin_id = ?", ownerAdminID)
	}
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

	remoteInstanceMap := buildRemoteInstanceIdentitySet(remoteInstances)

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
	orphanCandidates := make([]providerModel.Instance, 0, len(dbInstances))
	now := time.Now()
	for _, dbInst := range dbInstances {
		if !remoteInstanceMatchesDBInstance(prov.Type, remoteInstanceMap, dbInst) && isSafeSyncOrphanCandidate(dbInst, now) {
			orphanCandidates = append(orphanCandidates, dbInst)
		}
	}
	orphanCandidates, err = s.excludeInstancesWithActiveTasks(orphanCandidates)
	if err != nil {
		preview.Error = fmt.Sprintf("查询孤立实例活动任务失败: %v", err)
		preview.Healthy = false
		return preview
	}
	orphanCandidateIDs := make(map[uint]struct{}, len(orphanCandidates))
	for _, candidate := range orphanCandidates {
		orphanCandidateIDs[candidate.ID] = struct{}{}
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

	var candidatePorts []providerModel.Port
	if err := global.APP_DB.Where("provider_id = ? AND (is_automatic = ? OR port_type = ?)",
		prov.ID, true, "range_mapped").Find(&candidatePorts).Error; err != nil {
		preview.Error = fmt.Sprintf("查询同步候选失败: %v", err)
		preview.Healthy = false
		return preview
	}
	for _, port := range candidatePorts {
		inst, instanceExists := instanceMap[port.InstanceID]
		if prov.NetworkType == "no_port_mapping" {
			appendCandidate(port, inst, "no_port_mapping")
			continue
		}
		if _, orphaned := orphanCandidateIDs[port.InstanceID]; orphaned && instanceExists {
			appendCandidate(port, inst, "orphan_instance")
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

	portByInstance := make(map[uint][]providerModel.Port)
	for _, p := range filteredPorts {
		portByInstance[p.InstanceID] = append(portByInstance[p.InstanceID], p)
	}

	for instanceID, ports := range portByInstance {
		// 先清理节点侧的实际端口映射规则
		if inst, ok := instanceMap[instanceID]; ok {
			s.removePortMappingsFromNode(ctx, provInstance, prov, &inst, ports)
		}
	}

	cleaned, err := s.deleteSyncPortsInShortTransaction(ctx, prov.ID, filteredPorts)
	if err != nil {
		return 0, fmt.Errorf("清理无端口映射自动端口映射事务失败: %w", err)
	}
	return cleaned, nil
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
	if portMappingType == "proxmox" || portMappingType == "proxmoxve" || portMappingType == "kubevirt" || portMappingType == "qemu" || utils.IsVMOnlyProvider(portMappingType) {
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
		providerInstanceID := instance.ProviderInstanceIdentifier()
		for _, p := range ports {
			if p.MappingType == "controller" {
				continue
			}
			var removeErr error
			switch providerType {
			case "lxd":
				if lxdProv, ok := provInstance.(*lxd.LXDProvider); ok {
					removeErr = lxdProv.RemovePortMapping(providerInstanceID, p.HostPort, p.Protocol, prov.IPv4PortMappingMethod)
				}
			case "incus":
				if incusProv, ok := provInstance.(*incus.IncusProvider); ok {
					removeErr = incusProv.RemovePortMapping(providerInstanceID, p.HostPort, p.Protocol, prov.IPv4PortMappingMethod)
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
