package oauth2

import (
	"encoding/json"
	"errors"
	"oneclickvirt/global"
	"oneclickvirt/model/common"
	oauth2Model "oneclickvirt/model/oauth2"
	userModel "oneclickvirt/model/user"

	"go.uber.org/zap"
	"gorm.io/gorm"
)

// ProviderService OAuth2提供商管理服务
type ProviderService struct{}

// GetAllProviders 获取所有OAuth2提供商（包括禁用的）
func (s *ProviderService) GetAllProviders() ([]oauth2Model.OAuth2Provider, error) {
	var providers []oauth2Model.OAuth2Provider
	if err := global.APP_DB.Order("sort ASC, id ASC").Find(&providers).Error; err != nil {
		return nil, err
	}
	return providers, nil
}

// GetProvider 获取指定OAuth2提供商
func (s *ProviderService) GetProvider(id uint) (*oauth2Model.OAuth2Provider, error) {
	var provider oauth2Model.OAuth2Provider
	if err := global.APP_DB.First(&provider, id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, common.NewError(common.CodeNotFound, "OAuth2提供商不存在")
		}
		return nil, err
	}
	return &provider, nil
}

// CreateProvider 创建OAuth2提供商
func (s *ProviderService) CreateProvider(req *CreateProviderRequest) (*oauth2Model.OAuth2Provider, error) {
	// 检查名称是否已存在
	var count int64
	global.APP_DB.Model(&oauth2Model.OAuth2Provider{}).Where("name = ?", req.Name).Count(&count)
	if count > 0 {
		return nil, common.NewError(common.CodeValidationError, "提供商名称已存在")
	}

	// 序列化LevelMapping
	levelMappingJSON, _ := json.Marshal(req.LevelMapping)

	provider := &oauth2Model.OAuth2Provider{
		Name:             req.Name,
		DisplayName:      req.DisplayName,
		ProviderType:     req.ProviderType,
		Enabled:          req.Enabled,
		ClientID:         req.ClientID,
		ClientSecret:     req.ClientSecret,
		RedirectURL:      req.RedirectURL,
		AuthURL:          req.AuthURL,
		TokenURL:         req.TokenURL,
		UserInfoURL:      req.UserInfoURL,
		UserIDField:      req.UserIDField,
		UsernameField:    req.UsernameField,
		EmailField:       req.EmailField,
		AvatarField:      req.AvatarField,
		NicknameField:    req.NicknameField,
		TrustLevelField:  req.TrustLevelField,
		MaxRegistrations: req.MaxRegistrations,
		LevelMapping:     string(levelMappingJSON),
		DefaultLevel:     req.DefaultLevel,
		Sort:             req.Sort,
	}

	if err := global.APP_DB.Create(provider).Error; err != nil {
		global.APP_LOG.Error("创建OAuth2提供商失败", zap.Error(err))
		return nil, common.NewError(common.CodeInternalError, "创建失败")
	}

	global.APP_LOG.Info("创建OAuth2提供商成功", zap.String("name", req.Name))
	return provider, nil
}

// UpdateProvider 更新OAuth2提供商
func (s *ProviderService) UpdateProvider(id uint, req *UpdateProviderRequest) error {
	var provider oauth2Model.OAuth2Provider
	if err := global.APP_DB.First(&provider, id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return common.NewError(common.CodeNotFound, "OAuth2提供商不存在")
		}
		return err
	}

	// 如果修改名称，检查是否重复
	if req.Name != nil && *req.Name != provider.Name {
		var count int64
		global.APP_DB.Model(&oauth2Model.OAuth2Provider{}).Where("name = ? AND id != ?", *req.Name, id).Count(&count)
		if count > 0 {
			return common.NewError(common.CodeValidationError, "提供商名称已存在")
		}
	}

	updates := make(map[string]interface{})

	if req.Name != nil {
		updates["name"] = *req.Name
	}
	if req.DisplayName != nil {
		updates["display_name"] = *req.DisplayName
	}
	if req.ProviderType != nil {
		updates["provider_type"] = *req.ProviderType
	}
	if req.Enabled != nil {
		updates["enabled"] = *req.Enabled
	}
	if req.ClientID != nil {
		updates["client_id"] = *req.ClientID
	}
	if req.ClientSecret != nil {
		updates["client_secret"] = *req.ClientSecret
	}
	if req.RedirectURL != nil {
		updates["redirect_url"] = *req.RedirectURL
	}
	if req.AuthURL != nil {
		updates["auth_url"] = *req.AuthURL
	}
	if req.TokenURL != nil {
		updates["token_url"] = *req.TokenURL
	}
	if req.UserInfoURL != nil {
		updates["user_info_url"] = *req.UserInfoURL
	}
	if req.UserIDField != nil {
		updates["user_id_field"] = *req.UserIDField
	}
	if req.UsernameField != nil {
		updates["username_field"] = *req.UsernameField
	}
	if req.EmailField != nil {
		updates["email_field"] = *req.EmailField
	}
	if req.AvatarField != nil {
		updates["avatar_field"] = *req.AvatarField
	}
	if req.NicknameField != nil {
		updates["nickname_field"] = *req.NicknameField
	}
	if req.TrustLevelField != nil {
		updates["trust_level_field"] = *req.TrustLevelField
	}
	if req.MaxRegistrations != nil {
		updates["max_registrations"] = *req.MaxRegistrations
	}
	if req.LevelMapping != nil {
		levelMappingJSON, _ := json.Marshal(req.LevelMapping)
		updates["level_mapping"] = string(levelMappingJSON)
	}
	if req.DefaultLevel != nil {
		updates["default_level"] = *req.DefaultLevel
	}
	if req.Sort != nil {
		updates["sort"] = *req.Sort
	}

	if len(updates) > 0 {
		if err := global.APP_DB.Model(&provider).Updates(updates).Error; err != nil {
			global.APP_LOG.Error("更新OAuth2提供商失败", zap.Error(err))
			return common.NewError(common.CodeInternalError, "更新失败")
		}
	}

	global.APP_LOG.Info("更新OAuth2提供商成功", zap.Uint("id", id))
	return nil
}

