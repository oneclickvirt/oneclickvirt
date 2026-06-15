package domain

import (
	"fmt"
	"net"
	"regexp"
	"strings"
	"time"

	"oneclickvirt/global"
	domainModel "oneclickvirt/model/domain"
	monitoringModel "oneclickvirt/model/monitoring"
	providerModel "oneclickvirt/model/provider"
	"oneclickvirt/service/agent"

	"go.uber.org/zap"
	"gorm.io/gorm"
)

// Service 域名绑定服务
type Service struct{}

type AdminDomainListRequest struct {
	Page       int
	PageSize   int
	Keyword    string
	Status     string
	UserID     uint
	ProviderID uint
	InstanceID uint
}

type AdminDomainListItem struct {
	ID           uint       `json:"id" gorm:"column:id"`
	CreatedAt    time.Time  `json:"createdAt" gorm:"column:created_at"`
	UpdatedAt    time.Time  `json:"updatedAt" gorm:"column:updated_at"`
	UserID       uint       `json:"userId" gorm:"column:user_id"`
	Username     string     `json:"username" gorm:"column:username"`
	UserNickname string     `json:"userNickname" gorm:"column:user_nickname"`
	InstanceID   uint       `json:"instanceId" gorm:"column:instance_id"`
	InstanceName string     `json:"instanceName" gorm:"column:instance_name"`
	ProviderID   uint       `json:"providerId" gorm:"column:provider_id"`
	ProviderName string     `json:"providerName" gorm:"column:provider_name"`
	ProviderType string     `json:"providerType" gorm:"column:provider_type"`
	DomainName   string     `json:"domainName" gorm:"column:domain_name"`
	Protocol     string     `json:"protocol" gorm:"column:protocol"`
	InternalIP   string     `json:"internalIP" gorm:"column:internal_ip"`
	InternalPort int        `json:"internalPort" gorm:"column:internal_port"`
	EnableSSL    bool       `json:"enableSSL" gorm:"column:enable_ssl"`
	HasCert      bool       `json:"hasCert" gorm:"column:has_cert"`
	Status       string     `json:"status" gorm:"column:status"`
	ErrorMsg     string     `json:"errorMsg" gorm:"column:error_msg"`
	ExpiresAt    *time.Time `json:"expiresAt" gorm:"column:expires_at"`
}

var domainRegex = regexp.MustCompile(`^([a-zA-Z0-9]([a-zA-Z0-9-]*[a-zA-Z0-9])?\.)+[a-zA-Z]{2,}$`)

func init() {
	agent.RegisterAgentReconnectHook(func(providerID uint) {
		svc := &Service{}
		result, err := svc.SyncProviderDomainProxies(providerID)
		if err != nil {
			global.APP_LOG.Warn("Agent 重连后同步域名代理失败",
				zap.Uint("providerID", providerID),
				zap.Error(err))
			return
		}
		if result.Total > 0 {
			global.APP_LOG.Info("Agent 重连后域名代理同步完成",
				zap.Uint("providerID", providerID),
				zap.Int("total", result.Total),
				zap.Int("success", result.Success),
				zap.Int("failed", result.Failed),
				zap.Int("skipped", result.Skipped))
		}
	})
}

// getAgentClient returns an agent client for the given provider, or nil if agent is not configured.
// For agent-mode providers behind NAT, the HTTP API is not directly reachable;
// the WS fallback in Client.doRequest handles connectivity via WebSocket.
func getAgentClient(providerID uint) *agent.Client {
	var p providerModel.Provider
	if err := global.APP_DB.First(&p, providerID).Error; err != nil {
		return nil
	}
	var config monitoringModel.MonitoringConfig
	if err := global.APP_DB.Where("provider_id = ?", providerID).First(&config).Error; err != nil {
		return nil
	}
	if config.AgentToken == "" {
		return nil
	}
	host := agent.ResolveAgentHost(p.Endpoint, p.AgentRemoteIP)
	if host == "" {
		if p.ConnectionType == "agent" {
			host = "127.0.0.1" // loopback fallback; calls are routed through WS fallback
		} else {
			return nil
		}
	}
	port := config.AgentPort
	if port == 0 {
		port = agent.AgentPort
	}
	return agent.GetClientWithMode(providerID, host, port, config.AgentToken, p.ConnectionType == "agent")
}

func normalizeDomainName(domain string) string {
	return strings.ToLower(strings.TrimSpace(domain))
}

