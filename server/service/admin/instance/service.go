package instance

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"oneclickvirt/service/database"
	"oneclickvirt/service/interfaces"
	"oneclickvirt/service/resources"
	"oneclickvirt/service/traffic"
	"oneclickvirt/utils"
	"time"

	"oneclickvirt/global"
	"oneclickvirt/model/admin"
	adminModel "oneclickvirt/model/admin"
	providerModel "oneclickvirt/model/provider"
	userModel "oneclickvirt/model/user"
	"oneclickvirt/service/cache"

	"go.uber.org/zap"
	"gorm.io/gorm"
)

// Service 管理员实例管理服务
type Service struct {
	taskService interfaces.TaskServiceInterface
}

// NewService 创建实例管理服务
func NewService(taskService interfaces.TaskServiceInterface) *Service {
	return &Service{
		taskService: taskService,
	}
}

func firstOwnerAdminID(ownerAdminID []uint) uint {
	if len(ownerAdminID) == 0 {
		return 0
	}
	return ownerAdminID[0]
}

// GetInstanceByID 根据ID获取实例详情
func (s *Service) GetInstanceByID(instanceID uint, ownerAdminID ...uint) (*providerModel.Instance, error) {
	var instance providerModel.Instance

	if err := global.APP_DB.First(&instance, instanceID).Error; err != nil {
		global.APP_LOG.Error("获取实例详情失败", zap.Error(err), zap.Uint("instanceID", instanceID))
		return nil, err
	}
	if err := s.checkInstanceOwnerAdmin(&instance, firstOwnerAdminID(ownerAdminID)); err != nil {
		return nil, err
	}

	return &instance, nil
}

