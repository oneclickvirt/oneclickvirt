package task

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"oneclickvirt/constant"
	"oneclickvirt/global"
	adminModel "oneclickvirt/model/admin"
	providerModel "oneclickvirt/model/provider"
	providerCore "oneclickvirt/provider"
	providerService "oneclickvirt/service/provider"
	"oneclickvirt/utils"

	"go.uber.org/zap"
)

type repairPortMappingPlan struct {
	providers       map[uint]providerModel.Provider
	instances       map[uint]providerModel.Instance
	portsByProvider map[uint][]providerModel.Port
}

type containerRuntimePortBinding struct {
	HostPort string `json:"HostPort"`
}

func quoteRemoteShellArg(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'"
}

func parseContainerRuntimePortBindings(output string) (map[string]map[int]struct{}, error) {
	output = strings.TrimSpace(output)
	if jsonStart := strings.Index(output, "{"); jsonStart > 0 {
		output = output[jsonStart:]
	}
	var raw map[string][]containerRuntimePortBinding
	if err := json.NewDecoder(strings.NewReader(output)).Decode(&raw); err != nil {
		return nil, fmt.Errorf("解析容器运行时端口绑定失败: %w", err)
	}
	bindings := make(map[string]map[int]struct{}, len(raw))
	for guestProtocol, hostBindings := range raw {
		ports := make(map[int]struct{}, len(hostBindings))
		for _, binding := range hostBindings {
			hostPort, err := strconv.Atoi(binding.HostPort)
			if err == nil && hostPort > 0 {
				ports[hostPort] = struct{}{}
			}
		}
		bindings[strings.ToLower(guestProtocol)] = ports
	}
	return bindings, nil
}

func containerRuntimeCLI(providerType string) string {
	switch utils.NormalizeProviderType(providerType) {
	case "podman":
		return "podman"
	case "containerd":
		return "nerdctl"
	default:
		return "docker"
	}
}

func verifyContainerRuntimePortMappings(ctx context.Context, providerInstance providerCore.Provider, providerType, instanceName string, ports []providerModel.Port) error {
	command := fmt.Sprintf(
		"%s inspect %s --format '{{json .NetworkSettings.Ports}}'",
		containerRuntimeCLI(providerType),
		quoteRemoteShellArg(instanceName),
	)
	output, err := providerInstance.ExecuteSSHCommand(ctx, command)
	if err != nil {
		return fmt.Errorf("读取容器运行时端口绑定失败: %w", err)
	}
	bindings, err := parseContainerRuntimePortBindings(output)
	if err != nil {
		return err
	}

	missing := make([]string, 0)
	for _, port := range ports {
		endpoints, expandErr := expandPortEndpoints(port)
		if expandErr != nil {
			return expandErr
		}
		protocols := []string{strings.ToLower(port.Protocol)}
		if len(protocols) == 1 && (protocols[0] == "" || protocols[0] == "both") {
			protocols = []string{"tcp", "udp"}
		}
		for _, endpoint := range endpoints {
			for _, protocol := range protocols {
				guestProtocol := fmt.Sprintf("%d/%s", endpoint.guest, protocol)
				if _, exists := bindings[guestProtocol][endpoint.host]; !exists {
					missing = append(missing, fmt.Sprintf("%d:%d/%s", endpoint.host, endpoint.guest, protocol))
				}
			}
		}
	}
	if len(missing) > 0 {
		if len(missing) > 10 {
			missing = append(missing[:10], "...")
		}
		return fmt.Errorf("容器重启后仍缺少数据库要求的端口绑定: %s", strings.Join(missing, ", "))
	}
	return nil
}

func uniqueUintCount(values []uint) int {
	set := make(map[uint]struct{}, len(values))
	for _, value := range values {
		if value > 0 {
			set[value] = struct{}{}
		}
	}
	return len(set)
}

