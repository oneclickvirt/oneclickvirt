package resources

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"sync"
	"time"

	"go.uber.org/zap"
	"gorm.io/gorm"

	"oneclickvirt/global"
	"oneclickvirt/model/resource"
	"oneclickvirt/service/database"
)

// ResourceReservationService 资源预留服务 - 基于会话ID
// 资源预留机制说明：
// 1. 预留的目的：防止并发创建实例时的资源超配
// 2. 预留的生命周期：创建实例时预留 → 成功后立即消费（硬删除） → 失败时释放或自动过期
// 3. 预留不计入用户资源统计：用户看到的是实际分配的实例，预留是临时防并发机制
// 4. 预留用于Provider资源计算：计算Provider可用资源时需考虑预留，防止超配
type ResourceReservationService struct {
	dbService   *database.DatabaseService
	stopCleanup chan bool
}

var (
	reservationService     *ResourceReservationService
	reservationServiceOnce sync.Once
)

// GenerateSessionID 生成会话ID
func GenerateSessionID() string {
	bytes := make([]byte, 16)
	rand.Read(bytes)
	return hex.EncodeToString(bytes)
}

// GetResourceReservationService 获取资源预留服务单例
func GetResourceReservationService() *ResourceReservationService {
	reservationServiceOnce.Do(func() {
		reservationService = &ResourceReservationService{
			dbService:   database.GetDatabaseService(),
			stopCleanup: make(chan bool),
		}
		reservationService.startPeriodicCleanup()
	})
	return reservationService
}

// startPeriodicCleanup 启动自适应定期清理任务
func (s *ResourceReservationService) startPeriodicCleanup() {
	go func() {
		defer func() {
			if r := recover(); r != nil {
				global.APP_LOG.Error("资源预留清理goroutine panic", zap.Any("panic", r))
			}
		}()

		ticker := time.NewTicker(10 * time.Minute)
		defer ticker.Stop()

		for {
			select {
			case <-ticker.C:
				// 检查是否有预留记录
				var count int64
				if global.APP_DB != nil {
					global.APP_DB.Model(&resource.ResourceReservation{}).Count(&count)
				}

				// 有预留时10分钟清理，无预留时1小时检查（节省资源）
				newInterval := 1 * time.Hour
				if count > 0 {
					newInterval = 10 * time.Minute
					if err := s.cleanupExpiredReservations(); err != nil {
						global.APP_LOG.Error("清理过期预留记录失败", zap.Error(err))
					}
				}
				ticker.Reset(newInterval)

			case <-s.stopCleanup:
				return
			}
		}
	}()
}

// StopCleanup 停止清理任务
func (s *ResourceReservationService) StopCleanup() {
	close(s.stopCleanup)
}

// cleanupExpiredReservations 清理过期的预留记录
func (s *ResourceReservationService) cleanupExpiredReservations() error {
	// 使用Unscoped进行硬删除，确保过期预留记录真正从数据库中移除
	result := global.APP_DB.Unscoped().Where("expires_at < ?", time.Now()).Delete(&resource.ResourceReservation{})
	if result.Error != nil {
		return result.Error
	}

	if result.RowsAffected > 0 {
		global.APP_LOG.Info("清理过期预留记录（硬删除）", zap.Int64("删除数量", result.RowsAffected))
	}

	return nil
}

// ========================================
// 核心预留接口
// ========================================

// ReserveResources 预留资源（基于会话ID）
func (s *ResourceReservationService) ReserveResources(userID uint, providerID uint, sessionID string,
	instanceType string, cpu int, memory int64, disk int64, bandwidth int, ttlMinutes int) (*resource.ResourceReservation, error) {

	if sessionID == "" {
		sessionID = GenerateSessionID()
	}

	expiresAt := time.Now().Add(time.Duration(ttlMinutes) * time.Minute)

	reservation := &resource.ResourceReservation{
		UserID:       userID,
		ProviderID:   providerID,
		SessionID:    sessionID,
		InstanceType: instanceType,
		CPU:          cpu,
		Memory:       memory,
		Disk:         disk,
		Bandwidth:    bandwidth,
		ExpiresAt:    expiresAt,
	}

	if err := global.APP_DB.Create(reservation).Error; err != nil {
		global.APP_LOG.Error("创建预留记录失败",
			zap.Error(err),
			zap.String("sessionId", sessionID),
			zap.Uint("userId", userID),
			zap.Uint("providerId", providerID))
		return nil, err
	}

	global.APP_LOG.Info("资源预留成功",
		zap.String("sessionId", sessionID),
		zap.Uint("userId", userID),
		zap.Uint("providerId", providerID),
		zap.Int("cpu", cpu),
		zap.Int64("memory", memory),
		zap.Time("expiresAt", expiresAt))

	return reservation, nil
}

// ReserveResourcesInTx 在事务中预留资源（不立即消费）
func (s *ResourceReservationService) ReserveResourcesInTx(tx *gorm.DB, userID uint, providerID uint, sessionID string,
	instanceType string, cpu int, memory int64, disk int64, bandwidth int) error {

	if sessionID == "" {
		sessionID = GenerateSessionID()
	}

	// 预留时间设置为1小时，足够任务执行
	expiresAt := time.Now().Add(1 * time.Hour)

	reservation := &resource.ResourceReservation{
		UserID:       userID,
		ProviderID:   providerID,
		SessionID:    sessionID,
		InstanceType: instanceType,
		CPU:          cpu,
		Memory:       memory,
		Disk:         disk,
		Bandwidth:    bandwidth,
		ExpiresAt:    expiresAt,
	}

	// 在事务中创建预留记录
	if err := tx.Create(reservation).Error; err != nil {
		global.APP_LOG.Error("事务中创建预留记录失败",
			zap.Error(err),
			zap.String("sessionId", sessionID))
		return err
	}

	global.APP_LOG.Info("事务中预留资源成功",
		zap.String("sessionId", sessionID),
		zap.Uint("userId", userID),
		zap.Uint("providerId", providerID),
		zap.Time("expiresAt", expiresAt))

	return nil
}

