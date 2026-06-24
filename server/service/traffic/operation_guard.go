package traffic

import (
	"errors"
	"fmt"
	"strings"

	"oneclickvirt/global"

	"gorm.io/gorm"
)

// InstanceOperationLock 描述普通用户对实例操作的流量锁定状态。
type InstanceOperationLock struct {
	Locked  bool              `json:"locked"`
	Level   TrafficLimitLevel `json:"level"`
	Reason  string            `json:"reason"`
	Message string            `json:"message"`
}

func operationActionLabel(action string) string {
	switch strings.TrimSpace(action) {
	case "start":
		return "启动实例"
	case "stop":
		return "停止实例"
	case "restart":
		return "重启实例"
	case "reset":
		return "重装实例"
	case "delete":
		return "删除实例"
	case "reset-password":
		return "重置密码"
	case "ssh":
		return "SSH连接"
	case "exec":
		return "执行命令"
	case "sftp":
		return "SFTP文件操作"
	case "vnc":
		return "VNC连接"
	case "snapshot-create":
		return "创建快照"
	case "snapshot-delete":
		return "删除快照"
	case "snapshot-restore":
		return "恢复快照"
	case "share":
		return "创建共享"
	default:
		return "操作实例"
	}
}

// DescribeInstanceOperationLock 按 Provider > User > Instance 的优先级生成统一的操作锁描述。
func DescribeInstanceOperationLock(instanceLimited bool, instanceReason string, userLimited bool, providerLimited bool, action string) InstanceOperationLock {
	actionLabel := operationActionLabel(action)
	if providerLimited {
		return InstanceOperationLock{
			Locked:  true,
			Level:   LimitLevelProvider,
			Reason:  "provider",
			Message: fmt.Sprintf("节点当月总流量已超限，当前节点下所有实例已被系统限制，普通用户禁止%s，请等待自然月自动重置或联系管理员。", actionLabel),
		}
	}
	if userLimited {
		return InstanceOperationLock{
			Locked:  true,
			Level:   LimitLevelUser,
			Reason:  "user",
			Message: fmt.Sprintf("用户当月总流量已超限，当前账号下所有实例已被系统限制，普通用户禁止%s，请等待自然月自动重置或联系管理员。", actionLabel),
		}
	}
	if instanceLimited || strings.TrimSpace(instanceReason) != "" {
		if strings.TrimSpace(instanceReason) == "" {
			instanceReason = "instance"
		}
		return InstanceOperationLock{
			Locked:  true,
			Level:   LimitLevelInstance,
			Reason:  instanceReason,
			Message: fmt.Sprintf("实例因流量超限已被系统限制，普通用户禁止%s，请等待自然月自动重置或联系管理员。", actionLabel),
		}
	}
	return InstanceOperationLock{}
}

// CheckUserInstanceOperationLock 查询实例、用户、Provider 三层流量锁定状态，避免调用方自行拼接多次查询。
func (s *ThreeTierLimitService) CheckUserInstanceOperationLock(userID, instanceID uint, action string) (InstanceOperationLock, error) {
	type lockRow struct {
		InstanceTrafficLimited bool
		TrafficLimitReason     string
		UserTrafficLimited     bool
		ProviderTrafficLimited bool
	}

	var row lockRow
	err := global.APP_DB.Table("instances").
		Select(`instances.traffic_limited AS instance_traffic_limited,
			instances.traffic_limit_reason,
			users.traffic_limited AS user_traffic_limited,
			providers.traffic_limited AS provider_traffic_limited`).
		Joins("LEFT JOIN users ON users.id = instances.user_id").
		Joins("LEFT JOIN providers ON providers.id = instances.provider_id").
		Where("instances.id = ? AND instances.user_id = ? AND instances.deleted_at IS NULL", instanceID, userID).
		Take(&row).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return InstanceOperationLock{}, errors.New("实例不存在或无权限")
		}
		return InstanceOperationLock{}, fmt.Errorf("检查实例流量操作锁失败: %w", err)
	}

	return DescribeInstanceOperationLock(row.InstanceTrafficLimited, row.TrafficLimitReason, row.UserTrafficLimited, row.ProviderTrafficLimited, action), nil
}

// EnsureUserInstanceOperationAllowed 确保普通用户可执行实例操作。
func (s *ThreeTierLimitService) EnsureUserInstanceOperationAllowed(userID, instanceID uint, action string) error {
	lock, err := s.CheckUserInstanceOperationLock(userID, instanceID, action)
	if err != nil {
		return err
	}
	if lock.Locked {
		return errors.New(lock.Message)
	}
	return nil
}

// RefreshUserInstanceTrafficLimits 在启动等关键操作前主动刷新三层限制状态。
func (s *ThreeTierLimitService) RefreshUserInstanceTrafficLimits(userID, instanceID uint) error {
	type instanceRef struct {
		ProviderID uint
	}
	var ref instanceRef
	if err := global.APP_DB.Table("instances").
		Select("provider_id").
		Where("id = ? AND user_id = ? AND deleted_at IS NULL", instanceID, userID).
		Take(&ref).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return errors.New("实例不存在或无权限")
		}
		return fmt.Errorf("获取实例流量检查引用失败: %w", err)
	}

	if _, err := s.CheckProviderTrafficLimit(ref.ProviderID); err != nil {
		return fmt.Errorf("检查节点流量限制失败: %w", err)
	}
	if _, err := s.CheckUserTrafficLimit(userID); err != nil {
		return fmt.Errorf("检查用户流量限制失败: %w", err)
	}
	if _, err := s.CheckInstanceTrafficLimit(instanceID); err != nil {
		return fmt.Errorf("检查实例流量限制失败: %w", err)
	}
	return nil
}

// RefreshAndEnsureUserInstanceOperationAllowed 刷新三层状态后再判定操作权限。
func (s *ThreeTierLimitService) RefreshAndEnsureUserInstanceOperationAllowed(userID, instanceID uint, action string) error {
	if err := s.RefreshUserInstanceTrafficLimits(userID, instanceID); err != nil {
		return err
	}
	return s.EnsureUserInstanceOperationAllowed(userID, instanceID, action)
}
