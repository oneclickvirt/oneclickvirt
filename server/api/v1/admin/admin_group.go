package admin

import (
	"errors"
	"strconv"
	"strings"

	"oneclickvirt/global"
	"oneclickvirt/middleware"
	"oneclickvirt/model/common"
	providerModel "oneclickvirt/model/provider"
	"oneclickvirt/utils"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
	"gorm.io/gorm"
)

// AdminGroupInfoRequest 保留旧接口结构，同时作为单个分组的基础字段。
type AdminGroupInfoRequest struct {
	GroupName        string `json:"groupName" binding:"max=64"`
	GroupDescription string `json:"groupDescription"`
	ProviderIDs      []uint `json:"providerIds"`
	SortOrder        int    `json:"sortOrder"`
}

type AdminGroupInfoResponse struct {
	GroupName            string `json:"groupName"`
	GroupDescription     string `json:"groupDescription"`
	GroupDescriptionHTML string `json:"groupDescriptionHtml"`
}

type AdminGroupResponse struct {
	ID                   uint                 `json:"id"`
	OwnerAdminID         uint                 `json:"ownerAdminId"`
	GroupName            string               `json:"groupName"`
	GroupDescription     string               `json:"groupDescription"`
	GroupDescriptionHTML string               `json:"groupDescriptionHtml"`
	SortOrder            int                  `json:"sortOrder"`
	ProviderIDs          []uint               `json:"providerIds"`
	ProviderCount        int                  `json:"providerCount"`
	Providers            []AdminGroupProvider `json:"providers"`
}

type AdminGroupProvider struct {
	ID          uint   `json:"id"`
	Name        string `json:"name"`
	Type        string `json:"type"`
	Status      string `json:"status"`
	GroupID     uint   `json:"groupId"`
	GroupName   string `json:"groupName"`
	Description string `json:"description"`
}

type AdminGroupsPayload struct {
	Groups    []AdminGroupResponse `json:"groups"`
	Providers []AdminGroupProvider `json:"providers"`
}

func defaultAdminGroupName(c *gin.Context) string {
	lang := c.GetHeader("Accept-Language")
	if lang != "" && (lang == "en" || lang == "en-US" || lang == "en_US" || len(lang) >= 2 && lang[:2] == "en") {
		return "Test"
	}
	return "测试"
}

func validateAdminGroupRequest(c *gin.Context, req *AdminGroupInfoRequest) error {
	req.GroupName = strings.TrimSpace(req.GroupName)
	if req.GroupName == "" {
		req.GroupName = defaultAdminGroupName(c)
	}
	if utils.ContainsHTMLTags(req.GroupName) || utils.ContainsSQLInjectionPattern(req.GroupName) {
		return common.NewError(common.CodeValidationError, "分组名称包含非法内容")
	}
	if len(req.GroupName) > 64 {
		return common.NewError(common.CodeValidationError, "分组名称不能超过64个字符")
	}
	req.GroupDescription = strings.TrimSpace(req.GroupDescription)
	if len(req.GroupDescription) > 20000 {
		return common.NewError(common.CodeValidationError, "分组描述不能超过20000个字符")
	}
	return nil
}

func groupOwnerScope(db *gorm.DB, ownerAdminID uint) *gorm.DB {
	if ownerAdminID > 0 {
		return db.Where("owner_admin_id = ?", ownerAdminID)
	}
	return db.Where("owner_admin_id = 0")
}

func loadAdminGroupsPayload(ownerAdminID uint) (*AdminGroupsPayload, error) {
	var groups []providerModel.AdminGroupSetting
	if err := groupOwnerScope(global.APP_DB.Model(&providerModel.AdminGroupSetting{}), ownerAdminID).
		Order("sort_order ASC, id ASC").Find(&groups).Error; err != nil {
		return nil, err
	}

	var providers []providerModel.Provider
	providerQuery := global.APP_DB.Select("id, name, description, type, status, owner_admin_id, provider_group_id, group_name").Order("id ASC")
	if err := groupOwnerScope(providerQuery, ownerAdminID).Find(&providers).Error; err != nil {
		return nil, err
	}

	providerResp := make([]AdminGroupProvider, 0, len(providers))
	providersByGroup := make(map[uint][]AdminGroupProvider)
	providerIDsByGroup := make(map[uint][]uint)
	for _, p := range providers {
		item := AdminGroupProvider{
			ID:          p.ID,
			Name:        p.Name,
			Description: p.Description,
			Type:        p.Type,
			Status:      p.Status,
			GroupID:     p.ProviderGroupID,
			GroupName:   p.GroupName,
		}
		providerResp = append(providerResp, item)
		if p.ProviderGroupID > 0 {
			providersByGroup[p.ProviderGroupID] = append(providersByGroup[p.ProviderGroupID], item)
			providerIDsByGroup[p.ProviderGroupID] = append(providerIDsByGroup[p.ProviderGroupID], p.ID)
		}
	}

	groupResp := make([]AdminGroupResponse, 0, len(groups))
	for _, g := range groups {
		assigned := providersByGroup[g.ID]
		ids := providerIDsByGroup[g.ID]
		if assigned == nil {
			assigned = []AdminGroupProvider{}
		}
		if ids == nil {
			ids = []uint{}
		}
		groupResp = append(groupResp, AdminGroupResponse{
			ID:                   g.ID,
			OwnerAdminID:         g.OwnerAdminID,
			GroupName:            g.GroupName,
			GroupDescription:     g.GroupDescription,
			GroupDescriptionHTML: utils.MarkdownToSafeHTML(g.GroupDescription),
			SortOrder:            g.SortOrder,
			ProviderIDs:          ids,
			ProviderCount:        len(assigned),
			Providers:            assigned,
		})
	}

	return &AdminGroupsPayload{Groups: groupResp, Providers: providerResp}, nil
}

