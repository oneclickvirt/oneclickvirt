package oauth2

import (
	"time"

	"gorm.io/gorm"
)

// OAuth2Provider OAuth2提供商配置模型
type OAuth2Provider struct {
	ID        uint           `json:"id" gorm:"primarykey"`
	CreatedAt time.Time      `json:"createdAt"`
	UpdatedAt time.Time      `json:"updatedAt"`
	DeletedAt gorm.DeletedAt `json:"-" gorm:"index"`

	// 基础信息
	Name         string `json:"name" gorm:"uniqueIndex;not null;size:64"` // 提供商名称（唯一标识，如 linuxdo, github, google）
	DisplayName  string `json:"displayName" gorm:"not null;size:128"`     // 显示名称
	ProviderType string `json:"providerType" gorm:"not null;size:32"`     // 提供商类型（preset:预设, generic:通用）
	Enabled      bool   `json:"enabled" gorm:"default:false"`             // 是否启用

	// OAuth2配置
	ClientID     string `json:"clientId" gorm:"not null;size:255"`     // OAuth2客户端ID
	ClientSecret string `json:"clientSecret" gorm:"not null;size:255"` // OAuth2客户端密钥
	RedirectURL  string `json:"redirectUrl" gorm:"not null;size:512"`  // OAuth2回调地址
	AuthURL      string `json:"authUrl" gorm:"not null;size:512"`      // OAuth2授权地址
	TokenURL     string `json:"tokenUrl" gorm:"not null;size:512"`     // OAuth2令牌地址
	UserInfoURL  string `json:"userInfoUrl" gorm:"not null;size:512"`  // OAuth2用户信息地址

	// 字段映射（支持嵌套字段，如 user.profile.name）
	UserIDField   string `json:"userIdField" gorm:"default:id;size:128"`         // 用户ID字段映射
	UsernameField string `json:"usernameField" gorm:"default:username;size:128"` // 用户名字段映射
	EmailField    string `json:"emailField" gorm:"default:email;size:128"`       // 邮箱字段映射
	AvatarField   string `json:"avatarField" gorm:"default:avatar;size:128"`     // 头像字段映射

	// 可选字段映射
	NicknameField   string `json:"nicknameField" gorm:"size:128"`   // 昵称字段映射（可选）
	TrustLevelField string `json:"trustLevelField" gorm:"size:128"` // 信任等级字段映射（可选，如LinuxDo的trust_level）

	// 注册限制
	MaxRegistrations     int `json:"maxRegistrations" gorm:"default:0"`     // 注册数量限制，0为无限制
	CurrentRegistrations int `json:"currentRegistrations" gorm:"default:0"` // 当前注册数量

	// 等级映射（JSON字符串，如 {"0":1,"1":2,"2":3}）
	// 映射外部信任等级到系统用户等级
	LevelMapping string `json:"levelMapping" gorm:"type:text"` // JSON格式的等级映射配置

	// 默认等级（当没有等级映射或无法提取等级时使用）
	DefaultLevel int `json:"defaultLevel" gorm:"default:1"` // 默认用户等级

	// 统计信息
	TotalUsers int `json:"totalUsers" gorm:"default:0"` // 通过此提供商注册的总用户数

	// 显示顺序
	Sort int `json:"sort" gorm:"default:0"` // 显示顺序，数字越小越靠前
}
