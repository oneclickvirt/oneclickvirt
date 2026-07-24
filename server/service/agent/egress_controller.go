package agent

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/netip"
	"net/url"
	"os"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"oneclickvirt/global"
	monitoringModel "oneclickvirt/model/monitoring"
	providerModel "oneclickvirt/model/provider"
	providerService "oneclickvirt/service/provider"

	"go.uber.org/zap"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var egressIdentifierPattern = regexp.MustCompile(`^[A-Za-z0-9._:-]+$`)
var egressInterfacePattern = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)

const (
	maxEgressRouteTable = uint32(9999)
	maxEgressMark       = uint32(0x00ffffff)
)

// InstanceEgressTraffic exposes the counter source used by the egress dialog.
// Binding counters take precedence over the regular instance monitor when the
// Agent supports per-egress counters.
type InstanceEgressTraffic struct {
	Monitored    bool       `json:"monitored"`
	Source       string     `json:"source,omitempty"`
	Interfaces   []string   `json:"interfaces,omitempty"`
	BytesIn      uint64     `json:"bytes_in"`
	BytesOut     uint64     `json:"bytes_out"`
	DroppedBytes uint64     `json:"dropped_bytes"`
	LastSyncAt   *time.Time `json:"last_sync_at,omitempty"`
	UpdatedAt    int64      `json:"updated_at,omitempty"`
}

// InstanceEgressStatus is the controller's sanitized view of Agent state. It
// intentionally contains no tunnel private or preshared key material.
type InstanceEgressStatus struct {
	InstanceID          uint                  `json:"instance_id"`
	InstanceKey         string                `json:"instance_key"`
	ProviderID          uint                  `json:"provider_id"`
	ProviderType        string                `json:"provider_type"`
	AgentInstalled      bool                  `json:"agent_installed"`
	AgentConnected      bool                  `json:"agent_connected"`
	AgentError          string                `json:"agent_error,omitempty"`
	NativeSupported     bool                  `json:"native_supported"`
	RecommendedMode     string                `json:"recommended_mode,omitempty"`
	UnsupportedReasons  []string              `json:"unsupported_reasons,omitempty"`
	Capabilities        *EgressCapabilities   `json:"capabilities,omitempty"`
	Profiles            []EgressProfile       `json:"profiles"`
	Binding             *EgressBinding        `json:"binding,omitempty"`
	ConfiguredProfileID string                `json:"configured_profile_id,omitempty"`
	ExpectedIPv4        string                `json:"expected_ipv4,omitempty"`
	ExpectedIPv6        string                `json:"expected_ipv6,omitempty"`
	EffectiveIPv4       string                `json:"effective_ipv4,omitempty"`
	EffectiveIPv6       string                `json:"effective_ipv6,omitempty"`
	EffectiveVerified   bool                  `json:"effective_verified"`
	FailClosedRequired  bool                  `json:"fail_closed_required"`
	FailClosedEnforced  *bool                 `json:"fail_closed_enforced,omitempty"`
	Traffic             InstanceEgressTraffic `json:"traffic"`
}

type InstanceEgressBindRequest struct {
	Profile     EgressProfileRequest `json:"profile"`
	Source      string               `json:"source"`
	Sources     []string             `json:"sources,omitempty"`
	Interface   *string              `json:"interface,omitempty"`
	InterfaceV4 *string              `json:"interface_v4,omitempty"`
	InterfaceV6 *string              `json:"interface_v6,omitempty"`
	Enabled     *bool                `json:"enabled,omitempty"`
	Apply       *bool                `json:"apply,omitempty"`

	explicitSources []string
}

type InstanceEgressBindResult struct {
	Profile        *EgressProfile           `json:"profile"`
	Binding        *EgressBinding           `json:"binding"`
	Reconcile      *EgressReconcileResponse `json:"reconcile,omitempty"`
	ReconcileError string                   `json:"reconcile_error,omitempty"`
}

type InstanceEgressUnbindResult struct {
	Reconcile      *EgressReconcileResponse `json:"reconcile,omitempty"`
	ReconcileError string                   `json:"reconcile_error,omitempty"`
}

type InstanceEgressReconcileResult struct {
	Reconcile *EgressReconcileResponse `json:"reconcile"`
}

type InstanceEgressDependencyResult struct {
	Result *EgressDependencyEnsureResponse `json:"result"`
}

// InstanceEgressService coordinates desired state between the controller DB
// and one node Agent. Remote calls are never performed inside a DB transaction.
type InstanceEgressService struct {
	db *gorm.DB
}

func NewInstanceEgressService(db *gorm.DB) *InstanceEgressService {
	return &InstanceEgressService{db: db}
}

func egressApplyRequested(value *bool) bool {
	return value == nil || *value
}

func egressSecretAAD(providerID uint, profileID, kind string) []byte {
	return []byte(fmt.Sprintf("oneclickvirt-egress:v1:%d:%s:%s", providerID, profileID, kind))
}

func egressMasterKey() ([]byte, error) {
	if encoded := strings.TrimSpace(os.Getenv("ONECLICKVIRT_EGRESS_MASTER_KEY")); encoded != "" {
		key, err := base64.StdEncoding.DecodeString(encoded)
		if err != nil || len(key) != 32 {
			return nil, fmt.Errorf("ONECLICKVIRT_EGRESS_MASTER_KEY必须是32字节密钥的标准Base64编码")
		}
		return key, nil
	}
	if strings.TrimSpace(global.APP_JWT_SECRET) == "" {
		return nil, fmt.Errorf("托管出口密钥加密不可用，请配置ONECLICKVIRT_EGRESS_MASTER_KEY")
	}
	// Compatibility fallback for existing installations. A dedicated egress
	// key is preferred because rotating APP_JWT_SECRET invalidates this derivation.
	digest := sha256.Sum256([]byte("oneclickvirt-egress-master-v1\x00" + global.APP_JWT_SECRET))
	return digest[:], nil
}

func encryptEgressSecret(key, plaintext, aad []byte) (string, error) {
	if len(plaintext) == 0 {
		return "", nil
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", fmt.Errorf("初始化出口密钥加密失败")
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return "", fmt.Errorf("初始化出口密钥加密失败")
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", fmt.Errorf("生成出口密钥随机数失败")
	}
	sealed := aead.Seal(nonce, nonce, plaintext, aad)
	return "v1." + base64.RawStdEncoding.EncodeToString(sealed), nil
}

func decryptEgressSecret(key []byte, encoded string, aad []byte) (string, error) {
	if encoded == "" {
		return "", nil
	}
	if !strings.HasPrefix(encoded, "v1.") {
		return "", fmt.Errorf("出口密钥密文版本无效")
	}
	sealed, err := base64.RawStdEncoding.DecodeString(strings.TrimPrefix(encoded, "v1."))
	if err != nil {
		return "", fmt.Errorf("出口密钥密文无效")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", fmt.Errorf("初始化出口密钥解密失败")
	}
	aead, err := cipher.NewGCM(block)
	if err != nil || len(sealed) < aead.NonceSize() {
		return "", fmt.Errorf("出口密钥密文无效")
	}
	plaintext, err := aead.Open(nil, sealed[:aead.NonceSize()], sealed[aead.NonceSize():], aad)
	if err != nil {
		return "", fmt.Errorf("出口密钥解密失败；请确认主密钥未发生变化")
	}
	return string(plaintext), nil
}

func managedWireGuard(req *EgressWireGuardRequest) bool {
	return req != nil && (req.Managed == nil || *req.Managed)
}

func (s *InstanceEgressService) persistDesiredState(
	ctx context.Context,
	instance *providerModel.Instance,
	node *providerModel.Provider,
	req *InstanceEgressBindRequest,
) (*monitoringModel.EgressDesiredProfile, *monitoringModel.EgressDesiredBinding, error) {
	var savedProfile monitoringModel.EgressDesiredProfile
	var savedBinding monitoringModel.EgressDesiredBinding
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var existing monitoringModel.EgressDesiredProfile
		existingErr := tx.Where("provider_id = ? AND profile_id = ?", node.ID, req.Profile.ID).First(&existing).Error
		if existingErr != nil && !errors.Is(existingErr, gorm.ErrRecordNotFound) {
			return existingErr
		}

		sanitized := req.Profile
		privateCiphertext := existing.PrivateKeyCiphertext
		presharedCiphertext := existing.PresharedKeyCiphertext
		if sanitized.TunnelType != "wireguard" {
			sanitized.WireGuard = nil
			privateCiphertext = ""
			presharedCiphertext = ""
		} else if sanitized.WireGuard == nil && existing.ConfigJSON != "" {
			var previous EgressProfileRequest
			if err := json.Unmarshal([]byte(existing.ConfigJSON), &previous); err != nil {
				return fmt.Errorf("读取已有出口配置失败")
			}
			sanitized.WireGuard = previous.WireGuard
		} else if sanitized.WireGuard != nil {
			privateKey := strings.TrimSpace(sanitized.WireGuard.PrivateKey)
			presharedKey := strings.TrimSpace(sanitized.WireGuard.PresharedKey)
			sanitized.WireGuard.PrivateKey = ""
			sanitized.WireGuard.PresharedKey = ""
			if managedWireGuard(sanitized.WireGuard) {
				if privateKey != "" || presharedKey != "" {
					masterKey, err := egressMasterKey()
					if err != nil {
						return err
					}
					if privateKey != "" {
						privateCiphertext, err = encryptEgressSecret(masterKey, []byte(privateKey), egressSecretAAD(node.ID, sanitized.ID, "private"))
						if err != nil {
							return err
						}
					}
					if presharedKey != "" {
						presharedCiphertext, err = encryptEgressSecret(masterKey, []byte(presharedKey), egressSecretAAD(node.ID, sanitized.ID, "preshared"))
						if err != nil {
							return err
						}
					}
				}
				if privateCiphertext == "" {
					return fmt.Errorf("新的托管WireGuard配置必须提交私钥")
				}
			} else {
				privateCiphertext = ""
				presharedCiphertext = ""
			}
		}

		configJSON, err := json.Marshal(sanitized)
		if err != nil {
			return fmt.Errorf("序列化出口配置失败")
		}
		sourcesJSON, err := json.Marshal(req.Sources)
		if err != nil {
			return fmt.Errorf("序列化实例源地址失败")
		}
		explicitSources := req.explicitSources
		if explicitSources == nil {
			explicitSources = []string{}
		}
		explicitSourcesJSON, err := json.Marshal(explicitSources)
		if err != nil {
			return fmt.Errorf("序列化实例显式源地址失败")
		}
		iface := ""
		if req.Interface != nil {
			iface = *req.Interface
		}
		ifaceV4 := ""
		if req.InterfaceV4 != nil {
			ifaceV4 = *req.InterfaceV4
		}
		ifaceV6 := ""
		if req.InterfaceV6 != nil {
			ifaceV6 = *req.InterfaceV6
		}

		savedProfile = existing
		savedProfile.ProviderID = node.ID
		savedProfile.ProfileID = sanitized.ID
		savedProfile.ConfigJSON = string(configJSON)
		savedProfile.PrivateKeyCiphertext = privateCiphertext
		savedProfile.PresharedKeyCiphertext = presharedCiphertext
		if err := tx.Save(&savedProfile).Error; err != nil {
			return err
		}

		bindingErr := tx.Where("instance_id = ?", instance.ID).First(&savedBinding).Error
		if bindingErr != nil && !errors.Is(bindingErr, gorm.ErrRecordNotFound) {
			return bindingErr
		}
		savedBinding.InstanceID = instance.ID
		savedBinding.InstanceKey = instanceEgressKey(instance)
		savedBinding.ProviderID = node.ID
		savedBinding.ProfileID = sanitized.ID
		savedBinding.SourcesJSON = string(sourcesJSON)
		savedBinding.ExplicitSourcesJSON = string(explicitSourcesJSON)
		savedBinding.Interface = iface
		savedBinding.InterfaceV4 = ifaceV4
		savedBinding.InterfaceV6 = ifaceV6
		savedBinding.Enabled = req.Enabled == nil || *req.Enabled
		savedBinding.PendingDelete = false
		if err := tx.Save(&savedBinding).Error; err != nil {
			return err
		}
		return tx.Model(&providerModel.Instance{}).Where("id = ?", instance.ID).
			Update("egress_profile_id", sanitized.ID).Error
	})
	if err != nil {
		return nil, nil, fmt.Errorf("保存控制端出口期望状态失败: %w", err)
	}
	return &savedProfile, &savedBinding, nil
}