// GetInstanceList 获取实例列表
func (s *Service) GetInstanceList(req admin.InstanceListRequest, ownerAdminID uint) ([]admin.InstanceManageResponse, int64, error) {
	var instances []providerModel.Instance
	var total int64

	// 管理员查看所有实例，不限制user_id
	query := global.APP_DB.Model(&providerModel.Instance{})
	// 普通管理员数据隔离：只能看到归属自己的Provider下的实例
	if ownerAdminID > 0 {
		var providerIDs []uint
		if err := global.APP_DB.Model(&providerModel.Provider{}).
			Where("owner_admin_id = ?", ownerAdminID).
			Pluck("id", &providerIDs).Error; err != nil {
			return nil, 0, err
		}
		if len(providerIDs) == 0 {
			return []admin.InstanceManageResponse{}, 0, nil
		}
		query = query.Where("provider_id IN ?", providerIDs)
	}
	// 使用索引友好的查询条件
	if req.Name != "" {
		query = query.Where("name LIKE ?", "%"+req.Name+"%")
	}
	if req.ProviderName != "" {
		query = query.Where("provider LIKE ?", "%"+req.ProviderName+"%")
	}
	if req.OwnerName != "" {
		// 通过用户名搜索，需要连接 users 表
		query = query.Joins("LEFT JOIN users ON users.id = instances.user_id").
			Where("users.username LIKE ?", "%"+req.OwnerName+"%")
	}
	if req.Status != "" {
		query = query.Where("status = ?", req.Status)
	}
	if req.InstanceType != "" {
		query = query.Where("instance_type = ?", req.InstanceType)
	}
	// 如果指定了用户ID，则按用户筛选
	if req.UserID != 0 {
		query = query.Where("user_id = ?", req.UserID)
	}

	// 先计数，避免不必要的数据查询
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}

	// 如果没有数据，直接返回
	if total == 0 {
		return []admin.InstanceManageResponse{}, 0, nil
	}

	offset := (req.Page - 1) * req.PageSize
	// 只查询需要的字段，减少数据传输
	if err := query.Offset(offset).Limit(req.PageSize).Find(&instances).Error; err != nil {
		return nil, 0, err
	}

	// 批量查询用户信息
	var userIDs []uint
	userIDSet := make(map[uint]bool)
	for _, instance := range instances {
		if instance.UserID != 0 && !userIDSet[instance.UserID] {
			userIDs = append(userIDs, instance.UserID)
			userIDSet[instance.UserID] = true
		}
	}

	var users []userModel.User
	if len(userIDs) > 0 {
		// 只查询必要字段，减少数据传输
		if err := global.APP_DB.Select("id, username, email, level, status").
			Where("id IN ?", userIDs).
			Find(&users).Error; err != nil {
			// 查询用户失败时记录日志但不中断流程
			global.APP_LOG.Warn("批量查询实例关联用户信息失败，将返回不含用户名的列表",
				zap.Error(err),
				zap.Int("userCount", len(userIDs)))
		}
	}

	// 将用户信息按ID映射
	userMap := make(map[uint]userModel.User)
	for _, user := range users {
		userMap[user.ID] = user
	}

	// 批量查询Provider信息
	var providerIDs []uint
	providerIDSet := make(map[uint]bool)
	for _, instance := range instances {
		if instance.ProviderID > 0 && !providerIDSet[instance.ProviderID] {
			providerIDs = append(providerIDs, instance.ProviderID)
			providerIDSet[instance.ProviderID] = true
		}
	}

	var providers []providerModel.Provider
	if len(providerIDs) > 0 {
		// 只查询必要字段
		if err := global.APP_DB.Select("id, name, type, region, status, network_type").
			Where("id IN ?", providerIDs).
			Find(&providers).Error; err != nil {
			global.APP_LOG.Warn("批量查询Provider信息失败", zap.Error(err))
		}
	}

	// 将Provider信息按ID映射
	providerMap := make(map[uint]providerModel.Provider)
	for _, provider := range providers {
		providerMap[provider.ID] = provider
	}

	// 批量查询SSH端口映射
	var instanceIDs []uint
	for _, instance := range instances {
		instanceIDs = append(instanceIDs, instance.ID)
	}

	var sshPorts []providerModel.Port
	if len(instanceIDs) > 0 {
		// 使用索引idx_instance_ssh (instance_id, is_ssh, status)
		if err := global.APP_DB.Select("instance_id, host_port").
			Where("instance_id IN ? AND is_ssh = ? AND status = ?", instanceIDs, true, "active").
			Find(&sshPorts).Error; err != nil {
			global.APP_LOG.Warn("批量查询SSH端口信息失败", zap.Error(err))
		}
	}

	// 将SSH端口映射按instance_id映射
	sshPortMap := make(map[uint]providerModel.Port)
	for _, port := range sshPorts {
		sshPortMap[port.InstanceID] = port
	}

	// 批量查询实例当月流量历史数据
	now := time.Now()
	year := now.Year()
	month := int(now.Month())

	// 使用流量查询服务批量获取实例流量数据
	trafficQueryService := traffic.NewQueryService()
	trafficStatsMap, err := trafficQueryService.BatchGetInstancesMonthlyTraffic(instanceIDs, year, month)
	if err != nil {
		global.APP_LOG.Warn("批量查询实例流量数据失败", zap.Error(err))
		trafficStatsMap = make(map[uint]*traffic.TrafficStats) // 使用空map
	}

	var instanceResponses []admin.InstanceManageResponse
	for _, instance := range instances {
		var userName, providerName string

		// 从预加载的map中获取用户名
		if instance.UserID != 0 {
			if user, ok := userMap[instance.UserID]; ok {
				userName = user.Username
			} else {
				userName = "未知用户"
			}
		} else {
			userName = "系统"
		}

		// 获取Provider名称
		if instance.Provider != "" {
			providerName = instance.Provider
		} else {
			providerName = "未知提供商"
		}

		// 从预加载的map中获取SSH端口映射
		var sshPort int
		if sshPortMapping, ok := sshPortMap[instance.ID]; ok {
			sshPort = sshPortMapping.HostPort // 使用映射的公网端口
		} else {
			sshPort = instance.SSHPort // fallback到默认值
		}

		// 创建修改后的实例副本，更新SSH端口
		modifiedInstance := instance
		if sshPort > 0 {
			modifiedInstance.SSHPort = sshPort
		}

		instanceResponse := admin.InstanceManageResponse{
			Instance:       modifiedInstance,
			UserName:       userName,
			ProviderName:   providerName,
			ProviderType:   "",
			HealthStatus:   "healthy",
			UsedTrafficIn:  0,
			UsedTrafficOut: 0,
			HasSshMapping:  false,
		}

		// 判断是否有SSH端口映射
		if _, hasSSH := sshPortMap[instance.ID]; hasSSH {
			instanceResponse.HasSshMapping = true
		}

		// 从流量查询服务获取的数据中获取（已应用Provider的流量计算模式）
		if stats, ok := trafficStatsMap[instance.ID]; ok {
			// 将字节转换为MB
			instanceResponse.UsedTrafficIn = stats.RxBytes / 1048576
			instanceResponse.UsedTrafficOut = stats.TxBytes / 1048576
		}

		// 从预加载的Provider map中获取Provider类型
		if instance.ProviderID > 0 {
			if prov, ok := providerMap[instance.ProviderID]; ok {
				instanceResponse.ProviderType = prov.Type
				// 如果实例的NetworkType为空或为默认值nat_ipv4，使用Provider的NetworkType作为fallback
				// 兼容旧实例（创建时未设置NetworkType字段）
				if instanceResponse.NetworkType == "" || instanceResponse.NetworkType == "nat_ipv4" {
					if prov.NetworkType != "" {
						instanceResponse.NetworkType = prov.NetworkType
					}
				}
			}
		}
		instanceResponses = append(instanceResponses, instanceResponse)
	}

	return instanceResponses, total, nil
}

