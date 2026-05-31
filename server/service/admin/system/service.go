package system

import (
	"context"
	"errors"
	"oneclickvirt/service/database"
	"time"

	"oneclickvirt/global"
	"oneclickvirt/model/admin"
	"oneclickvirt/model/system"
	userModel "oneclickvirt/model/user"
	"oneclickvirt/utils"

	"gorm.io/gorm"
)

// Service 管理员系统管理服务
type Service struct{}

// NewService 创建系统管理服务
func NewService() *Service {
	return &Service{}
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
	// 状态过滤逻辑：只有当status不是-1时才进行状态过滤
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

	// Batch-load creator usernames to avoid N+1 queries
	creatorIDSet := make(map[uint]struct{})
	for _, a := range announcements {
		if a.CreatedBy != nil && *a.CreatedBy != 0 {
			creatorIDSet[*a.CreatedBy] = struct{}{}
		}
	}
	userMap := make(map[uint]string)
	if len(creatorIDSet) > 0 {
		creatorIDs := make([]uint, 0, len(creatorIDSet))
		for id := range creatorIDSet {
			creatorIDs = append(creatorIDs, id)
		}
		var users []userModel.User
		if err := global.APP_DB.Select("id", "username").Where("id IN ?", creatorIDs).Find(&users).Error; err == nil {
			for _, u := range users {
				userMap[u.ID] = u.Username
			}
		}
	}

	var announcementResponses []admin.AnnouncementResponse
	for _, announcement := range announcements {
		var createdByUser string
		if announcement.CreatedBy != nil && *announcement.CreatedBy != 0 {
			createdByUser = userMap[*announcement.CreatedBy]
		}
		announcementResponses = append(announcementResponses, admin.AnnouncementResponse{
			Announcement:  announcement,
			CreatedByUser: createdByUser,
		})
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
		Content:     utils.SanitizeHTML(req.Content),
		ContentHTML: utils.SanitizeHTML(req.ContentHTML),
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
		announcement.Content = utils.SanitizeHTML(req.Content)
	}
	if req.ContentHTML != "" {
		announcement.ContentHTML = utils.SanitizeHTML(req.ContentHTML)
	}
	if req.Type != "" {
		announcement.Type = req.Type
	}

	// 对于数值字段，需要检查是否在请求中被设置
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
	for i := range announcements {
		announcements[i].Content = utils.SanitizeHTML(announcements[i].Content)
		announcements[i].ContentHTML = utils.SanitizeHTML(announcements[i].ContentHTML)
	}

	return announcements, nil
}
