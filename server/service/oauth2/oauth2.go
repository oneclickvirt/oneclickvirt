package oauth2

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"oneclickvirt/global"
	"oneclickvirt/model/common"
	oauth2Model "oneclickvirt/model/oauth2"
	"oneclickvirt/model/user"
	"oneclickvirt/utils"

	"go.uber.org/zap"
	"golang.org/x/crypto/bcrypt"
	"golang.org/x/oauth2"
	"gorm.io/gorm"
)

// Service OAuth2服务
type Service struct {
	states map[string]*StateInfo // state令牌存储
	mu     sync.RWMutex          // 保护states的互斥锁
}

// StateInfo 状态信息
type StateInfo struct {
	ProviderID uint      // OAuth2提供商ID
	Expiry     time.Time // 过期时间
}

// NewService 创建OAuth2服务实例
func NewService() *Service {
	return &Service{
		states: make(map[string]*StateInfo),
	}
}

// cleanExpiredStates 清理过期的state令牌
func (s *Service) cleanExpiredStates() {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now()
	for state, info := range s.states {
		if now.After(info.Expiry) {
			delete(s.states, state)
		}
	}
}

// GenerateStateToken 生成OAuth2 state令牌
func (s *Service) GenerateStateToken(providerID uint) (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	state := base64.URLEncoding.EncodeToString(b)

	// 从配置读取state令牌有效期，默认15分钟
	stateTokenMinutes := global.APP_CONFIG.System.OAuth2StateTokenMinutes
	if stateTokenMinutes <= 0 {
		stateTokenMinutes = 15 // 默认15分钟
	}

	expiryDuration := time.Duration(stateTokenMinutes) * time.Minute
	expiry := time.Now().Add(expiryDuration)

	s.mu.Lock()
	s.states[state] = &StateInfo{
		ProviderID: providerID,
		Expiry:     expiry,
	}
	s.mu.Unlock()

	global.APP_LOG.Debug("生成OAuth2 state令牌",
		zap.Uint("provider_id", providerID),
		zap.String("state", state[:16]+"..."), // 只记录部分state用于调试
		zap.Time("expiry", expiry),
		zap.Duration("valid_for", expiryDuration))

	// 清理过期的state
	go s.cleanExpiredStates()

	return state, nil
}

// ValidateStateToken 验证OAuth2 state令牌
func (s *Service) ValidateStateToken(state string) (uint, bool) {
	if state == "" {
		global.APP_LOG.Warn("收到空的state令牌")
		return 0, false
	}

	s.mu.RLock()
	info, exists := s.states[state]
	s.mu.RUnlock()

	if !exists {
		global.APP_LOG.Warn("state令牌不存在（可能已过期或已使用）",
			zap.String("state", state[:16]+"..."))
		return 0, false
	}

	// 检查是否过期
	if time.Now().After(info.Expiry) {
		s.mu.Lock()
		delete(s.states, state)
		s.mu.Unlock()

		global.APP_LOG.Warn("state令牌已过期",
			zap.String("state", state[:16]+"..."),
			zap.Time("expiry", info.Expiry),
			zap.Duration("过期时长", time.Since(info.Expiry)))
		return 0, false
	}

	// 验证成功后删除state（一次性使用）
	s.mu.Lock()
	delete(s.states, state)
	s.mu.Unlock()

	global.APP_LOG.Debug("state令牌验证成功",
		zap.Uint("provider_id", info.ProviderID),
		zap.String("state", state[:16]+"..."))

	return info.ProviderID, true
}

// GetProviderByID 根据ID获取OAuth2提供商配置
func (s *Service) GetProviderByID(id uint) (*oauth2Model.OAuth2Provider, error) {
	var provider oauth2Model.OAuth2Provider
	if err := global.APP_DB.First(&provider, id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, common.NewError(common.CodeOAuth2Failed, "OAuth2提供商不存在")
		}
		return nil, err
	}

	if !provider.Enabled {
		return nil, common.NewError(common.CodeOAuth2Failed, "OAuth2提供商未启用")
	}

	return &provider, nil
}

// GetProviderByName 根据名称获取OAuth2提供商配置
func (s *Service) GetProviderByName(name string) (*oauth2Model.OAuth2Provider, error) {
	var provider oauth2Model.OAuth2Provider
	if err := global.APP_DB.Where("name = ? AND enabled = ?", name, true).First(&provider).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, common.NewError(common.CodeOAuth2Failed, "OAuth2提供商不存在或未启用")
		}
		return nil, err
	}

	return &provider, nil
}

