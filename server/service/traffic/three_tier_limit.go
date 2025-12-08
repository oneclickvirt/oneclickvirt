package traffic

import (
	"context"
	"fmt"
	"time"

	"oneclickvirt/global"
	adminModel "oneclickvirt/model/admin"
	"oneclickvirt/model/provider"
	"oneclickvirt/model/user"

	"go.uber.org/zap"
)

// ThreeTierLimitService 三层级流量限制服务
// 实现实例级、用户级、Provider级的独立流量限制
type ThreeTierLimitService struct {
	service *Service
}

// NewThreeTierLimitService 创建三层级流量限制服务
func NewThreeTierLimitService() *ThreeTierLimitService {
	return &ThreeTierLimitService{
		service: NewService(),
	}
}

// TrafficLimitLevel 流量限制层级
type TrafficLimitLevel string

const (
	LimitLevelInstance TrafficLimitLevel = "instance" // 实例层级
	LimitLevelUser     TrafficLimitLevel = "user"     // 用户层级
	LimitLevelProvider TrafficLimitLevel = "provider" // Provider层级
)

// CheckAllTrafficLimits 检查所有三层级的流量限制
// 按优先级顺序检查: Provider > User > Instance
func (s *ThreeTierLimitService) CheckAllTrafficLimits(ctx context.Context) error {
	global.APP_LOG.Info("开始三层级流量限制检查")

	// 第一层：检查Provider层级（最高优先级）
	if err := s.CheckAllProvidersTrafficLimit(ctx); err != nil {
		global.APP_LOG.Error("Provider层级流量检查失败", zap.Error(err))
	}

	// 第二层：检查用户层级
	if err := s.CheckAllUsersTrafficLimit(ctx); err != nil {
		global.APP_LOG.Error("用户层级流量检查失败", zap.Error(err))
	}

	// 第三层：检查实例层级（最低优先级）
	if err := s.CheckAllInstancesTrafficLimit(ctx); err != nil {
		global.APP_LOG.Error("实例层级流量检查失败", zap.Error(err))
	}

	global.APP_LOG.Info("三层级流量限制检查完成")
	return nil
}

// ============ 实例层级流量限制 ============

// CheckAllInstancesTrafficLimit 检查所有实例的流量限制
func (s *ThreeTierLimitService) CheckAllInstancesTrafficLimit(ctx context.Context) error {
	// 获取所有活跃实例（未被用户级或Provider级限制的）
	var instances []provider.Instance
	err := global.APP_DB.Where("status NOT IN (?) AND traffic_limited = ? AND (traffic_limit_reason = ? OR traffic_limit_reason = ?)",
		[]string{"deleted", "deleting"}, false, "", "instance").
		Limit(1000). // 限制最多1000个实例
		Find(&instances).Error
	if err != nil {
		return fmt.Errorf("获取实例列表失败: %w", err)
	}

	limitedCount := 0
	for _, instance := range instances {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		isLimited, err := s.CheckInstanceTrafficLimit(instance.ID)
		if err != nil {
			global.APP_LOG.Error("检查实例流量限制失败",
				zap.Uint("instanceID", instance.ID),
				zap.Error(err))
			continue
		}

		if isLimited {
			limitedCount++
		}
	}

	global.APP_LOG.Info("实例层级流量检查完成",
		zap.Int("总实例数", len(instances)),
		zap.Int("超限实例数", limitedCount))
	return nil
}