// ReserveAndConsumeInTx 在事务中原子化预留并立即消费资源
func (s *ResourceReservationService) ReserveAndConsumeInTx(tx *gorm.DB, userID uint, providerID uint, sessionID string,
	instanceType string, cpu int, memory int64, disk int64, bandwidth int) error {

	if sessionID == "" {
		sessionID = GenerateSessionID()
	}

	// 短期预留（1小时），用于原子化操作
	expiresAt := time.Now().Add(1 * time.Hour)

	reservation := &resource.ResourceReservation{
		UserID:       userID,
		ProviderID:   providerID,
		SessionID:    sessionID,
		InstanceType: instanceType,
		CPU:          cpu,
		Memory:       memory,
		Disk:         disk,
		Bandwidth:    bandwidth,
		ExpiresAt:    expiresAt,
	}

	// 在事务中创建预留记录
	if err := tx.Create(reservation).Error; err != nil {
		global.APP_LOG.Error("事务中创建预留记录失败",
			zap.Error(err),
			zap.String("sessionId", sessionID))
		return err
	}

	// 立即消费（硬删除预留记录）- 使用Unscoped确保真正删除
	if err := tx.Unscoped().Delete(reservation).Error; err != nil {
		global.APP_LOG.Error("事务中消费预留记录失败",
			zap.Error(err),
			zap.String("sessionId", sessionID))
		return err
	}

	global.APP_LOG.Info("事务中原子化预留并消费资源成功（硬删除）",
		zap.String("sessionId", sessionID),
		zap.Uint("userId", userID),
		zap.Uint("providerId", providerID))

	return nil
}

// ConsumeReservationBySessionInTx 在事务中按会话ID消费预留
func (s *ResourceReservationService) ConsumeReservationBySessionInTx(tx *gorm.DB, sessionID string) error {
	var reservation resource.ResourceReservation

	// 查找预留记录
	if err := tx.Where("session_id = ?", sessionID).First(&reservation).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			global.APP_LOG.Warn("预留记录不存在", zap.String("sessionId", sessionID))
			return fmt.Errorf("预留记录不存在: %s", sessionID)
		}
		global.APP_LOG.Error("查询预留记录失败", zap.Error(err), zap.String("sessionId", sessionID))
		return err
	}

	// 检查是否过期
	if reservation.IsExpired() {
		global.APP_LOG.Warn("预留记录已过期",
			zap.String("sessionId", sessionID),
			zap.Time("expiresAt", reservation.ExpiresAt))
		return fmt.Errorf("预留记录已过期: %s", sessionID)
	}

	// 硬删除预留记录（消费）- 使用Unscoped确保真正删除，避免资源重复计算
	if err := tx.Unscoped().Delete(&reservation).Error; err != nil {
		global.APP_LOG.Error("消费预留记录失败",
			zap.Error(err),
			zap.String("sessionId", sessionID))
		return err
	}

	global.APP_LOG.Info("消费预留记录成功（硬删除）",
		zap.String("sessionId", sessionID),
		zap.Uint("userId", reservation.UserID),
		zap.Uint("providerId", reservation.ProviderID))

	return nil
}

// ReleaseReservationBySession 释放（删除）预留资源（用于任务取消或失败）
func (s *ResourceReservationService) ReleaseReservationBySession(sessionID string) error {
	var reservation resource.ResourceReservation

	// 查找预留记录
	if err := global.APP_DB.Where("session_id = ?", sessionID).First(&reservation).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			// 预留记录不存在或已被消费，这是正常情况
			global.APP_LOG.Debug("预留记录不存在或已消费", zap.String("sessionId", sessionID))
			return nil
		}
		global.APP_LOG.Error("查询预留记录失败", zap.Error(err), zap.String("sessionId", sessionID))
		return err
	}

	// 删除预留记录（释放资源）
	if err := global.APP_DB.Unscoped().Delete(&reservation).Error; err != nil {
		global.APP_LOG.Error("释放预留记录失败",
			zap.Error(err),
			zap.String("sessionId", sessionID))
		return err
	}

	global.APP_LOG.Info("释放预留记录成功",
		zap.String("sessionId", sessionID),
		zap.Uint("userId", reservation.UserID),
		zap.Uint("providerId", reservation.ProviderID))

	return nil
}

// ========================================
// 公共查询接口
// ========================================

// GetActiveReservations 获取活跃的预留记录（包含未过期的记录）
func (s *ResourceReservationService) GetActiveReservations() ([]resource.ResourceReservation, error) {
	var reservations []resource.ResourceReservation

	// 查询未过期的预留记录
	err := global.APP_DB.Where("expires_at > ?", time.Now()).
		Limit(5000). // 限制最多5000条预留记录
		Find(&reservations).Error
	if err != nil {
		global.APP_LOG.Error("查询活跃预留记录失败", zap.Error(err))
		return nil, err
	}

	global.APP_LOG.Debug("查询活跃预留记录成功", zap.Int("count", len(reservations)))
	return reservations, nil
}