// GetAllEnabledProviders 获取所有启用的OAuth2提供商
func (s *Service) GetAllEnabledProviders() ([]oauth2Model.OAuth2Provider, error) {
	var providers []oauth2Model.OAuth2Provider
	if err := global.APP_DB.Where("enabled = ?", true).Order("sort ASC, id ASC").Find(&providers).Error; err != nil {
		return nil, err
	}
	return providers, nil
}

// GetOAuth2Config 获取OAuth2配置对象
func (s *Service) GetOAuth2Config(provider *oauth2Model.OAuth2Provider) *oauth2.Config {
	return &oauth2.Config{
		ClientID:     provider.ClientID,
		ClientSecret: provider.ClientSecret,
		RedirectURL:  provider.RedirectURL,
		Scopes:       []string{}, // 不使用权限范围，只用于基本用户注册
		Endpoint: oauth2.Endpoint{
			AuthURL:  provider.AuthURL,
			TokenURL: provider.TokenURL,
		},
	}
}

// GetNestedField 从嵌套的map中提取字段值，支持点号分隔的路径
func (s *Service) GetNestedField(data map[string]interface{}, fieldPath string) interface{} {
	if fieldPath == "" {
		return nil
	}

	parts := strings.Split(fieldPath, ".")
	current := data

	for i, part := range parts {
		val, ok := current[part]
		if !ok {
			return nil
		}

		// 如果是最后一个部分，返回值
		if i == len(parts)-1 {
			return val
		}

		// 否则继续深入
		current, ok = val.(map[string]interface{})
		if !ok {
			return nil
		}
	}

	return nil
}

// ExtractUserInfo 从OAuth2用户信息中提取字段
func (s *Service) ExtractUserInfo(provider *oauth2Model.OAuth2Provider, userInfoData map[string]interface{}) (*UserInfo, error) {
	userInfo := &UserInfo{}

	// 提取必需字段
	if uid := s.GetNestedField(userInfoData, provider.UserIDField); uid != nil {
		userInfo.ID = fmt.Sprintf("%v", uid)
	}
	if userInfo.ID == "" {
		return nil, errors.New("无法提取用户ID")
	}

	// 提取用户名（必需）
	if username := s.GetNestedField(userInfoData, provider.UsernameField); username != nil {
		userInfo.Username = fmt.Sprintf("%v", username)
	}
	if userInfo.Username == "" {
		// 如果没有用户名，使用提供商名称+ID作为用户名
		userInfo.Username = fmt.Sprintf("%s_user_%s", provider.Name, userInfo.ID)
	}

	// 提取可选字段
	if email := s.GetNestedField(userInfoData, provider.EmailField); email != nil {
		userInfo.Email = fmt.Sprintf("%v", email)
	}

	if avatar := s.GetNestedField(userInfoData, provider.AvatarField); avatar != nil {
		userInfo.Avatar = fmt.Sprintf("%v", avatar)
	}

	if nickname := s.GetNestedField(userInfoData, provider.NicknameField); nickname != nil {
		userInfo.Nickname = fmt.Sprintf("%v", nickname)
	}

	// 提取信任等级（可选）
	if provider.TrustLevelField != "" {
		if trustLevel := s.GetNestedField(userInfoData, provider.TrustLevelField); trustLevel != nil {
			// 尝试转换为整数
			switch v := trustLevel.(type) {
			case float64:
				userInfo.TrustLevel = int(v)
			case int:
				userInfo.TrustLevel = v
			case string:
				fmt.Sscanf(v, "%d", &userInfo.TrustLevel)
			}
		}
	}

	// 存储原始数据
	extraData, _ := json.Marshal(userInfoData)
	userInfo.RawData = string(extraData)

	return userInfo, nil
}

// UserInfo OAuth2用户信息
type UserInfo struct {
	ID         string // OAuth2提供商返回的用户ID
	Username   string // 用户名
	Email      string // 邮箱
	Avatar     string // 头像URL
	Nickname   string // 昵称
	TrustLevel int    // 信任等级
	RawData    string // 原始JSON数据
}

// FetchUserInfo 获取OAuth2用户信息
func (s *Service) FetchUserInfo(provider *oauth2Model.OAuth2Provider, token *oauth2.Token) (map[string]interface{}, error) {
	client := oauth2.NewClient(context.Background(), oauth2.StaticTokenSource(token))
	resp, err := client.Get(provider.UserInfoURL)
	if err != nil {
		return nil, fmt.Errorf("获取用户信息失败: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("获取用户信息失败: HTTP %d, %s", resp.StatusCode, string(body))
	}

	var userInfoData map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&userInfoData); err != nil {
		return nil, fmt.Errorf("解析用户信息失败: %w", err)
	}

	return userInfoData, nil
}