func materializeDesiredProfile(desired *monitoringModel.EgressDesiredProfile) (EgressProfileRequest, error) {
	var req EgressProfileRequest
	if desired == nil || json.Unmarshal([]byte(desired.ConfigJSON), &req) != nil {
		return req, fmt.Errorf("控制端出口配置损坏")
	}
	if managedWireGuard(req.WireGuard) {
		key, err := egressMasterKey()
		if err != nil {
			return req, err
		}
		privateKey, err := decryptEgressSecret(key, desired.PrivateKeyCiphertext, egressSecretAAD(desired.ProviderID, desired.ProfileID, "private"))
		if err != nil {
			return req, err
		}
		presharedKey, err := decryptEgressSecret(key, desired.PresharedKeyCiphertext, egressSecretAAD(desired.ProviderID, desired.ProfileID, "preshared"))
		if err != nil {
			return req, err
		}
		req.WireGuard.PrivateKey = privateKey
		req.WireGuard.PresharedKey = presharedKey
	}
	return req, nil
}

func desiredProfileResponse(desired *monitoringModel.EgressDesiredProfile) (*EgressProfile, error) {
	var req EgressProfileRequest
	if desired == nil || json.Unmarshal([]byte(desired.ConfigJSON), &req) != nil {
		return nil, fmt.Errorf("控制端出口配置损坏")
	}
	enabled := req.Enabled != nil && *req.Enabled
	failClosed := req.FailClosed == nil || *req.FailClosed
	profile := &EgressProfile{
		ID:              req.ID,
		Mode:            req.Mode,
		TunnelType:      req.TunnelType,
		TunnelInterface: req.TunnelInterface,
		Gateway:         req.Gateway,
		RouteTable:      req.RouteTable,
		Mark:            req.Mark,
		PublicIPv4:      req.PublicIPv4,
		PublicIPv6:      req.PublicIPv6,
		Enabled:         enabled,
		FailClosed:      failClosed,
		Status:          "pending",
		UpdatedAt:       desired.UpdatedAt.Unix(),
	}
	if req.WireGuard != nil {
		keepalive := uint16(25)
		if req.WireGuard.PersistentKeepalive != nil {
			keepalive = *req.WireGuard.PersistentKeepalive
		}
		mtu := uint16(1420)
		if req.WireGuard.MTU != nil {
			mtu = *req.WireGuard.MTU
		}
		managed := managedWireGuard(req.WireGuard)
		var peer, endpoint *string
		if req.WireGuard.PeerPublicKey != "" {
			value := req.WireGuard.PeerPublicKey
			peer = &value
		}
		if req.WireGuard.Endpoint != "" {
			value := req.WireGuard.Endpoint
			endpoint = &value
		}
		profile.WireGuard = &EgressWireGuardStatus{
			Managed:                managed,
			PeerPublicKey:          peer,
			Endpoint:               endpoint,
			Addresses:              append([]string(nil), req.WireGuard.Addresses...),
			AllowedIPs:             append([]string(nil), req.WireGuard.AllowedIPs...),
			PersistentKeepalive:    keepalive,
			MTU:                    mtu,
			PrivateKeyConfigured:   managed && desired.PrivateKeyCiphertext != "",
			PresharedKeyConfigured: managed && desired.PresharedKeyCiphertext != "",
		}
	}
	return profile, nil
}

func desiredBindingRequest(desired *monitoringModel.EgressDesiredBinding) (EgressBindingRequest, error) {
	var sources []string
	if desired == nil || json.Unmarshal([]byte(desired.SourcesJSON), &sources) != nil || len(sources) == 0 {
		return EgressBindingRequest{}, fmt.Errorf("控制端出口绑定源地址损坏")
	}
	var iface *string
	if desired.Interface != "" {
		value := desired.Interface
		iface = &value
	}
	var ifaceV4 *string
	if desired.InterfaceV4 != "" {
		value := desired.InterfaceV4
		ifaceV4 = &value
	}
	var ifaceV6 *string
	if desired.InterfaceV6 != "" {
		value := desired.InterfaceV6
		ifaceV6 = &value
	}
	enabled := desired.Enabled
	return EgressBindingRequest{
		InstanceID:  desired.InstanceKey,
		ProfileID:   desired.ProfileID,
		Source:      sources[0],
		Sources:     sources,
		Interface:   iface,
		InterfaceV4: ifaceV4,
		InterfaceV6: ifaceV6,
		Enabled:     &enabled,
	}, nil
}

func desiredBindingResponse(desired *monitoringModel.EgressDesiredBinding) (*EgressBinding, error) {
	req, err := desiredBindingRequest(desired)
	if err != nil {
		return nil, err
	}
	return &EgressBinding{
		InstanceID:  req.InstanceID,
		ProfileID:   req.ProfileID,
		Source:      req.Source,
		Sources:     req.Sources,
		Interface:   req.Interface,
		InterfaceV4: req.InterfaceV4,
		InterfaceV6: req.InterfaceV6,
		Enabled:     desired.Enabled,
		State:       "pending",
		UpdatedAt:   desired.UpdatedAt.Unix(),
	}, nil
}

func cleanEgressValue(value, field string, max int, required bool) (string, error) {
	value = strings.TrimSpace(value)
	if required && value == "" {
		return "", fmt.Errorf("%s不能为空", field)
	}
	if len(value) > max || strings.IndexFunc(value, func(r rune) bool { return r < 0x20 || r == 0x7f }) >= 0 {
		return "", fmt.Errorf("%s格式无效", field)
	}
	return value, nil
}

func validateEgressIdentifier(value, field string, max int, required bool) (string, error) {
	value, err := cleanEgressValue(value, field, max, required)
	if err != nil || value == "" {
		return value, err
	}
	if !egressIdentifierPattern.MatchString(value) {
		return "", fmt.Errorf("%s只能包含字母、数字、点、下划线、冒号和短横线", field)
	}
	return value, nil
}

func validateIPOrCIDR(value, field string, required bool) (string, error) {
	value, err := cleanEgressValue(value, field, 128, required)
	if err != nil || value == "" {
		return value, err
	}
	if strings.Contains(value, "/") {
		prefix, parseErr := netip.ParsePrefix(value)
		if parseErr != nil || prefix.Addr().IsUnspecified() || prefix.Addr().IsMulticast() {
			return "", fmt.Errorf("%s必须是有效的IP地址或CIDR", field)
		}
		return prefix.Masked().String(), nil
	}
	address, parseErr := netip.ParseAddr(value)
	if parseErr != nil || address.IsUnspecified() || address.IsMulticast() {
		return "", fmt.Errorf("%s必须是有效的IP地址或CIDR", field)
	}
	return address.String(), nil
}

func validateHostIP(value *string, field string, family int) error {
	if value == nil || strings.TrimSpace(*value) == "" {
		return nil
	}
	raw, err := cleanEgressValue(*value, field, 128, true)
	if err != nil {
		return err
	}
	var address netip.Addr
	if strings.Contains(raw, "/") {
		prefix, parseErr := netip.ParsePrefix(raw)
		if parseErr != nil || prefix.Bits() != prefix.Addr().BitLen() {
			return fmt.Errorf("%s必须是单个IP地址", field)
		}
		address = prefix.Addr()
	} else {
		address, err = netip.ParseAddr(raw)
		if err != nil {
			return fmt.Errorf("%s必须是单个IP地址", field)
		}
	}
	if address.IsUnspecified() || address.IsMulticast() || (family == 4 && !address.Is4()) || (family == 6 && !address.Is6()) {
		return fmt.Errorf("%s地址族或格式无效", field)
	}
	*value = address.String()
	return nil
}

func validateWireGuardKey(value, field string, required bool) (string, error) {
	value, err := cleanEgressValue(value, field, 128, required)
	if err != nil || value == "" {
		return value, err
	}
	decoded, err := base64.StdEncoding.DecodeString(value)
	if err != nil || len(decoded) != 32 {
		return "", fmt.Errorf("%s必须是32字节的标准Base64 WireGuard密钥", field)
	}
	return value, nil
}