// CheckInstanceTrafficLimit 检查单个实例的流量限制
// 返回是否被限制
func (s *ThreeTierLimitService) CheckInstanceTrafficLimit(instanceID uint) (bool, error) {
	var instance provider.Instance
	if err := global.APP_DB.First(&instance, instanceID).Error; err != nil {
		return false, fmt.Errorf("获取实例信息失败: %w", err)
	}

	// 检查实例所属 Provider 是否启用流量统计
	var p provider.Provider
	if err := global.APP_DB.Select("enable_traffic_control").First(&p, instance.ProviderID).Error; err != nil {
		global.APP_LOG.Warn("获取Provider流量统计开关失败，跳过实例检查",
			zap.Uint("instanceID", instanceID),
			zap.Uint("providerID", instance.ProviderID),
			zap.Error(err))
		return false, nil
	}

	// 如果Provider未启用流量统计，解除可能存在的实例层级限制
	if !p.EnableTrafficControl {
		if instance.TrafficLimited && instance.TrafficLimitReason == "instance" {
			return s.unlimitInstance(instanceID, "Provider已禁用流量统计")
		}
		return false, nil
	}

	// 如果实例已经被更高层级限制，跳过
	if instance.TrafficLimited && instance.TrafficLimitReason != "" && instance.TrafficLimitReason != "instance" {
		return true, nil // 已被用户或Provider层级限制
	}

	// 如果实例没有设置流量限制（MaxTraffic=0），跳过
	if instance.MaxTraffic <= 0 {
		// 如果之前是实例层级限制的，现在解除
		if instance.TrafficLimited && instance.TrafficLimitReason == "instance" {
			return s.unlimitInstance(instanceID, "实例无流量限制")
		}
		return false, nil
	}

	// 获取实例当月流量
	now := time.Now()
	year := now.Year()
	month := int(now.Month())

	// 使用统一的流量查询服务
	queryService := NewQueryService()
	monthlyStats, err := queryService.GetInstanceMonthlyTraffic(instanceID, year, month)
	if err != nil {
		global.APP_LOG.Warn("获取实例 pmacct 流量失败",
			zap.Uint("instanceID", instanceID),
			zap.Error(err))
		return false, fmt.Errorf("获取实例流量失败: %w", err)
	}

	usedTraffic := int64(monthlyStats.ActualUsageMB)

	// 不再更新instance.used_traffic字段（已删除）

	// 检查是否超限
	if usedTraffic >= instance.MaxTraffic {
		// 实例超限，仅停止该实例
		global.APP_LOG.Info("实例流量超限",
			zap.Uint("instanceID", instanceID),
			zap.String("instanceName", instance.Name),
			zap.Int64("usedTraffic", usedTraffic),
			zap.Int64("maxTraffic", instance.MaxTraffic))

		return s.limitInstance(instanceID, "instance", fmt.Sprintf("实例流量超限: %dMB/%dMB", usedTraffic, instance.MaxTraffic))
	}

	// 未超限，如果之前是实例层级限制的，解除限制
	if instance.TrafficLimited && instance.TrafficLimitReason == "instance" {
		return s.unlimitInstance(instanceID, "实例流量恢复正常")
	}

	return false, nil
}

// limitInstance 限制单个实例
func (s *ThreeTierLimitService) limitInstance(instanceID uint, reason string, message string) (bool, error) {
	var instance provider.Instance
	if err := global.APP_DB.First(&instance, instanceID).Error; err != nil {
		return false, err
	}

	// 保存需要使用的字段
	userID := instance.UserID
	providerID := instance.ProviderID

	// 标记实例为受限状态
	updates := map[string]interface{}{
		"traffic_limited":      true,
		"traffic_limit_reason": reason,
		"status":               "stopped",
	}

	if err := global.APP_DB.Model(&instance).Updates(updates).Error; err != nil {
		return false, fmt.Errorf("标记实例为受限状态失败: %w", err)
	}

	// 创建停止任务
	if err := s.createStopTask(userID, instanceID, providerID, message); err != nil {
		global.APP_LOG.Error("创建实例停止任务失败",
			zap.Uint("instanceID", instanceID),
			zap.Error(err))
	}

	return true, nil
}

// unlimitInstance 解除单个实例的限制
func (s *ThreeTierLimitService) unlimitInstance(instanceID uint, reason string) (bool, error) {
	updates := map[string]interface{}{
		"traffic_limited":      false,
		"traffic_limit_reason": "",
	}

	if err := global.APP_DB.Model(&provider.Instance{}).Where("id = ?", instanceID).Updates(updates).Error; err != nil {
		return false, fmt.Errorf("解除实例限制失败: %w", err)
	}

	global.APP_LOG.Info("解除实例流量限制",
		zap.Uint("instanceID", instanceID),
		zap.String("reason", reason))

	return false, nil
}

// ============ 用户层级流量限制 ============