// GetUserLevel 根据信任等级获取系统用户等级
func (s *Service) GetUserLevel(provider *oauth2Model.OAuth2Provider, trustLevel int) int {
	// 解析等级映射
	if provider.LevelMapping != "" {
		var mapping map[string]int
		if err := json.Unmarshal([]byte(provider.LevelMapping), &mapping); err == nil {
			// 尝试查找对应的等级
			key := fmt.Sprintf("%d", trustLevel)
			if level, ok := mapping[key]; ok {
				return level
			}
		}
	}

	// 返回默认等级
	return provider.DefaultLevel
}

// HandleCallback 处理OAuth2回调
func (s *Service) HandleCallback(providerID uint, code string) (*user.User, string, error) {
	// 获取提供商配置
	provider, err := s.GetProviderByID(providerID)
	if err != nil {
		return nil, "", err
	}

	// 交换授权码获取令牌
	oauth2Cfg := s.GetOAuth2Config(provider)
	token, err := oauth2Cfg.Exchange(context.Background(), code)
	if err != nil {
		global.APP_LOG.Error("交换OAuth2令牌失败", zap.Error(err))
		return nil, "", common.NewError(common.CodeOAuth2Failed, "授权失败")
	}

	// 获取用户信息
	userInfoData, err := s.FetchUserInfo(provider, token)
	if err != nil {
		global.APP_LOG.Error("获取OAuth2用户信息失败", zap.Error(err))
		return nil, "", common.NewError(common.CodeOAuth2Failed, "获取用户信息失败")
	}

	// 提取用户信息
	userInfo, err := s.ExtractUserInfo(provider, userInfoData)
	if err != nil {
		global.APP_LOG.Error("提取OAuth2用户信息失败", zap.Error(err))
		return nil, "", common.NewError(common.CodeOAuth2Failed, "用户信息格式错误")
	}

	// 检查用户是否已存在
	var existingUser user.User
	err = global.APP_DB.Where(&user.User{OAuth2ProviderID: providerID, OAuth2UID: userInfo.ID}).First(&existingUser).Error
	isUserExists := err == nil

	// 如果用户不存在且已达到注册限制，拒绝注册
	if !isUserExists && provider.MaxRegistrations > 0 && provider.CurrentRegistrations >= provider.MaxRegistrations {
		return nil, "", common.NewError(common.CodeOAuth2RegistrationLimit, fmt.Sprintf("%s 注册已达限制", provider.DisplayName))
	}

	// 查找或创建用户
	usr, isNewUser, err := s.FindOrCreateUser(provider, userInfo)
	if err != nil {
		return nil, "", err
	}

	// 生成JWT令牌
	jwtToken, err := utils.GenerateToken(usr.ID, usr.Username, usr.UserType)
	if err != nil {
		global.APP_LOG.Error("生成JWT令牌失败", zap.Error(err))
		return nil, "", common.NewError(common.CodeInternalError, "生成令牌失败")
	}

	// 更新最后登录时间
	now := time.Now()
	global.APP_DB.Model(usr).Update("last_login_at", now)

	// 如果是新用户，更新提供商的注册计数
	if isNewUser {
		global.APP_DB.Model(&oauth2Model.OAuth2Provider{}).
			Where("id = ?", providerID).
			Updates(map[string]interface{}{
				"current_registrations": gorm.Expr("current_registrations + 1"),
				"total_users":           gorm.Expr("total_users + 1"),
			})
	}

	return usr, jwtToken, nil
}

// FindOrCreateUser 查找或创建用户
func (s *Service) FindOrCreateUser(provider *oauth2Model.OAuth2Provider, userInfo *UserInfo) (*user.User, bool, error) {
	// 首先通过OAuth2 UID查找用户
	var usr user.User
	err := global.APP_DB.Where(&user.User{OAuth2ProviderID: provider.ID, OAuth2UID: userInfo.ID}).First(&usr).Error

	if err == nil {
		// 用户已存在，更新OAuth2相关信息
		updates := map[string]interface{}{
			"OAuth2Username": userInfo.Username,
			"OAuth2Email":    userInfo.Email,
			"OAuth2Avatar":   userInfo.Avatar,
			"OAuth2Extra":    userInfo.RawData,
		}

		// 更新昵称和头像（如果用户未自定义）
		if userInfo.Nickname != "" && usr.Nickname == "" {
			updates["nickname"] = userInfo.Nickname
		}
		if userInfo.Avatar != "" && usr.Avatar == "" {
			updates["avatar"] = userInfo.Avatar
		}

		global.APP_DB.Model(&usr).Updates(updates)
		return &usr, false, nil
	}

	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, false, err
	}

	// 用户不存在，创建新用户
	return s.CreateUser(provider, userInfo)
}