func validateWireGuardEndpoint(value string) (string, error) {
	value, err := cleanEgressValue(value, "WireGuard对端地址", 320, false)
	if err != nil || value == "" {
		return value, err
	}
	host, port, err := net.SplitHostPort(value)
	if err != nil || host == "" {
		return "", fmt.Errorf("WireGuard对端地址必须包含有效端口")
	}
	portNumber, err := strconv.ParseUint(port, 10, 16)
	if err != nil || portNumber == 0 {
		return "", fmt.Errorf("WireGuard对端端口无效")
	}
	if strings.IndexFunc(host, func(r rune) bool {
		return !(r >= 'a' && r <= 'z') && !(r >= 'A' && r <= 'Z') && !(r >= '0' && r <= '9') && !strings.ContainsRune(".-:%", r)
	}) >= 0 {
		return "", fmt.Errorf("WireGuard对端主机名无效")
	}
	return value, nil
}

func validateWireGuardNetworks(values []string, field string, allowDefault, preserveHost bool) ([]string, error) {
	if len(values) > 64 {
		return nil, fmt.Errorf("%s最多允许64项", field)
	}
	result := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		value, err := cleanEgressValue(value, field, 160, true)
		if err != nil || !strings.Contains(value, "/") {
			return nil, fmt.Errorf("%s必须是带前缀长度的有效网段", field)
		}
		prefix, err := netip.ParsePrefix(value)
		if err != nil || prefix.Addr().IsMulticast() || (prefix.Addr().IsUnspecified() && !(allowDefault && prefix.Bits() == 0)) {
			return nil, fmt.Errorf("%s包含无效网段", field)
		}
		normalized := prefix.Masked().String()
		if preserveHost {
			normalized = prefix.String()
		}
		if _, exists := seen[normalized]; exists {
			continue
		}
		seen[normalized] = struct{}{}
		result = append(result, normalized)
	}
	return result, nil
}

func validateWireGuardRequest(req *EgressWireGuardRequest) error {
	if req == nil {
		return nil
	}
	var err error
	managed := true
	if req.Managed != nil {
		managed = *req.Managed
	} else {
		req.Managed = &managed
	}
	if req.PrivateKey, err = validateWireGuardKey(req.PrivateKey, "WireGuard私钥", false); err != nil {
		return err
	}
	if req.PeerPublicKey, err = validateWireGuardKey(req.PeerPublicKey, "WireGuard对端公钥", managed); err != nil {
		return err
	}
	if req.PresharedKey, err = validateWireGuardKey(req.PresharedKey, "WireGuard预共享密钥", false); err != nil {
		return err
	}
	if req.Endpoint, err = validateWireGuardEndpoint(req.Endpoint); err != nil {
		return err
	}
	if !managed && (req.PrivateKey != "" || req.PresharedKey != "") {
		return fmt.Errorf("非托管WireGuard配置不能提交私钥或预共享密钥")
	}
	req.Addresses, err = validateWireGuardNetworks(req.Addresses, "WireGuard本地地址", false, true)
	if err != nil {
		return err
	}
	if managed && len(req.Addresses) == 0 {
		return fmt.Errorf("托管WireGuard必须至少配置一个本地隧道地址")
	}
	if managed && len(req.AllowedIPs) == 0 {
		req.AllowedIPs = []string{"0.0.0.0/0", "::/0"}
	}
	req.AllowedIPs, err = validateWireGuardNetworks(req.AllowedIPs, "WireGuard允许网段", true, false)
	if err != nil {
		return err
	}
	if req.MTU != nil && (*req.MTU < 576 || *req.MTU > 9000) {
		return fmt.Errorf("WireGuard MTU必须在576-9000之间")
	}
	return nil
}

func normalizeBindingSources(values []string) ([]string, error) {
	if len(values) > 65 { // one legacy source may duplicate the complete list
		return nil, fmt.Errorf("实例源地址最多允许64项")
	}
	prefixes := make([]netip.Prefix, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		var prefix netip.Prefix
		var err error
		if strings.Contains(value, "/") {
			prefix, err = netip.ParsePrefix(value)
			prefix = prefix.Masked()
		} else {
			var address netip.Addr
			address, err = netip.ParseAddr(value)
			if err == nil {
				prefix = netip.PrefixFrom(address, address.BitLen())
			}
		}
		if err != nil || !prefix.IsValid() || prefix.Addr().IsUnspecified() || prefix.Addr().IsMulticast() {
			return nil, fmt.Errorf("实例源地址必须是有效的IPv4/IPv6地址或CIDR")
		}
		normalized := prefix.String()
		if _, exists := seen[normalized]; exists {
			continue
		}
		seen[normalized] = struct{}{}
		prefixes = append(prefixes, prefix)
	}
	if len(prefixes) > 64 {
		return nil, fmt.Errorf("实例源地址最多允许64项")
	}
	for left := range prefixes {
		for right := left + 1; right < len(prefixes); right++ {
			if prefixes[left].Addr().BitLen() == prefixes[right].Addr().BitLen() &&
				(prefixes[left].Contains(prefixes[right].Addr()) || prefixes[right].Contains(prefixes[left].Addr())) {
				return nil, fmt.Errorf("同一绑定内的实例源地址不能重叠")
			}
		}
	}
	result := make([]string, 0, len(prefixes))
	for _, prefix := range prefixes {
		result = append(result, prefix.String())
	}
	return result, nil
}

// ValidateInstanceEgressBindRequest rejects multiline command output and
// shell-like identifiers before any value reaches the Agent API.
func ValidateInstanceEgressBindRequest(req *InstanceEgressBindRequest) error {
	if req == nil {
		return fmt.Errorf("请求不能为空")
	}
	var err error
	req.Profile.ID, err = validateEgressIdentifier(req.Profile.ID, "出口配置ID", 128, true)
	if err != nil {
		return err
	}
	req.Profile.Mode, err = cleanEgressValue(req.Profile.Mode, "出口模式", 16, true)
	if err != nil {
		return err
	}
	req.Profile.Mode = strings.ToLower(req.Profile.Mode)
	if req.Profile.Mode != "native" && req.Profile.Mode != "gateway" && req.Profile.Mode != "cni" {
		return fmt.Errorf("出口模式仅支持native、gateway或cni")
	}
	req.Profile.TunnelType, err = cleanEgressValue(req.Profile.TunnelType, "隧道类型", 16, false)
	if err != nil {
		return err
	}
	if req.Profile.TunnelType == "" {
		req.Profile.TunnelType = "wireguard"
	}
	req.Profile.TunnelType = strings.ToLower(req.Profile.TunnelType)
	if req.Profile.TunnelType != "wireguard" && req.Profile.TunnelType != "ipsec" && req.Profile.TunnelType != "gateway" {
		return fmt.Errorf("隧道类型仅支持wireguard、ipsec或gateway")
	}
	req.Profile.TunnelInterface, err = validateEgressIdentifier(req.Profile.TunnelInterface, "隧道接口", 15, true)
	if err != nil {
		return err
	}
	if !egressInterfacePattern.MatchString(req.Profile.TunnelInterface) {
		return fmt.Errorf("隧道接口只能包含字母、数字、点、下划线和短横线")
	}
	if req.Profile.Enabled == nil {
		enabled := true
		req.Profile.Enabled = &enabled
	}
	if req.Profile.FailClosed == nil {
		failClosed := true
		req.Profile.FailClosed = &failClosed
	}
	if !*req.Profile.FailClosed {
		return fmt.Errorf("透明出口必须启用fail-closed，禁止回落到宿主默认出口")
	}
	if *req.Profile.Enabled && (req.Profile.RouteTable == 0 || req.Profile.RouteTable > maxEgressRouteTable || (req.Profile.RouteTable >= 253 && req.Profile.RouteTable <= 255)) {
		return fmt.Errorf("启用的出口配置路由表必须在1-%d且不能使用253-255", maxEgressRouteTable)
	}
	if *req.Profile.Enabled && (req.Profile.Mark == 0 || req.Profile.Mark > maxEgressMark) {
		return fmt.Errorf("启用的出口配置fwmark必须在1-0x%06x", maxEgressMark)
	}
	if req.Profile.Gateway != nil && strings.TrimSpace(*req.Profile.Gateway) != "" {
		gateway := strings.TrimSpace(*req.Profile.Gateway)
		if strings.Contains(gateway, "/") || net.ParseIP(gateway) == nil {
			return fmt.Errorf("网关必须是有效的IP地址")
		}
		*req.Profile.Gateway = net.ParseIP(gateway).String()
	}
	if err := validateHostIP(req.Profile.PublicIPv4, "预期公网IPv4", 4); err != nil {
		return err
	}
	if err := validateHostIP(req.Profile.PublicIPv6, "预期公网IPv6", 6); err != nil {
		return err
	}
	if req.Profile.WireGuard != nil && req.Profile.TunnelType != "wireguard" {
		return fmt.Errorf("WireGuard配置要求tunnel_type=wireguard")
	}
	if err := validateWireGuardRequest(req.Profile.WireGuard); err != nil {
		return err
	}
	allSources := append([]string(nil), req.Sources...)
	if strings.TrimSpace(req.Source) != "" {
		allSources = append(allSources, req.Source)
	}
	req.Sources, err = normalizeBindingSources(allSources)
	if err != nil {
		return err
	}
	if len(req.Sources) == 0 {
		return fmt.Errorf("实例源地址不能为空")
	}
	req.Source = req.Sources[0]
	for _, source := range req.Sources {
		prefix, _ := netip.ParsePrefix(source)
		if prefix.Addr().Is4() && req.Profile.PublicIPv4 == nil {
			return fmt.Errorf("IPv4透明出口必须配置预期公网IPv4以执行严格出口验证")
		}
		if prefix.Addr().Is6() && req.Profile.PublicIPv6 == nil {
			return fmt.Errorf("IPv6透明出口必须配置预期公网IPv6以执行严格出口验证")
		}
	}
	for _, candidate := range []struct {
		value **string
		label string
	}{
		{&req.Interface, "实例接口"},
		{&req.InterfaceV4, "实例IPv4接口"},
		{&req.InterfaceV6, "实例IPv6接口"},
	} {
		if *candidate.value == nil || strings.TrimSpace(**candidate.value) == "" {
			continue
		}
		iface, ifaceErr := validateEgressIdentifier(**candidate.value, candidate.label, 15, false)
		if ifaceErr != nil {
			return ifaceErr
		}
		if !egressInterfacePattern.MatchString(iface) {
			return fmt.Errorf("%s只能包含字母、数字、点、下划线和短横线", candidate.label)
		}
		**candidate.value = iface
	}
	if req.InterfaceV4 == nil && req.Interface != nil {
		req.InterfaceV4 = req.Interface
	}
	if req.InterfaceV6 == nil && req.Interface != nil {
		req.InterfaceV6 = req.Interface
	}
	if req.Enabled == nil {
		enabled := true
		req.Enabled = &enabled
	}
	if *req.Enabled && req.Profile.Mode == "native" {
		for _, source := range req.Sources {
			prefix, _ := netip.ParsePrefix(source)
			if prefix.Addr().Is4() && (req.InterfaceV4 == nil || strings.TrimSpace(*req.InterfaceV4) == "") {
				return fmt.Errorf("native出口的IPv4源地址要求可验证的宿主入口接口")
			}
			if prefix.Addr().Is6() && (req.InterfaceV6 == nil || strings.TrimSpace(*req.InterfaceV6) == "") {
				return fmt.Errorf("native出口的IPv6源地址要求可验证的宿主入口接口")
			}
		}
	}
	return nil
}