func normalizeProtocol(protocol string) (string, error) {
	protocol = strings.ToLower(strings.TrimSpace(protocol))
	if protocol == "" {
		return "http", nil
	}
	if protocol != "http" && protocol != "https" {
		return "", fmt.Errorf("协议仅支持 http 或 https")
	}
	return protocol, nil
}

func normalizeAllowedSuffixes(raw string) (string, error) {
	if strings.TrimSpace(raw) == "" {
		return "", nil
	}
	parts := strings.Split(raw, ",")
	normalized := make([]string, 0, len(parts))
	seen := map[string]struct{}{}
	for _, part := range parts {
		suffix := normalizeDomainName(part)
		suffix = strings.TrimPrefix(suffix, "*")
		if suffix == "" {
			continue
		}
		if !strings.HasPrefix(suffix, ".") {
			suffix = "." + suffix
		}
		candidate := strings.TrimPrefix(suffix, ".")
		if !domainRegex.MatchString(candidate) {
			return "", fmt.Errorf("域名后缀格式无效: %s", suffix)
		}
		if _, ok := seen[suffix]; ok {
			continue
		}
		seen[suffix] = struct{}{}
		normalized = append(normalized, suffix)
	}
	return strings.Join(normalized, ","), nil
}

func domainMatchesAllowedSuffix(domainName, allowedSuffixes string) bool {
	if strings.TrimSpace(allowedSuffixes) == "" {
		return true
	}
	for _, suffix := range strings.Split(allowedSuffixes, ",") {
		suffix = normalizeDomainName(suffix)
		suffix = strings.TrimPrefix(suffix, "*")
		if suffix == "" {
			continue
		}
		if !strings.HasPrefix(suffix, ".") {
			suffix = "." + suffix
		}
		rootDomain := strings.TrimPrefix(suffix, ".")
		if domainName == rootDomain || strings.HasSuffix(domainName, suffix) {
			return true
		}
	}
	return false
}

func applyDomainProxy(domain *domainModel.Domain) error {
	client := getAgentClient(domain.ProviderID)
	if client == nil {
		return nil
	}
	protocol, err := normalizeProtocol(domain.Protocol)
	if err != nil {
		return err
	}
	_, err = client.AddDomainProxy(
		domain.DomainName,
		domain.InternalIP,
		domain.InternalPort,
		protocol,
		domain.EnableSSL,
		domain.SSLCertContent,
		domain.SSLKeyContent,
	)
	if err != nil {
		updateErr := global.APP_DB.Model(domain).Updates(map[string]interface{}{
			"status":    "error",
			"error_msg": fmt.Sprintf("agent proxy error: %v", err),
		}).Error
		if updateErr != nil {
			global.APP_LOG.Warn("更新域名代理错误状态失败",
				zap.String("domain", domain.DomainName),
				zap.Error(updateErr))
		}
		return err
	}
	updateErr := global.APP_DB.Model(domain).Updates(map[string]interface{}{
		"status":    "active",
		"error_msg": "",
	}).Error
	if updateErr != nil {
		global.APP_LOG.Warn("更新域名代理成功状态失败",
			zap.String("domain", domain.DomainName),
			zap.Error(updateErr))
	}
	return nil
}

func removeDomainProxy(domain *domainModel.Domain) error {
	client := getAgentClient(domain.ProviderID)
	if client == nil {
		return nil
	}
	return client.RemoveDomainProxy(domain.DomainName)
}

// GetUserDomains 获取用户域名列表
func (s *Service) GetUserDomains(userID uint) ([]domainModel.Domain, error) {
	var domains []domainModel.Domain
	err := global.APP_DB.Where("user_id = ?", userID).Find(&domains).Error
	return domains, err
}

