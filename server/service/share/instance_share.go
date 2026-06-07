package share

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"oneclickvirt/global"
	providerModel "oneclickvirt/model/provider"

	"gorm.io/gorm"
)

const (
	CreatorTypeUser        = "user"
	CreatorTypeAdmin       = "admin"
	CreatorTypeNormalAdmin = "normal_admin"
	DefaultShareMinutes    = 30
	MaxShareMinutes        = 7 * 24 * 60
)

type CreateInstanceShareResult struct {
	Token     string    `json:"token"`
	URL       string    `json:"url"`
	ExpiresAt time.Time `json:"expiresAt"`
}

type InstanceShareService struct{}

func NewInstanceShareService() *InstanceShareService {
	return &InstanceShareService{}
}

func normalizeShareMinutes(minutes int) int {
	if minutes <= 0 {
		return DefaultShareMinutes
	}
	if minutes > MaxShareMinutes {
		return MaxShareMinutes
	}
	return minutes
}

func hashShareToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

func generateShareToken() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

func buildShareURL(token string) string {
	base := strings.TrimRight(global.GetAppConfig().System.FrontendURL, "/")
	if base == "" {
		base = "/"
	}
	if base == "/" {
		return "/#/share/instances/" + token
	}
	return base + "/#/share/instances/" + token
}

func (s *InstanceShareService) CreateForUser(userID, instanceID uint, expiresInMinutes int) (*CreateInstanceShareResult, error) {
	var instance providerModel.Instance
	if err := global.APP_DB.Where("id = ? AND user_id = ?", instanceID, userID).First(&instance).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, fmt.Errorf("实例不存在或无权限")
		}
		return nil, err
	}
	return s.create(instance, userID, userID, CreatorTypeUser, expiresInMinutes)
}

func (s *InstanceShareService) CreateForAdmin(actorID uint, creatorType string, ownerAdminID uint, instanceID uint, expiresInMinutes int) (*CreateInstanceShareResult, error) {
	var instance providerModel.Instance
	if err := global.APP_DB.First(&instance, instanceID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, fmt.Errorf("实例不存在")
		}
		return nil, err
	}
	if ownerAdminID > 0 {
		var count int64
		if err := global.APP_DB.Model(&providerModel.Provider{}).
			Where("id = ? AND owner_admin_id = ?", instance.ProviderID, ownerAdminID).
			Count(&count).Error; err != nil {
			return nil, fmt.Errorf("检查节点归属失败: %w", err)
		}
		if count == 0 {
			return nil, fmt.Errorf("无权分享该实例")
		}
	}
	if creatorType != CreatorTypeNormalAdmin {
		creatorType = CreatorTypeAdmin
	}
	return s.create(instance, instance.UserID, actorID, creatorType, expiresInMinutes)
}

func (s *InstanceShareService) create(instance providerModel.Instance, ownerUserID, createdByID uint, createdByType string, expiresInMinutes int) (*CreateInstanceShareResult, error) {
	minutes := normalizeShareMinutes(expiresInMinutes)
	expiresAt := time.Now().Add(time.Duration(minutes) * time.Minute)

	var token string
	var tokenHash string
	for attempts := 0; attempts < 3; attempts++ {
		generated, err := generateShareToken()
		if err != nil {
			return nil, err
		}
		token = generated
		tokenHash = hashShareToken(generated)

		var count int64
		if err := global.APP_DB.Model(&providerModel.InstanceShareLink{}).
			Where("token_hash = ?", tokenHash).
			Count(&count).Error; err != nil {
			return nil, err
		}
		if count == 0 {
			break
		}
		token = ""
	}
	if token == "" {
		return nil, fmt.Errorf("生成分享令牌失败")
	}

	prefix := token
	if len(prefix) > 12 {
		prefix = prefix[:12]
	}
	link := providerModel.InstanceShareLink{
		InstanceID:    instance.ID,
		OwnerUserID:   ownerUserID,
		CreatedByID:   createdByID,
		CreatedByType: createdByType,
		TokenHash:     tokenHash,
		TokenPrefix:   prefix,
		ExpiresAt:     expiresAt,
	}
	if err := global.APP_DB.Create(&link).Error; err != nil {
		return nil, err
	}
	return &CreateInstanceShareResult{
		Token:     token,
		URL:       buildShareURL(token),
		ExpiresAt: expiresAt,
	}, nil
}

func (s *InstanceShareService) Validate(token string) (*providerModel.InstanceShareLink, *providerModel.Instance, error) {
	token = strings.TrimSpace(token)
	if token == "" {
		return nil, nil, fmt.Errorf("分享令牌不能为空")
	}
	hash := hashShareToken(token)
	var link providerModel.InstanceShareLink
	if err := global.APP_DB.Where("token_hash = ?", hash).First(&link).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil, fmt.Errorf("分享链接无效")
		}
		return nil, nil, err
	}
	now := time.Now()
	if link.RevokedAt != nil || link.ExpiresAt.Before(now) {
		_ = global.APP_DB.Delete(&providerModel.InstanceShareLink{}, link.ID).Error
		return nil, nil, fmt.Errorf("分享链接已过期")
	}
	var instance providerModel.Instance
	if err := global.APP_DB.First(&instance, link.InstanceID).Error; err != nil {
		return nil, nil, fmt.Errorf("实例不存在")
	}
	_ = global.APP_DB.Model(&providerModel.InstanceShareLink{}).
		Where("id = ?", link.ID).
		Updates(map[string]interface{}{
			"last_used_at": now,
			"use_count":    gorm.Expr("use_count + ?", 1),
		}).Error
	return &link, &instance, nil
}