func compactRepairPreviewProviders(preview *adminModel.RepairPortMappingsPreviewResponse) {
	providers := preview.Providers[:0]
	for _, providerPreview := range preview.Providers {
		if providerPreview.CandidateCount == 0 && providerPreview.SkippedCount == 0 {
			continue
		}
		providers = append(providers, providerPreview)
	}
	preview.Providers = providers
	preview.ProviderCount = len(providers)
}

func repairPortMappingSkipReason(providerInfo providerModel.Provider, instance *providerModel.Instance, port providerModel.Port) string {
	switch strings.ToLower(strings.TrimSpace(providerInfo.Status)) {
	case "active", "partial":
	default:
		return "provider_unavailable"
	}
	switch port.Status {
	case "active", "failed", "error":
	default:
		return "status_not_repairable"
	}
	if instance == nil || instance.ID == 0 {
		return "instance_missing"
	}
	if constant.IsBusyStatus(instance.Status) {
		return "instance_busy"
	}
	if constant.IsTerminalStatus(instance.Status) {
		return "instance_unavailable"
	}

	mappingType := port.MappingType
	if mappingType == "" {
		mappingType = "node"
	}
	if mappingType == "controller" {
		if effectivePortCount(port) != 1 {
			return "controller_range_unsupported"
		}
		if strings.ToLower(port.Protocol) != "tcp" {
			return "controller_protocol_unsupported"
		}
		if strings.TrimSpace(port.InternalHost) == "" && strings.TrimSpace(instance.PrivateIP) == "" {
			return "target_missing"
		}
		return ""
	}
	if mappingType != "node" {
		return "mapping_type_unsupported"
	}

	switch providerInfo.NetworkType {
	case "dedicated_ipv4", "dedicated_ipv4_ipv6", "ipv6_only", "no_port_mapping":
		return "network_mode_has_no_node_mapping"
	}
	if utils.IsDockerFamilyProvider(providerInfo.Type) {
		if instance.Status != constant.InstanceStatusRunning {
			return "container_not_running"
		}
		return ""
	}
	if normalizePortMappingMethod(port.MappingMethod) == "native" {
		return "native_mapping_not_repairable"
	}
	if strings.TrimSpace(instance.PrivateIP) == "" {
		return "target_missing"
	}
	return ""
}