// CreateDomain 用户创建域名绑定
func (s *Service) CreateDomain(userID uint, req *CreateDomainRequest) (*domainModel.Domain, error) {
	req.DomainName = normalizeDomainName(req.DomainName)
	req.InternalIP = strings.TrimSpace(req.InternalIP)
	protocol, err := normalizeProtocol(req.Protocol)
	if err != nil {
		return nil, err
	}
	req.Protocol = protocol
	// 验证域名格式
	if !domainRegex.MatchString(req.DomainName) {
		return nil, fmt.Errorf("域名格式无效")
	}
	// 验证域名长度
	if len(req.DomainName) > 253 {
		return nil, fmt.Errorf("域名长度不能超过253个字符")
	}
	// 验证IP格式
	if net.ParseIP(req.InternalIP) == nil {
		return nil, fmt.Errorf("内部IP格式无效")
	}
	// 验证端口范围
	if req.InternalPort < 1 || req.InternalPort > 65535 {
		return nil, fmt.Errorf("端口范围无效")
	}
	// 验证实例归属
	var instance providerModel.Instance
	if err := global.APP_DB.Where("id = ? AND user_id = ?", req.InstanceID, userID).First(&instance).Error; err != nil {
		return nil, fmt.Errorf("实例不存在或无权限")
	}
	// 检查节点是否开启了域名绑定
	var provider providerModel.Provider
	if err := global.APP_DB.First(&provider, instance.ProviderID).Error; err != nil {
		return nil, fmt.Errorf("节点不存在")
	}
	if !provider.EnableDomainBinding {
		return nil, fmt.Errorf("该节点未启用域名绑定功能")
	}
	// 检查域名配置。没有显式配置时使用节点开关派生的默认配置。
	domainConfig, err := s.GetDomainConfig(provider.ID)
	if err != nil {
		return nil, fmt.Errorf("读取节点域名配置失败: %w", err)
	}
	if !domainConfig.Enabled {
		return nil, fmt.Errorf("该节点域名绑定未启用")
	}
	if !domainMatchesAllowedSuffix(req.DomainName, domainConfig.AllowedSuffixes) {
		return nil, fmt.Errorf("不允许绑定此后缀的域名")
	}
	// 检查配额 + 唯一性 + 创建在同一事务中，避免 TOCTOU 竞争
	domain := &domainModel.Domain{
		UserID:         userID,
		InstanceID:     req.InstanceID,
		ProviderID:     provider.ID,
		DomainName:     req.DomainName,
		Protocol:       req.Protocol,
		InternalIP:     req.InternalIP,
		InternalPort:   req.InternalPort,
		EnableSSL:      req.EnableSSL,
		SSLCertContent: req.SSLCertContent,
		SSLKeyContent:  req.SSLKeyContent,
		HasCert:        req.SSLCertContent != "" && req.SSLKeyContent != "",
		Status:         "active",
	}
	if err := global.APP_DB.Transaction(func(tx *gorm.DB) error {
		// 检查配额
		var count int64
		if err := tx.Model(&domainModel.Domain{}).Where("user_id = ? AND provider_id = ?", userID, provider.ID).Count(&count).Error; err != nil {
			return fmt.Errorf("查询域名配额失败: %w", err)
		}
		if int(count) >= domainConfig.MaxDomainsPerUser {
			return fmt.Errorf("已达到域名绑定上限(%d)", domainConfig.MaxDomainsPerUser)
		}
		// 检查域名唯一性
		var existing int64
		if err := tx.Model(&domainModel.Domain{}).Where("domain_name = ?", req.DomainName).Count(&existing).Error; err != nil {
			return fmt.Errorf("查询域名唯一性失败: %w", err)
		}
		if existing > 0 {
			return fmt.Errorf("该域名已被绑定")
		}
		return tx.Create(domain).Error
	}); err != nil {
		return nil, err
	}

	// Apply reverse proxy via agent
	if err := applyDomainProxy(domain); err != nil {
		global.APP_LOG.Error("域名代理应用到Agent失败",
			zap.String("domain", req.DomainName),
			zap.Error(err))
	}

	global.APP_LOG.Info("用户域名绑定成功",
		zap.Uint("userID", userID),
		zap.String("domain", req.DomainName))

	return domain, nil
}

// DeleteDomain 用户删除域名绑定
func (s *Service) DeleteDomain(userID, domainID uint) error {
	// Fetch domain first for agent cleanup
	var domain domainModel.Domain
	if err := global.APP_DB.Where("id = ? AND user_id = ?", domainID, userID).First(&domain).Error; err != nil {
		return fmt.Errorf("域名绑定不存在或无权限")
	}

	if err := removeDomainProxy(&domain); err != nil {
		global.APP_LOG.Warn("从Agent移除域名代理失败",
			zap.String("domain", domain.DomainName),
			zap.Error(err))
	}

	// 硬删除：確保域名可被重新注册
	return global.APP_DB.Delete(&domain).Error
}

func (s *Service) GetInstanceDomains(instanceID uint) ([]domainModel.Domain, error) {
	var domains []domainModel.Domain
	err := global.APP_DB.Where("instance_id = ?", instanceID).Find(&domains).Error
	return domains, err
}

func (s *Service) DeleteInstanceDomainsInTx(tx *gorm.DB, instanceID uint) error {
	return tx.Where("instance_id = ?", instanceID).Delete(&domainModel.Domain{}).Error
}

