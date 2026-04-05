package operation

import (
	"fmt"

	"oneclickvirt/global"
	"oneclickvirt/model/monitoring"
	"oneclickvirt/model/provider"
	userModel "oneclickvirt/model/user"
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
	var inst provider.Instance
	if err := global.APP_DB.First(&inst, instanceID).Error; err != nil {
		return fmt.Errorf("实例不存在")
	}

	var targetUser userModel.User
	if err := global.APP_DB.First(&targetUser, targetUserID).Error; err != nil {
		return fmt.Errorf("目标用户不存在")
	}

	if inst.UserID == targetUserID {
		return fmt.Errorf("实例已属于该用户")
	}

	oldUserID := inst.UserID

	// 计算实例资源占用量
	resourceUsage := inst.CPU*4 + int(inst.Memory/512)*2 + int(inst.Disk/5)*1

	err := global.APP_DB.Transaction(func(tx *gorm.DB) error {
		// 1. 转移实例
		if err := tx.Model(&provider.Instance{}).Where("id = ?", instanceID).
			Update("user_id", targetUserID).Error; err != nil {
			return fmt.Errorf("转移实例失败: %v", err)
		}

		// 2. 减少原用户已使用配额
		var sourceUser userModel.User
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&sourceUser, oldUserID).Error; err != nil {
			return fmt.Errorf("查询原用户失败: %v", err)
		}
		newSourceUsed := sourceUser.UsedQuota - resourceUsage
		if newSourceUsed < 0 {
			newSourceUsed = 0
		}
		if err := tx.Model(&sourceUser).Update("used_quota", newSourceUsed).Error; err != nil {
			return fmt.Errorf("更新原用户配额失败: %v", err)
		}

		// 3. 增加目标用户已使用配额
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&targetUser, targetUserID).Error; err != nil {
			return fmt.Errorf("查询目标用户失败: %v", err)
		}
		newTargetUsed := targetUser.UsedQuota + resourceUsage
		if err := tx.Model(&targetUser).Update("used_quota", newTargetUsed).Error; err != nil {
			return fmt.Errorf("更新目标用户配额失败: %v", err)
		}

		// 4. 更新 agent_monitors 的 user_id
		if err := tx.Model(&monitoring.AgentMonitor{}).
			Where("instance_id = ?", instanceID).
			Update("user_id", targetUserID).Error; err != nil {
			return fmt.Errorf("更新监控归属失败: %v", err)
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
