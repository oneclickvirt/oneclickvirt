package system

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"oneclickvirt/service/database"
	"strings"
	"time"

	"oneclickvirt/global"
	"oneclickvirt/model/admin"
	"oneclickvirt/model/common"
	"oneclickvirt/model/system"
	userModel "oneclickvirt/model/user"

	"gorm.io/gorm"
)

// Service 管理员系统管理服务
type Service struct{}

// NewService 创建系统管理服务
func NewService() *Service {
	return &Service{}
}

// GetSystemConfigList 获取系统配置列表
func (s *Service) GetSystemConfigList(req admin.SystemConfigListRequest) ([]admin.SystemConfigResponse, int64, error) {
	var configs []admin.SystemConfig
	var total int64

	query := global.APP_DB.Model(&admin.SystemConfig{})

	if req.Key != "" {
		query = query.Where("key LIKE ?", "%"+req.Key+"%")
	}
	if req.Category != "" {
		query = query.Where("category = ?", req.Category)
	}

	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}

	offset := (req.Page - 1) * req.PageSize
	if err := query.Offset(offset).Limit(req.PageSize).Find(&configs).Error; err != nil {
		return nil, 0, err
	}

	var configResponses []admin.SystemConfigResponse
	for _, config := range configs {
		configResponse := admin.SystemConfigResponse{
			SystemConfig: config,
		}
		configResponses = append(configResponses, configResponse)
	}

	return configResponses, total, nil
}

// UpdateSystemConfig 更新系统配置
func (s *Service) UpdateSystemConfig(req admin.UpdateSystemConfigRequest) error {
	var config admin.SystemConfig
	if err := global.APP_DB.Where("key = ?", req.Key).First(&config).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			config = admin.SystemConfig{
				Key:         req.Key,
				Value:       req.Value,
				Description: req.Remark,
				Category:    req.Category,
			}
			dbService := database.GetDatabaseService()
			return dbService.ExecuteTransaction(context.Background(), func(tx *gorm.DB) error {
				return tx.Create(&config).Error
			})
		}
		return err
	}

	config.Value = req.Value
	config.Description = req.Remark
	config.Category = req.Category

	dbService := database.GetDatabaseService()
	return dbService.ExecuteTransaction(context.Background(), func(tx *gorm.DB) error {
		return tx.Save(&config).Error
	})
}

// UpdateSystemConfigBatch 批量更新系统配置
func (s *Service) UpdateSystemConfigBatch(req admin.BatchUpdateSystemConfigRequest) error {
	if req.Config == nil {
		return common.NewError(common.CodeValidationError, "配置数据不能为空")
	}

	// 递归处理嵌套配置，直接使用全局数据库连接
	return s.processConfigSection(global.APP_DB, "", req.Config)
}

