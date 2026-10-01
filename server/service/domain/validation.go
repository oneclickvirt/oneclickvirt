package domain

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"gorm.io/gorm"
	"net"
	"net/url"
	"oneclickvirt/constant"
	"oneclickvirt/global"
	domainModel "oneclickvirt/model/domain"
	providerModel "oneclickvirt/model/provider"
	"strings"
	"time"
)

func addressIP(raw string) net.IP {
	raw = strings.TrimSpace(raw)
	if ip, _, err := net.ParseCIDR(raw); err == nil {
		return ip
	}
	if parsed, err := url.Parse(raw); err == nil && parsed.Hostname() != "" {
		raw = parsed.Hostname()
	}
	if host, _, err := net.SplitHostPort(raw); err == nil {
		raw = host
	}
	return net.ParseIP(strings.Trim(raw, "[]"))
}

func validateInstanceTarget(instance providerModel.Instance, provider providerModel.Provider, target string) error {
	ip := net.ParseIP(strings.TrimSpace(target))
	if ip == nil || !ip.IsGlobalUnicast() {
		return fmt.Errorf("请选择此实例的内部 IPv4 或 IPv6 地址")
	}
	for _, host := range []string{provider.Endpoint, provider.PortIP, provider.AgentRemoteIP} {
		if ip.Equal(addressIP(host)) {
			return fmt.Errorf("不能使用节点自身的地址作为实例目标")
		}
	}
	addresses := []string{instance.PrivateIP, instance.IPv6Address, instance.PublicIPv6}
	// NAT PublicIP is the shared host, never an instance-owned address.
	if instance.NetworkType == "dedicated_ipv4" || instance.NetworkType == "dedicated_ipv4_ipv6" {
		addresses = append(addresses, instance.PublicIP)
	}
	for _, address := range addresses {
		// A stored prefix identifies this address only, not the whole subnet.
		if ip.Equal(addressIP(address)) {
			return nil
		}
	}
	return fmt.Errorf("目标地址不属于所选实例，请刷新实例信息后重新选择")
}

var errDomainTargetUnavailable = errors.New("暂时无法读取实例信息，请稍后重试")

var errDomainInstanceBusy = errors.New("请等待实例操作完成后再同步域名")

func validateDomainTarget(domain *domainModel.Domain) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var instance providerModel.Instance
	if err := global.APP_DB.WithContext(ctx).First(&instance, domain.InstanceID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return fmt.Errorf("实例不存在或归属已改变")
		}
		return fmt.Errorf("%w: %v", errDomainTargetUnavailable, err)
	}
	var provider providerModel.Provider
	if err := global.APP_DB.WithContext(ctx).First(&provider, domain.ProviderID).Error; err != nil {
		return fmt.Errorf("%w: %v", errDomainTargetUnavailable, err)
	}
	return validateDomainTargetRow(domain, instance, provider)
}

func validateDomainTargetRow(domain *domainModel.Domain, instance providerModel.Instance, provider providerModel.Provider) error {
	if instance.ID == 0 || instance.ID != domain.InstanceID || instance.UserID != domain.UserID || instance.ProviderID != domain.ProviderID {
		return fmt.Errorf("实例不存在或归属已改变")
	}
	if constant.IsBusyStatus(instance.Status) {
		return errDomainInstanceBusy
	}
	if instance.Status != constant.InstanceStatusRunning && instance.Status != constant.InstanceStatusStopped {
		return fmt.Errorf("请先恢复实例状态，再同步域名")
	}
	return validateInstanceTarget(instance, provider, domain.InternalIP)
}

// One provider snapshot per reconciliation, independent of its domain count.
// A failed read aborts the pass before any existing route is changed.
func loadDomainTargets(providerID uint) (providerModel.Provider, map[uint]providerModel.Instance, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var provider providerModel.Provider
	if err := global.APP_DB.WithContext(ctx).First(&provider, providerID).Error; err != nil {
		return provider, nil, err
	}
	var rows []providerModel.Instance
	if err := global.APP_DB.WithContext(ctx).Select("id", "user_id", "provider_id", "status", "private_ip", "public_ip", "ipv6_address", "public_ipv6", "network_type").
		Where("provider_id = ?", providerID).Find(&rows).Error; err != nil {
		return provider, nil, err
	}
	instances := make(map[uint]providerModel.Instance, len(rows))
	for _, instance := range rows {
		instances[instance.ID] = instance
	}
	return provider, instances, nil
}

type DomainVerification struct {
	Name  string `json:"name"`
	Value string `json:"value"`
	Type  string `json:"type"`
}

func domainVerification(userID uint, name string) (DomainVerification, error) {
	name = normalizeDomainName(name)
	if !domainRegex.MatchString(name) || len(name) > 253 {
		return DomainVerification{}, fmt.Errorf("域名格式无效")
	}
	key := global.GetAppConfig().JWT.SigningKey
	if strings.TrimSpace(key) == "" {
		return DomainVerification{}, fmt.Errorf("系统密钥未配置，请联系管理员")
	}
	mac := hmac.New(sha256.New, []byte(key))
	fmt.Fprintf(mac, "oneclickvirt-domain:%d:%s", userID, name)
	return DomainVerification{Name: "_oneclickvirt." + name, Value: "oneclickvirt=" + hex.EncodeToString(mac.Sum(nil)), Type: "TXT"}, nil
}

func (s *Service) GetDomainVerification(userID uint, name string) (DomainVerification, error) {
	return domainVerification(userID, name)
}

func verifyDomainOwnership(ctx context.Context, userID uint, name string, lookup func(context.Context, string) ([]string, error)) error {
	challenge, err := domainVerification(userID, name)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	values, err := lookup(ctx, challenge.Name)
	if err == nil {
		for _, value := range values {
			if strings.TrimSpace(value) == challenge.Value {
				return nil
			}
		}
	}
	return fmt.Errorf("请先添加页面显示的 TXT 记录，等待 DNS 生效后重试")
}