func assignProvidersToGroup(tx *gorm.DB, ownerAdminID uint, group providerModel.AdminGroupSetting, providerIDs []uint) error {
	// 清理从该分组移除的节点，避免节点切换后仍保留旧描述/旧分组。
	clearQuery := tx.Model(&providerModel.Provider{}).Where("provider_group_id = ?", group.ID)
	if ownerAdminID > 0 {
		clearQuery = clearQuery.Where("owner_admin_id = ?", ownerAdminID)
	} else {
		clearQuery = clearQuery.Where("owner_admin_id = 0")
	}
	if len(providerIDs) > 0 {
		clearQuery = clearQuery.Where("id NOT IN ?", providerIDs)
	}
	if err := clearQuery.Updates(map[string]interface{}{
		"provider_group_id": 0,
		"group_name":        "",
		"group_description": "",
	}).Error; err != nil {
		return err
	}

	if len(providerIDs) == 0 {
		return nil
	}
	assignQuery := tx.Model(&providerModel.Provider{}).Where("id IN ?", providerIDs)
	if ownerAdminID > 0 {
		assignQuery = assignQuery.Where("owner_admin_id = ?", ownerAdminID)
	} else {
		assignQuery = assignQuery.Where("owner_admin_id = 0")
	}
	return assignQuery.Updates(map[string]interface{}{
		"provider_group_id": group.ID,
		"group_name":        group.GroupName,
		"group_description": group.GroupDescription,
	}).Error
}

func GetAdminGroups(c *gin.Context) {
	ownerAdminID := middleware.GetOwnerAdminID(c)
	payload, err := loadAdminGroupsPayload(ownerAdminID)
	if err != nil {
		global.APP_LOG.Error("查询管理员分组列表失败", zap.Uint("ownerAdminID", ownerAdminID), zap.Error(err))
		common.ResponseWithError(c, common.ClassifyError(err))
		return
	}
	common.ResponseSuccess(c, payload, "获取成功")
}

func CreateAdminGroup(c *gin.Context) {
	var req AdminGroupInfoRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeValidationError, "参数错误: "+err.Error()))
		return
	}
	if err := validateAdminGroupRequest(c, &req); err != nil {
		common.ResponseWithError(c, err)
		return
	}
	ownerAdminID := middleware.GetOwnerAdminID(c)
	var group providerModel.AdminGroupSetting
	if err := global.APP_DB.Transaction(func(tx *gorm.DB) error {
		group = providerModel.AdminGroupSetting{OwnerAdminID: ownerAdminID, GroupName: req.GroupName, GroupDescription: req.GroupDescription, SortOrder: req.SortOrder}
		if err := tx.Create(&group).Error; err != nil {
			return err
		}
		return assignProvidersToGroup(tx, ownerAdminID, group, req.ProviderIDs)
	}); err != nil {
		common.ResponseWithError(c, common.ClassifyError(err))
		return
	}
	common.ResponseSuccess(c, AdminGroupResponse{ID: group.ID, GroupName: group.GroupName, GroupDescription: group.GroupDescription, GroupDescriptionHTML: utils.MarkdownToSafeHTML(group.GroupDescription)}, "创建成功")
}

func UpdateAdminGroup(c *gin.Context) {
	id64, err := strconv.ParseUint(c.Param("id"), 10, 32)
	if err != nil || id64 == 0 {
		common.ResponseWithError(c, common.NewError(common.CodeValidationError, "分组ID格式错误"))
		return
	}
	var req AdminGroupInfoRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeValidationError, "参数错误: "+err.Error()))
		return
	}
	if err := validateAdminGroupRequest(c, &req); err != nil {
		common.ResponseWithError(c, err)
		return
	}
	ownerAdminID := middleware.GetOwnerAdminID(c)
	if err := global.APP_DB.Transaction(func(tx *gorm.DB) error {
		var group providerModel.AdminGroupSetting
		q := tx.Where("id = ?", uint(id64))
		if ownerAdminID > 0 {
			q = q.Where("owner_admin_id = ?", ownerAdminID)
		} else {
			q = q.Where("owner_admin_id = 0")
		}
		if err := q.First(&group).Error; err != nil {
			return err
		}
		if err := tx.Model(&group).Updates(map[string]interface{}{"group_name": req.GroupName, "group_description": req.GroupDescription, "sort_order": req.SortOrder}).Error; err != nil {
			return err
		}
		group.GroupName = req.GroupName
		group.GroupDescription = req.GroupDescription
		return assignProvidersToGroup(tx, ownerAdminID, group, req.ProviderIDs)
	}); err != nil {
		common.ResponseWithError(c, common.ClassifyError(err))
		return
	}
	common.ResponseSuccess(c, nil, "更新成功")
}