// processConfigSection 递归处理配置节
func (s *Service) processConfigSection(tx *gorm.DB, prefix string, configData map[string]interface{}) error {
	for key, value := range configData {
		fullKey := key
		if prefix != "" {
			fullKey = prefix + "." + key
		}

		switch v := value.(type) {
		case map[string]interface{}:
			// 递归处理嵌套对象
			if err := s.processConfigSection(tx, fullKey, v); err != nil {
				return err
			}
		default:
			// 处理基本值 - 使用JSON序列化复杂类型
			var valueStr string
			if v == nil {
				valueStr = ""
			} else {
				switch val := v.(type) {
				case string:
					valueStr = val
				case int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64:
					valueStr = fmt.Sprintf("%d", val)
				case float32:
					valueStr = fmt.Sprintf("%g", val)
				case float64:
					// 如果是整数，显示为整数
					if val == float64(int64(val)) {
						valueStr = fmt.Sprintf("%d", int64(val))
					} else {
						valueStr = fmt.Sprintf("%g", val)
					}
				case bool:
					valueStr = fmt.Sprintf("%t", val)
				case []interface{}, []string, []int, []map[string]interface{}:
					// 对于切片类型，使用JSON序列化
					jsonBytes, err := json.Marshal(val)
					if err != nil {
						return fmt.Errorf("序列化配置值失败 (key: %s): %w", fullKey, err)
					}
					valueStr = string(jsonBytes)
				case map[string]interface{}:
					// 对于map类型，使用JSON序列化
					jsonBytes, err := json.Marshal(val)
					if err != nil {
						return fmt.Errorf("序列化配置值失败 (key: %s): %w", fullKey, err)
					}
					valueStr = string(jsonBytes)
				default:
					// 其他复杂类型尝试JSON序列化
					jsonBytes, err := json.Marshal(val)
					if err != nil {
						// JSON序列化失败，记录错误并返回
						global.APP_LOG.Error(fmt.Sprintf("配置值JSON序列化失败 (key: %s, type: %T): %v", fullKey, val, err))
						return fmt.Errorf("不支持的配置值类型 (key: %s, type: %T)", fullKey, val)
					}
					valueStr = string(jsonBytes)
				}
			}

			var config admin.SystemConfig
			if err := tx.Where("key = ?", fullKey).First(&config).Error; err != nil {
				if errors.Is(err, gorm.ErrRecordNotFound) {
					// 创建新配置
					config = admin.SystemConfig{
						Key:         fullKey,
						Value:       valueStr,
						Category:    strings.Split(fullKey, ".")[0], // 使用第一段作为分类
						Description: fmt.Sprintf("自动创建的配置项: %s", fullKey),
					}
					if err := tx.Create(&config).Error; err != nil {
						return err
					}
				} else {
					return err
				}
			} else {
				// 更新现有配置
				config.Value = valueStr
				if err := tx.Save(&config).Error; err != nil {
					return err
				}
			}
		}
	}
	return nil
}

// GetAnnouncementList 获取公告列表
func (s *Service) GetAnnouncementList(req admin.AnnouncementListRequest) ([]admin.AnnouncementResponse, int64, error) {
	var announcements []system.Announcement
	var total int64

	query := global.APP_DB.Model(&system.Announcement{})

	if req.Title != "" {
		query = query.Where("title LIKE ?", "%"+req.Title+"%")
	}
	if req.Type != "" {
		query = query.Where("type = ?", req.Type)
	}
	// 修复状态过滤逻辑：只有当status不是-1时才进行状态过滤
	if req.Status != -1 {
		query = query.Where("status = ?", req.Status)
	}

	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}

	offset := (req.Page - 1) * req.PageSize
	if err := query.Offset(offset).Limit(req.PageSize).Find(&announcements).Error; err != nil {
		return nil, 0, err
	}

	var announcementResponses []admin.AnnouncementResponse
	for _, announcement := range announcements {
		var createdByUser string
		if announcement.CreatedBy != nil && *announcement.CreatedBy != 0 {
			var user userModel.User
			if err := global.APP_DB.First(&user, *announcement.CreatedBy).Error; err == nil {
				createdByUser = user.Username
			}
		}

		announcementResponse := admin.AnnouncementResponse{
			Announcement:  announcement,
			CreatedByUser: createdByUser,
		}
		announcementResponses = append(announcementResponses, announcementResponse)
	}

	return announcementResponses, total, nil
}

// CreateAnnouncement 创建公告
func (s *Service) CreateAnnouncement(req admin.CreateAnnouncementRequest, createdBy uint) error {
	var startTime, endTime *time.Time

	if req.StartTime != "" {
		if parsedTime, err := time.Parse("2006-01-02 15:04:05", req.StartTime); err == nil {
			startTime = &parsedTime
		}
	}
	if req.EndTime != "" {
		if parsedTime, err := time.Parse("2006-01-02 15:04:05", req.EndTime); err == nil {
			endTime = &parsedTime
		}
	}

	// 设置默认类型
	announcementType := req.Type
	if announcementType == "" {
		announcementType = "homepage"
	}

	announcement := system.Announcement{
		Title:       req.Title,
		Content:     req.Content,
		ContentHTML: req.ContentHTML,
		Type:        announcementType,
		Priority:    req.Priority,
		IsSticky:    req.IsSticky,
		StartTime:   startTime,
		EndTime:     endTime,
		CreatedBy:   &createdBy,
		Status:      1,
	}

	dbService := database.GetDatabaseService()
	return dbService.ExecuteTransaction(context.Background(), func(tx *gorm.DB) error {
		return tx.Create(&announcement).Error
	})
}

