package auth

// LoginRequest 登录请求
type LoginRequest struct {
	Username   string `json:"username" example:"admin"`    // 用户名登录时必填
	Password   string `json:"password" example:"password"` // 密码登录时必填
	Captcha    string `json:"captcha,omitempty"`           // 图形验证码
	CaptchaId  string `json:"captchaId,omitempty"`         // 图形验证码ID
	LoginType  string `json:"loginType,omitempty"`         // 登录类型: username(用户名密码), email(邮箱验证码), telegram(TG验证码), qq(QQ验证码)
	UserType   string `json:"userType,omitempty"`          // 用户类型: admin, user
	Target     string `json:"target,omitempty"`            // 验证码登录时的目标: 邮箱地址/TG用户名/QQ号
	VerifyCode string `json:"verifyCode,omitempty"`        // 验证码登录时的验证码
}

// SendVerifyCodeRequest 发送验证码请求
type SendVerifyCodeRequest struct {
	Type      string `json:"type" binding:"required"`   // 验证码类型: email, telegram, qq
	Target    string `json:"target" binding:"required"` // 目标: 邮箱地址/TG用户名或ID/QQ号
	CaptchaId string `json:"captchaId,omitempty"`       // 图形验证码ID（可选，根据配置）
	Captcha   string `json:"captcha,omitempty"`         // 图形验证码（可选，根据配置）
}

// RegisterRequest 注册请求
type RegisterRequest struct {
	Username     string `json:"username" binding:"required" example:"user123"`
	Password     string `json:"password" binding:"required" example:"password123"`
	Nickname     string `json:"nickname" example:"昵称"`
	Email        string `json:"email" example:"user@example.com"`
	Phone        string `json:"phone,omitempty" example:"13800138000"`
	Telegram     string `json:"telegram,omitempty"`
	QQ           string `json:"qq,omitempty"`
	InviteCode   string `json:"inviteCode" example:"INVITE123"`
	Captcha      string `json:"captcha"`
	CaptchaId    string `json:"captchaId"`
	RegisterType string `json:"registerType,omitempty"` // 注册类型，前端兼容字段
}

// ForgotPasswordRequest 忘记密码请求
type ForgotPasswordRequest struct {
	Email     string `json:"email" binding:"required,email" example:"user@example.com"`
	CaptchaId string `json:"captchaId,omitempty"`
	Captcha   string `json:"captcha,omitempty"`
	UserType  string `json:"userType,omitempty"`
}

// ResetPasswordRequest 重置密码请求
type ResetPasswordRequest struct {
	Token string `json:"token" binding:"required"`
}

// GetCaptchaRequest 获取验证码请求
type GetCaptchaRequest struct {
	Type   string `json:"type,omitempty"` // 验证码类型，可选
	Width  int    `json:"width,omitempty"`
	Height int    `json:"height,omitempty"`
}

// LoginResponse 登录响应
type LoginResponse struct {
	Token string `json:"token"`
}

// CaptchaResponse 验证码响应
type CaptchaResponse struct {
	CaptchaId string `json:"captchaId"`
	PicPath   string `json:"picPath"`
	ImageData string `json:"imageData"`
}

type ExportUsersRequest struct {
	UserIDs   []uint   `json:"userIds"`
	Status    *int     `json:"status"`
	StartTime string   `json:"startTime"`
	EndTime   string   `json:"endTime"`
	Format    string   `json:"format"`
	Fields    []string `json:"fields"`
}
type ExportOperationLogsRequest struct {
	UserID    *uint  `json:"userId"`
	UserIDs   []uint `json:"userIds"`
	Action    string `json:"action"`
	Resource  string `json:"resource"`
	StartTime string `json:"startTime"`
	EndTime   string `json:"endTime"`
	Format    string `json:"format"`
}
type BatchGenerateInviteCodesRequest struct {
	Count       int    `json:"count"`
	MaxUse      int    `json:"maxUse"`
	ExpireAt    string `json:"expireAt"`
	ExpireDays  int    `json:"expireDays"`
	Remark      string `json:"remark"`
	Description string `json:"description"`
	Length      int    `json:"length"` // 邀请码长度，默认8位
}
type SearchUsersRequest struct {
	Keyword   string `json:"keyword"`
	UserType  string `json:"userType"`
	Status    *int   `json:"status"`
	RoleID    *uint  `json:"roleId"`
	StartTime string `json:"startTime"`
	EndTime   string `json:"endTime"`
	SortBy    string `json:"sortBy"`
	SortOrder string `json:"sortOrder"`
	Page      int    `json:"page"`
	PageSize  int    `json:"pageSize"`
}
