package config

type Server struct {
	JWT        JWT        `mapstructure:"jwt" json:"jwt" yaml:"jwt"`
	Zap        Zap        `mapstructure:"zap" json:"zap" yaml:"zap"`
	System     System     `mapstructure:"system" json:"system" yaml:"system"`
	Mysql      Mysql      `mapstructure:"mysql" json:"mysql" yaml:"mysql"`
	Auth       Auth       `mapstructure:"auth" json:"auth" yaml:"auth"`
	Quota      Quota      `mapstructure:"quota" json:"quota" yaml:"quota"`
	InviteCode InviteCode `mapstructure:"invite-code" json:"invite-code" yaml:"invite-code"`
	Captcha    Captcha    `mapstructure:"captcha" json:"captcha" yaml:"captcha"`
	Cors       CORS       `mapstructure:"cors" json:"cors" yaml:"cors"`
	Redis      Redis      `mapstructure:"redis" json:"redis" yaml:"redis"`
	CDN        CDN        `mapstructure:"cdn" json:"cdn" yaml:"cdn"`
	Task       Task       `mapstructure:"task" json:"task" yaml:"task"`
	Upload     Upload     `mapstructure:"upload" json:"upload" yaml:"upload"`
	Other      Other      `mapstructure:"other" json:"other" yaml:"other"`
}

type Other struct {
	MaxAvatarSize   float64 `mapstructure:"max-avatar-size" json:"max-avatar-size" yaml:"max-avatar-size"`    // 头像最大大小（MB）
	DefaultLanguage string  `mapstructure:"default-language" json:"default-language" yaml:"default-language"` // 默认语言
}

type CORS struct {
	Mode      string   `mapstructure:"mode" json:"mode" yaml:"mode"`
	Whitelist []string `mapstructure:"whitelist" json:"whitelist" yaml:"whitelist"`
}

type Auth struct {
	EnableEmail              bool   `mapstructure:"enable-email" json:"enable-email" yaml:"enable-email"`
	EnableTelegram           bool   `mapstructure:"enable-telegram" json:"enable-telegram" yaml:"enable-telegram"`
	EnableQQ                 bool   `mapstructure:"enable-qq" json:"enable-qq" yaml:"enable-qq"`
	EnableOAuth2             bool   `mapstructure:"enable-oauth2" json:"enable-oauth2" yaml:"enable-oauth2"`                                        // 是否启用OAuth2登录（全局开关）
	EnablePublicRegistration bool   `mapstructure:"enable-public-registration" json:"enable-public-registration" yaml:"enable-public-registration"` // 是否启用公开注册（无需邀请码）
	EmailSMTPHost            string `mapstructure:"email-smtp-host" json:"email-smtp-host" yaml:"email-smtp-host"`
	EmailSMTPPort            int    `mapstructure:"email-smtp-port" json:"email-smtp-port" yaml:"email-smtp-port"`
	EmailUsername            string `mapstructure:"email-username" json:"email-username" yaml:"email-username"`
	EmailPassword            string `mapstructure:"email-password" json:"email-password" yaml:"email-password"`
	TelegramBotToken         string `mapstructure:"telegram-bot-token" json:"telegram-bot-token" yaml:"telegram-bot-token"`
	QQAppID                  string `mapstructure:"qq-app-id" json:"qq-app-id" yaml:"qq-app-id"`
	QQAppKey                 string `mapstructure:"qq-app-key" json:"qq-app-key" yaml:"qq-app-key"`
}

type Quota struct {
	DefaultLevel            int                     `mapstructure:"default-level" json:"default-level" yaml:"default-level"`
	LevelLimits             map[int]LevelLimitInfo  `mapstructure:"level-limits" json:"level-limits" yaml:"level-limits"`
	InstanceTypePermissions InstanceTypePermissions `mapstructure:"instance-type-permissions" json:"instance-type-permissions" yaml:"instance-type-permissions"`
}