func instanceEgressKey(instance *providerModel.Instance) string {
	if value := strings.TrimSpace(instance.UUID); value != "" {
		return value
	}
	return strconv.FormatUint(uint64(instance.ID), 10)
}

func (s *InstanceEgressService) loadContext(ctx context.Context, instanceID uint) (*providerModel.Instance, *providerModel.Provider, *monitoringModel.MonitoringConfig, error) {
	if s == nil || s.db == nil {
		return nil, nil, nil, fmt.Errorf("数据库连接不可用")
	}
	var instance providerModel.Instance
	if err := s.db.WithContext(ctx).First(&instance, instanceID).Error; err != nil {
		return nil, nil, nil, err
	}
	node, config, err := s.loadProviderContext(ctx, instance.ProviderID)
	if err != nil {
		return nil, nil, nil, err
	}
	return &instance, node, config, nil
}

func (s *InstanceEgressService) loadProviderContext(ctx context.Context, providerID uint) (*providerModel.Provider, *monitoringModel.MonitoringConfig, error) {
	if s == nil || s.db == nil {
		return nil, nil, fmt.Errorf("数据库连接不可用")
	}
	var node providerModel.Provider
	if err := s.db.WithContext(ctx).First(&node, providerID).Error; err != nil {
		return nil, nil, err
	}
	config, err := GetMonitoringConfig(s.db.WithContext(ctx), node.ID)
	if err != nil {
		return nil, nil, err
	}
	return &node, config, nil
}

func egressClient(node *providerModel.Provider, config *monitoringModel.MonitoringConfig) (*Client, error) {
	if !config.AgentInstalled && node.ConnectionType != "agent" {
		return nil, fmt.Errorf("节点尚未安装Agent")
	}
	if strings.TrimSpace(config.AgentToken) == "" {
		return nil, fmt.Errorf("节点Agent令牌未配置")
	}
	host := ResolveAgentHost(node.Endpoint, node.AgentRemoteIP)
	if host == "" && node.ConnectionType == "agent" {
		host = "127.0.0.1"
	}
	if host == "" {
		return nil, fmt.Errorf("节点没有可用的Agent地址")
	}
	port := config.AgentPort
	if port == 0 {
		port = AgentPort
	}
	return GetClientWithMode(node.ID, host, port, config.AgentToken, node.ConnectionType == "agent"), nil
}

func validateEgressProfileTransport(node *providerModel.Provider, profile *EgressProfileRequest) error {
	if node == nil || profile == nil {
		return fmt.Errorf("出口配置传输参数不完整")
	}
	if strings.ToLower(strings.TrimSpace(node.ConnectionType)) == "agent" || profile.TunnelType != "wireguard" {
		return nil
	}
	if managedWireGuard(profile.WireGuard) {
		return fmt.Errorf("托管WireGuard密钥仅允许通过反向Agent传输，当前SSH/HTTP节点不支持安全下发")
	}
	return nil
}

func (s *InstanceEgressService) rejectPersistedManagedWireGuard(ctx context.Context, node *providerModel.Provider, req *InstanceEgressBindRequest) error {
	if node == nil || req == nil || strings.ToLower(strings.TrimSpace(node.ConnectionType)) == "agent" || req.Profile.TunnelType != "wireguard" {
		return nil
	}
	if managedWireGuard(req.Profile.WireGuard) {
		return validateEgressProfileTransport(node, &req.Profile)
	}
	var existing monitoringModel.EgressDesiredProfile
	err := s.db.WithContext(ctx).Where("provider_id = ? AND profile_id = ?", node.ID, req.Profile.ID).First(&existing).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	var previous EgressProfileRequest
	if json.Unmarshal([]byte(existing.ConfigJSON), &previous) == nil && managedWireGuard(previous.WireGuard) {
		return validateEgressProfileTransport(node, &previous)
	}
	return nil
}

func splitEgressInterfaces(raw string) []string {
	parts := strings.Split(raw, ",")
	result := make([]string, 0, len(parts))
	seen := make(map[string]struct{}, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		if _, ok := seen[part]; ok {
			continue
		}
		seen[part] = struct{}{}
		result = append(result, part)
	}
	return result
}

func (s *InstanceEgressService) loadTraffic(ctx context.Context, instanceID, providerID uint) InstanceEgressTraffic {
	var monitor monitoringModel.AgentMonitor
	if err := s.db.WithContext(ctx).
		Where("instance_id = ? AND provider_id = ? AND is_enabled = ?", instanceID, providerID, true).
		First(&monitor).Error; err != nil {
		return InstanceEgressTraffic{}
	}
	lastSync := monitor.LastSyncAt
	var lastSyncPtr *time.Time
	if !lastSync.IsZero() {
		lastSyncPtr = &lastSync
	}
	return InstanceEgressTraffic{
		Monitored:  true,
		Source:     "instance-monitor",
		Interfaces: splitEgressInterfaces(monitor.Interfaces),
		BytesIn:    monitor.LastTrafficBytesIn,
		BytesOut:   monitor.LastTrafficBytesOut,
		LastSyncAt: lastSyncPtr,
	}
}

func endpointAddress(value string) (netip.Addr, bool) {
	value = strings.TrimSpace(value)
	if value == "" {
		return netip.Addr{}, false
	}
	if parsed, err := url.Parse(value); err == nil && parsed.Hostname() != "" {
		value = parsed.Hostname()
	} else if host, _, err := net.SplitHostPort(value); err == nil {
		value = host
	} else {
		value = strings.Trim(value, "[]")
	}
	address, err := netip.ParseAddr(value)
	if err != nil || address.Zone() != "" {
		return netip.Addr{}, false
	}
	return address.Unmap(), true
}

func providerEgressHostAddresses(node *providerModel.Provider) map[netip.Addr]struct{} {
	result := make(map[netip.Addr]struct{}, 3)
	if node == nil {
		return result
	}
	for _, candidate := range []string{node.Endpoint, node.PortIP, node.AgentRemoteIP} {
		if address, ok := endpointAddress(candidate); ok {
			result[address] = struct{}{}
		}
	}
	return result
}

func normalizeInstanceEgressAddress(candidate string, family int) (netip.Addr, error) {
	candidate = strings.TrimSpace(candidate)
	if candidate == "" {
		return netip.Addr{}, nil
	}
	var address netip.Addr
	var err error
	if strings.Contains(candidate, "/") {
		var prefix netip.Prefix
		prefix, err = netip.ParsePrefix(candidate)
		address = prefix.Addr()
	} else {
		address, err = netip.ParseAddr(candidate)
	}
	address = address.Unmap()
	if err != nil || !address.IsValid() || address.Zone() != "" || address.IsUnspecified() || address.IsMulticast() ||
		(family == 4 && !address.Is4()) || (family == 6 && !address.Is6()) {
		return netip.Addr{}, fmt.Errorf("实例数据库包含无效的IPv%d出口源地址", family)
	}
	return address, nil
}

func firstSafeInstanceEgressAddress(candidates []string, family int, hostAddresses map[netip.Addr]struct{}) (string, error) {
	for _, candidate := range candidates {
		address, err := normalizeInstanceEgressAddress(candidate, family)
		if err != nil {
			return "", err
		}
		if !address.IsValid() {
			continue
		}
		if _, isHostAddress := hostAddresses[address]; isHostAddress {
			continue
		}
		return address.String(), nil
	}
	return "", nil
}

func instanceEgressNetworkType(instance *providerModel.Instance, node *providerModel.Provider) string {
	networkType := strings.ToLower(strings.TrimSpace(instance.NetworkType))
	if networkType == "" && node != nil {
		networkType = strings.ToLower(strings.TrimSpace(node.NetworkType))
	}
	return networkType
}