// CheckAllUsersTrafficLimit 检查所有用户的流量限制
func (s *ThreeTierLimitService) CheckAllUsersTrafficLimit(ctx context.Context) error {
	// 获取所有活跃用户
	var users []user.User
	if err := global.APP_DB.Where("status = ?", 1).Find(&users).Error; err != nil {
		return fmt.Errorf("获取用户列表失败: %w", err)
	}

	limitedCount := 0
	for _, u := range users {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		isLimited, err := s.CheckUserTrafficLimit(u.ID)
		if err != nil {
			global.APP_LOG.Error("检查用户流量限制失败",
				zap.Uint("userID", u.ID),
				zap.Error(err))
			continue
		}

		if isLimited {
			limitedCount++
		}
	}

	global.APP_LOG.Info("用户层级流量检查完成",
		zap.Int("总用户数", len(users)),
		zap.Int("超限用户数", limitedCount))
	return nil
}

// CheckUserTrafficLimit 检查单个用户的流量限制
// 返回是否被限制
func (s *ThreeTierLimitService) CheckUserTrafficLimit(userID uint) (bool, error) {
	var u user.User
	if err := global.APP_DB.First(&u, userID).Error; err != nil {
		return false, fmt.Errorf("获取用户信息失败: %w", err)
	}

	// 检查用户的所有实例所在的Provider是否都禁用了流量统计
	var enabledProviderCount int64
	err := global.APP_DB.Table("instances").
		Joins("LEFT JOIN providers ON instances.provider_id = providers.id").
		Where("instances.user_id = ?", userID).
		Where("providers.enable_traffic_control = ?", true).
		Count(&enabledProviderCount).Error

	if err != nil {
		global.APP_LOG.Warn("检查Provider流量统计状态失败", zap.Error(err))
	}

	// 如果所有Provider都禁用了流量统计，解除用户层级限制
	if enabledProviderCount == 0 {
		if u.TrafficLimited {
			return s.unlimitUserInstances(userID, "所有Provider已禁用流量统计")
		}
		return false, nil
	}

	// checkAndResetMonthlyTraffic方法已删除，流量重置由单独的调度器处理

	// 重新加载用户数据
	if err := global.APP_DB.First(&u, userID).Error; err != nil {
		return false, fmt.Errorf("重新加载用户信息失败: %w", err)
	}

	// 自动同步用户流量限额
	if u.TotalTraffic == 0 {
		levelLimits, exists := global.APP_CONFIG.Quota.LevelLimits[u.Level]
		if exists && levelLimits.MaxTraffic > 0 {
			u.TotalTraffic = levelLimits.MaxTraffic
			if err := global.APP_DB.Model(&u).Update("total_traffic", u.TotalTraffic).Error; err != nil {
				global.APP_LOG.Warn("同步用户流量限额失败", zap.Error(err))
			}
		}
	}

	// 如果用户没有流量限制，解除可能存在的用户级限制
	if u.TotalTraffic <= 0 {
		if u.TrafficLimited {
			return s.unlimitUserInstances(userID, "用户无流量限制")
		}
		return false, nil
	}

	// 从pmacct_traffic_records实时汇总用户当月总流量（已包含流量模式和倍率计算）
	now := time.Now()
	year := now.Year()
	month := int(now.Month())

	// 使用统一的流量查询服务（会自动包含软删除实例的流量统计）
	queryService := NewQueryService()
	monthlyStats, err := queryService.GetUserMonthlyTraffic(userID, year, month)
	if err != nil {
		return false, fmt.Errorf("获取用户流量失败: %w", err)
	}

	totalUsedMB := int64(monthlyStats.ActualUsageMB)

	// 更新用户已使用流量
	if err := global.APP_DB.Model(&u).Update("used_traffic", totalUsedMB).Error; err != nil {
		return false, fmt.Errorf("更新用户流量失败: %w", err)
	}

	// 检查是否超限
	if totalUsedMB >= u.TotalTraffic {
		// 用户超限，停止用户所有实例
		global.APP_LOG.Info("用户流量超限",
			zap.Uint("userID", userID),
			zap.String("username", u.Username),
			zap.Int64("usedTraffic", totalUsedMB),
			zap.Int64("totalTraffic", u.TotalTraffic))

		return s.limitUserInstances(userID, fmt.Sprintf("用户流量超限: %dMB/%dMB", totalUsedMB, u.TotalTraffic))
	}

	// 未超限，解除用户级限制
	if u.TrafficLimited {
		return s.unlimitUserInstances(userID, "用户流量恢复正常")
	}

	return false, nil
}