func (s *Service) RemoveDomainProxies(domains []domainModel.Domain) {
	for i := range domains {
		domain := &domains[i]
		if err := removeDomainProxy(domain); err != nil {
			global.APP_LOG.Warn("移除实例域名代理失败",
				zap.Uint("instanceID", domain.InstanceID),
				zap.Uint("domainID", domain.ID),
				zap.String("domain", domain.DomainName),
				zap.Error(err))
		}
	}
}

// UpdateDomain 用户更新域名绑定
func (s *Service) UpdateDomain(userID, domainID uint, req *UpdateDomainRequest) error {
	var domain domainModel.Domain
	if err := global.APP_DB.Where("id = ? AND user_id = ?", domainID, userID).First(&domain).Error; err != nil {
		return fmt.Errorf("域名绑定不存在或无权限")
	}

	updates := map[string]interface{}{}
	if req.InternalIP != "" {
		req.InternalIP = strings.TrimSpace(req.InternalIP)
		if net.ParseIP(req.InternalIP) == nil {
			return fmt.Errorf("内部IP格式无效")
		}
		updates["internal_ip"] = req.InternalIP
	}
	if req.InternalPort < 0 || req.InternalPort > 65535 {
		return fmt.Errorf("端口范围无效")
	}
	if req.InternalPort > 0 {
		updates["internal_port"] = req.InternalPort
	}
	if req.Protocol != "" {
		protocol, err := normalizeProtocol(req.Protocol)
		if err != nil {
			return err
		}
		updates["protocol"] = protocol
	}
	updates["enable_ssl"] = req.EnableSSL

	// Handle cert content
	if req.SSLCertContent != "" && req.SSLKeyContent != "" {
		updates["ssl_cert_content"] = req.SSLCertContent
		updates["ssl_key_content"] = req.SSLKeyContent
		updates["has_cert"] = true
	}

	if err := global.APP_DB.Model(&domain).Updates(updates).Error; err != nil {
		return err
	}

	if err := global.APP_DB.First(&domain, domain.ID).Error; err != nil {
		return err
	}
	if err := applyDomainProxy(&domain); err != nil {
		global.APP_LOG.Warn("域名代理更新Agent失败",
			zap.String("domain", domain.DomainName),
			zap.Error(err))
	}

	return nil
}

// AdminGetAllDomains 管理员获取所有域名(支持按节点过滤)
func (s *Service) AdminGetAllDomains(ownerAdminID uint) ([]domainModel.Domain, error) {
	var domains []domainModel.Domain
	query := global.APP_DB.Model(&domainModel.Domain{})
	if ownerAdminID > 0 {
		// 普通管理员只看自己节点的域名
		var providerIDs []uint
		global.APP_DB.Model(&providerModel.Provider{}).Where("owner_admin_id = ?", ownerAdminID).Pluck("id", &providerIDs)
		if len(providerIDs) == 0 {
			return domains, nil
		}
		query = query.Where("provider_id IN ?", providerIDs)
	}
	err := query.Find(&domains).Error
	return domains, err
}

func (s *Service) AdminGetDomainList(ownerAdminID uint, req AdminDomainListRequest) ([]AdminDomainListItem, int64, error) {
	var domains []AdminDomainListItem
	var total int64

	if req.Page <= 0 {
		req.Page = 1
	}
	if req.PageSize <= 0 {
		req.PageSize = 10
	}

	query := global.APP_DB.Table("domains AS d").
		Joins("LEFT JOIN users AS u ON u.id = d.user_id").
		Joins("LEFT JOIN instances AS i ON i.id = d.instance_id").
		Joins("LEFT JOIN providers AS p ON p.id = d.provider_id")

	if ownerAdminID > 0 {
		query = query.Where("p.owner_admin_id = ?", ownerAdminID)
	}
	if req.UserID > 0 {
		query = query.Where("d.user_id = ?", req.UserID)
	}
	if req.ProviderID > 0 {
		query = query.Where("d.provider_id = ?", req.ProviderID)
	}
	if req.InstanceID > 0 {
		query = query.Where("d.instance_id = ?", req.InstanceID)
	}
	if req.Status != "" {
		query = query.Where("d.status = ?", req.Status)
	}
	if req.Keyword != "" {
		keyword := "%" + strings.TrimSpace(req.Keyword) + "%"
		query = query.Where(
			"d.domain_name LIKE ? OR d.internal_ip LIKE ? OR u.username LIKE ? OR u.nickname LIKE ? OR i.name LIKE ? OR p.name LIKE ?",
			keyword, keyword, keyword, keyword, keyword, keyword,
		)
	}

	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}

	offset := (req.Page - 1) * req.PageSize
	err := query.Select(`
			d.id,
			d.created_at,
			d.updated_at,
			d.user_id,
			COALESCE(u.username, '') AS username,
			COALESCE(u.nickname, '') AS user_nickname,
			d.instance_id,
			COALESCE(i.name, '') AS instance_name,
			d.provider_id,
			COALESCE(p.name, '') AS provider_name,
			COALESCE(p.type, '') AS provider_type,
			d.domain_name,
			d.protocol,
			d.internal_ip,
			d.internal_port,
			d.enable_ssl,
			d.has_cert,
			d.status,
			d.error_msg,
			d.expires_at
		`).
		Order("d.created_at DESC, d.id DESC").
		Offset(offset).
		Limit(req.PageSize).
		Scan(&domains).Error
	return domains, total, err
}