func (s *TaskService) loadRepairPortMappingPlan(ctx context.Context, req *adminModel.RepairPortMappingsRequest, ownerAdminID uint) (*repairPortMappingPlan, *adminModel.RepairPortMappingsPreviewResponse, error) {
	db := global.APP_DB.WithContext(ctx)
	var providers []providerModel.Provider
	providerQuery := db.Model(&providerModel.Provider{})
	if ownerAdminID > 0 {
		providerQuery = providerQuery.Where("owner_admin_id = ?", ownerAdminID)
	}
	if len(req.ProviderIDs) > 0 {
		providerQuery = providerQuery.Where("id IN ?", req.ProviderIDs)
	}
	if err := providerQuery.Order("id ASC").Find(&providers).Error; err != nil {
		return nil, nil, fmt.Errorf("查询Provider列表失败: %w", err)
	}
	if len(providers) == 0 {
		return &repairPortMappingPlan{
				providers:       map[uint]providerModel.Provider{},
				instances:       map[uint]providerModel.Instance{},
				portsByProvider: map[uint][]providerModel.Port{},
			}, &adminModel.RepairPortMappingsPreviewResponse{
				Providers: []adminModel.RepairProviderPortMappingsPreview{},
			}, nil
	}

	providerIDs := make([]uint, 0, len(providers))
	providerMap := make(map[uint]providerModel.Provider, len(providers))
	for _, providerInfo := range providers {
		providerIDs = append(providerIDs, providerInfo.ID)
		providerMap[providerInfo.ID] = providerInfo
	}

	var ports []providerModel.Port
	portQuery := db.Where("provider_id IN ?", providerIDs)
	if len(req.PortIDs) > 0 {
		portQuery = portQuery.Where("id IN ?", req.PortIDs)
	}
	if err := portQuery.Order("provider_id ASC, id ASC").Find(&ports).Error; err != nil {
		return nil, nil, fmt.Errorf("查询端口映射失败: %w", err)
	}
	if len(req.PortIDs) > 0 && len(ports) != uniqueUintCount(req.PortIDs) {
		return nil, nil, fmt.Errorf("部分端口映射不存在或不在当前管理员权限范围内")
	}

	instanceIDSet := make(map[uint]struct{}, len(ports))
	instanceIDs := make([]uint, 0, len(ports))
	for _, port := range ports {
		if _, exists := instanceIDSet[port.InstanceID]; !exists {
			instanceIDSet[port.InstanceID] = struct{}{}
			instanceIDs = append(instanceIDs, port.InstanceID)
		}
	}
	var instances []providerModel.Instance
	if len(instanceIDs) > 0 {
		if err := db.Where("id IN ?", instanceIDs).Find(&instances).Error; err != nil {
			return nil, nil, fmt.Errorf("查询实例列表失败: %w", err)
		}
	}
	instanceMap := make(map[uint]providerModel.Instance, len(instances))
	for _, instance := range instances {
		instanceMap[instance.ID] = instance
	}

	plan := &repairPortMappingPlan{
		providers:       providerMap,
		instances:       instanceMap,
		portsByProvider: make(map[uint][]providerModel.Port, len(providers)),
	}
	preview := &adminModel.RepairPortMappingsPreviewResponse{
		ProviderCount: len(providers),
		Providers:     make([]adminModel.RepairProviderPortMappingsPreview, 0, len(providers)),
	}
	previewByProvider := make(map[uint]*adminModel.RepairProviderPortMappingsPreview, len(providers))
	restartInstanceSet := make(map[uint]struct{})
	for _, providerInfo := range providers {
		preview.Providers = append(preview.Providers, adminModel.RepairProviderPortMappingsPreview{
			ProviderID:   providerInfo.ID,
			ProviderName: providerInfo.Name,
			Candidates:   []adminModel.RepairPortMappingCandidate{},
			Skipped:      []adminModel.RepairPortMappingSkipped{},
		})
		previewByProvider[providerInfo.ID] = &preview.Providers[len(preview.Providers)-1]
	}

	for _, port := range ports {
		providerInfo := providerMap[port.ProviderID]
		providerPreview := previewByProvider[port.ProviderID]
		instance, exists := instanceMap[port.InstanceID]
		var instancePtr *providerModel.Instance
		if exists {
			instanceCopy := instance
			instancePtr = &instanceCopy
		}
		reason := repairPortMappingSkipReason(providerInfo, instancePtr, port)
		if reason != "" {
			instanceName := ""
			if instancePtr != nil {
				instanceName = instancePtr.Name
			}
			providerPreview.Skipped = append(providerPreview.Skipped, adminModel.RepairPortMappingSkipped{
				PortID:       port.ID,
				InstanceID:   port.InstanceID,
				InstanceName: instanceName,
				ProviderID:   providerInfo.ID,
				ProviderName: providerInfo.Name,
				HostPort:     port.HostPort,
				Reason:       reason,
			})
			providerPreview.SkippedCount++
			preview.SkippedCount++
			continue
		}

		requiresRestart := utils.IsDockerFamilyProvider(providerInfo.Type) && port.MappingType != "controller"
		candidate := adminModel.RepairPortMappingCandidate{
			PortID:                  port.ID,
			InstanceID:              port.InstanceID,
			InstanceName:            instance.Name,
			ProviderID:              providerInfo.ID,
			ProviderName:            providerInfo.Name,
			HostPort:                port.HostPort,
			HostPortEnd:             port.HostPortEnd,
			GuestPort:               port.GuestPort,
			GuestPortEnd:            port.GuestPortEnd,
			PortCount:               effectivePortCount(port),
			Protocol:                port.Protocol,
			Status:                  port.Status,
			MappingType:             port.MappingType,
			PortType:                port.PortType,
			RequiresInstanceRestart: requiresRestart,
		}
		providerPreview.Candidates = append(providerPreview.Candidates, candidate)
		providerPreview.CandidateCount++
		providerPreview.RuleCount += candidate.PortCount
		preview.CandidateCount++
		preview.RuleCount += candidate.PortCount
		if requiresRestart {
			restartInstanceSet[port.InstanceID] = struct{}{}
		}
		plan.portsByProvider[port.ProviderID] = append(plan.portsByProvider[port.ProviderID], port)
	}
	preview.RequiresInstanceRestartCount = len(restartInstanceSet)
	compactRepairPreviewProviders(preview)

	return plan, preview, nil
}