// DeleteProvider 删除OAuth2提供商
func (s *ProviderService) DeleteProvider(id uint) error {
	var provider oauth2Model.OAuth2Provider
	if err := global.APP_DB.First(&provider, id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return common.NewError(common.CodeNotFound, "OAuth2提供商不存在")
		}
		return err
	}

	// 检查是否有用户使用此提供商
	var userCount int64
	global.APP_DB.Model(&userModel.User{}).Where(&userModel.User{OAuth2ProviderID: id}).Count(&userCount)
	if userCount > 0 {
		return common.NewError(common.CodeValidationError, "无法删除：有用户正在使用此提供商")
	}

	if err := global.APP_DB.Delete(&provider).Error; err != nil {
		global.APP_LOG.Error("删除OAuth2提供商失败", zap.Error(err))
		return common.NewError(common.CodeInternalError, "删除失败")
	}

	global.APP_LOG.Info("删除OAuth2提供商成功", zap.Uint("id", id))
	return nil
}

// ResetRegistrationCount 重置注册计数
func (s *ProviderService) ResetRegistrationCount(id uint) error {
	if err := global.APP_DB.Model(&oauth2Model.OAuth2Provider{}).
		Where("id = ?", id).
		Update("current_registrations", 0).Error; err != nil {
		global.APP_LOG.Error("重置注册计数失败", zap.Error(err))
		return common.NewError(common.CodeInternalError, "重置失败")
	}

	global.APP_LOG.Info("重置OAuth2提供商注册计数成功", zap.Uint("id", id))
	return nil
}

// CreateProviderRequest 创建提供商请求
type CreateProviderRequest struct {
	Name             string         `json:"name" binding:"required"`
	DisplayName      string         `json:"displayName" binding:"required"`
	ProviderType     string         `json:"providerType" binding:"required,oneof=preset generic"` // preset 或 generic
	Enabled          bool           `json:"enabled"`
	ClientID         string         `json:"clientId" binding:"required"`
	ClientSecret     string         `json:"clientSecret" binding:"required"`
	RedirectURL      string         `json:"redirectUrl" binding:"required"`
	AuthURL          string         `json:"authUrl" binding:"required"`
	TokenURL         string         `json:"tokenUrl" binding:"required"`
	UserInfoURL      string         `json:"userInfoUrl" binding:"required"`
	UserIDField      string         `json:"userIdField"`
	UsernameField    string         `json:"usernameField"`
	EmailField       string         `json:"emailField"`
	AvatarField      string         `json:"avatarField"`
	NicknameField    string         `json:"nicknameField"`
	TrustLevelField  string         `json:"trustLevelField"`
	MaxRegistrations int            `json:"maxRegistrations"`
	LevelMapping     map[string]int `json:"levelMapping"`
	DefaultLevel     int            `json:"defaultLevel"`
	Sort             int            `json:"sort"`
}

// UpdateProviderRequest 更新提供商请求
type UpdateProviderRequest struct {
	Name             *string        `json:"name"`
	DisplayName      *string        `json:"displayName"`
	ProviderType     *string        `json:"providerType" binding:"omitempty,oneof=preset generic"`
	Enabled          *bool          `json:"enabled"`
	ClientID         *string        `json:"clientId"`
	ClientSecret     *string        `json:"clientSecret"`
	RedirectURL      *string        `json:"redirectUrl"`
	AuthURL          *string        `json:"authUrl"`
	TokenURL         *string        `json:"tokenUrl"`
	UserInfoURL      *string        `json:"userInfoUrl"`
	UserIDField      *string        `json:"userIdField"`
	UsernameField    *string        `json:"usernameField"`
	EmailField       *string        `json:"emailField"`
	AvatarField      *string        `json:"avatarField"`
	NicknameField    *string        `json:"nicknameField"`
	TrustLevelField  *string        `json:"trustLevelField"`
	MaxRegistrations *int           `json:"maxRegistrations"`
	LevelMapping     map[string]int `json:"levelMapping"`
	DefaultLevel     *int           `json:"defaultLevel"`
	Sort             *int           `json:"sort"`
}
