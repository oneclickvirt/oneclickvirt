package config

import "time"

type UpdateConfigRequest struct {
	Auth       AuthConfig       `json:"auth"`
	Quota      QuotaConfig      `json:"quota"`
	InviteCode InviteCodeConfig `json:"inviteCode"`
	Other      OtherConfig      `json:"other"`
}

type AuthConfig struct {
	EnableEmail              bool   `json:"enableEmail"`
	EnableTelegram           bool   `json:"enableTelegram"`
	EnableQQ                 bool   `json:"enableQQ"`
	EnableOAuth2             bool   `json:"enableOAuth2"`             // 是否启用OAuth2登录
	EnablePublicRegistration bool   `json:"enablePublicRegistration"` // 是否启用公开注册（无需邀请码）
	EmailSMTPHost            string `json:"emailSMTPHost"`
	EmailSMTPPort            int    `json:"emailSMTPPort"`
	EmailUsername            string `json:"emailUsername"`
	EmailPassword            string `json:"emailPassword"`
	TelegramBotToken         string `json:"telegramBotToken"`
	QQAppID                  string `json:"qqAppID"`
	QQAppKey                 string `json:"qqAppKey"`
}

type InviteCodeConfig struct {
	Enabled  bool `json:"enabled"`  // 是否启用邀请码系统
	Required bool `json:"required"` // 是否必须邀请码（兼容旧字段）
}

type OtherConfig struct {
	MaxAvatarSize   float64 `json:"maxAvatarSize"`   // 头像最大大小(MB)
	DefaultLanguage string  `json:"defaultLanguage"` // 系统默认语言，空字符串表示使用浏览器语言
}

type QuotaConfig struct {
	DefaultLevel int                    `json:"defaultLevel"`
	LevelLimits  map[int]LevelLimitInfo `json:"levelLimits"`
}

type LevelLimitInfo struct {
	MaxInstances int                    `json:"maxInstances"`
	MaxResources map[string]interface{} `json:"maxResources"`
	MaxTraffic   int64                  `json:"maxTraffic"`                                // 最大流量限制(MB)
	ExpiryDays   int                    `json:"expiryDays"`                                // 新注册用户的默认过期天数，0表示不过期
	ExpiryTime   *time.Time             `json:"expiryTime,omitempty" swaggertype:"string"` // 具体过期时间（用于计算，前端不需要传）
}

// DatabaseConfig 数据库初始化配置
type DatabaseConfig struct {
	Type         string `json:"type" binding:"required"`
	Host         string `json:"host" binding:"required"`
	Port         int    `json:"port" binding:"required,min=1,max=65535"`
	Username     string `json:"username" binding:"required"`
	Password     string `json:"password"`
	Database     string `json:"database" binding:"required"`
	DatabaseName string `json:"databaseName"`
	SSLMode      string `json:"sslMode"`
}

// ConfigTaskContext 配置任务上下文
type ConfigTaskContext struct {
	RequestID    string                 `json:"requestId"`
	UserID       uint                   `json:"userId"`
	OriginalData map[string]interface{} `json:"originalData"`
	TargetData   map[string]interface{} `json:"targetData"`
}

// DailyLogConfig 日志轮转配置
type DailyLogConfig struct {
	BaseDir    string `json:"baseDir"`    // 基础日志目录
	FileName   string `json:"fileName"`   // 文件名
	MaxSize    int64  `json:"maxSize"`    // 最大文件大小(字节)
	MaxAge     int    `json:"maxAge"`     // 保留天数
	MaxBackups int    `json:"maxBackups"` // 最大备份数
	LocalTime  bool   `json:"localTime"`  // 使用本地时间
}