func (s *TaskService) PreviewRepairPortMappings(ctx context.Context, req *adminModel.RepairPortMappingsRequest, ownerAdminID uint) (*adminModel.RepairPortMappingsPreviewResponse, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	_, preview, err := s.loadRepairPortMappingPlan(ctx, req, ownerAdminID)
	return preview, err
}

func (s *TaskService) CreateRepairPortMappingsTasks(ctx context.Context, userID uint, req *adminModel.RepairPortMappingsRequest, ownerAdminID uint) (*adminModel.RepairPortMappingsTaskResponse, error) {
	if len(req.PortIDs) == 0 {
		return nil, fmt.Errorf("必须选择至少一条端口映射")
	}
	plan, preview, err := s.loadRepairPortMappingPlan(ctx, req, ownerAdminID)
	if err != nil {
		return nil, err
	}
	if preview.CandidateCount != uniqueUintCount(req.PortIDs) {
		return nil, fmt.Errorf("选中的端口映射中包含当前不可修复的记录，请重新生成预览")
	}

	providerIDs := make([]uint, 0, len(plan.portsByProvider))
	for providerID := range plan.portsByProvider {
		providerIDs = append(providerIDs, providerID)
	}
	sort.Slice(providerIDs, func(i, j int) bool { return providerIDs[i] < providerIDs[j] })

	// A Provider may allow multiple concurrent tasks, but two repair jobs must never
	// rebuild the same runtime/firewall state at the same time. Serialize the short
	// check-and-create window and query all target Providers in one batch.
	s.repairSubmitMu.Lock()
	defer s.repairSubmitMu.Unlock()
	var activeProviderIDs []uint
	if err := global.APP_DB.WithContext(ctx).
		Model(&adminModel.Task{}).
		Where("provider_id IN ? AND task_type = ? AND status IN ?", providerIDs, "repair-port-mappings", []string{
			mainTaskStatusPending,
			mainTaskStatusProcessing,
			mainTaskStatusRunning,
			mainTaskStatusCancelling,
		}).
		Distinct("provider_id").
		Pluck("provider_id", &activeProviderIDs).Error; err != nil {
		return nil, fmt.Errorf("查询进行中的端口映射修复任务失败: %w", err)
	}
	activeProviders := make(map[uint]struct{}, len(activeProviderIDs))
	for _, providerID := range activeProviderIDs {
		activeProviders[providerID] = struct{}{}
	}

	response := &adminModel.RepairPortMappingsTaskResponse{
		Tasks:  []*adminModel.Task{},
		Failed: []adminModel.RepairPortMappingsTaskFailure{},
	}
	for _, providerID := range providerIDs {
		if _, active := activeProviders[providerID]; active {
			response.Failed = append(response.Failed, adminModel.RepairPortMappingsTaskFailure{
				ProviderID:   providerID,
				ProviderName: plan.providers[providerID].Name,
				Error:        "Provider已有端口映射修复任务正在进行",
			})
			continue
		}
		ports := plan.portsByProvider[providerID]
		portIDs := make([]uint, len(ports))
		for i, port := range ports {
			portIDs[i] = port.ID
		}
		taskData, marshalErr := json.Marshal(adminModel.RepairPortMappingsTaskRequest{PortIDs: portIDs})
		if marshalErr != nil {
			response.Failed = append(response.Failed, adminModel.RepairPortMappingsTaskFailure{
				ProviderID: providerID, ProviderName: plan.providers[providerID].Name, Error: marshalErr.Error(),
			})
			continue
		}
		timeout := utils.GetDefaultTaskTimeout("repair-port-mappings")
		task, createErr := s.CreateTask(userID, &providerID, nil, "repair-port-mappings", string(taskData), timeout)
		if createErr == nil {
			createErr = s.StartTask(task.ID)
		}
		if createErr != nil {
			if task != nil {
				_ = s.CompleteTask(task.ID, false, createErr.Error(), nil)
			}
			response.Failed = append(response.Failed, adminModel.RepairPortMappingsTaskFailure{
				ProviderID: providerID, ProviderName: plan.providers[providerID].Name, Error: createErr.Error(),
			})
			continue
		}
		response.Tasks = append(response.Tasks, task)
	}
	response.TaskCount = len(response.Tasks)
	response.FailedCount = len(response.Failed)
	if response.TaskCount == 0 {
		if response.FailedCount > 0 {
			return nil, fmt.Errorf("所有端口映射修复任务创建失败: %s", response.Failed[0].Error)
		}
		return nil, fmt.Errorf("所有端口映射修复任务创建失败")
	}
	return response, nil
}

