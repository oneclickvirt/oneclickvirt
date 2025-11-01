package resource

import (
	"context"
	"errors"
	"fmt"
	"oneclickvirt/service/database"
	"oneclickvirt/service/resources"
	"time"

	"oneclickvirt/global"
	providerModel "oneclickvirt/model/provider"
	resourceModel "oneclickvirt/model/resource"
	userModel "oneclickvirt/model/user"

	"go.uber.org/zap"
	"gorm.io/gorm"
)

// Service 处理用户资源相关功能
type Service struct{}

// NewService 创建资源服务
func NewService() *Service {
	return &Service{}
}

// GetAvailableResources 获取可用资源列表
func (s *Service) GetAvailableResources(req userModel.AvailableResourcesRequest) ([]userModel.AvailableResourceResponse, int64, error) {
	var providers []providerModel.Provider
	var total int64

	// 允许 active 和 partial 状态的Provider（与GetAvailableProviders保持一致）
	query := global.APP_DB.Model(&providerModel.Provider{}).Where("(status = ? OR status = ?) AND allow_claim = ?", "active", "partial", true)

	if req.Country != "" {
		query = query.Where("country = ?", req.Country)
	}

	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}

	offset := (req.Page - 1) * req.PageSize
	if err := query.Offset(offset).Limit(req.PageSize).Find(&providers).Error; err != nil {
		return nil, 0, err
	}

	var resourceResponses []userModel.AvailableResourceResponse
	for _, provider := range providers {
		// 统计当前活跃的预留资源（新机制：基于过期时间）
		var activeReservations []resourceModel.ResourceReservation
		if err := global.APP_DB.Where("provider_id = ? AND expires_at > ?",
			provider.ID, time.Now()).Find(&activeReservations).Error; err != nil {
			global.APP_LOG.Warn("查询预留资源失败",
				zap.Uint("providerId", provider.ID),
				zap.Error(err))
			continue
		}

		// 计算预留资源占用
		reservedContainers := 0
		reservedVMs := 0
		for _, reservation := range activeReservations {
			if reservation.InstanceType == "vm" {
				reservedVMs++
			} else {
				reservedContainers++
			}
		}

		// 计算实际可用配额（考虑预留资源）
		actualUsedQuota := provider.UsedQuota
		reservedQuota := reservedContainers + reservedVMs
		availableQuota := provider.TotalQuota - actualUsedQuota - reservedQuota

		// 确保不出现负数
		if availableQuota < 0 {
			availableQuota = 0
		}

		resourceResponse := userModel.AvailableResourceResponse{
			ID:                    provider.ID,
			Name:                  provider.Name,
			Type:                  provider.Type,
			Region:                provider.Region,
			Country:               provider.Country,
			CountryCode:           provider.CountryCode,
			ContainerEnabled:      provider.ContainerEnabled,
			VirtualMachineEnabled: provider.VirtualMachineEnabled,
			AvailableQuota:        availableQuota, // 减去预留的配额
			Status:                provider.Status,
		}

		resourceResponses = append(resourceResponses, resourceResponse)
	}

	return resourceResponses, total, nil
}

