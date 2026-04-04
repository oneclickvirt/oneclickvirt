package domain

import (
	"fmt"
	"net"
	"regexp"
	"strings"

	"oneclickvirt/global"
	domainModel "oneclickvirt/model/domain"
	providerModel "oneclickvirt/model/provider"

	"go.uber.org/zap"
	"gorm.io/gorm"
)

// Service 域名绑定服务
type Service struct{}

var domainRegex = regexp.MustCompile(`^([a-zA-Z0-9]([a-zA-Z0-9-]*[a-zA-Z0-9])?\.)+[a-zA-Z]{2,}$`)

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
	// 检查配额
	var count int64
	global.APP_DB.Model(&domainModel.Domain{}).Where("user_id = ? AND provider_id = ?", userID, provider.ID).Count(&count)
	if int(count) >= domainConfig.MaxDomainsPerUser {
		return nil, fmt.Errorf("已达到域名绑定上限(%d)", domainConfig.MaxDomainsPerUser)
	}
	// 检查域名唯一性
	var existing int64
	global.APP_DB.Model(&domainModel.Domain{}).Where("domain_name = ?", req.DomainName).Count(&existing)
	if existing > 0 {
		return nil, fmt.Errorf("该域名已被绑定")
	}

	domain := &domainModel.Domain{
		UserID:       userID,
		InstanceID:   req.InstanceID,
		ProviderID:   provider.ID,
		DomainName:   req.DomainName,
		Protocol:     req.Protocol,
		InternalIP:   req.InternalIP,
		InternalPort: req.InternalPort,
		EnableSSL:    req.EnableSSL,
		Status:       "active",
	}
	if err := global.APP_DB.Create(domain).Error; err != nil {
		return nil, fmt.Errorf("创建域名绑定失败: %v", err)
	}

	global.APP_LOG.Info("用户域名绑定成功",
		zap.Uint("userID", userID),
		zap.String("domain", req.DomainName))

	return domain, nil
}

// DeleteDomain 用户删除域名绑定
func (s *Service) DeleteDomain(userID, domainID uint) error {
	result := global.APP_DB.Where("id = ? AND user_id = ?", domainID, userID).Delete(&domainModel.Domain{})
	if result.RowsAffected == 0 {
		return fmt.Errorf("域名绑定不存在或无权限")
	}
	return result.Error
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

	return global.APP_DB.Model(&domain).Updates(updates).Error
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
func (s *Service) AdminDeleteDomain(domainID uint) error {
	return global.APP_DB.Delete(&domainModel.Domain{}, domainID).Error
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
	InstanceID   uint   `json:"instanceId" binding:"required"`
	DomainName   string `json:"domainName" binding:"required"`
	Protocol     string `json:"protocol"`
	InternalIP   string `json:"internalIP" binding:"required"`
	InternalPort int    `json:"internalPort" binding:"required"`
	EnableSSL    bool   `json:"enableSSL"`
}

type UpdateDomainRequest struct {
	InternalIP   string `json:"internalIP"`
	InternalPort int    `json:"internalPort"`
	Protocol     string `json:"protocol"`
	EnableSSL    bool   `json:"enableSSL"`
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
