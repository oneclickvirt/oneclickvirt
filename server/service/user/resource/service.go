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

	query := global.APP_DB.Model(&providerModel.Provider{}).Where("status = ? AND allow_claim = ?", "active", true)

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

	// 构建资源请求
	quotaReq := resources.ResourceRequest{
		UserID:       userID,
		CPU:          req.CPU,
		Memory:       req.Memory,
		Disk:         req.Disk,
		InstanceType: req.InstanceType,
		ProviderID:   req.ProviderID, //  Provider ID 用于节点级限制检查
	}

	// 验证配额
	quotaResult, err := quotaService.ValidateInstanceCreation(quotaReq)
	if err != nil {
		return nil, fmt.Errorf("配额验证失败: %v", err)
	}

	if !quotaResult.Allowed {
		return nil, errors.New(quotaResult.Reason)
	}

	// 验证提供商
	var provider providerModel.Provider
	if err := global.APP_DB.First(&provider, req.ProviderID).Error; err != nil {
		return nil, errors.New("提供商不存在")
	}

	if !provider.AllowClaim {
		return nil, errors.New("该提供商不允许申领")
	}

	// 检查提供商状态
	if provider.IsFrozen {
		return nil, errors.New("提供商已被冻结")
	}

	// 检查提供商是否过期
	if provider.ExpiresAt != nil && provider.ExpiresAt.Before(time.Now()) {
		return nil, errors.New("提供商已过期")
	}

	// 设置实例到期时间，与Provider的到期时间同步
	var expiredAt time.Time
	if provider.ExpiresAt != nil {
		// 如果Provider有到期时间，使用Provider的到期时间
		expiredAt = *provider.ExpiresAt
	} else {
		// 如果Provider没有到期时间，默认为1年后
		expiredAt = time.Now().AddDate(1, 0, 0)
	}

	// 创建实例
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

	// 在单个事务中创建实例并更新配额
	err = dbService.ExecuteTransaction(context.Background(), func(tx *gorm.DB) error {
		// 创建实例
		if err := tx.Create(&instance).Error; err != nil {
			return fmt.Errorf("创建实例失败: %v", err)
		}

		// 在同一事务中更新用户配额
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
		return nil, err
	}

	return &instance, nil
}