type InstanceTypePermissions struct {
	MinLevelForContainer       int `mapstructure:"min-level-for-container" json:"min-level-for-container" yaml:"min-level-for-container"`
	MinLevelForVM              int `mapstructure:"min-level-for-vm" json:"min-level-for-vm" yaml:"min-level-for-vm"`
	MinLevelForDeleteContainer int `mapstructure:"min-level-for-delete-container" json:"min-level-for-delete-container" yaml:"min-level-for-delete-container"`
	MinLevelForDeleteVM        int `mapstructure:"min-level-for-delete-vm" json:"min-level-for-delete-vm" yaml:"min-level-for-delete-vm"`
	MinLevelForResetContainer  int `mapstructure:"min-level-for-reset-container" json:"min-level-for-reset-container" yaml:"min-level-for-reset-container"`
	MinLevelForResetVM         int `mapstructure:"min-level-for-reset-vm" json:"min-level-for-reset-vm" yaml:"min-level-for-reset-vm"`
}

type LevelLimitInfo struct {
	MaxInstances int                    `mapstructure:"max-instances" json:"max-instances" yaml:"max-instances"`
	MaxResources map[string]interface{} `mapstructure:"max-resources" json:"max-resources" yaml:"max-resources"`
	MaxTraffic   int64                  `mapstructure:"max-traffic" json:"max-traffic" yaml:"max-traffic"` // 最大流量限制（MB）
	ExpiryDays   int                    `mapstructure:"expiry-days" json:"expiry-days" yaml:"expiry-days"` // 新注册用户的默认过期天数，0表示不过期
}

type System struct {
	Env                     string `mapstructure:"env" json:"env" yaml:"env"`                                                                      // 环境值
	Addr                    int    `mapstructure:"addr" json:"addr" yaml:"addr"`                                                                   // 端口值
	DbType                  string `mapstructure:"db-type" json:"db-type" yaml:"db-type"`                                                          // 数据库类型:mysql(默认)|mariadb
	OssType                 string `mapstructure:"oss-type" json:"oss-type" yaml:"oss-type"`                                                       // Oss类型
	UseMultipoint           bool   `mapstructure:"use-multipoint" json:"use-multipoint" yaml:"use-multipoint"`                                     // 多点登录拦截
	UseRedis                bool   `mapstructure:"use-redis" json:"use-redis" yaml:"use-redis"`                                                    // 使用redis
	LimitCountIP            int    `mapstructure:"iplimit-count" json:"iplimit-count" yaml:"iplimit-count"`                                        // IP限流计数
	LimitTimeIP             int    `mapstructure:"iplimit-time" json:"iplimit-time" yaml:"iplimit-time"`                                           // IP限流时间
	FrontendURL             string `mapstructure:"frontend-url" json:"frontend-url" yaml:"frontend-url"`                                           // 前端URL，用于OAuth2回调跳转
	ProviderInactiveHours   int    `mapstructure:"provider-inactive-hours" json:"provider-inactive-hours" yaml:"provider-inactive-hours"`          // Provider不活动阈值（小时），默认72小时
	OAuth2StateTokenMinutes int    `mapstructure:"oauth2-state-token-minutes" json:"oauth2-state-token-minutes" yaml:"oauth2-state-token-minutes"` // OAuth2 State令牌有效期（分钟），默认15分钟
}

type JWT struct {
	SigningKey  string `mapstructure:"signing-key" json:"signing-key" yaml:"signing-key"`    // jwt签名
	ExpiresTime string `mapstructure:"expires-time" json:"expires-time" yaml:"expires-time"` // 过期时间
	BufferTime  string `mapstructure:"buffer-time" json:"buffer-time" yaml:"buffer-time"`    // 缓冲时间
	Issuer      string `mapstructure:"issuer" json:"issuer" yaml:"issuer"`                   // 签发者
}