func (s *Service) ensureAdminCanAccessDomain(domain *domainModel.Domain, ownerAdminID uint) error {
	if ownerAdminID == 0 {
		return nil
	}
	var count int64
	if err := global.APP_DB.Model(&providerModel.Provider{}).
		Where("id = ? AND owner_admin_id = ?", domain.ProviderID, ownerAdminID).
		Count(&count).Error; err != nil {
		return err
	}
	if count == 0 {
		return fmt.Errorf("无权操作该域名")
	}
	return nil
}

// AdminDeleteDomain 管理员删除域名
// ownerAdminID > 0 时校验域名所在节点是否属于该管理员（普通管理员隔离）
func (s *Service) AdminDeleteDomain(domainID, ownerAdminID uint) error {
	var domain domainModel.Domain
	if err := global.APP_DB.First(&domain, domainID).Error; err != nil {
		return fmt.Errorf("域名不存在")
	}
	if err := s.ensureAdminCanAccessDomain(&domain, ownerAdminID); err != nil {
		return err
	}
	if err := removeDomainProxy(&domain); err != nil {
		global.APP_LOG.Warn("管理员从Agent移除域名代理失败",
			zap.String("domain", domain.DomainName),
			zap.Error(err))
	}
	// 硬删除：确保域名可被重新注册
	return global.APP_DB.Delete(&domain).Error
}