// UpdateAnnouncement 更新公告
func (s *Service) UpdateAnnouncement(req admin.UpdateAnnouncementRequest) error {
	var announcement system.Announcement
	if err := global.APP_DB.First(&announcement, req.ID).Error; err != nil {
		return err
	}

	// 只有在请求中明确提供了非空值时才更新对应字段
	if req.Title != "" {
		announcement.Title = req.Title
	}
	if req.Content != "" {
		announcement.Content = req.Content
	}
	if req.ContentHTML != "" {
		announcement.ContentHTML = req.ContentHTML
	}
	if req.Type != "" {
		announcement.Type = req.Type
	}

	// 对于数值字段，我们需要检查是否在请求中被设置
	// Priority 和 IsSticky 应该总是被更新，因为它们有明确的默认值
	announcement.Priority = req.Priority
	announcement.IsSticky = req.IsSticky
	announcement.Status = req.Status

	if req.StartTime != "" {
		if parsedTime, err := time.Parse("2006-01-02 15:04:05", req.StartTime); err == nil {
			announcement.StartTime = &parsedTime
		}
	}
	if req.EndTime != "" {
		if parsedTime, err := time.Parse("2006-01-02 15:04:05", req.EndTime); err == nil {
			announcement.EndTime = &parsedTime
		}
	}

	dbService := database.GetDatabaseService()
	return dbService.ExecuteTransaction(context.Background(), func(tx *gorm.DB) error {
		return tx.Save(&announcement).Error
	})
}

// DeleteAnnouncement 删除公告
func (s *Service) DeleteAnnouncement(announcementID uint) error {
	dbService := database.GetDatabaseService()
	return dbService.ExecuteTransaction(context.Background(), func(tx *gorm.DB) error {
		return tx.Delete(&system.Announcement{}, announcementID).Error
	})
}

// BatchDeleteAnnouncements 批量删除公告
func (s *Service) BatchDeleteAnnouncements(announcementIDs []uint) error {
	if len(announcementIDs) == 0 {
		return errors.New("没有要删除的公告")
	}
	dbService := database.GetDatabaseService()
	return dbService.ExecuteTransaction(context.Background(), func(tx *gorm.DB) error {
		return tx.Delete(&system.Announcement{}, announcementIDs).Error
	})
}

// BatchUpdateAnnouncementStatus 批量更新公告状态
func (s *Service) BatchUpdateAnnouncementStatus(announcementIDs []uint, status int) error {
	if len(announcementIDs) == 0 {
		return errors.New("没有要更新的公告")
	}
	return global.APP_DB.Model(&system.Announcement{}).Where("id IN ?", announcementIDs).Update("status", status).Error
}

// GetActiveAnnouncements 获取当前有效的公告（供公开API使用）
func (s *Service) GetActiveAnnouncements(announcementType string) ([]system.Announcement, error) {
	var announcements []system.Announcement

	query := global.APP_DB.Model(&system.Announcement{}).
		Where("status = ?", 1). // 启用状态
		Where("(start_time IS NULL OR start_time <= CURRENT_TIMESTAMP)").
		Where("(end_time IS NULL OR end_time >= CURRENT_TIMESTAMP)")

	if announcementType != "" {
		query = query.Where("type = ?", announcementType)
	}

	// 按照是否置顶和优先级排序
	query = query.Order("is_sticky DESC, priority DESC, created_at DESC")

	if err := query.Find(&announcements).Error; err != nil {
		return nil, err
	}

	return announcements, nil
}