// instanceEgressSources returns only addresses visible as packet sources on
// the node. NAT modes must never use PublicIP/PublicIPv6 because those fields
// can contain the node's shared address after SNAT. Each family contributes at
// most one identity to prevent one instance binding from capturing host flows.
func instanceEgressSources(instance *providerModel.Instance, node *providerModel.Provider) ([]string, error) {
	if instance == nil {
		return nil, fmt.Errorf("实例不能为空")
	}
	hostAddresses := providerEgressHostAddresses(node)
	networkType := instanceEgressNetworkType(instance, node)
	var ipv4Candidates, ipv6Candidates []string
	switch networkType {
	case "nat_ipv4":
		ipv4Candidates = []string{instance.PrivateIP}
	case "nat_ipv4_ipv6":
		ipv4Candidates = []string{instance.PrivateIP}
		ipv6Candidates = []string{instance.IPv6Address}
	case "dedicated_ipv4":
		ipv4Candidates = []string{instance.PublicIP, instance.PrivateIP}
	case "dedicated_ipv4_ipv6":
		ipv4Candidates = []string{instance.PublicIP, instance.PrivateIP}
		ipv6Candidates = []string{instance.PublicIPv6, instance.IPv6Address}
	case "ipv6_only":
		ipv6Candidates = []string{instance.PublicIPv6, instance.IPv6Address}
	case "no_port_mapping":
		ipv4Candidates = []string{instance.PrivateIP}
		ipv6Candidates = []string{instance.IPv6Address}
	default:
		return nil, fmt.Errorf("无法为网络类型%q安全推导实例出口源地址", networkType)
	}
	result := make([]string, 0, 2)
	for _, selection := range []struct {
		candidates []string
		family     int
	}{{ipv4Candidates, 4}, {ipv6Candidates, 6}} {
		address, err := firstSafeInstanceEgressAddress(selection.candidates, selection.family, hostAddresses)
		if err != nil {
			return nil, err
		}
		if address != "" {
			result = append(result, address)
		}
	}
	return result, nil
}

func rejectHostEgressSources(sources []string, node *providerModel.Provider) error {
	hostAddresses := providerEgressHostAddresses(node)
	for _, source := range sources {
		prefix, err := netip.ParsePrefix(source)
		if err != nil {
			return fmt.Errorf("实例源地址格式无效")
		}
		for hostAddress := range hostAddresses {
			if prefix.Addr().BitLen() == hostAddress.BitLen() && prefix.Contains(hostAddress) {
				return fmt.Errorf("实例源地址不能包含节点管理或公网地址%s", hostAddress)
			}
		}
	}
	return nil
}

func mergeInstanceEgressSources(explicitSources, derivedSources []string, node *providerModel.Provider) ([]string, error) {
	combined := make([]string, 0, len(explicitSources)+len(derivedSources))
	combined = append(combined, explicitSources...)
	combined = append(combined, derivedSources...)
	normalized, err := normalizeBindingSources(combined)
	if err != nil {
		return nil, err
	}
	if err := rejectHostEgressSources(normalized, node); err != nil {
		return nil, err
	}
	return normalized, nil
}

func desiredExplicitEgressSources(desired *monitoringModel.EgressDesiredBinding, instance *providerModel.Instance) ([]string, error) {
	if desired == nil {
		return nil, fmt.Errorf("控制端出口绑定不存在")
	}
	if strings.TrimSpace(desired.ExplicitSourcesJSON) != "" {
		var explicit []string
		if err := json.Unmarshal([]byte(desired.ExplicitSourcesJSON), &explicit); err != nil {
			return nil, fmt.Errorf("控制端出口显式源地址损坏")
		}
		return normalizeBindingSources(explicit)
	}

	// Upgrade compatibility: old rows mixed explicit and automatically-derived
	// sources. Remove exact instance inventory identities, then re-derive them
	// under the current network-mode safety policy.
	var previous []string
	if err := json.Unmarshal([]byte(desired.SourcesJSON), &previous); err != nil {
		return nil, fmt.Errorf("控制端出口绑定源地址损坏")
	}
	knownAutomatic := make(map[netip.Addr]struct{}, 4)
	for _, candidate := range []string{instance.PrivateIP, instance.PublicIP, instance.IPv6Address, instance.PublicIPv6} {
		candidate = strings.TrimSpace(candidate)
		if candidate == "" {
			continue
		}
		var address netip.Addr
		if strings.Contains(candidate, "/") {
			if prefix, err := netip.ParsePrefix(candidate); err == nil {
				address = prefix.Addr().Unmap()
			}
		} else if parsed, err := netip.ParseAddr(candidate); err == nil {
			address = parsed.Unmap()
		}
		if address.IsValid() {
			knownAutomatic[address] = struct{}{}
		}
	}
	normalized, err := normalizeBindingSources(previous)
	if err != nil {
		return nil, err
	}
	explicit := make([]string, 0, len(normalized))
	for _, source := range normalized {
		prefix, _ := netip.ParsePrefix(source)
		if prefix.Bits() == prefix.Addr().BitLen() {
			if _, automatic := knownAutomatic[prefix.Addr().Unmap()]; automatic {
				continue
			}
		}
		explicit = append(explicit, source)
	}
	return explicit, nil
}

func instanceEgressInterfaces(instance *providerModel.Instance, monitor *monitoringModel.AgentMonitor) (*string, *string) {
	var v4, v6 *string
	if candidate := strings.TrimSpace(instance.PmacctInterfaceV4); candidate != "" {
		v4 = &candidate
	}
	if candidate := strings.TrimSpace(instance.PmacctInterfaceV6); candidate != "" {
		v6 = &candidate
	}
	if monitor != nil {
		interfaces := splitEgressInterfaces(monitor.Interfaces)
		if v4 == nil && len(interfaces) > 0 {
			candidate := interfaces[0]
			v4 = &candidate
		}
		if v6 == nil && len(interfaces) > 1 {
			candidate := interfaces[1]
			v6 = &candidate
		}
	}
	return v4, v6
}

func deriveEgressCapabilities(instance *providerModel.Instance, node *providerModel.Provider) (bool, string, []string) {
	nativeSupported := false
	recommendedMode := "native"
	reasons := make([]string, 0, 3)
	providerType := strings.ToLower(strings.TrimSpace(node.Type))
	// Native mode is intentionally an explicit allowlist. Unknown adapters,
	// externally managed CNIs, and passthrough links must report a qualified
	// gateway/CNI mode instead of claiming transparent host coverage.
	switch providerType {
	case "docker", "podman", "containerd", "lxd", "incus", "proxmox", "proxmoxve", "qemu", "libvirt":
		nativeSupported = true
	default:
		if providerType == "kubevirt" {
			recommendedMode = "cni"
		} else {
			recommendedMode = "gateway"
		}
		reasons = append(reasons, fmt.Sprintf("Provider类型%s不在已验证的宿主netfilter白名单中", providerType))
	}
	networkType := strings.ToLower(strings.TrimSpace(instance.NetworkType))
	if networkType == "host" || networkType == "host_network" || networkType == "hostnetwork" {
		nativeSupported = false
		recommendedMode = "gateway"
		reasons = append(reasons, "host network实例可能绕过宿主netfilter，不能静默声明native支持")
	}
	// Some adapters expose rootless/host-network hints in Config. Only parse
	// boolean hints; configuration values are never interpolated into commands.
	var hints map[string]interface{}
	if json.Unmarshal([]byte(node.Config), &hints) == nil {
		for _, key := range []string{"rootless", "hostNetwork", "host_network", "externalCNI", "external_cni", "macvlan", "macvtap", "sriov", "passthrough", "passthroughNIC", "passthrough_nic"} {
			if value, ok := hints[key].(bool); ok && value {
				nativeSupported = false
				if strings.Contains(strings.ToLower(key), "cni") || strings.Contains(strings.ToLower(key), "sriov") {
					recommendedMode = "cni"
				} else {
					recommendedMode = "gateway"
				}
				reasons = append(reasons, fmt.Sprintf("节点网络提示%s会绕过宿主Agent的可验证数据面", key))
				break
			}
		}
		for _, key := range []string{"networkMode", "network_mode", "cniMode", "cni_mode"} {
			if value, ok := hints[key].(string); ok {
				mode := strings.ToLower(strings.TrimSpace(value))
				if mode == "host" || mode == "external" || mode == "macvlan" || mode == "macvtap" || mode == "sriov" || mode == "passthrough" {
					nativeSupported = false
					recommendedMode = "gateway"
					reasons = append(reasons, fmt.Sprintf("节点网络模式%s不保证宿主netfilter覆盖", mode))
				}
			}
		}
	}
	if nativeSupported && strings.TrimSpace(instance.PmacctInterfaceV4) == "" && strings.TrimSpace(instance.PmacctInterfaceV6) == "" {
		nativeSupported = false
		recommendedMode = "gateway"
		reasons = append(reasons, "未探测到实例的宿主可见veth/TAP入口接口")
	}
	if len(reasons) > 0 && recommendedMode == "native" {
		recommendedMode = "gateway"
	}
	return nativeSupported, recommendedMode, reasons
}

func applyBindingTraffic(status *InstanceEgressStatus) {
	if status.Binding == nil {
		return
	}
	status.Traffic.Monitored = true
	status.Traffic.Source = "egress-binding"
	status.Traffic.BytesIn = status.Binding.TrafficBytesIn
	status.Traffic.BytesOut = status.Binding.TrafficBytesOut
	status.Traffic.DroppedBytes = status.Binding.TrafficBytesDropped
	status.Traffic.UpdatedAt = status.Binding.UpdatedAt
	status.FailClosedEnforced = status.Binding.FailClosedEnforced
	interfaces := make([]string, 0, 2)
	for _, candidate := range []*string{status.Binding.InterfaceV4, status.Binding.InterfaceV6, status.Binding.Interface} {
		if candidate == nil || strings.TrimSpace(*candidate) == "" {
			continue
		}
		value := strings.TrimSpace(*candidate)
		if !slices.Contains(interfaces, value) {
			interfaces = append(interfaces, value)
		}
	}
	if len(interfaces) > 0 {
		status.Traffic.Interfaces = interfaces
	}
}

func enrichEffectiveEgress(status *InstanceEgressStatus) {
	profileID := status.ConfiguredProfileID
	if status.Binding != nil {
		profileID = status.Binding.ProfileID
	}
	for i := range status.Profiles {
		profile := &status.Profiles[i]
		if profile.ID != profileID {
			continue
		}
		status.ConfiguredProfileID = profile.ID
		if profile.PublicIPv4 != nil {
			status.ExpectedIPv4 = *profile.PublicIPv4
		}
		if profile.PublicIPv6 != nil {
			status.ExpectedIPv6 = *profile.PublicIPv6
		}
		status.FailClosedRequired = profile.FailClosed
		status.EffectiveVerified = status.Binding != nil &&
			status.Binding.State == "applied" && profile.Status == "applied" &&
			status.Binding.FailClosedEnforced != nil && *status.Binding.FailClosedEnforced
		if status.EffectiveVerified {
			status.EffectiveIPv4 = status.ExpectedIPv4
			status.EffectiveIPv6 = status.ExpectedIPv6
		}
		return
	}
}