// CreateInstance 创建实例，返回异步创建任务。
func (s *Service) CreateInstance(req admin.CreateInstanceRequest, ownerAdminID ...uint) (*adminModel.Task, error) {
	ownerID := firstOwnerAdminID(ownerAdminID)

	// 检查提供商是否存在和冻结状态（这些是非并发敏感的快速读，无需放入创建事务）
	provider, err := s.resolveCreateProvider(req, ownerID)
	if err != nil {
		return nil, err
	}
	req.Provider = provider.Name
	if req.InstanceType == "" {
		if utils.IsDockerFamilyProvider(provider.Type) || provider.Type == "lxd" || provider.Type == "incus" {
			req.InstanceType = "container"
		} else {
			req.InstanceType = "vm"
		}
	}
	if req.InstanceType != "container" && req.InstanceType != "vm" {
		return nil, errors.New("实例类型必须为container或vm")
	}
	if req.Image == "" {
		return nil, errors.New("镜像不能为空")
	}
	if req.CPU <= 0 {
		return nil, errors.New("CPU必须大于0")
	}
	if req.Memory <= 0 {
		return nil, errors.New("内存必须大于0")
	}
	if req.Disk <= 0 {
		return nil, errors.New("磁盘必须大于0")
	}

	// 检查提供商是否冻结
	if provider.IsFrozen {
		return nil, fmt.Errorf("提供商 %s 已被冻结，无法创建实例", req.Provider)
	}

	// 检查提供商是否过期
	if provider.ExpiresAt != nil && provider.ExpiresAt.Before(time.Now()) {
		return nil, fmt.Errorf("提供商 %s 已过期，无法创建实例", req.Provider)
	}
	providerAvailable := (provider.ConnectionType == "agent" && provider.AgentStatus == "online") ||
		(provider.ConnectionType != "agent" && (provider.Status == "active" || provider.Status == "partial"))
	if !providerAvailable {
		return nil, fmt.Errorf("提供商 %s 当前不可用", req.Provider)
	}
	if req.InstanceType == "container" && !provider.ContainerEnabled {
		return nil, fmt.Errorf("提供商 %s 不支持容器实例", req.Provider)
	}
	if req.InstanceType == "vm" && !provider.VirtualMachineEnabled {
		return nil, fmt.Errorf("提供商 %s 不支持虚拟机实例", req.Provider)
	}

	// 检查Provider节点实例数量限制（实时查询，排除已删除/失败的实例）
	if provider.MaxContainerInstances > 0 || provider.MaxVMInstances > 0 {
		var containerCount, vmCount int64
		if err := global.APP_DB.Model(&providerModel.Instance{}).
			Where("provider_id = ? AND instance_type = ? AND status NOT IN ?",
				provider.ID, "container", []string{"deleted", "deleting", "failed"}).
			Count(&containerCount).Error; err == nil {
			if req.InstanceType == "container" && provider.MaxContainerInstances > 0 && int(containerCount) >= provider.MaxContainerInstances {
				return nil, fmt.Errorf("节点容器数量已达上限：%d/%d", containerCount, provider.MaxContainerInstances)
			}
		}
		if err := global.APP_DB.Model(&providerModel.Instance{}).
			Where("provider_id = ? AND instance_type = ? AND status NOT IN ?",
				provider.ID, "vm", []string{"deleted", "deleting", "failed"}).
			Count(&vmCount).Error; err == nil {
			if req.InstanceType == "vm" && provider.MaxVMInstances > 0 && int(vmCount) >= provider.MaxVMInstances {
				return nil, fmt.Errorf("节点虚拟机数量已达上限：%d/%d", vmCount, provider.MaxVMInstances)
			}
		}
	}

	if req.UserID > 0 {
		quotaService := resources.NewQuotaService()
		quotaResult, err := quotaService.ValidateAdminInstanceCreation(resources.ResourceRequest{
			UserID:       req.UserID,
			ProviderID:   provider.ID,
			CPU:          req.CPU,
			Memory:       req.Memory,
			Disk:         req.Disk * 1024,
			Bandwidth:    req.Bandwidth,
			InstanceType: req.InstanceType,
		})
		if err != nil {
			return nil, fmt.Errorf("配额验证失败: %v", err)
		}
		if quotaResult != nil && !quotaResult.Allowed {
			return nil, fmt.Errorf("无法为用户创建实例: %s", quotaResult.Reason)
		}
	}
	networkType := req.NetworkType
	if networkType == "" {
		networkType = provider.NetworkType
	}
	if networkType == "" {
		networkType = "nat_ipv4"
	}

	taskReq := adminModel.CreateInstanceTaskRequest{
		ProviderId:   provider.ID,
		AdminDirect:  true,
		Name:         req.Name,
		Image:        req.Image,
		CPU:          req.CPU,
		Memory:       req.Memory,
		Disk:         req.Disk,
		Bandwidth:    req.Bandwidth,
		InstanceType: req.InstanceType,
		NetworkType:  networkType,
		GpuEnabled:   req.GpuEnabled,
		GpuDeviceIds: req.GpuDeviceIds,
	}
	taskData, err := json.Marshal(taskReq)
	if err != nil {
		return nil, fmt.Errorf("序列化创建任务失败: %w", err)
	}
	task, err := s.taskService.CreateTask(req.UserID, &provider.ID, nil, "create", string(taskData), 1800)
	if err != nil {
		return nil, err
	}
	if global.APP_SCHEDULER != nil {
		global.APP_SCHEDULER.TriggerTaskProcessing()
	}
	return task, nil
}