// limitUserInstances 限制用户的所有实例
func (s *ThreeTierLimitService) limitUserInstances(userID uint, message string) (bool, error) {
	// 标记用户为受限状态
	if err := global.APP_DB.Model(&user.User{}).Where("id = ?", userID).Update("traffic_limited", true).Error; err != nil {
		return false, fmt.Errorf("标记用户为受限状态失败: %w", err)
	}

	// 批量更新实例状态，避免逐个UPDATE
	updates := map[string]interface{}{
		"traffic_limited":      true,
		"traffic_limit_reason": "user",
		"status":               "stopped",
	}

	result := global.APP_DB.Model(&provider.Instance{}).
		Where("user_id = ? AND status = ?", userID, "running").
		Updates(updates)

	if result.Error != nil {
		return false, fmt.Errorf("批量标记实例为受限状态失败: %w", result.Error)
	}

	// 获取被停止的实例ID列表用于创建任务
	var instances []provider.Instance
	if err := global.APP_DB.Select("id, provider_id").
		Where("user_id = ? AND traffic_limited = ? AND traffic_limit_reason = ?",
			userID, true, "user").
		Find(&instances).Error; err != nil {
		global.APP_LOG.Error("获取受限实例列表失败", zap.Error(err))
		// 不返回错误，状态已更新，任务创建是次要的
	} else if len(instances) > 0 {
		// 批量创建停止任务
		if err := s.batchCreateStopTasks(userID, instances, message); err != nil {
			global.APP_LOG.Error("批量创建实例停止任务失败",
				zap.Uint("userID", userID),
				zap.Int("instanceCount", len(instances)),
				zap.Error(err))
		}
	}

	global.APP_LOG.Info("已批量限制用户所有实例",
		zap.Uint("userID", userID),
		zap.Int64("影响实例数", result.RowsAffected))

	return true, nil
}

// unlimitUserInstances 解除用户所有实例的限制
func (s *ThreeTierLimitService) unlimitUserInstances(userID uint, reason string) (bool, error) {
	// 标记用户为非受限状态
	if err := global.APP_DB.Model(&user.User{}).Where("id = ?", userID).Update("traffic_limited", false).Error; err != nil {
		return false, fmt.Errorf("解除用户限制失败: %w", err)
	}

	// 解除所有因用户层级限制的实例
	updates := map[string]interface{}{
		"traffic_limited":      false,
		"traffic_limit_reason": "",
	}

	if err := global.APP_DB.Model(&provider.Instance{}).
		Where("user_id = ? AND traffic_limit_reason = ?", userID, "user").
		Updates(updates).Error; err != nil {
		return false, fmt.Errorf("解除用户实例限制失败: %w", err)
	}

	global.APP_LOG.Info("解除用户流量限制",
		zap.Uint("userID", userID),
		zap.String("reason", reason))

	return false, nil
}

// ============ Provider层级流量限制 ============

// CheckAllProvidersTrafficLimit 检查所有Provider的流量限制
func (s *ThreeTierLimitService) CheckAllProvidersTrafficLimit(ctx context.Context) error {
	// 获取所有活跃Provider
	var providers []provider.Provider
	if err := global.APP_DB.Where("status IN (?)", []string{"active", "partial"}).Find(&providers).Error; err != nil {
		return fmt.Errorf("获取Provider列表失败: %w", err)
	}

	limitedCount := 0
	for _, p := range providers {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		isLimited, err := s.CheckProviderTrafficLimit(p.ID)
		if err != nil {
			global.APP_LOG.Error("检查Provider流量限制失败",
				zap.Uint("providerID", p.ID),
				zap.Error(err))
			continue
		}

		if isLimited {
			limitedCount++
		}
	}

	global.APP_LOG.Info("Provider层级流量检查完成",
		zap.Int("总Provider数", len(providers)),
		zap.Int("超限Provider数", limitedCount))
	return nil
}