func (s *InstanceEgressService) GetStatus(ctx context.Context, instanceID uint) (*InstanceEgressStatus, error) {
	instance, node, config, err := s.loadContext(ctx, instanceID)
	if err != nil {
		return nil, err
	}
	status := &InstanceEgressStatus{
		InstanceID:          instance.ID,
		InstanceKey:         instanceEgressKey(instance),
		ProviderID:          node.ID,
		ProviderType:        node.Type,
		AgentInstalled:      config.AgentInstalled || node.ConnectionType == "agent",
		ConfiguredProfileID: instance.EgressProfileID,
		Profiles:            []EgressProfile{},
		Traffic:             s.loadTraffic(ctx, instance.ID, node.ID),
	}
	if len(status.Traffic.Interfaces) > 0 && strings.TrimSpace(instance.PmacctInterfaceV4) == "" && strings.TrimSpace(instance.PmacctInterfaceV6) == "" {
		instance.PmacctInterfaceV4 = status.Traffic.Interfaces[0]
	}
	status.NativeSupported, status.RecommendedMode, status.UnsupportedReasons = deriveEgressCapabilities(instance, node)

	var controllerProfile *EgressProfile
	var desiredBinding monitoringModel.EgressDesiredBinding
	bindingErr := s.db.WithContext(ctx).Where("instance_id = ? AND pending_delete = ?", instance.ID, false).First(&desiredBinding).Error
	if bindingErr != nil && !errors.Is(bindingErr, gorm.ErrRecordNotFound) {
		return nil, bindingErr
	}
	if bindingErr == nil {
		binding, bindingErr := desiredBindingResponse(&desiredBinding)
		if bindingErr != nil {
			return nil, bindingErr
		}
		status.Binding = binding
		var desiredProfile monitoringModel.EgressDesiredProfile
		if err := s.db.WithContext(ctx).
			Where("provider_id = ? AND profile_id = ?", node.ID, desiredBinding.ProfileID).
			First(&desiredProfile).Error; err != nil {
			return nil, err
		}
		controllerProfile, err = desiredProfileResponse(&desiredProfile)
		if err != nil {
			return nil, err
		}
		status.Profiles = append(status.Profiles, *controllerProfile)
	}
	enrichEffectiveEgress(status)

	client, err := egressClient(node, config)
	if err != nil {
		status.AgentError = err.Error()
		return status, nil
	}
	capabilities, err := client.GetEgressCapabilities()
	if err != nil {
		status.AgentError = err.Error()
		return status, nil
	}
	status.AgentConnected = true
	status.Capabilities = capabilities
	profiles, err := client.ListEgressProfiles()
	if err != nil {
		status.AgentError = err.Error()
		return status, nil
	}
	status.Profiles = profiles.Profiles
	if controllerProfile != nil {
		found := false
		for i := range status.Profiles {
			if status.Profiles[i].ID == controllerProfile.ID {
				found = true
				break
			}
		}
		if !found {
			status.Profiles = append(status.Profiles, *controllerProfile)
		}
	}
	bindings, err := client.ListEgressBindings()
	if err != nil {
		status.AgentError = err.Error()
		return status, nil
	}
	for i := range bindings.Bindings {
		if bindings.Bindings[i].InstanceID == status.InstanceKey {
			binding := bindings.Bindings[i]
			status.Binding = &binding
			break
		}
	}
	applyBindingTraffic(status)
	enrichEffectiveEgress(status)
	return status, nil
}

func (s *InstanceEgressService) Bind(ctx context.Context, instanceID uint, req InstanceEgressBindRequest) (*InstanceEgressBindResult, error) {
	instance, node, config, err := s.loadContext(ctx, instanceID)
	if err != nil {
		return nil, err
	}
	// Resolve defaults from controller-side instance/monitor metadata once. A
	// caller may still provide explicit values for unusual bridge layouts.
	var monitor monitoringModel.AgentMonitor
	if monitorErr := s.db.WithContext(ctx).
		Where("instance_id = ? AND provider_id = ? AND is_enabled = ?", instance.ID, node.ID, true).
		First(&monitor).Error; monitorErr != nil {
		monitor = monitoringModel.AgentMonitor{}
	}
	if req.InterfaceV4 == nil && req.InterfaceV6 == nil && req.Interface == nil {
		req.InterfaceV4, req.InterfaceV6 = instanceEgressInterfaces(instance, &monitor)
		if req.InterfaceV4 == nil || (isIPv6Capable(instance.NetworkType) && req.InterfaceV6 == nil) {
			// One bounded discovery pass is allowed when neither the instance
			// cache nor the local monitor knows the host attachment.
			if providerInstance, providerErr := providerService.GetProviderInstanceByID(node.ID); providerErr == nil {
				detectCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
				_ = DetectAndSaveInstanceInterfaces(detectCtx, s.db, providerInstance, instance, "")
				cancel()
				_ = s.db.WithContext(ctx).First(instance, instance.ID).Error
				req.InterfaceV4, req.InterfaceV6 = instanceEgressInterfaces(instance, &monitor)
			}
		}
	}
	if req.Interface != nil && strings.TrimSpace(*req.Interface) != "" && req.InterfaceV4 == nil && req.InterfaceV6 == nil {
		req.InterfaceV4 = req.Interface
		req.InterfaceV6 = req.Interface
	}
	if req.InterfaceV4 != nil && strings.TrimSpace(*req.InterfaceV4) != "" && strings.TrimSpace(instance.PmacctInterfaceV4) == "" {
		instance.PmacctInterfaceV4 = strings.TrimSpace(*req.InterfaceV4)
	}
	if req.InterfaceV6 != nil && strings.TrimSpace(*req.InterfaceV6) != "" && strings.TrimSpace(instance.PmacctInterfaceV6) == "" {
		instance.PmacctInterfaceV6 = strings.TrimSpace(*req.InterfaceV6)
	}
	explicitValues := append([]string(nil), req.Sources...)
	if strings.TrimSpace(req.Source) != "" {
		explicitValues = append(explicitValues, req.Source)
	}
	explicitSources, err := normalizeBindingSources(explicitValues)
	if err != nil {
		return nil, err
	}
	derivedSources, err := instanceEgressSources(instance, node)
	if err != nil {
		return nil, err
	}
	completeSources, err := mergeInstanceEgressSources(explicitSources, derivedSources, node)
	if err != nil {
		return nil, err
	}
	req.Source = ""
	req.Sources = completeSources
	req.explicitSources = explicitSources
	if err := ValidateInstanceEgressBindRequest(&req); err != nil {
		return nil, err
	}
	if supported, recommended, reasons := deriveEgressCapabilities(instance, node); req.Profile.Mode == "native" && !supported {
		detail := strings.Join(reasons, "; ")
		return nil, fmt.Errorf("当前实例不支持native出口模式，建议使用%s: %s", recommended, detail)
	}
	if err := s.rejectPersistedManagedWireGuard(ctx, node, &req); err != nil {
		return nil, err
	}
	desiredProfile, desiredBinding, err := s.persistDesiredState(ctx, instance, node, &req)
	if err != nil {
		return nil, err
	}
	profileRequest, err := materializeDesiredProfile(desiredProfile)
	if err != nil {
		return nil, err
	}
	if err := validateEgressProfileTransport(node, &profileRequest); err != nil {
		return nil, err
	}
	profileFallback, err := desiredProfileResponse(desiredProfile)
	if err != nil {
		return nil, err
	}
	bindingRequest, err := desiredBindingRequest(desiredBinding)
	if err != nil {
		return nil, err
	}
	bindingFallback, err := desiredBindingResponse(desiredBinding)
	if err != nil {
		return nil, err
	}
	result := &InstanceEgressBindResult{Profile: profileFallback, Binding: bindingFallback}
	client, err := egressClient(node, config)
	if err != nil {
		result.ReconcileError = err.Error()
		return result, nil
	}
	profile, err := client.PutEgressProfile(profileRequest)
	if err != nil {
		result.ReconcileError = fmt.Sprintf("保存节点出口配置失败: %v", err)
		return result, nil
	}
	result.Profile = profile
	bindingRequest.ProfileID = profile.ID
	binding, err := client.PutEgressBinding(bindingRequest)
	if err != nil {
		result.ReconcileError = fmt.Sprintf("绑定实例出口失败: %v", err)
		return result, nil
	}
	result.Binding = binding
	reconcile, reconcileErr := client.ReconcileEgress(egressApplyRequested(req.Apply))
	if reconcileErr != nil {
		result.ReconcileError = reconcileErr.Error()
		return result, nil
	}
	result.Reconcile = reconcile
	return result, nil
}

func (s *InstanceEgressService) Unbind(ctx context.Context, instanceID uint, apply bool) (*InstanceEgressUnbindResult, error) {
	instance, node, config, err := s.loadContext(ctx, instanceID)
	if err != nil {
		return nil, err
	}
	var desired monitoringModel.EgressDesiredBinding
	err = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		bindingErr := tx.Where("instance_id = ?", instance.ID).First(&desired).Error
		if bindingErr != nil && !errors.Is(bindingErr, gorm.ErrRecordNotFound) {
			return bindingErr
		}
		if bindingErr == nil {
			desired.PendingDelete = true
			desired.Enabled = false
			if err := tx.Save(&desired).Error; err != nil {
				return err
			}
		}
		return tx.Model(&providerModel.Instance{}).Where("id = ?", instance.ID).
			Update("egress_profile_id", "").Error
	})
	if err != nil {
		return nil, fmt.Errorf("记录实例出口清理状态失败: %w", err)
	}
	result := &InstanceEgressUnbindResult{}
	client, err := egressClient(node, config)
	if err != nil {
		result.ReconcileError = err.Error()
		return result, nil
	}
	if err := client.DeleteEgressBinding(instanceEgressKey(instance)); err != nil {
		result.ReconcileError = fmt.Sprintf("解除实例出口绑定失败: %v", err)
		return result, nil
	}
	reconcile, reconcileErr := client.ReconcileEgress(apply)
	if reconcileErr != nil {
		result.ReconcileError = reconcileErr.Error()
		return result, nil
	}
	result.Reconcile = reconcile
	if desired.ID != 0 {
		if err := s.finalizeBindingDeletion(ctx, client, &desired); err != nil {
			result.ReconcileError = err.Error()
		}
	}
	return result, nil
}