// ClaimResource 申领资源
func (s *Service) ClaimResource(userID uint, req userModel.ClaimResourceRequest) (*providerModel.Instance, error) {
	// 初始化服务
	dbService := database.GetDatabaseService()
	quotaService := resources.NewQuotaService()
	reservationService := resources.GetResourceReservationService()

	// 生成会话ID用于资源预留
	sessionID := resources.GenerateSessionID()

	// ===== 阶段1: 短事务 - 快速验证和预留资源 =====
	var provider providerModel.Provider
	var expiredAt time.Time

	err := dbService.ExecuteTransaction(context.Background(), func(tx *gorm.DB) error {
		// 1. 获取并锁定用户（防止并发）
		var currentUser userModel.User
		if err := tx.Set("gorm:query_option", "FOR UPDATE").First(&currentUser, userID).Error; err != nil {
			return fmt.Errorf("获取用户信息失败: %v", err)
		}

		// 检查用户状态
		if currentUser.Status != 1 {
			return errors.New("用户账户已被禁用")
		}

		// 2. 获取并锁定Provider（防止并发）
		if err := tx.Set("gorm:query_option", "FOR UPDATE").First(&provider, req.ProviderID).Error; err != nil {
			return errors.New("提供商不存在")
		}

		if !provider.AllowClaim {
			return errors.New("该提供商不允许申领")
		}

		// 检查提供商状态
		if provider.IsFrozen {
			return errors.New("提供商已被冻结")
		}

		// 检查提供商是否过期
		if provider.ExpiresAt != nil && provider.ExpiresAt.Before(time.Now()) {
			return errors.New("提供商已过期")
		}

		// 设置实例到期时间
		if provider.ExpiresAt != nil {
			expiredAt = *provider.ExpiresAt
		} else {
			expiredAt = time.Now().AddDate(1, 0, 0)
		}

		// 3. 在事务中验证配额（使用行锁）
		quotaReq := resources.ResourceRequest{
			UserID:       userID,
			CPU:          req.CPU,
			Memory:       req.Memory,
			Disk:         req.Disk,
			InstanceType: req.InstanceType,
			ProviderID:   req.ProviderID,
		}

		quotaResult, err := quotaService.ValidateInTransaction(tx, quotaReq)
		if err != nil {
			return fmt.Errorf("配额验证失败: %v", err)
		}

		if !quotaResult.Allowed {
			return errors.New(quotaResult.Reason)
		}

		// 4. 检查Provider节点级别的实例数量限制
		if req.InstanceType == "container" && provider.MaxContainerInstances > 0 {
			if provider.ContainerCount >= provider.MaxContainerInstances {
				return fmt.Errorf("节点容器数量已达上限：%d/%d", provider.ContainerCount, provider.MaxContainerInstances)
			}
		} else if req.InstanceType == "vm" && provider.MaxVMInstances > 0 {
			if provider.VMCount >= provider.MaxVMInstances {
				return fmt.Errorf("节点虚拟机数量已达上限：%d/%d", provider.VMCount, provider.MaxVMInstances)
			}
		}

		// 5. 检查该用户在此节点的等级实例数量限制
		providerLevelLimits, err := quotaService.GetProviderLevelLimitsInTx(tx, req.ProviderID, currentUser.Level)
		if err == nil && providerLevelLimits != nil && providerLevelLimits.MaxInstances > 0 {
			currentProviderInstances, err := quotaService.GetCurrentProviderInstanceCountInTx(tx, userID, req.ProviderID)
			if err != nil {
				return fmt.Errorf("获取节点实例数量失败: %v", err)
			}

			if currentProviderInstances >= providerLevelLimits.MaxInstances {
				return fmt.Errorf("该节点实例数量已达上限：当前在此节点 %d/%d", currentProviderInstances, providerLevelLimits.MaxInstances)
			}
		}

		// 6. 预留资源（关键步骤，防止并发超配）
		if err := reservationService.ReserveResourcesInTx(tx, userID, req.ProviderID, sessionID,
			req.InstanceType, req.CPU, req.Memory, req.Disk, 0); err != nil {
			global.APP_LOG.Error("预留资源失败",
				zap.Uint("userID", userID),
				zap.String("sessionId", sessionID),
				zap.Error(err))
			return fmt.Errorf("资源分配失败: %v", err)
		}

		global.APP_LOG.Info("资源预留成功，准备创建实例",
			zap.Uint("userId", userID),
			zap.String("sessionId", sessionID))

		return nil
	})

	if err != nil {
		return nil, err
	}

	// ===== 阶段2: 创建实例（事务外，失败时通过预留过期自动释放） =====
	instance := providerModel.Instance{
		Name:         req.Name,
		Provider:     provider.Name,
		Image:        req.Image,
		CPU:          req.CPU,
		Memory:       req.Memory,
		Disk:         req.Disk,
		InstanceType: req.InstanceType,
		UserID:       userID,
		Status:       "creating",
		ExpiredAt:    expiredAt,
	}

	// ===== 阶段3: 短事务 - 创建实例、消费预留、更新配额 =====
	err = dbService.ExecuteTransaction(context.Background(), func(tx *gorm.DB) error {
		// 1. 创建实例
		if err := tx.Create(&instance).Error; err != nil {
			return fmt.Errorf("创建实例失败: %v", err)
		}

		// 2. 消费预留的资源
		if err := reservationService.ConsumeReservationBySessionInTx(tx, sessionID); err != nil {
			global.APP_LOG.Warn("消费预留资源失败（可能已过期）",
				zap.String("sessionId", sessionID),
				zap.Error(err))
			// 不阻断流程，预留会自动过期清理
		}

		// 3. 更新用户配额
		usage := resources.ResourceUsage{
			CPU:    req.CPU,
			Memory: req.Memory,
			Disk:   req.Disk,
		}

		if err := quotaService.UpdateUserQuotaAfterCreationWithTx(tx, userID, usage); err != nil {
			return fmt.Errorf("更新用户配额失败: %v", err)
		}

		return nil
	})

	if err != nil {
		// 如果创建失败，预留的资源会在1小时后自动过期释放
		global.APP_LOG.Error("创建实例失败",
			zap.Uint("userId", userID),
			zap.String("sessionId", sessionID),
			zap.Error(err))
		return nil, err
	}

	global.APP_LOG.Info("申领资源成功",
		zap.Uint("userId", userID),
		zap.Uint("providerId", req.ProviderID),
		zap.Uint("instanceId", instance.ID),
		zap.String("sessionId", sessionID))

	return &instance, nil
}