// Database 数据库配置，支持MySQL和MariaDB
type Mysql struct {
	Path         string `mapstructure:"path" json:"path" yaml:"path"`                               // 服务器地址:端口
	Port         string `mapstructure:"port" json:"port" yaml:"port"`                               //:端口
	Config       string `mapstructure:"config" json:"config" yaml:"config"`                         // 高级配置
	Dbname       string `mapstructure:"db-name" json:"db-name" yaml:"db-name"`                      // 数据库名
	Username     string `mapstructure:"username" json:"username" yaml:"username"`                   // 数据库用户名
	Password     string `mapstructure:"password" json:"password" yaml:"password"`                   // 数据库密码
	Prefix       string `mapstructure:"prefix" json:"prefix" yaml:"prefix"`                         //全局表前缀，单独定义TableName则不生效
	Singular     bool   `mapstructure:"singular" json:"singular" yaml:"singular"`                   //是否开启全局禁用复数，true表示开启
	Engine       string `mapstructure:"engine" json:"engine" yaml:"engine" default:"InnoDB"`        //数据库引擎，默认InnoDB
	MaxIdleConns int    `mapstructure:"max-idle-conns" json:"max-idle-conns" yaml:"max-idle-conns"` // 空闲中的最大连接数
	MaxOpenConns int    `mapstructure:"max-open-conns" json:"max-open-conns" yaml:"max-open-conns"` // 打开到数据库的最大连接数
	LogMode      string `mapstructure:"log-mode" json:"log-mode" yaml:"log-mode"`                   // 是否开启Gorm全局日志
	LogZap       bool   `mapstructure:"log-zap" json:"log-zap" yaml:"log-zap"`                      // 是否通过zap写入日志文件
	MaxLifetime  int    `mapstructure:"max-lifetime" json:"max-lifetime" yaml:"max-lifetime"`       // 连接最大生存时间（秒）
	AutoCreate   bool   `mapstructure:"auto-create" json:"auto-create" yaml:"auto-create"`          // 是否自动创建数据库
}

type InviteCode struct {
	Enabled  bool `mapstructure:"enabled" json:"enabled" yaml:"enabled"`    // 是否启用邀请码
	Required bool `mapstructure:"required" json:"required" yaml:"required"` // 是否必须邀请码
}

type Captcha struct {
	Enabled    bool `mapstructure:"enabled" json:"enabled" yaml:"enabled"`             // 是否启用验证码
	Width      int  `mapstructure:"width" json:"width" yaml:"width"`                   // 验证码宽度
	Height     int  `mapstructure:"height" json:"height" yaml:"height"`                // 验证码高度
	Length     int  `mapstructure:"length" json:"length" yaml:"length"`                // 验证码长度
	ExpireTime int  `mapstructure:"expire-time" json:"expire-time" yaml:"expire-time"` // 过期时间(分钟)
}

// Redis 配置
type Redis struct {
	Addr     string `mapstructure:"addr" json:"addr" yaml:"addr"`             // Redis服务器地址
	Password string `mapstructure:"password" json:"password" yaml:"password"` // Redis密码
	DB       int    `mapstructure:"db" json:"db" yaml:"db"`                   // Redis数据库
}

// CDN 配置
type CDN struct {
	Endpoints    []string `mapstructure:"endpoints" json:"endpoints" yaml:"endpoints"`             // CDN端点列表
	BaseEndpoint string   `mapstructure:"base-endpoint" json:"base-endpoint" yaml:"base-endpoint"` // 基础CDN端点
}

// Task 任务配置
type Task struct {
	DeleteRetryCount int `mapstructure:"delete-retry-count" json:"delete-retry-count" yaml:"delete-retry-count"` // 删除实例重试次数，默认3
	DeleteRetryDelay int `mapstructure:"delete-retry-delay" json:"delete-retry-delay" yaml:"delete-retry-delay"` // 删除实例重试延迟（秒），默认2
}

// Upload 上传配置
type Upload struct {
	MaxAvatarSize int64 `mapstructure:"max-avatar-size" json:"max-avatar-size" yaml:"max-avatar-size"` // 头像最大大小（MB）
}