func (s *InstanceEgressService) finalizeBindingDeletion(ctx context.Context, client *Client, desired *monitoringModel.EgressDesiredBinding) error {
	var references int64
	if err := s.db.WithContext(ctx).Model(&monitoringModel.EgressDesiredBinding{}).
		Where("provider_id = ? AND profile_id = ? AND pending_delete = ? AND id <> ?", desired.ProviderID, desired.ProfileID, false, desired.ID).
		Count(&references).Error; err != nil {
		return err
	}
	if references == 0 {
		if err := client.DeleteEgressProfile(desired.ProfileID); err != nil {
			return fmt.Errorf("节点出口配置垃圾回收失败: %w", err)
		}
	}
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Delete(&monitoringModel.EgressDesiredBinding{}, desired.ID).Error; err != nil {
			return fmt.Errorf("清除控制端出口绑定失败: %w", err)
		}
		if references == 0 {
			return tx.Where("provider_id = ? AND profile_id = ?", desired.ProviderID, desired.ProfileID).
				Delete(&monitoringModel.EgressDesiredProfile{}).Error
		}
		return nil
	})
}

func (s *InstanceEgressService) replayPendingBindingDeletion(ctx context.Context, desired *monitoringModel.EgressDesiredBinding, apply bool) (*EgressReconcileResponse, error) {
	if desired == nil || !desired.PendingDelete {
		return nil, fmt.Errorf("出口绑定不是待清理状态")
	}
	node, config, err := s.loadProviderContext(ctx, desired.ProviderID)
	if err != nil {
		return nil, err
	}
	client, err := egressClient(node, config)
	if err != nil {
		return nil, err
	}
	if err := client.DeleteEgressBinding(desired.InstanceKey); err != nil {
		return nil, err
	}
	reconcile, err := client.ReconcileEgress(apply)
	if err != nil {
		return nil, err
	}
	if err := s.finalizeBindingDeletion(ctx, client, desired); err != nil {
		return reconcile, err
	}
	return reconcile, nil
}

func (s *InstanceEgressService) Reconcile(ctx context.Context, instanceID uint, apply bool) (*InstanceEgressReconcileResult, error) {
	reconcile, err := s.refreshBinding(ctx, instanceID, apply)
	if err != nil {
		return nil, err
	}
	if reconcile == nil {
		_, node, config, loadErr := s.loadContext(ctx, instanceID)
		if loadErr != nil {
			return nil, loadErr
		}
		client, clientErr := egressClient(node, config)
		if clientErr != nil {
			return nil, clientErr
		}
		reconcile, err = client.ReconcileEgress(apply)
		if err != nil {
			return nil, err
		}
	}
	return &InstanceEgressReconcileResult{Reconcile: reconcile}, nil
}

// RefreshBinding re-derives the source/interface after a lifecycle event and
// then reconciles the desired route. It performs bounded Agent calls and no
// remote work while holding a database transaction.
func (s *InstanceEgressService) RefreshBinding(ctx context.Context, instanceID uint, apply bool) error {
	_, err := s.refreshBinding(ctx, instanceID, apply)
	return err
}

func (s *InstanceEgressService) refreshBinding(ctx context.Context, instanceID uint, apply bool) (*EgressReconcileResponse, error) {
	var desiredBinding monitoringModel.EgressDesiredBinding
	desiredErr := s.db.WithContext(ctx).Where("instance_id = ?", instanceID).First(&desiredBinding).Error
	if desiredErr == nil && desiredBinding.PendingDelete {
		return s.replayPendingBindingDeletion(ctx, &desiredBinding, apply)
	}
	instance, node, config, err := s.loadContext(ctx, instanceID)
	if err != nil {
		return nil, err
	}
	if desiredErr != nil {
		if errors.Is(desiredErr, gorm.ErrRecordNotFound) && strings.TrimSpace(instance.EgressProfileID) == "" {
			return nil, nil
		}
		if errors.Is(desiredErr, gorm.ErrRecordNotFound) {
			return nil, fmt.Errorf("实例缺少可恢复的控制端出口期望状态")
		}
		return nil, desiredErr
	}
	client, err := egressClient(node, config)
	if err != nil {
		return nil, err
	}
	var desiredProfile monitoringModel.EgressDesiredProfile
	if err := s.db.WithContext(ctx).
		Where("provider_id = ? AND profile_id = ?", node.ID, desiredBinding.ProfileID).
		First(&desiredProfile).Error; err != nil {
		return nil, err
	}
	profileRequest, err := materializeDesiredProfile(&desiredProfile)
	if err != nil {
		return nil, err
	}
	if err := validateEgressProfileTransport(node, &profileRequest); err != nil {
		return nil, err
	}
	var monitor monitoringModel.AgentMonitor
	_ = s.db.WithContext(ctx).
		Where("instance_id = ? AND provider_id = ? AND is_enabled = ?", instance.ID, node.ID, true).
		First(&monitor).Error
	explicitSources, err := desiredExplicitEgressSources(&desiredBinding, instance)
	if err != nil {
		return nil, err
	}
	derivedSources, err := instanceEgressSources(instance, node)
	if err != nil {
		return nil, err
	}
	completeSources, err := mergeInstanceEgressSources(explicitSources, derivedSources, node)
	if err != nil {
		return nil, err
	}
	if len(completeSources) == 0 {
		return nil, fmt.Errorf("实例源地址不能为空")
	}
	desiredBinding.SourcesJSON = string(mustJSON(completeSources))
	desiredBinding.ExplicitSourcesJSON = string(mustJSON(explicitSources))
	if derivedV4, derivedV6 := instanceEgressInterfaces(instance, &monitor); derivedV4 != nil || derivedV6 != nil {
		if derivedV4 != nil {
			desiredBinding.InterfaceV4 = *derivedV4
		}
		if derivedV6 != nil {
			desiredBinding.InterfaceV6 = *derivedV6
		}
	}
	if err := s.db.WithContext(ctx).Model(&monitoringModel.EgressDesiredBinding{}).
		Where("id = ?", desiredBinding.ID).
		Updates(map[string]interface{}{
			"sources_json":          desiredBinding.SourcesJSON,
			"explicit_sources_json": desiredBinding.ExplicitSourcesJSON,
			"interface":             desiredBinding.Interface,
			"interface_v4":          desiredBinding.InterfaceV4,
			"interface_v6":          desiredBinding.InterfaceV6,
		}).Error; err != nil {
		return nil, err
	}
	bindingRequest, err := desiredBindingRequest(&desiredBinding)
	if err != nil {
		return nil, err
	}
	if _, err := client.PutEgressProfile(profileRequest); err != nil {
		return nil, fmt.Errorf("恢复节点出口配置失败: %w", err)
	}
	if _, err := client.PutEgressBinding(bindingRequest); err != nil {
		return nil, fmt.Errorf("恢复节点出口绑定失败: %w", err)
	}
	return client.ReconcileEgress(apply)
}

func mustJSON(value interface{}) []byte {
	encoded, _ := json.Marshal(value)
	return encoded
}

func validateReplacedEgressState(response *EgressStateResponse, profileCount, bindingCount int, apply bool) error {
	if response == nil {
		return fmt.Errorf("Agent未返回出口状态替换结果")
	}
	if response.ProfileCount != profileCount || response.BindingCount != bindingCount {
		return fmt.Errorf("Agent出口状态数量不一致: profiles=%d/%d bindings=%d/%d",
			response.ProfileCount, profileCount, response.BindingCount, bindingCount)
	}
	if apply && (!response.Reconcile.Applied || len(response.Reconcile.Errors) > 0) {
		detail := "Agent未确认出口规则已应用"
		if len(response.Reconcile.Errors) > 0 {
			detail = strings.Join(response.Reconcile.Errors, "; ")
		}
		return fmt.Errorf("节点出口规则批量应用未生效: %s", detail)
	}
	return nil
}