func (s *TaskService) executeRepairPortMappingsTask(ctx context.Context, task *adminModel.Task) error {
	if task.ProviderID == nil {
		return fmt.Errorf("任务没有关联Provider")
	}
	var taskReq adminModel.RepairPortMappingsTaskRequest
	if err := json.Unmarshal([]byte(task.TaskData), &taskReq); err != nil {
		return fmt.Errorf("解析端口映射修复任务失败: %w", err)
	}
	if len(taskReq.PortIDs) == 0 {
		return fmt.Errorf("修复任务没有端口映射")
	}
	s.updateTaskProgress(task.ID, 5, "step.parseTaskData")

	req := &adminModel.RepairPortMappingsRequest{ProviderIDs: []uint{*task.ProviderID}, PortIDs: taskReq.PortIDs}
	plan, preview, err := s.loadRepairPortMappingPlan(ctx, req, 0)
	if err != nil {
		return err
	}
	if preview.CandidateCount != uniqueUintCount(taskReq.PortIDs) {
		return fmt.Errorf("端口映射状态已变化，请重新生成修复预览")
	}
	providerInfo := plan.providers[*task.ProviderID]
	ports := plan.portsByProvider[*task.ProviderID]
	if len(ports) == 0 {
		return fmt.Errorf("没有可修复的端口映射")
	}
	s.updateTaskProgress(task.ID, 15, "step.getProviderInfo")

	successful := make(map[uint]struct{}, len(ports))
	failed := make(map[uint]string)
	var providerInstance providerCore.Provider
	loadProvider := func() (providerCore.Provider, error) {
		if providerInstance != nil {
			return providerInstance, nil
		}
		loaded, loadErr := providerService.GetProviderInstanceByID(providerInfo.ID)
		if loadErr != nil {
			return nil, loadErr
		}
		providerInstance = loaded
		return providerInstance, nil
	}

	containerPortsByInstance := make(map[uint][]providerModel.Port)
	regularPorts := make([]providerModel.Port, 0, len(ports))
	for _, port := range ports {
		if utils.IsDockerFamilyProvider(providerInfo.Type) && port.MappingType != "controller" {
			containerPortsByInstance[port.InstanceID] = append(containerPortsByInstance[port.InstanceID], port)
		} else {
			regularPorts = append(regularPorts, port)
		}
	}

	processed := 0
	total := len(ports)
	for instanceID, instancePorts := range containerPortsByInstance {
		if err := ctx.Err(); err != nil {
			return err
		}
		instance := plan.instances[instanceID]
		providerInstanceID := instance.ProviderInstanceIdentifier()
		loaded, loadErr := loadProvider()
		if loadErr == nil {
			loadErr = loaded.RestartInstance(ctx, providerInstanceID)
		}
		if loadErr == nil {
			loadErr = verifyContainerRuntimePortMappings(ctx, loaded, providerInfo.Type, providerInstanceID, instancePorts)
		}
		for _, port := range instancePorts {
			if loadErr != nil {
				failed[port.ID] = loadErr.Error()
			} else {
				successful[port.ID] = struct{}{}
			}
			processed++
		}
		s.updateTaskProgress(task.ID, 15+processed*70/total, "step.repairingPortMappings")
	}

	var applier *portMappingApplier
	for _, port := range regularPorts {
		if err := ctx.Err(); err != nil {
			return err
		}
		instance := plan.instances[port.InstanceID]
		if applier == nil {
			if port.MappingType == "controller" {
				applier = newPortMappingApplier(ctx, nil, &providerInfo)
			} else {
				loaded, loadErr := loadProvider()
				if loadErr != nil {
					failed[port.ID] = loadErr.Error()
					processed++
					continue
				}
				applier = newPortMappingApplier(ctx, loaded, &providerInfo)
			}
		} else if applier.providerInstance == nil && port.MappingType != "controller" {
			loaded, loadErr := loadProvider()
			if loadErr != nil {
				failed[port.ID] = loadErr.Error()
				processed++
				continue
			}
			applier.providerInstance = loaded
		}
		if applyErr := applier.Apply(&instance, &port, true); applyErr != nil {
			failed[port.ID] = applyErr.Error()
		} else {
			successful[port.ID] = struct{}{}
		}
		processed++
		s.updateTaskProgress(task.ID, 15+processed*70/total, "step.repairingPortMappings")
	}
	if applier != nil {
		applier.Finish()
	}

	successIDs := make([]uint, 0, len(successful))
	for portID := range successful {
		successIDs = append(successIDs, portID)
	}
	failedIDs := make([]uint, 0, len(failed))
	for portID := range failed {
		failedIDs = append(failedIDs, portID)
	}
	if len(successIDs) > 0 {
		if err := global.APP_DB.Model(&providerModel.Port{}).Where("id IN ?", successIDs).Update("status", "active").Error; err != nil {
			return fmt.Errorf("批量更新修复成功状态失败: %w", err)
		}
	}
	if len(failedIDs) > 0 {
		if err := global.APP_DB.Model(&providerModel.Port{}).Where("id IN ?", failedIDs).Update("status", "failed").Error; err != nil {
			return fmt.Errorf("批量更新修复失败状态失败: %w", err)
		}
	}
	s.updateTaskProgress(task.ID, 95, "step.updatingPortStatus")

	global.APP_LOG.Info("端口映射修复任务完成",
		zap.Uint("taskId", task.ID),
		zap.Uint("providerId", providerInfo.ID),
		zap.Int("successful", len(successIDs)),
		zap.Int("failed", len(failedIDs)))
	if len(failed) > 0 {
		failureMessages := make([]string, 0, len(failed))
		for portID, failure := range failed {
			failureMessages = append(failureMessages, fmt.Sprintf("端口记录 %d: %s", portID, failure))
		}
		sort.Strings(failureMessages)
		if len(failureMessages) > 10 {
			failureMessages = append(failureMessages[:10], fmt.Sprintf("另有 %d 条失败", len(failed)-10))
		}
		return fmt.Errorf("已修复 %d 条，失败 %d 条；%s", len(successIDs), len(failedIDs), strings.Join(failureMessages, "; "))
	}
	return nil
}