func (s *Service) resolveCreateProvider(req admin.CreateInstanceRequest, ownerAdminID uint) (*providerModel.Provider, error) {
	var provider providerModel.Provider
	providerID := req.ProviderID
	if providerID == 0 && req.ProviderIDCamel > 0 {
		providerID = req.ProviderIDCamel
	}
	query := global.APP_DB.Model(&providerModel.Provider{})
	if providerID > 0 {
		query = query.Where("id = ?", providerID)
	} else {
		if req.Provider == "" {
			return nil, errors.New("必须指定provider或provider_id")
		}
		query = query.Where("name = ?", req.Provider)
	}
	if ownerAdminID > 0 {
		query = query.Where("owner_admin_id = ?", ownerAdminID)
	}

	if err := query.First(&provider).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			if ownerAdminID > 0 {
				return nil, errors.New("Provider不存在或无权操作该Provider")
			}
			if providerID > 0 {
				return nil, fmt.Errorf("Provider不存在: %d", providerID)
			}
			return nil, fmt.Errorf("提供商不存在: %s", req.Provider)
		}
		return nil, fmt.Errorf("查询Provider失败: %v", err)
	}
	if providerID > 0 && req.Provider != "" && req.Provider != provider.Name {
		return nil, errors.New("provider_id与provider名称不一致")
	}
	return &provider, nil
}