// RestoreProviderEgress replays the complete controller-authoritative state
// after an Agent reconnect or local SQLite loss. All controller reads are
// batched, remote work happens outside database transactions, and the Agent is
// called exactly once so nftables is rebuilt only once.
func (s *InstanceEgressService) RestoreProviderEgress(ctx context.Context, providerID uint, apply bool) (int, error) {
	if s == nil || s.db == nil {
		return 0, fmt.Errorf("数据库连接不可用")
	}
	if providerID == 0 {
		return 0, fmt.Errorf("ProviderID不能为空")
	}
	var bindings []monitoringModel.EgressDesiredBinding
	if err := s.db.WithContext(ctx).Where("provider_id = ?", providerID).Order("id ASC").Find(&bindings).Error; err != nil {
		return 0, err
	}
	if len(bindings) == 0 {
		return 0, nil
	}

	active := make([]monitoringModel.EgressDesiredBinding, 0, len(bindings))
	pending := make([]monitoringModel.EgressDesiredBinding, 0)
	instanceIDs := make([]uint, 0, len(bindings))
	for i := range bindings {
		if bindings[i].PendingDelete {
			pending = append(pending, bindings[i])
			continue
		}
		active = append(active, bindings[i])
		instanceIDs = append(instanceIDs, bindings[i].InstanceID)
	}

	instancesByID := make(map[uint]*providerModel.Instance, len(instanceIDs))
	monitorsByInstanceID := make(map[uint]*monitoringModel.AgentMonitor, len(instanceIDs))
	if len(instanceIDs) > 0 {
		var instances []providerModel.Instance
		if err := s.db.WithContext(ctx).
			Where("provider_id = ? AND id IN ?", providerID, instanceIDs).
			Find(&instances).Error; err != nil {
			return 0, fmt.Errorf("批量读取出口实例失败: %w", err)
		}
		for i := range instances {
			instancesByID[instances[i].ID] = &instances[i]
		}
		var monitors []monitoringModel.AgentMonitor
		if err := s.db.WithContext(ctx).
			Where("provider_id = ? AND instance_id IN ? AND is_enabled = ?", providerID, instanceIDs, true).
			Find(&monitors).Error; err != nil {
			return 0, fmt.Errorf("批量读取出口监控接口失败: %w", err)
		}
		for i := range monitors {
			monitorsByInstanceID[monitors[i].InstanceID] = &monitors[i]
		}
	}

	var desiredProfiles []monitoringModel.EgressDesiredProfile
	if err := s.db.WithContext(ctx).
		Where("provider_id = ?", providerID).
		Order("id ASC").
		Find(&desiredProfiles).Error; err != nil {
		return 0, fmt.Errorf("批量读取出口配置失败: %w", err)
	}
	desiredProfilesByID := make(map[string]*monitoringModel.EgressDesiredProfile, len(desiredProfiles))
	for i := range desiredProfiles {
		desiredProfilesByID[desiredProfiles[i].ProfileID] = &desiredProfiles[i]
	}

	node, config, err := s.loadProviderContext(ctx, providerID)
	if err != nil {
		return 0, fmt.Errorf("读取Provider Agent配置失败: %w", err)
	}
	profileRequestsByID := make(map[string]EgressProfileRequest)
	profileOrder := make([]string, 0, len(desiredProfiles))
	bindingRequests := make([]EgressBindingRequest, 0, len(active))
	errs := make([]error, 0)
	for i := range active {
		instance := instancesByID[active[i].InstanceID]
		if instance == nil {
			errs = append(errs, fmt.Errorf("实例%d不存在或不属于当前Provider", active[i].InstanceID))
			continue
		}
		desiredProfile := desiredProfilesByID[active[i].ProfileID]
		if desiredProfile == nil {
			errs = append(errs, fmt.Errorf("实例%d引用的出口配置%s不存在", instance.ID, active[i].ProfileID))
			continue
		}
		if _, exists := profileRequestsByID[active[i].ProfileID]; !exists {
			profileRequest, profileErr := materializeDesiredProfile(desiredProfile)
			if profileErr != nil {
				errs = append(errs, fmt.Errorf("出口配置%s恢复失败: %w", active[i].ProfileID, profileErr))
				continue
			}
			if profileRequest.ID != active[i].ProfileID {
				errs = append(errs, fmt.Errorf("出口配置%s的控制端标识不一致", active[i].ProfileID))
				continue
			}
			if profileErr = validateEgressProfileTransport(node, &profileRequest); profileErr != nil {
				errs = append(errs, fmt.Errorf("出口配置%s恢复失败: %w", active[i].ProfileID, profileErr))
				continue
			}
			profileRequestsByID[active[i].ProfileID] = profileRequest
			profileOrder = append(profileOrder, active[i].ProfileID)
		}

		explicitSources, sourceErr := desiredExplicitEgressSources(&active[i], instance)
		if sourceErr != nil {
			errs = append(errs, fmt.Errorf("实例%d出口源地址恢复失败: %w", instance.ID, sourceErr))
			continue
		}
		derivedSources, sourceErr := instanceEgressSources(instance, node)
		if sourceErr != nil {
			errs = append(errs, fmt.Errorf("实例%d出口源地址恢复失败: %w", instance.ID, sourceErr))
			continue
		}
		completeSources, sourceErr := mergeInstanceEgressSources(explicitSources, derivedSources, node)
		if sourceErr != nil || len(completeSources) == 0 {
			if sourceErr == nil {
				sourceErr = fmt.Errorf("实例源地址不能为空")
			}
			errs = append(errs, fmt.Errorf("实例%d出口源地址恢复失败: %w", instance.ID, sourceErr))
			continue
		}
		active[i].InstanceKey = instanceEgressKey(instance)
		active[i].SourcesJSON = string(mustJSON(completeSources))
		active[i].ExplicitSourcesJSON = string(mustJSON(explicitSources))
		active[i].UpdatedAt = time.Now()
		if derivedV4, derivedV6 := instanceEgressInterfaces(instance, monitorsByInstanceID[instance.ID]); derivedV4 != nil || derivedV6 != nil {
			if derivedV4 != nil {
				active[i].InterfaceV4 = *derivedV4
			}
			if derivedV6 != nil {
				active[i].InterfaceV6 = *derivedV6
			}
		}
		bindingRequest, bindingErr := desiredBindingRequest(&active[i])
		if bindingErr != nil {
			errs = append(errs, fmt.Errorf("实例%d出口绑定恢复失败: %w", instance.ID, bindingErr))
			continue
		}
		bindingRequests = append(bindingRequests, bindingRequest)
	}
	if err := errors.Join(errs...); err != nil {
		return 0, err
	}

	profileRequests := make([]EgressProfileRequest, 0, len(profileOrder))
	for _, profileID := range profileOrder {
		profileRequests = append(profileRequests, profileRequestsByID[profileID])
	}
	if len(active) > 0 {
		result := s.db.WithContext(ctx).Clauses(clause.OnConflict{
			Columns: []clause.Column{{Name: "id"}},
			DoUpdates: clause.AssignmentColumns([]string{
				"instance_key", "sources_json", "explicit_sources_json",
				"interface", "interface_v4", "interface_v6", "updated_at",
			}),
		}).CreateInBatches(&active, 500)
		if result.Error != nil {
			return 0, fmt.Errorf("批量更新控制端出口期望状态失败: %w", result.Error)
		}
	}

	client, err := egressClient(node, config)
	if err != nil {
		return 0, err
	}
	response, err := client.ReplaceEgressState(EgressStateRequest{
		Profiles: profileRequests,
		Bindings: bindingRequests,
		Apply:    apply,
	})
	if err != nil {
		return 0, fmt.Errorf("批量恢复节点出口状态失败: %w", err)
	}
	if err := validateReplacedEgressState(response, len(profileRequests), len(bindingRequests), apply); err != nil {
		return 0, err
	}

	if apply {
		activeProfileIDs := make([]string, 0, len(profileOrder))
		activeProfileIDs = append(activeProfileIDs, profileOrder...)
		if err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			if len(pending) > 0 {
				pendingIDs := make([]uint, 0, len(pending))
				for i := range pending {
					pendingIDs = append(pendingIDs, pending[i].ID)
				}
				if err := tx.Where("id IN ?", pendingIDs).
					Delete(&monitoringModel.EgressDesiredBinding{}).Error; err != nil {
					return err
				}
			}
			profiles := tx.Where("provider_id = ?", providerID)
			if len(activeProfileIDs) > 0 {
				profiles = profiles.Where("profile_id NOT IN ?", activeProfileIDs)
			}
			return profiles.Delete(&monitoringModel.EgressDesiredProfile{}).Error
		}); err != nil {
			return 0, fmt.Errorf("清理已确认的控制端出口待删除状态失败: %w", err)
		}
	}
	return len(bindings), nil
}

// CleanupProviderEgress removes all egress state owned by one provider before
// the controller deletes that provider. Remote work is deliberately performed
// outside the provider database transaction: a failed Agent cleanup leaves the
// authoritative desired rows available for a later retry or manual recovery.
func (s *InstanceEgressService) CleanupProviderEgress(ctx context.Context, providerID uint) error {
	if s == nil || s.db == nil {
		return fmt.Errorf("数据库连接不可用")
	}
	if providerID == 0 {
		return fmt.Errorf("ProviderID不能为空")
	}

	var desiredBindings []monitoringModel.EgressDesiredBinding
	if err := s.db.WithContext(ctx).
		Where("provider_id = ?", providerID).
		Order("id ASC").
		Find(&desiredBindings).Error; err != nil {
		return fmt.Errorf("读取Provider出口绑定失败: %w", err)
	}
	var desiredProfiles []monitoringModel.EgressDesiredProfile
	if err := s.db.WithContext(ctx).
		Where("provider_id = ?", providerID).
		Order("id ASC").
		Find(&desiredProfiles).Error; err != nil {
		return fmt.Errorf("读取Provider出口配置失败: %w", err)
	}
	if len(desiredBindings) == 0 && len(desiredProfiles) == 0 {
		return nil
	}

	node, config, err := s.loadProviderContext(ctx, providerID)
	if err != nil {
		return fmt.Errorf("读取Provider Agent配置失败: %w", err)
	}
	client, err := egressClient(node, config)
	if err != nil {
		return err
	}

	response, err := client.ReplaceEgressState(EgressStateRequest{
		Profiles: []EgressProfileRequest{},
		Bindings: []EgressBindingRequest{},
		Apply:    true,
	})
	if err != nil {
		return fmt.Errorf("批量清理节点出口状态失败: %w", err)
	}
	if err := validateReplacedEgressState(response, 0, 0, true); err != nil {
		return fmt.Errorf("节点出口状态清理未生效: %w", err)
	}
	return nil
}

func init() {
	RegisterAgentReconnectHook(func(providerID uint) {
		if global.APP_DB == nil {
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), 6*time.Minute)
		defer cancel()
		restored, err := NewInstanceEgressService(global.APP_DB).RestoreProviderEgress(ctx, providerID, true)
		if err != nil {
			if global.APP_LOG != nil {
				global.APP_LOG.Warn("Agent重连后恢复独立出口失败",
					zap.Uint("provider_id", providerID), zap.Error(err))
			}
			return
		}
		if restored > 0 && global.APP_LOG != nil {
			global.APP_LOG.Info("Agent重连后独立出口恢复完成",
				zap.Uint("provider_id", providerID), zap.Int("restored", restored))
		}
	})
}

func (s *InstanceEgressService) EnsureDependencies(ctx context.Context, instanceID uint, packageSet string) (*InstanceEgressDependencyResult, error) {
	packageSet = strings.TrimSpace(packageSet)
	if packageSet == "" {
		packageSet = "wireguard"
	}
	if packageSet != "native" && packageSet != "wireguard" {
		return nil, fmt.Errorf("依赖集合仅支持native或wireguard")
	}
	_, node, config, err := s.loadContext(ctx, instanceID)
	if err != nil {
		return nil, err
	}
	client, err := egressClient(node, config)
	if err != nil {
		return nil, err
	}
	result, err := client.EnsureEgressDependencies(packageSet)
	if err != nil {
		return nil, err
	}
	return &InstanceEgressDependencyResult{Result: result}, nil
}
