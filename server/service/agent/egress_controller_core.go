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
	"os"
	"regexp"
	"strings"
	"time"

	"oneclickvirt/global"
	monitoringModel "oneclickvirt/model/monitoring"
	providerModel "oneclickvirt/model/provider"

	"gorm.io/gorm"
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