// UpdateInstance 更新实例
func (s *Service) UpdateInstance(req admin.UpdateInstanceRequest, ownerAdminID ...uint) error {
	var instance providerModel.Instance
	if err := global.APP_DB.First(&instance, req.ID).Error; err != nil {
		return err
	}
	if err := s.checkInstanceOwnerAdmin(&instance, firstOwnerAdminID(ownerAdminID)); err != nil {
		return err
	}

	instance.Name = utils.SanitizeShellArg(req.Name)
	instance.CPU = req.CPU
	instance.Memory = req.Memory
	instance.Disk = req.Disk
	instance.Status = req.Status

	dbService := database.GetDatabaseService()
	return dbService.ExecuteTransaction(context.Background(), func(tx *gorm.DB) error {
		return tx.Save(&instance).Error
	})
}

// DeleteInstance 删除实例 - 使用异步任务机制
func (s *Service) DeleteInstance(instanceID uint, ownerAdminID ...uint) error {
	lk := getAdminInstanceActionLock(instanceID)
	lk.mu.Lock()
	defer func() {
		lk.mu.Unlock()
		releaseAdminInstanceActionLock(instanceID)
	}()

	// 获取实例信息
	var instance providerModel.Instance
	if err := global.APP_DB.First(&instance, instanceID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return fmt.Errorf("实例不存在")
		}
		return fmt.Errorf("获取实例信息失败: %v", err)
	}
	if err := s.checkInstanceOwnerAdmin(&instance, firstOwnerAdminID(ownerAdminID)); err != nil {
		return err
	}

	// 检查实例状态，避免重复删除
	if instance.Status == "deleting" {
		return fmt.Errorf("实例正在删除中")
	}

	if err := s.ensureNoActiveInstanceTask(instance.ID); err != nil {
		return err
	}

	// 创建管理员删除任务
	taskData := map[string]interface{}{
		"instanceId":     instanceID,
		"providerId":     instance.ProviderID,
		"adminOperation": true, // 标记为管理员操作
	}

	taskDataJSON, err := json.Marshal(taskData)
	if err != nil {
		return fmt.Errorf("序列化任务数据失败: %v", err)
	}

	// 创建删除任务，设置为不可被用户取消
	task, err := s.taskService.CreateTask(instance.UserID, &instance.ProviderID, &instanceID, "delete", string(taskDataJSON), 0)
	if err != nil {
		return fmt.Errorf("创建删除任务失败: %v", err)
	}

	// 标记任务为管理员操作，不允许用户取消
	if err := global.APP_DB.Model(task).Update("is_force_stoppable", false).Error; err != nil {
		global.APP_LOG.Warn("更新任务可取消状态失败", zap.Uint("taskId", task.ID), zap.Error(err))
	}

	// 更新实例状态为删除中
	if err := global.APP_DB.Model(&instance).Update("status", "deleting").Error; err != nil {
		global.APP_LOG.Warn("更新实例状态失败", zap.Uint("instanceId", instanceID), zap.Error(err))
	}

	global.APP_LOG.Info("管理员创建删除任务成功",
		zap.Uint("instanceId", instanceID),
		zap.String("instanceName", instance.Name),
		zap.Uint("taskId", task.ID))

	return nil
}

// InstanceAction 管理员执行实例操作
func (s *Service) InstanceAction(instanceID uint, req admin.InstanceActionRequest, ownerAdminID uint) error {
	lk := getAdminInstanceActionLock(instanceID)
	lk.mu.Lock()
	defer func() {
		lk.mu.Unlock()
		releaseAdminInstanceActionLock(instanceID)
	}()

	var instance providerModel.Instance
	if err := global.APP_DB.First(&instance, instanceID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return errors.New("实例不存在")
		}
		return fmt.Errorf("获取实例信息失败: %v", err)
	}
	if err := s.checkInstanceOwnerAdmin(&instance, ownerAdminID); err != nil {
		return err
	}
	if err := validateAdminInstanceAction(instance.Status, req.Action); err != nil {
		return err
	}
	if err := s.ensureNoActiveInstanceTask(instance.ID); err != nil {
		return err
	}

	taskData := map[string]interface{}{
		"instanceId": instance.ID,
		"providerId": instance.ProviderID,
	}
	if req.Action == "reset" || req.Action == "rebuild" {
		taskData["originalStatus"] = instance.Status
		if req.Image != "" {
			taskData["resetImage"] = req.Image
		}
	}
	if req.Action == "delete" {
		taskData["adminOperation"] = true
	}

	taskDataJSON, err := json.Marshal(taskData)
	if err != nil {
		return fmt.Errorf("序列化任务数据失败: %v", err)
	}

	timeout := 1800
	if req.Action == "delete" {
		timeout = 0
	}
	task, err := s.taskService.CreateTask(instance.UserID, &instance.ProviderID, &instance.ID, req.Action, string(taskDataJSON), timeout)
	if err != nil {
		return fmt.Errorf("创建任务失败: %v", err)
	}
	if req.Action == "delete" {
		if err := global.APP_DB.Model(task).Update("is_force_stoppable", false).Error; err != nil {
			return fmt.Errorf("更新任务权限失败: %v", err)
		}
	}

	instance.Status = nextAdminInstanceStatus(req.Action)
	if err := global.APP_DB.Model(&instance).Update("status", instance.Status).Error; err != nil {
		return fmt.Errorf("更新实例状态失败: %v", err)
	}

	cacheService := cache.GetUserCacheService()
	cacheService.InvalidateUserCache(instance.UserID)
	cacheService.InvalidateInstanceCache(instance.ID)
	return nil
}