func (s *Service) AdminUpdateDomain(domainID, ownerAdminID uint, req *AdminUpdateDomainRequest) error {
	var domain domainModel.Domain
	if err := global.APP_DB.First(&domain, domainID).Error; err != nil {
		return fmt.Errorf("域名不存在")
	}
	if err := s.ensureAdminCanAccessDomain(&domain, ownerAdminID); err != nil {
		return err
	}

	oldDomain := domain
	updates := map[string]interface{}{}
	targetProviderID := domain.ProviderID
	providerChanged := false

	if req.InstanceID > 0 && req.InstanceID != domain.InstanceID {
		var instance providerModel.Instance
		if err := global.APP_DB.First(&instance, req.InstanceID).Error; err != nil {
			return fmt.Errorf("实例不存在")
		}
		if ownerAdminID > 0 {
			var providerCount int64
			if err := global.APP_DB.Model(&providerModel.Provider{}).
				Where("id = ? AND owner_admin_id = ?", instance.ProviderID, ownerAdminID).
				Count(&providerCount).Error; err != nil {
				return err
			}
			if providerCount == 0 {
				return fmt.Errorf("无权绑定到该实例")
			}
		}
		targetProviderID = instance.ProviderID
		providerChanged = targetProviderID != domain.ProviderID
		config, err := s.GetDomainConfig(targetProviderID)
		if err != nil {
			return fmt.Errorf("读取节点域名配置失败: %w", err)
		}
		var count int64
		if err := global.APP_DB.Model(&domainModel.Domain{}).
			Where("user_id = ? AND provider_id = ? AND id <> ?", instance.UserID, instance.ProviderID, domain.ID).
			Count(&count).Error; err != nil {
			return fmt.Errorf("查询域名配额失败: %w", err)
		}
		if int(count) >= config.MaxDomainsPerUser {
			return fmt.Errorf("目标用户已达到该节点域名绑定上限(%d)", config.MaxDomainsPerUser)
		}
		updates["instance_id"] = instance.ID
		updates["provider_id"] = instance.ProviderID
		updates["user_id"] = instance.UserID
	}

	domainName := normalizeDomainName(req.DomainName)
	domainNameChanged := domainName != "" && domainName != domain.DomainName
	if domainNameChanged {
		if !domainRegex.MatchString(domainName) {
			return fmt.Errorf("域名格式无效")
		}
		if len(domainName) > 253 {
			return fmt.Errorf("域名长度不能超过253个字符")
		}
		var existing int64
		if err := global.APP_DB.Model(&domainModel.Domain{}).
			Where("domain_name = ? AND id <> ?", domainName, domain.ID).
			Count(&existing).Error; err != nil {
			return fmt.Errorf("查询域名唯一性失败: %w", err)
		}
		if existing > 0 {
			return fmt.Errorf("该域名已被绑定")
		}
		updates["domain_name"] = domainName
	}

	if domainNameChanged || providerChanged {
		config, err := s.GetDomainConfig(targetProviderID)
		if err != nil {
			return fmt.Errorf("读取节点域名配置失败: %w", err)
		}
		if !config.Enabled {
			return fmt.Errorf("该节点域名绑定未启用")
		}
		nameForSuffixCheck := domain.DomainName
		if domainNameChanged {
			nameForSuffixCheck = domainName
		}
		if !domainMatchesAllowedSuffix(nameForSuffixCheck, config.AllowedSuffixes) {
			return fmt.Errorf("不允许绑定此后缀的域名")
		}
	}

	if req.InternalIP != "" {
		internalIP := strings.TrimSpace(req.InternalIP)
		if net.ParseIP(internalIP) == nil {
			return fmt.Errorf("内部IP格式无效")
		}
		updates["internal_ip"] = internalIP
	}
	if req.InternalPort < 0 || req.InternalPort > 65535 {
		return fmt.Errorf("端口范围无效")
	}
	if req.InternalPort > 0 {
		updates["internal_port"] = req.InternalPort
	}
	if req.Protocol != "" {
		protocol, err := normalizeProtocol(req.Protocol)
		if err != nil {
			return err
		}
		updates["protocol"] = protocol
	}
	if req.EnableSSL != nil {
		updates["enable_ssl"] = *req.EnableSSL
	}
	if req.ClearCert {
		updates["ssl_cert_content"] = ""
		updates["ssl_key_content"] = ""
		updates["has_cert"] = false
	} else if req.SSLCertContent != "" || req.SSLKeyContent != "" {
		if req.SSLCertContent == "" || req.SSLKeyContent == "" {
			return fmt.Errorf("SSL证书和私钥必须同时填写")
		}
		updates["ssl_cert_content"] = req.SSLCertContent
		updates["ssl_key_content"] = req.SSLKeyContent
		updates["has_cert"] = true
	}

	if len(updates) == 0 {
		return nil
	}

	if err := global.APP_DB.Model(&domain).Updates(updates).Error; err != nil {
		return err
	}
	if err := global.APP_DB.First(&domain, domain.ID).Error; err != nil {
		return err
	}
	if oldDomain.DomainName != domain.DomainName || oldDomain.ProviderID != domain.ProviderID {
		if err := removeDomainProxy(&oldDomain); err != nil {
			global.APP_LOG.Warn("管理员更新域名时移除旧代理失败",
				zap.String("domain", oldDomain.DomainName),
				zap.Error(err))
		}
	}
	if err := applyDomainProxy(&domain); err != nil {
		return err
	}

	return nil
}

func (s *Service) AdminSyncDomainProxy(domainID, ownerAdminID uint) error {
	var domain domainModel.Domain
	if err := global.APP_DB.First(&domain, domainID).Error; err != nil {
		return fmt.Errorf("域名不存在")
	}
	if err := s.ensureAdminCanAccessDomain(&domain, ownerAdminID); err != nil {
		return err
	}
	if getAgentClient(domain.ProviderID) == nil {
		return fmt.Errorf("节点Agent未配置或不可用")
	}
	return applyDomainProxy(&domain)
}

// GetDomainConfig 获取节点域名配置
func (s *Service) GetDomainConfig(providerID uint) (*domainModel.DomainConfig, error) {
	var config domainModel.DomainConfig
	err := global.APP_DB.Where("provider_id = ?", providerID).First(&config).Error
	if err == gorm.ErrRecordNotFound {
		var provider providerModel.Provider
		if dbErr := global.APP_DB.Select("enable_domain_binding").First(&provider, providerID).Error; dbErr != nil && dbErr != gorm.ErrRecordNotFound {
			return nil, dbErr
		}
		// 返回默认配置
		return &domainModel.DomainConfig{
			ProviderID:        providerID,
			Enabled:           provider.EnableDomainBinding,
			MaxDomainsPerUser: 3,
			DNSType:           "hosts",
		}, nil
	}
	return &config, err
}