func DeleteAdminGroup(c *gin.Context) {
	id64, err := strconv.ParseUint(c.Param("id"), 10, 32)
	if err != nil || id64 == 0 {
		common.ResponseWithError(c, common.NewError(common.CodeValidationError, "分组ID格式错误"))
		return
	}
	ownerAdminID := middleware.GetOwnerAdminID(c)
	if err := global.APP_DB.Transaction(func(tx *gorm.DB) error {
		var group providerModel.AdminGroupSetting
		q := tx.Where("id = ?", uint(id64))
		if ownerAdminID > 0 {
			q = q.Where("owner_admin_id = ?", ownerAdminID)
		} else {
			q = q.Where("owner_admin_id = 0")
		}
		if err := q.First(&group).Error; err != nil {
			return err
		}
		if err := tx.Model(&providerModel.Provider{}).Where("provider_group_id = ?", group.ID).Updates(map[string]interface{}{"provider_group_id": 0, "group_name": "", "group_description": ""}).Error; err != nil {
			return err
		}
		return tx.Delete(&group).Error
	}); err != nil {
		common.ResponseWithError(c, common.ClassifyError(err))
		return
	}
	common.ResponseSuccess(c, nil, "删除成功")
}

// GetAdminGroupInfo 兼容旧单分组接口：返回第一个分组。
func GetAdminGroupInfo(c *gin.Context) {
	ownerAdminID := middleware.GetOwnerAdminID(c)
	payload, err := loadAdminGroupsPayload(ownerAdminID)
	if err != nil {
		common.ResponseWithError(c, common.ClassifyError(err))
		return
	}
	if len(payload.Groups) == 0 {
		common.ResponseSuccess(c, AdminGroupInfoResponse{GroupName: defaultAdminGroupName(c)}, "获取成功")
		return
	}
	g := payload.Groups[0]
	common.ResponseSuccess(c, AdminGroupInfoResponse{GroupName: g.GroupName, GroupDescription: g.GroupDescription, GroupDescriptionHTML: g.GroupDescriptionHTML}, "获取成功")
}

// UpdateAdminGroupInfo 兼容旧单分组接口：无分组时创建第一个分组，否则更新第一个分组并应用到全部节点。
func UpdateAdminGroupInfo(c *gin.Context) {
	var req AdminGroupInfoRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeValidationError, "参数错误: "+err.Error()))
		return
	}
	if err := validateAdminGroupRequest(c, &req); err != nil {
		common.ResponseWithError(c, err)
		return
	}
	ownerAdminID := middleware.GetOwnerAdminID(c)
	if err := global.APP_DB.Transaction(func(tx *gorm.DB) error {
		var group providerModel.AdminGroupSetting
		q := tx.Where("owner_admin_id = ?", ownerAdminID).Order("sort_order ASC, id ASC").First(&group)
		if q.Error != nil {
			if !errors.Is(q.Error, gorm.ErrRecordNotFound) {
				return q.Error
			}
			group = providerModel.AdminGroupSetting{OwnerAdminID: ownerAdminID, GroupName: req.GroupName, GroupDescription: req.GroupDescription}
			if err := tx.Create(&group).Error; err != nil {
				return err
			}
		} else if err := tx.Model(&group).Updates(map[string]interface{}{"group_name": req.GroupName, "group_description": req.GroupDescription}).Error; err != nil {
			return err
		}
		var providerIDs []uint
		providerQuery := tx.Model(&providerModel.Provider{}).Select("id")
		if ownerAdminID > 0 {
			providerQuery = providerQuery.Where("owner_admin_id = ?", ownerAdminID)
		} else {
			providerQuery = providerQuery.Where("owner_admin_id = 0")
		}
		if err := providerQuery.Pluck("id", &providerIDs).Error; err != nil {
			return err
		}
		return assignProvidersToGroup(tx, ownerAdminID, group, providerIDs)
	}); err != nil {
		global.APP_LOG.Error("更新管理员分组信息失败", zap.Error(err))
		common.ResponseWithError(c, common.ClassifyError(err))
		return
	}
	common.ResponseSuccess(c, nil, "更新成功")
}