func (s *Service) BatchInstanceAction(req admin.BatchInstanceActionRequest, ownerAdminID uint) admin.BatchInstanceActionResponse {
	response := admin.BatchInstanceActionResponse{
		Action:  req.Action,
		Total:   len(req.InstanceIDs),
		Results: make([]admin.BatchInstanceActionResult, 0, len(req.InstanceIDs)),
	}
	seen := make(map[uint]struct{}, len(req.InstanceIDs))
	for _, instanceID := range req.InstanceIDs {
		if instanceID == 0 {
			response.FailCount++
			response.Results = append(response.Results, admin.BatchInstanceActionResult{
				InstanceID: instanceID,
				Success:    false,
				Error:      "无效的实例ID",
			})
			continue
		}
		if _, exists := seen[instanceID]; exists {
			response.FailCount++
			response.Results = append(response.Results, admin.BatchInstanceActionResult{
				InstanceID: instanceID,
				Success:    false,
				Error:      "实例ID重复",
			})
			continue
		}
		seen[instanceID] = struct{}{}

		if err := s.InstanceAction(instanceID, admin.InstanceActionRequest{Action: req.Action}, ownerAdminID); err != nil {
			response.FailCount++
			response.Results = append(response.Results, admin.BatchInstanceActionResult{
				InstanceID: instanceID,
				Success:    false,
				Error:      err.Error(),
			})
			continue
		}
		response.SuccessCount++
		response.Results = append(response.Results, admin.BatchInstanceActionResult{
			InstanceID: instanceID,
			Success:    true,
			Message:    "操作已提交",
		})
	}
	return response
}

func (s *Service) checkInstanceOwnerAdmin(instance *providerModel.Instance, ownerAdminID uint) error {
	if ownerAdminID == 0 {
		return nil
	}
	var count int64
	if err := global.APP_DB.Model(&providerModel.Provider{}).
		Where("id = ? AND owner_admin_id = ?", instance.ProviderID, ownerAdminID).
		Count(&count).Error; err != nil {
		return fmt.Errorf("检查Provider归属失败: %v", err)
	}
	if count == 0 {
		return errors.New("无权操作该实例")
	}
	return nil
}

func (s *Service) ensureNoActiveInstanceTask(instanceID uint) error {
	activeTypes := []string{"start", "stop", "restart", "reset", "rebuild", "delete", "reset-password"}
	var existingTask adminModel.Task
	err := global.APP_DB.Where("instance_id = ? AND task_type IN ? AND status IN ?", instanceID, activeTypes, []string{"pending", "processing", "running", "cancelling"}).
		First(&existingTask).Error
	if err == nil {
		return fmt.Errorf("实例已有%s任务正在进行", existingTask.TaskType)
	}
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil
	}
	return err
}

