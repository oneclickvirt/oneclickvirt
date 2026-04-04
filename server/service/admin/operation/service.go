package operation

import (
	"fmt"

	"oneclickvirt/global"
	"oneclickvirt/model/provider"
	userModel "oneclickvirt/model/user"
	"oneclickvirt/utils"

	"go.uber.org/zap"
	"gorm.io/gorm"
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
	err := global.APP_DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&provider.Instance{}).Where("id = ?", instanceID).
			Update("user_id", targetUserID).Error; err != nil {
			return fmt.Errorf("转移实例失败: %v", err)
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
		zap.Uint("toUserID", targetUserID))

	return nil
}
