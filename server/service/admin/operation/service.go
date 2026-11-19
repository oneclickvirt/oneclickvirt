package operation

import (
	"fmt"

	"oneclickvirt/constant"
	"oneclickvirt/global"
	"oneclickvirt/model/monitoring"
	"oneclickvirt/model/provider"
	userModel "oneclickvirt/model/user"
	"oneclickvirt/service/resources"
	"oneclickvirt/utils"

	"go.uber.org/zap"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// OperationService 管理员特殊操作服务
type OperationService struct{}

// LoginAsUser 管理员代登录用户，生成用户JWT
func (s *OperationService) LoginAsUser(adminID, targetUserID uint) (string, error) {
	var targetUser userModel.User
	if err := global.APP_DB.First(&targetUser, targetUserID).Error; err != nil {
		return "", fmt.Errorf("目标用户不存在")
	}
	if targetUser.UserType == "admin" {
		return "", fmt.Errorf("不能代登录其他管理员")
	}

	token, err := utils.GenerateToken(targetUser.ID, targetUser.Username, targetUser.UserType)
	if err != nil {
		return "", fmt.Errorf("生成Token失败: %v", err)
	}

	global.APP_LOG.Info("管理员代登录用户",
		zap.Uint("adminID", adminID),
		zap.Uint("targetUserID", targetUserID),
		zap.String("targetUsername", targetUser.Username))

	return token, nil
}

// TransferInstance 转移实例到另一个用户
func (s *OperationService) TransferInstance(adminID, instanceID, targetUserID uint) error {
	var oldUserID uint
	var resourceUsage int
	err := global.APP_DB.Transaction(func(tx *gorm.DB) error {
		var inst provider.Instance
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&inst, instanceID).Error; err != nil {
			return fmt.Errorf("实例不存在")
		}
		if constant.IsBusyStatus(inst.Status) {
			return fmt.Errorf("实例正在操作，请在任务结束后再转移")
		}
		oldUserID = inst.UserID
		usage := resources.ResourceUsage{CPU: inst.CPU, Memory: inst.Memory, Disk: inst.Disk, Bandwidth: inst.Bandwidth}
		resourceUsage = usage.GetResourceUsage()

		ownerIDs := []uint{targetUserID}
		if oldUserID != 0 && oldUserID != targetUserID {
			ownerIDs = append(ownerIDs, oldUserID)
		}
		// Lock owners in the same order when two transfers involve the same users.
		var lockedUsers []userModel.User
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("id IN ?", ownerIDs).Order("id ASC").Find(&lockedUsers).Error; err != nil {
			return fmt.Errorf("查询用户配额失败: %w", err)
		}
		users := make(map[uint]userModel.User, len(lockedUsers))
		for _, user := range lockedUsers {
			users[user.ID] = user
		}
		targetUser, ok := users[targetUserID]
		if !ok {
			return fmt.Errorf("目标用户不存在")
		}
		if oldUserID == targetUserID {
			return fmt.Errorf("实例已属于该用户")
		}

		newTargetUsed := targetUser.UsedQuota + resourceUsage
		if targetUser.TotalQuota > 0 && newTargetUsed+targetUser.PendingQuota > targetUser.TotalQuota {
			return fmt.Errorf("目标用户配额不足: 已用%d, 待确认%d, 需要%d, 总量%d", targetUser.UsedQuota, targetUser.PendingQuota, resourceUsage, targetUser.TotalQuota)
		}
		// An instance created without an owner has no source quota to release.
		if oldUserID != 0 {
			sourceUser, ok := users[oldUserID]
			if !ok {
				return fmt.Errorf("查询原用户失败: %w", gorm.ErrRecordNotFound)
			}
			newSourceUsed := max(0, sourceUser.UsedQuota-resourceUsage)
			if err := tx.Model(&sourceUser).Update("used_quota", newSourceUsed).Error; err != nil {
				return fmt.Errorf("更新原用户配额失败: %w", err)
			}
		}
		if err := tx.Model(&targetUser).Update("used_quota", newTargetUsed).Error; err != nil {
			return fmt.Errorf("更新目标用户配额失败: %w", err)
		}

		if err := tx.Model(&inst).Update("user_id", targetUserID).Error; err != nil {
			return fmt.Errorf("转移实例失败: %w", err)
		}

		if err := tx.Model(&monitoring.AgentMonitor{}).
			Where("instance_id = ?", instanceID).
			Update("user_id", targetUserID).Error; err != nil {
			return fmt.Errorf("更新监控归属失败: %w", err)
		}

		return nil
	})
	if err != nil {
		return err
	}

	global.APP_LOG.Info("管理员转移实例",
		zap.Uint("adminID", adminID),
		zap.Uint("instanceID", instanceID),
		zap.Uint("fromUserID", oldUserID),
		zap.Uint("toUserID", targetUserID),
		zap.Int("resourceUsage", resourceUsage))

	return nil
}