// CheckProviderTrafficLimit 检查单个Provider的流量限制
// 返回是否被限制
// 该方法假设Provider的流量数据已经通过SyncProviderInstancesTraffic更新
// 如果需要确保数据最新，调用方应先调用SyncProviderInstancesTraffic
func (s *ThreeTierLimitService) CheckProviderTrafficLimit(providerID uint) (bool, error) {
	var p provider.Provider
	if err := global.APP_DB.First(&p, providerID).Error; err != nil {
		return false, fmt.Errorf("获取Provider信息失败: %w", err)
	}

	// 如果Provider未启用流量统计和限制，直接跳过检查
	if !p.EnableTrafficControl {
		// 如果之前被限制过，解除限制
		if p.TrafficLimited {
			return s.unlimitProviderInstances(providerID, "Provider已禁用流量统计和限制")
		}
		return false, nil
	}

	// checkAndResetProviderMonthlyTraffic方法已删除，流量重置由单独的调度器处理

	// 重新加载 Provider 数据
	if err := global.APP_DB.First(&p, providerID).Error; err != nil {
		return false, fmt.Errorf("重新加载Provider信息失败: %w", err)
	}

	// 如果Provider没有流量限制，解除可能存在的限制
	if p.MaxTraffic <= 0 {
		if p.TrafficLimited {
			return s.unlimitProviderInstances(providerID, "Provider无流量限制")
		}
		return false, nil
	}

	// 使用统一的流量查询服务获取Provider当月流量
	now := time.Now()
	year := now.Year()
	month := int(now.Month())
	queryService := NewQueryService()
	monthlyStats, err := queryService.GetProviderMonthlyTraffic(providerID, year, month)
	if err != nil {
		global.APP_LOG.Error("获取Provider流量失败",
			zap.Uint("providerID", providerID),
			zap.Error(err))
		return false, fmt.Errorf("获取Provider流量失败: %w", err)
	}
	totalUsedMB := int64(monthlyStats.ActualUsageMB)

	global.APP_LOG.Debug("检查Provider流量限制",
		zap.Uint("providerID", providerID),
		zap.String("providerName", p.Name),
		zap.Int64("usedTraffic", totalUsedMB),
		zap.Int64("maxTraffic", p.MaxTraffic))

	// 检查是否超限
	if totalUsedMB >= p.MaxTraffic {
		// Provider超限，停止Provider所有实例，禁止申请
		global.APP_LOG.Info("Provider流量超限",
			zap.Uint("providerID", providerID),
			zap.String("providerName", p.Name),
			zap.Int64("usedTraffic", totalUsedMB),
			zap.Int64("maxTraffic", p.MaxTraffic))

		return s.limitProviderInstances(providerID, fmt.Sprintf("Provider流量超限: %dMB/%dMB", totalUsedMB, p.MaxTraffic))
	}

	// 未超限，解除Provider级限制
	if p.TrafficLimited {
		return s.unlimitProviderInstances(providerID, "Provider流量恢复正常")
	}

	return false, nil
}

// limitProviderInstances 限制Provider的所有实例
func (s *ThreeTierLimitService) limitProviderInstances(providerID uint, message string) (bool, error) {
	// 标记Provider为受限状态
	if err := global.APP_DB.Model(&provider.Provider{}).Where("id = ?", providerID).
		Update("traffic_limited", true).Error; err != nil {
		return false, fmt.Errorf("标记Provider为受限状态失败: %w", err)
	}

	// 批量更新实例状态，避免逐个UPDATE
	updates := map[string]interface{}{
		"traffic_limited":      true,
		"traffic_limit_reason": "provider",
		"status":               "stopped",
	}

	result := global.APP_DB.Model(&provider.Instance{}).
		Where("provider_id = ? AND status = ?", providerID, "running").
		Updates(updates)

	if result.Error != nil {
		return false, fmt.Errorf("批量标记实例为受限状态失败: %w", result.Error)
	}

	// 获取被停止的实例ID列表用于创建任务
	var instances []provider.Instance
	if err := global.APP_DB.Select("id, user_id").
		Where("provider_id = ? AND traffic_limited = ? AND traffic_limit_reason = ?",
			providerID, true, "provider").
		Find(&instances).Error; err != nil {
		global.APP_LOG.Error("获取受限实例列表失败", zap.Error(err))
		// 不返回错误，状态已更新，任务创建是次要的
	} else if len(instances) > 0 {
		// 批量创建停止任务
		// 这里的userID来自instance，需要特殊处理
		if err := s.batchCreateStopTasksForProvider(providerID, instances, message); err != nil {
			global.APP_LOG.Error("批量创建实例停止任务失败",
				zap.Uint("providerID", providerID),
				zap.Int("instanceCount", len(instances)),
				zap.Error(err))
		}
	}

	global.APP_LOG.Info("已批量限制Provider所有实例",
		zap.Uint("providerID", providerID),
		zap.Int64("影响实例数", result.RowsAffected))

	return true, nil
}