// UpdateDomainConfig 更新节点域名配置
func (s *Service) UpdateDomainConfig(providerID uint, req *UpdateDomainConfigRequest) error {
	if req.MaxDomainsPerUser <= 0 {
		return fmt.Errorf("每用户最大域名数必须大于0")
	}
	dnsType := strings.ToLower(strings.TrimSpace(req.DNSType))
	if dnsType == "" {
		dnsType = "hosts"
	}
	if dnsType != "hosts" && dnsType != "nginx" {
		return fmt.Errorf("DNS类型仅支持 hosts 或 nginx")
	}
	allowedSuffixes, err := normalizeAllowedSuffixes(req.AllowedSuffixes)
	if err != nil {
		return err
	}

	var config domainModel.DomainConfig
	err = global.APP_DB.Where("provider_id = ?", providerID).First(&config).Error
	if err != nil && err != gorm.ErrRecordNotFound {
		return err
	}

	return global.APP_DB.Transaction(func(tx *gorm.DB) error {
		var providerCount int64
		if err := tx.Model(&providerModel.Provider{}).
			Where("id = ?", providerID).
			Count(&providerCount).Error; err != nil {
			return err
		}
		if providerCount == 0 {
			return fmt.Errorf("节点不存在")
		}
		if err := tx.Model(&providerModel.Provider{}).
			Where("id = ?", providerID).
			Update("enable_domain_binding", req.Enabled).Error; err != nil {
			return err
		}

		values := map[string]interface{}{
			"enabled":              req.Enabled,
			"max_domains_per_user": req.MaxDomainsPerUser,
			"dns_type":             dnsType,
			"dns_config_path":      strings.TrimSpace(req.DNSConfigPath),
			"nginx_config_path":    strings.TrimSpace(req.NginxConfigPath),
			"nginx_reload_cmd":     strings.TrimSpace(req.NginxReloadCmd),
			"allowed_suffixes":     allowedSuffixes,
		}
		if err == gorm.ErrRecordNotFound {
			config = domainModel.DomainConfig{
				ProviderID:        providerID,
				Enabled:           req.Enabled,
				MaxDomainsPerUser: req.MaxDomainsPerUser,
				DNSType:           dnsType,
				DNSConfigPath:     strings.TrimSpace(req.DNSConfigPath),
				NginxConfigPath:   strings.TrimSpace(req.NginxConfigPath),
				NginxReloadCmd:    strings.TrimSpace(req.NginxReloadCmd),
				AllowedSuffixes:   allowedSuffixes,
			}
			return tx.Create(&config).Error
		}
		return tx.Model(&config).Updates(values).Error
	})
}

// SyncDomainProxiesResult records the outcome of replaying domain proxy config.
type SyncDomainProxiesResult struct {
	Total   int `json:"total"`
	Success int `json:"success"`
	Failed  int `json:"failed"`
	Skipped int `json:"skipped"`
	Removed int `json:"removed"`
}

// AdminSyncDomainProxies replays active/error domain bindings for all providers visible to the admin.
func (s *Service) AdminSyncDomainProxies(ownerAdminID uint) (*SyncDomainProxiesResult, error) {
	providerIDs, err := s.getVisibleDomainProviderIDs(ownerAdminID)
	if err != nil {
		return nil, err
	}
	result := &SyncDomainProxiesResult{}
	for _, providerID := range providerIDs {
		domains, err := s.getProviderDomains(providerID)
		if err != nil {
			return nil, err
		}
		result.merge(s.syncProviderDomainProxyRows(providerID, domains))
	}
	return result, nil
}

// SyncProviderDomainProxies replays active/error domain bindings for a single provider.
// It is used as an agent reconnect hook so the controller database remains the
// authoritative source if the agent restarts, is reinstalled, or loses its local sqlite DB.
func (s *Service) SyncProviderDomainProxies(providerID uint) (*SyncDomainProxiesResult, error) {
	domains, err := s.getProviderDomains(providerID)
	if err != nil {
		return nil, err
	}
	return s.syncProviderDomainProxyRows(providerID, domains), nil
}

