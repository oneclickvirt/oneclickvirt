package auth

import (
	"errors"
	"time"

	"oneclickvirt/global"
	"oneclickvirt/model/common"
	"oneclickvirt/model/system"

	"go.uber.org/zap"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// verifyInviteCode 验证邀请码
func (s *AuthService) verifyInviteCode(code string) error {
	var inviteCode system.InviteCode
	err := global.APP_DB.Where("code = ? AND status = ?", code, 1).First(&inviteCode).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return common.NewError(common.CodeInviteCodeInvalid)
		}
		return err
	}
	// 检查使用次数
	if inviteCode.MaxUses > 0 && inviteCode.UsedCount >= inviteCode.MaxUses {
		return common.NewError(common.CodeInviteCodeUsed)
	}
	// 检查过期时间
	if inviteCode.ExpiresAt != nil {
		now := time.Now()
		global.APP_LOG.Debug("verifyInviteCode邀请码过期时间检查",
			zap.String("code", code),
			zap.Time("expiresAt", *inviteCode.ExpiresAt),
			zap.Time("now", now),
			zap.Bool("isExpired", inviteCode.ExpiresAt.Before(now)))

		if inviteCode.ExpiresAt.Before(now) {
			return common.NewError(common.CodeInviteCodeExpired)
		}
	}
	return nil
}

// useInviteCodeWithTx 使用邀请码（带事务支持）
// 在事务内验证并标记邀请码为已使用，确保原子性
func (s *AuthService) useInviteCodeWithTx(db *gorm.DB, code string, ip string, userAgent string) error {
	var inviteCode system.InviteCode

	// 使用行级锁获取邀请码记录，防止并发使用
	err := db.Clauses(clause.Locking{Strength: "UPDATE"}).
		Where("code = ? AND status = ?", code, 1).
		First(&inviteCode).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return common.NewError(common.CodeInviteCodeInvalid, "邀请码无效")
		}
		return err
	}

	// 检查过期时间
	if inviteCode.ExpiresAt != nil {
		now := time.Now()
		global.APP_LOG.Debug("useInviteCodeWithTx邀请码过期时间检查",
			zap.String("code", code),
			zap.Time("expiresAt", *inviteCode.ExpiresAt),
			zap.Time("now", now),
			zap.Bool("isExpired", inviteCode.ExpiresAt.Before(now)))

		if inviteCode.ExpiresAt.Before(now) {
			global.APP_LOG.Warn("使用邀请码时检测到已过期",
				zap.String("code", code),
				zap.Time("expiresAt", *inviteCode.ExpiresAt),
				zap.Time("now", now))
			return common.NewError(common.CodeInviteCodeExpired, "邀请码已过期")
		}
	}

	// 检查使用次数
	if inviteCode.MaxUses > 0 && inviteCode.UsedCount >= inviteCode.MaxUses {
		return common.NewError(common.CodeInviteCodeUsed, "邀请码已被使用")
	}

	// 增加使用次数
	inviteCode.UsedCount++
	// 如果达到最大使用次数，设置为已用完
	if inviteCode.MaxUses > 0 && inviteCode.UsedCount >= inviteCode.MaxUses {
		inviteCode.Status = 0 // 0表示已用完
	}

	// 保存邀请码使用记录
	usage := system.InviteCodeUsage{
		InviteCodeID: inviteCode.ID,
		IP:           ip,
		UserAgent:    userAgent,
		UsedAt:       time.Now(),
	}

	// 使用传入的数据库连接（可能是事务）
	if err := db.Save(&inviteCode).Error; err != nil {
		return err
	}
	if err := db.Create(&usage).Error; err != nil {
		return err
	}
	return nil
}

// validateInviteCodeBeforeUse 提前验证邀请码（不消费，只检查有效性）
// 用于在注册流程的早期阶段验证邀请码，避免验证码被消费后才发现邀请码无效
func (s *AuthService) validateInviteCodeBeforeUse(code string) error {
	var inviteCode system.InviteCode

	// 查询邀请码记录
	err := global.APP_DB.Where("code = ? AND status = ?", code, 1).First(&inviteCode).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return common.NewError(common.CodeInviteCodeInvalid, "邀请码无效")
		}
		return err
	}

	// 检查过期时间
	if inviteCode.ExpiresAt != nil {
		now := time.Now()
		// 添加详细日志帮助调试
		global.APP_LOG.Debug("邀请码过期时间检查",
			zap.String("code", code),
			zap.Time("expiresAt", *inviteCode.ExpiresAt),
			zap.Time("now", now),
			zap.Bool("isExpired", inviteCode.ExpiresAt.Before(now)))

		if inviteCode.ExpiresAt.Before(now) {
			global.APP_LOG.Warn("邀请码已过期",
				zap.String("code", code),
				zap.Time("expiresAt", *inviteCode.ExpiresAt),
				zap.Time("now", now))
			return common.NewError(common.CodeInviteCodeExpired, "邀请码已过期")
		}
	}

	// 检查使用次数
	if inviteCode.MaxUses > 0 && inviteCode.UsedCount >= inviteCode.MaxUses {
		return common.NewError(common.CodeInviteCodeUsed, "邀请码已被使用")
	}

	return nil
}
