package provider

import (
	"context"
	"fmt"
	"time"

	"oneclickvirt/global"
	providerModel "oneclickvirt/model/provider"
	systemModel "oneclickvirt/model/system"
	"oneclickvirt/service/database"
	"oneclickvirt/service/resources"

	"go.uber.org/zap"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// RedeemCode 用户兑换码兑换实例
// 流程：验证码状态 → 在短事务中转移实例归属 + 标记码已使用
func (s *Service) RedeemCode(userID uint, code string) error {
	if code == "" {
		return fmt.Errorf("兑换码不能为空")
	}

	dbService := database.GetDatabaseService()

	return dbService.ExecuteTransaction(context.Background(), func(tx *gorm.DB) error {
		// 查询并锁定兑换码（FOR UPDATE 防止并发兑换同一个码）
		var redemptionCode systemModel.RedemptionCode
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("code = ?", code).
			First(&redemptionCode).Error; err != nil {
			if err == gorm.ErrRecordNotFound {
				return fmt.Errorf("兑换码不存在")
			}
			return fmt.Errorf("查询兑换码失败: %v", err)
		}

		// 状态校验：只有 pending_use 状态的兑换码才能被兑换
		if redemptionCode.Status != systemModel.RedemptionStatusPendingUse {
			switch redemptionCode.Status {
			case systemModel.RedemptionStatusUsed:
				return fmt.Errorf("兑换码已被使用")
			case systemModel.RedemptionStatusCreating, systemModel.RedemptionStatusPendingCreate:
				return fmt.Errorf("兑换码对应的实例还在创建中，请稍后重试")
			case systemModel.RedemptionStatusDeleting:
				return fmt.Errorf("兑换码已失效")
			default:
				return fmt.Errorf("兑换码状态异常，无法使用")
			}
		}

		// 验证实例存在且状态正常
		if redemptionCode.InstanceID == nil {
			return fmt.Errorf("兑换码关联实例不存在，请联系管理员")
		}
		instanceID := *redemptionCode.InstanceID

		var instance providerModel.Instance
		if err := tx.Where("id = ?", instanceID).First(&instance).Error; err != nil {
			if err == gorm.ErrRecordNotFound {
				return fmt.Errorf("兑换码关联实例不存在，请联系管理员")
			}
			return fmt.Errorf("查询实例失败: %v", err)
		}
		if instance.Status != "running" && instance.Status != "stopped" {
			return fmt.Errorf("兑换码关联实例状态异常，请联系管理员")
		}

		// 兑换前验证用户是否有足够的空闲配额承载该实例
		// 防止用户利用兑换机制绕过资源限制超量领取
		quotaService := resources.NewQuotaService()
		quotaReq := resources.ResourceRequest{
			UserID:       userID,
			CPU:          instance.CPU,
			Memory:       instance.Memory,
			Disk:         instance.Disk,
			Bandwidth:    instance.Bandwidth,
			InstanceType: instance.InstanceType,
			ProviderID:   instance.ProviderID,
		}
		quotaResult, err := quotaService.ValidateInTransaction(tx, quotaReq)
		if err != nil {
			return fmt.Errorf("配额验证失败: %v", err)
		}
		if !quotaResult.Allowed {
			return fmt.Errorf("您的资源配额不足，无法兑换该实例: %s", quotaResult.Reason)
		}

		now := time.Now()

		// 转移实例归属：UserID 从 0 → 当前用户
		if err := tx.Model(&providerModel.Instance{}).
			Where("id = ?", instanceID).
			Update("user_id", userID).Error; err != nil {
			return fmt.Errorf("转移实例归属失败: %v", err)
		}

		// 标记兑换码为已使用
		userIDVal := userID
		if err := tx.Model(&systemModel.RedemptionCode{}).
			Where("id = ?", redemptionCode.ID).
			Updates(map[string]interface{}{
				"status":      systemModel.RedemptionStatusUsed,
				"user_id":     userIDVal,
				"redeemed_at": now,
			}).Error; err != nil {
			return fmt.Errorf("更新兑换码状态失败: %v", err)
		}

		global.APP_LOG.Info("用户成功兑换码",
			zap.Uint("userID", userID),
			zap.String("code", code),
			zap.Uint("instanceID", instanceID))

		return nil
	})
}