// CreateUser 创建新用户
func (s *Service) CreateUser(provider *oauth2Model.OAuth2Provider, userInfo *UserInfo) (*user.User, bool, error) {
	// 生成用户名（确保唯一）
	username := s.GenerateUniqueUsername(userInfo.Username)

	// 生成随机密码（OAuth2用户不需要密码登录）
	randomPassword := generateRandomPassword(32)
	hashedPassword, err := bcrypt.GenerateFromPassword([]byte(randomPassword), bcrypt.DefaultCost)
	if err != nil {
		return nil, false, err
	}

	// 确定用户等级
	userLevel := s.GetUserLevel(provider, userInfo.TrustLevel)

	// 创建用户
	usr := &user.User{
		Username:         username,
		Password:         string(hashedPassword),
		Nickname:         userInfo.Nickname,
		Email:            userInfo.Email,
		Avatar:           userInfo.Avatar,
		Status:           1,
		Level:            userLevel,
		UserType:         "user",
		OAuth2ProviderID: provider.ID,
		OAuth2UID:        userInfo.ID,
		OAuth2Username:   userInfo.Username,
		OAuth2Email:      userInfo.Email,
		OAuth2Avatar:     userInfo.Avatar,
		OAuth2Extra:      userInfo.RawData,
	}

	// 设置昵称默认值
	if usr.Nickname == "" {
		usr.Nickname = username
	}

	// 根据用户等级设置配额
	s.SetUserQuotaByLevel(usr)

	// 使用带重试的数据库操作创建用户
	err = utils.RetryableDBOperation(context.Background(), func() error {
		return global.APP_DB.Create(usr).Error
	}, 3)

	if err != nil {
		global.APP_LOG.Error("创建OAuth2用户失败", zap.Error(err))
		return nil, false, common.NewError(common.CodeInternalError, "创建用户失败")
	}

	global.APP_LOG.Info("OAuth2用户注册成功",
		zap.String("provider", provider.Name),
		zap.String("oauth2_uid", userInfo.ID),
		zap.String("username", username),
		zap.Int("level", userLevel))

	return usr, true, nil
}

// generateRandomPassword 生成随机密码
func generateRandomPassword(length int) string {
	const charset = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	b := make([]byte, length)
	rand.Read(b)
	for i := range b {
		b[i] = charset[int(b[i])%len(charset)]
	}
	return string(b)
}

// GenerateUniqueUsername 生成唯一用户名
func (s *Service) GenerateUniqueUsername(baseUsername string) string {
	username := baseUsername
	suffix := 1

	for {
		var count int64
		global.APP_DB.Model(&user.User{}).Where("username = ?", username).Count(&count)
		if count == 0 {
			return username
		}
		username = fmt.Sprintf("%s_%d", baseUsername, suffix)
		suffix++
	}
}

// SetUserQuotaByLevel 根据用户等级设置配额
func (s *Service) SetUserQuotaByLevel(usr *user.User) {
	levelLimits, ok := global.APP_CONFIG.Quota.LevelLimits[usr.Level]
	if !ok {
		// 使用默认等级配置
		levelLimits = global.APP_CONFIG.Quota.LevelLimits[global.APP_CONFIG.Quota.DefaultLevel]
	}

	usr.MaxInstances = levelLimits.MaxInstances

	// 设置资源限制
	if maxRes, ok := levelLimits.MaxResources["cpu"].(int); ok {
		usr.MaxCPU = maxRes
	}
	if maxRes, ok := levelLimits.MaxResources["memory"].(int); ok {
		usr.MaxMemory = maxRes
	}
	if maxRes, ok := levelLimits.MaxResources["disk"].(int); ok {
		usr.MaxDisk = maxRes
	}
	if maxRes, ok := levelLimits.MaxResources["bandwidth"].(int); ok {
		usr.MaxBandwidth = maxRes
	}

	// 不再自动设置流量限制，保持为0，只有当用户实例所在Provider启用流量统计时才设置
	// usr.TotalTraffic = levelLimits.MaxTraffic
	usr.TotalTraffic = 0
}