// unlimitProviderInstances 解除Provider所有实例的限制
func (s *ThreeTierLimitService) unlimitProviderInstances(providerID uint, reason string) (bool, error) {
	// 标记Provider为非受限状态
	if err := global.APP_DB.Model(&provider.Provider{}).Where("id = ?", providerID).
		Update("traffic_limited", false).Error; err != nil {
		return false, fmt.Errorf("解除Provider限制失败: %w", err)
	}

	// 解除所有因Provider层级限制的实例
	updates := map[string]interface{}{
		"traffic_limited":      false,
		"traffic_limit_reason": "",
	}

	if err := global.APP_DB.Model(&provider.Instance{}).
		Where("provider_id = ? AND traffic_limit_reason = ?", providerID, "provider").
		Updates(updates).Error; err != nil {
		return false, fmt.Errorf("解除Provider实例限制失败: %w", err)
	}

	global.APP_LOG.Info("解除Provider流量限制",
		zap.Uint("providerID", providerID),
		zap.String("reason", reason))

	return false, nil
}

// ============ 辅助方法 ============

// createStopTask 创建停止实例的任务
func (s *ThreeTierLimitService) createStopTask(userID, instanceID, providerID uint, message string) error {
	// 构建任务数据
	taskData := fmt.Sprintf(`{"instanceId":%d,"providerId":%d}`, instanceID, providerID)

	task := &adminModel.Task{
		TaskType:         "stop",
		Status:           "pending",
		Progress:         0,
		StatusMessage:    message,
		TaskData:         taskData,
		UserID:           userID,
		ProviderID:       &providerID,
		InstanceID:       &instanceID,
		TimeoutDuration:  600,
		IsForceStoppable: true,
		CanForceStop:     false,
	}

	if err := global.APP_DB.Create(task).Error; err != nil {
		return err
	}

	// 触发调度器立即处理任务
	if global.APP_SCHEDULER != nil {
		global.APP_SCHEDULER.TriggerTaskProcessing()
	}

	return nil
}

// batchCreateStopTasks 批量创建停止任务（用户层级限流）
func (s *ThreeTierLimitService) batchCreateStopTasks(userID uint, instances []provider.Instance, message string) error {
	if len(instances) == 0 {
		return nil
	}

	tasks := make([]*adminModel.Task, 0, len(instances))
	for _, instance := range instances {
		taskData := fmt.Sprintf(`{"instanceId":%d,"providerId":%d}`, instance.ID, instance.ProviderID)

		task := &adminModel.Task{
			TaskType:         "stop",
			Status:           "pending",
			Progress:         0,
			StatusMessage:    message,
			TaskData:         taskData,
			UserID:           userID,
			ProviderID:       &instance.ProviderID,
			InstanceID:       &instance.ID,
			TimeoutDuration:  600,
			IsForceStoppable: true,
			CanForceStop:     false,
		}
		tasks = append(tasks, task)
	}

	// 批量插入任务
	if err := global.APP_DB.CreateInBatches(tasks, 100).Error; err != nil {
		return err
	}

	// 触发调度器立即处理任务
	if global.APP_SCHEDULER != nil {
		global.APP_SCHEDULER.TriggerTaskProcessing()
	}

	return nil
}

// batchCreateStopTasksForProvider 批量创建停止任务（Provider层级限流）
func (s *ThreeTierLimitService) batchCreateStopTasksForProvider(providerID uint, instances []provider.Instance, message string) error {
	if len(instances) == 0 {
		return nil
	}

	tasks := make([]*adminModel.Task, 0, len(instances))
	for _, instance := range instances {
		taskData := fmt.Sprintf(`{"instanceId":%d,"providerId":%d}`, instance.ID, providerID)

		task := &adminModel.Task{
			TaskType:         "stop",
			Status:           "pending",
			Progress:         0,
			StatusMessage:    message,
			TaskData:         taskData,
			UserID:           instance.UserID,
			ProviderID:       &providerID,
			InstanceID:       &instance.ID,
			TimeoutDuration:  600,
			IsForceStoppable: true,
			CanForceStop:     false,
		}
		tasks = append(tasks, task)
	}

	// 批量插入任务
	if err := global.APP_DB.CreateInBatches(tasks, 100).Error; err != nil {
		return err
	}

	// 触发调度器立即处理任务
	if global.APP_SCHEDULER != nil {
		global.APP_SCHEDULER.TriggerTaskProcessing()
	}

	return nil
}