func validateAdminInstanceAction(status, action string) error {
	switch action {
	case "start":
		if status != "stopped" {
			return errors.New("实例状态不允许启动")
		}
	case "stop":
		if status != "running" {
			return errors.New("实例状态不允许停止")
		}
	case "restart":
		if status != "running" {
			return errors.New("实例状态不允许重启")
		}
	case "reset", "rebuild":
		if status != "running" && status != "stopped" {
			return errors.New("实例状态不允许重置")
		}
	case "delete":
		if status == "deleting" || status == "deleted" {
			return errors.New("实例正在删除中")
		}
	default:
		return errors.New("不支持的操作类型")
	}
	return nil
}

func nextAdminInstanceStatus(action string) string {
	statusMap := map[string]string{
		"start":   "starting",
		"stop":    "stopping",
		"restart": "restarting",
		"reset":   "resetting",
		"rebuild": "rebuilding",
		"delete":  "deleting",
	}
	return statusMap[action]
}

// ResetInstancePassword 管理员重置实例密码（异步任务）
func (s *Service) ResetInstancePassword(instanceID uint, ownerAdminID ...uint) (uint, error) {
	lk := getAdminInstanceActionLock(instanceID)
	lk.mu.Lock()
	defer func() {
		lk.mu.Unlock()
		releaseAdminInstanceActionLock(instanceID)
	}()

	// 获取实例信息
	var instance providerModel.Instance
	if err := global.APP_DB.Where("id = ?", instanceID).First(&instance).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return 0, errors.New("实例不存在")
		}
		return 0, err
	}
	if err := s.checkInstanceOwnerAdmin(&instance, firstOwnerAdminID(ownerAdminID)); err != nil {
		return 0, err
	}

	// 检查实例状态
	if instance.Status != "running" {
		return 0, errors.New("参数错误: 只有运行中的实例才能重置密码")
	}

	if err := s.ensureNoActiveInstanceTask(instance.ID); err != nil {
		return 0, err
	}

	// 创建任务数据
	taskData := map[string]interface{}{
		"instanceId": instance.ID,
		"providerId": instance.ProviderID,
	}

	taskDataJSON, err := json.Marshal(taskData)
	if err != nil {
		return 0, fmt.Errorf("序列化任务数据失败: %v", err)
	}

	// 管理员任务使用实例的用户ID
	task, err := s.taskService.CreateTask(instance.UserID, &instance.ProviderID, &instance.ID, "reset-password", string(taskDataJSON), 600) // 10分钟超时
	if err != nil {
		global.APP_LOG.Error("管理员创建密码重置任务失败",
			zap.Uint("instanceID", instanceID),
			zap.Error(err))
		return 0, fmt.Errorf("创建密码重置任务失败: %v", err)
	}

	global.APP_LOG.Info("管理员创建密码重置任务成功",
		zap.Uint("instanceID", instanceID),
		zap.Uint("taskID", task.ID),
		zap.String("instanceName", instance.Name),
		zap.Uint("userID", instance.UserID))

	return task.ID, nil
}

// GetInstanceNewPassword 管理员获取实例重置后的新密码（通过任务ID）
func (s *Service) GetInstanceNewPassword(instanceID uint, taskID uint, ownerAdminID ...uint) (string, int64, error) {
	// 获取实例信息
	var instance providerModel.Instance
	if err := global.APP_DB.Where("id = ?", instanceID).First(&instance).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return "", 0, errors.New("实例不存在")
		}
		return "", 0, err
	}
	if err := s.checkInstanceOwnerAdmin(&instance, firstOwnerAdminID(ownerAdminID)); err != nil {
		return "", 0, err
	}

	// 获取任务信息
	var task adminModel.Task
	if err := global.APP_DB.Where("id = ? AND instance_id = ? AND task_type = 'reset-password'", taskID, instanceID).First(&task).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return "", 0, errors.New("任务不存在")
		}
		return "", 0, err
	}

	// 检查任务状态
	if task.Status != "completed" {
		return "", 0, errors.New("任务尚未完成")
	}

	// 解析任务结果
	var taskResult adminModel.ResetPasswordTaskResult

	if err := json.Unmarshal([]byte(task.TaskData), &taskResult); err != nil {
		return "", 0, errors.New("解析任务结果失败")
	}

	if taskResult.NewPassword == "" {
		return "", 0, errors.New("任务结果中没有新密码")
	}

	return taskResult.NewPassword, taskResult.ResetTime, nil
}
