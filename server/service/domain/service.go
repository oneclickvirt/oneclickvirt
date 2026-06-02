package domain

import (
	"fmt"
	"net"
	"regexp"
	"strings"

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

var domainRegex = regexp.MustCompile(`^([a-zA-Z0-9]([a-zA-Z0-9-]*[a-zA-Z0-9])?\.)+[a-zA-Z]{2,}$`)

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
	host := p.Endpoint
	if host == "" {
		host = p.PortIP
	}
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
	return agent.GetClient(providerID, host, port, config.AgentToken)
}

// GetUserDomains 获取用户域名列表
func (s *Service) GetUserDomains(userID uint) ([]domainModel.Domain, error) {
	var domains []domainModel.Domain
	err := global.APP_DB.Where("user_id = ?", userID).Find(&domains).Error
	return domains, err
}

// CreateDomain 用户创建域名绑定
func (s *Service) CreateDomain(userID uint, req *CreateDomainRequest) (*domainModel.Domain, error) {
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
	// 检查域名配置
	var domainConfig domainModel.DomainConfig
	if err := global.APP_DB.Where("provider_id = ?", provider.ID).First(&domainConfig).Error; err != nil {
		return nil, fmt.Errorf("节点域名配置不存在，请联系管理员")
	}
	if !domainConfig.Enabled {
		return nil, fmt.Errorf("该节点域名绑定未启用")
	}
	// 检查域名后缀限制
	if domainConfig.AllowedSuffixes != "" {
		allowed := false
		suffixes := strings.Split(domainConfig.AllowedSuffixes, ",")
		for _, suffix := range suffixes {
			suffix = strings.TrimSpace(suffix)
			if suffix != "" && strings.HasSuffix(req.DomainName, suffix) {
				allowed = true
				break
			}
		}
		if !allowed {
			return nil, fmt.Errorf("不允许绑定此后缀的域名")
		}
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
	if client := getAgentClient(provider.ID); client != nil {
		protocol := req.Protocol
		if protocol == "" {
			protocol = "http"
		}
		_, err := client.AddDomainProxy(req.DomainName, req.InternalIP, req.InternalPort, protocol, req.EnableSSL, req.SSLCertContent, req.SSLKeyContent)
		if err != nil {
			global.APP_LOG.Error("域名代理应用到Agent失败",
				zap.String("domain", req.DomainName),
				zap.Error(err))
			global.APP_DB.Model(domain).Updates(map[string]interface{}{
				"status":    "error",
				"error_msg": fmt.Sprintf("agent proxy error: %v", err),
			})
		}
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

	// Remove proxy from agent
	if client := getAgentClient(domain.ProviderID); client != nil {
		if err := client.RemoveDomainProxy(domain.DomainName); err != nil {
			global.APP_LOG.Warn("从Agent移除域名代理失败",
				zap.String("domain", domain.DomainName),
				zap.Error(err))
		}
	}

	// 硬删除：確保域名可被重新注册
	return global.APP_DB.Delete(&domain).Error
}

// UpdateDomain 用户更新域名绑定
func (s *Service) UpdateDomain(userID, domainID uint, req *UpdateDomainRequest) error {
	var domain domainModel.Domain
	if err := global.APP_DB.Where("id = ? AND user_id = ?", domainID, userID).First(&domain).Error; err != nil {
		return fmt.Errorf("域名绑定不存在或无权限")
	}

	updates := map[string]interface{}{}
	if req.InternalIP != "" {
		if net.ParseIP(req.InternalIP) == nil {
			return fmt.Errorf("内部IP格式无效")
		}
		updates["internal_ip"] = req.InternalIP
	}
	if req.InternalPort > 0 && req.InternalPort <= 65535 {
		updates["internal_port"] = req.InternalPort
	}
	if req.Protocol != "" {
		updates["protocol"] = req.Protocol
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

	// Re-apply proxy via agent with updated config
	if client := getAgentClient(domain.ProviderID); client != nil {
		ip := domain.InternalIP
		if v, ok := updates["internal_ip"].(string); ok {
			ip = v
		}
		port := domain.InternalPort
		if v, ok := updates["internal_port"].(int); ok {
			port = v
		}
		protocol := domain.Protocol
		if v, ok := updates["protocol"].(string); ok {
			protocol = v
		}
		enableSSL := req.EnableSSL
		// Use new cert if provided, otherwise use existing
		certContent := req.SSLCertContent
		keyContent := req.SSLKeyContent
		if certContent == "" {
			certContent = domain.SSLCertContent
		}
		if keyContent == "" {
			keyContent = domain.SSLKeyContent
		}
		if _, err := client.AddDomainProxy(domain.DomainName, ip, port, protocol, enableSSL, certContent, keyContent); err != nil {
			global.APP_LOG.Warn("域名代理更新Agent失败",
				zap.String("domain", domain.DomainName),
				zap.Error(err))
		}
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

// AdminDeleteDomain 管理员删除域名
// ownerAdminID > 0 时校验域名所在节点是否属于该管理员（普通管理员隔离）
func (s *Service) AdminDeleteDomain(domainID, ownerAdminID uint) error {
	var domain domainModel.Domain
	if err := global.APP_DB.First(&domain, domainID).Error; err != nil {
		return fmt.Errorf("域名不存在")
	}
	// 普通管理员只能删除自己节点的域名
	if ownerAdminID > 0 {
		var count int64
		global.APP_DB.Model(&providerModel.Provider{}).
			Where("id = ? AND owner_admin_id = ?", domain.ProviderID, ownerAdminID).
			Count(&count)
		if count == 0 {
			return fmt.Errorf("无权删除该域名")
		}
	}
	// Remove proxy from agent
	if client := getAgentClient(domain.ProviderID); client != nil {
		if err := client.RemoveDomainProxy(domain.DomainName); err != nil {
			global.APP_LOG.Warn("管理员从Agent移除域名代理失败",
				zap.String("domain", domain.DomainName),
				zap.Error(err))
		}
	}
	// 硬删除：确保域名可被重新注册
	return global.APP_DB.Delete(&domain).Error
}

// GetDomainConfig 获取节点域名配置
func (s *Service) GetDomainConfig(providerID uint) (*domainModel.DomainConfig, error) {
	var config domainModel.DomainConfig
	err := global.APP_DB.Where("provider_id = ?", providerID).First(&config).Error
	if err == gorm.ErrRecordNotFound {
		// 返回默认配置
		return &domainModel.DomainConfig{
			ProviderID:        providerID,
			Enabled:           false,
			MaxDomainsPerUser: 3,
			DNSType:           "hosts",
		}, nil
	}
	return &config, err
}

// UpdateDomainConfig 更新节点域名配置
func (s *Service) UpdateDomainConfig(providerID uint, req *UpdateDomainConfigRequest) error {
	var config domainModel.DomainConfig
	err := global.APP_DB.Where("provider_id = ?", providerID).First(&config).Error
	if err == gorm.ErrRecordNotFound {
		config = domainModel.DomainConfig{
			ProviderID:        providerID,
			Enabled:           req.Enabled,
			MaxDomainsPerUser: req.MaxDomainsPerUser,
			DNSType:           req.DNSType,
			DNSConfigPath:     req.DNSConfigPath,
			NginxConfigPath:   req.NginxConfigPath,
			NginxReloadCmd:    req.NginxReloadCmd,
			AllowedSuffixes:   req.AllowedSuffixes,
		}
		return global.APP_DB.Create(&config).Error
	}
	if err != nil {
		return err
	}
	return global.APP_DB.Model(&config).Updates(map[string]interface{}{
		"enabled":              req.Enabled,
		"max_domains_per_user": req.MaxDomainsPerUser,
		"dns_type":             req.DNSType,
		"dns_config_path":      req.DNSConfigPath,
		"nginx_config_path":    req.NginxConfigPath,
		"nginx_reload_cmd":     req.NginxReloadCmd,
		"allowed_suffixes":     req.AllowedSuffixes,
	}).Error
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

type UpdateDomainConfigRequest struct {
	Enabled           bool   `json:"enabled"`
	MaxDomainsPerUser int    `json:"maxDomainsPerUser"`
	DNSType           string `json:"dnsType"`
	DNSConfigPath     string `json:"dnsConfigPath"`
	NginxConfigPath   string `json:"nginxConfigPath"`
	NginxReloadCmd    string `json:"nginxReloadCmd"`
	AllowedSuffixes   string `json:"allowedSuffixes"`
}