func (s *Service) getVisibleDomainProviderIDs(ownerAdminID uint) ([]uint, error) {
	var providerIDs []uint
	query := global.APP_DB.Model(&providerModel.Provider{})
	if ownerAdminID > 0 {
		query = query.Where("owner_admin_id = ?", ownerAdminID)
	}
	if err := query.Pluck("id", &providerIDs).Error; err != nil {
		return nil, err
	}
	return providerIDs, nil
}

func (s *Service) getProviderDomains(providerID uint) ([]domainModel.Domain, error) {
	var domains []domainModel.Domain
	if err := global.APP_DB.Where("provider_id = ?", providerID).Find(&domains).Error; err != nil {
		return nil, err
	}
	return domains, nil
}

func (r *SyncDomainProxiesResult) merge(other *SyncDomainProxiesResult) {
	if other == nil {
		return
	}
	r.Total += other.Total
	r.Success += other.Success
	r.Failed += other.Failed
	r.Skipped += other.Skipped
	r.Removed += other.Removed
}

func (s *Service) syncProviderDomainProxyRows(providerID uint, domains []domainModel.Domain) *SyncDomainProxiesResult {
	result := &SyncDomainProxiesResult{Total: len(domains)}
	client := getAgentClient(providerID)
	if client == nil {
		result.Skipped += len(domains)
		return result
	}

	desiredDomains := make(map[string]struct{}, len(domains))
	for i := range domains {
		domain := &domains[i]
		if domain.Status != "" && domain.Status != "active" && domain.Status != "error" {
			result.Skipped++
			continue
		}
		desiredDomains[domain.DomainName] = struct{}{}
		if err := applyDomainProxy(domain); err != nil {
			result.Failed++
			global.APP_LOG.Warn("同步域名代理失败",
				zap.Uint("domainID", domain.ID),
				zap.String("domain", domain.DomainName),
				zap.Error(err))
			continue
		}
		result.Success++
	}

	existing, err := client.ListDomainProxies()
	if err != nil {
		result.Failed++
		global.APP_LOG.Warn("列出Agent域名代理失败，无法对账删除孤儿代理",
			zap.Uint("providerID", providerID),
			zap.Error(err))
		return result
	}
	for _, proxy := range existing.Proxies {
		if _, ok := desiredDomains[proxy.Domain]; ok {
			continue
		}
		if err := client.RemoveDomainProxy(proxy.Domain); err != nil {
			result.Failed++
			global.APP_LOG.Warn("删除Agent孤儿域名代理失败",
				zap.Uint("providerID", providerID),
				zap.String("domain", proxy.Domain),
				zap.Error(err))
			continue
		}
		result.Removed++
	}
	return result
}

// Request/Response types

type CreateDomainRequest struct {
	InstanceID     uint   `json:"instanceId" binding:"required"`
	DomainName     string `json:"domainName" binding:"required"`
	Protocol       string `json:"protocol"`
	InternalIP     string `json:"internalIP" binding:"required"`
	InternalPort   int    `json:"internalPort" binding:"required"`
	EnableSSL      bool   `json:"enableSSL"`
	SSLCertContent string `json:"sslCertContent"`
	SSLKeyContent  string `json:"sslKeyContent"`
}

type UpdateDomainRequest struct {
	InternalIP     string `json:"internalIP"`
	InternalPort   int    `json:"internalPort"`
	Protocol       string `json:"protocol"`
	EnableSSL      bool   `json:"enableSSL"`
	SSLCertContent string `json:"sslCertContent"`
	SSLKeyContent  string `json:"sslKeyContent"`
}

type AdminUpdateDomainRequest struct {
	InstanceID     uint   `json:"instanceId"`
	DomainName     string `json:"domainName"`
	InternalIP     string `json:"internalIP"`
	InternalPort   int    `json:"internalPort"`
	Protocol       string `json:"protocol"`
	EnableSSL      *bool  `json:"enableSSL"`
	SSLCertContent string `json:"sslCertContent"`
	SSLKeyContent  string `json:"sslKeyContent"`
	ClearCert      bool   `json:"clearCert"`
}

type UpdateDomainConfigRequest struct {
	Enabled           bool   `json:"enabled"`
	MaxDomainsPerUser int    `json:"maxDomainsPerUser"`
	DNSType           string `json:"dnsType"`
	DNSConfigPath     string `json:"dnsConfigPath"`
	NginxConfigPath   string `json:"nginxConfigPath"`
	NginxReloadCmd    string `json:"nginxReloadCmd"`
	AllowedSuffixes   string `json:"allowedSuffixes"`
}
