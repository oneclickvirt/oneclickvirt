package provider

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"sync"

	"oneclickvirt/model/provider"
	"oneclickvirt/provider/health"
)

// 类型别名，使用model包中的结构体
type Instance = provider.ProviderInstance
type Image = provider.ProviderImage
type InstanceConfig = provider.ProviderInstanceConfig
type NodeConfig = provider.ProviderNodeConfig

// ProgressCallback 进度回调函数类型
type ProgressCallback func(percentage int, message string)

// RecoveryInstanceIdentity is the provider-side runtime identity captured by
// an authoritative discovery pass.  It is intentionally credential-free.  A
// clustered provider may require Node in addition to the guest ID and type;
// providers without a node concept can leave Node empty and use ID as their
// stable instance name.
type RecoveryInstanceIdentity struct {
	Node string `json:"node,omitempty"`
	ID   string `json:"id"`
	Type string `json:"type"`
}

func (i RecoveryInstanceIdentity) Valid() bool {
	return strings.TrimSpace(i.ID) != "" && strings.TrimSpace(i.Type) != ""
}

// KnownRuntimeInstanceStarter is an optional capability used only by the
// reboot-recovery worker.  It lets a provider consume the identity returned by
// the same discovery pass instead of rediscovering a guest by name (or falling
// back to another transport).
type KnownRuntimeInstanceStarter interface {
	StartInstanceByRecoveryIdentity(ctx context.Context, identity RecoveryInstanceIdentity) error
}

// StartInstanceForRecovery starts a guest using its discovered runtime
// identity.  Providers which do not need a richer capability use the stable ID
// directly; an empty identity is always rejected so recovery cannot guess a
// different guest.
func StartInstanceForRecovery(ctx context.Context, instance Provider, identity RecoveryInstanceIdentity) error {
	if instance == nil {
		return fmt.Errorf("恢复启动Provider不可用")
	}
	if !identity.Valid() {
		return fmt.Errorf("恢复启动缺少有效实例运行时身份")
	}
	if starter, ok := instance.(KnownRuntimeInstanceStarter); ok {
		return starter.StartInstanceByRecoveryIdentity(ctx, identity)
	}
	return instance.StartInstance(ctx, strings.TrimSpace(identity.ID))
}

// Provider 统一接口
type Provider interface {
	// 基础信息
	GetType() string
	GetName() string
	GetSupportedInstanceTypes() []string // 获取支持的实例类型

	// 实例管理
	ListInstances(ctx context.Context) ([]Instance, error)
	CreateInstance(ctx context.Context, config InstanceConfig) error
	CreateInstanceWithProgress(ctx context.Context, config InstanceConfig, progressCallback ProgressCallback) error
	StartInstance(ctx context.Context, id string) error
	StopInstance(ctx context.Context, id string) error
	RestartInstance(ctx context.Context, id string) error
	DeleteInstance(ctx context.Context, id string) error
	GetInstance(ctx context.Context, id string) (*Instance, error)

	// 镜像管理
	ListImages(ctx context.Context) ([]Image, error)
	PullImage(ctx context.Context, image string) error
	DeleteImage(ctx context.Context, id string) error

	// 连接管理
	Connect(ctx context.Context, config NodeConfig) error
	Disconnect(ctx context.Context) error
	IsConnected() bool

	// 健康检查 - 使用新的health包
	HealthCheck(ctx context.Context) (*health.HealthResult, error)
	GetHealthChecker() health.HealthChecker

	// 平台信息
	GetVersion() string // 获取虚拟化平台版本

	// 密码管理
	SetInstancePassword(ctx context.Context, instanceID, password string) error
	ResetInstancePassword(ctx context.Context, instanceID string) (string, error)

	// SSH命令执行
	ExecuteSSHCommand(ctx context.Context, command string) (string, error)

	// 实例发现（用于纳管已有实例的provider）
	DiscoverInstances(ctx context.Context) ([]DiscoveredInstance, error)
}

// RecoveryInstanceDiscoverer is an optional capability for a provider that
// can return the identity, runtime state, and addresses needed after a node
// reboot without doing the import-oriented per-guest enrichment work.
//
// The normal Provider interface deliberately remains unchanged: providers
// which do not implement this capability keep their established discovery
// behaviour. Call DiscoverInstancesForRecovery instead of asserting this
// interface directly so every provider type remains eligible for recovery.
type RecoveryInstanceDiscoverer interface {
	DiscoverInstancesForRecovery(ctx context.Context) ([]DiscoveredInstance, error)
}

// DiscoverInstancesForRecovery invokes an optimized recovery discovery when a
// provider supplies one, otherwise it makes exactly one normal discovery call.
// It centralizes the fallback so SSH, reverse-Agent, and API-backed providers
// all share the same recovery control flow.
func DiscoverInstancesForRecovery(ctx context.Context, instance Provider) ([]DiscoveredInstance, error) {
	if recoveryProvider, ok := instance.(RecoveryInstanceDiscoverer); ok {
		return recoveryProvider.DiscoverInstancesForRecovery(ctx)
	}
	return instance.DiscoverInstances(ctx)
}

// DiscoveredPortMapping 发现的端口映射信息
type DiscoveredPortMapping struct {
	HostPort      int    `json:"hostPort"`      // 宿主机端口
	GuestPort     int    `json:"guestPort"`     // 容器/虚拟机内部端口
	Protocol      string `json:"protocol"`      // tcp, udp, both
	IsSSH         bool   `json:"isSsh"`         // 是否为SSH端口
	MappingMethod string `json:"mappingMethod"` // native, device_proxy, iptables
}

// DiscoveredAccelerator 发现到的加速设备信息（GPU/NPU）
type DiscoveredAccelerator struct {
	Kind   string `json:"kind"`   // gpu / npu
	ID     string `json:"id"`     // 设备ID（如索引或PCI地址）
	Name   string `json:"name"`   // 设备名称
	Vendor string `json:"vendor"` // 厂商
	Bus    string `json:"bus"`    // 总线地址（可选）
	Source string `json:"source"` // 来源：devices/lspci/nvidia-smi/npu-smi
}

// DiscoveredInstance 发现的实例信息结构体
type DiscoveredInstance struct {
	// 基本标识
	UUID               string `json:"uuid"`               // 发现结果的稳定唯一标识
	ProviderInstanceID string `json:"providerInstanceId"` // 虚拟化平台上的原始实例ID（如 Proxmox VMID/CTID）
	Name               string `json:"name"`               // 实例名称
	Status             string `json:"status"`             // 实例状态（running, stopped等）
	InstanceType       string `json:"instanceType"`       // 实例类型（container或vm）

	// 资源配置
	CPU    int   `json:"cpu"`    // CPU核心数
	Memory int64 `json:"memory"` // 内存大小（MB）
	Disk   int64 `json:"disk"`   // 磁盘大小（MB）

	// 硬件加速配置（主要用于 LXD/Incus 导入场景）
	GpuEnabled   bool                    `json:"gpuEnabled"`
	GpuDeviceIds string                  `json:"gpuDeviceIds"`
	NpuEnabled   bool                    `json:"npuEnabled"`
	NpuDeviceIds string                  `json:"npuDeviceIds"`
	Accelerators []DiscoveredAccelerator `json:"accelerators"`

	// 网络配置
	PrivateIP    string                  `json:"privateIP"`          // 内网IPv4地址
	PublicIP     string                  `json:"publicIP"`           // 公网IPv4地址
	IPv6Address  string                  `json:"ipv6Address"`        // IPv6地址
	SSHPort      int                     `json:"sshPort"`            // SSH端口
	Username     string                  `json:"username,omitempty"` // 导入时可用的SSH用户名
	Password     string                  `json:"-"`                  // 仅用于导入，绝不返回发现接口
	ExtraPorts   []int                   `json:"extraPorts"`         // 其他开放端口（向后兼容）
	PortMappings []DiscoveredPortMapping `json:"portMappings"`       // 完整的端口映射信息
	MACAddress   string                  `json:"macAddress"`         // MAC地址

	// 系统信息
	Image  string `json:"image"`  // 使用的镜像
	OSType string `json:"osType"` // 操作系统类型

	// 原始数据（用于调试）
	RawData interface{} `json:"rawData"` // provider特定的原始数据

	// RuntimeIdentity is captured during discovery and consumed by the narrow
	// reboot-recovery start path. It is safe to persist because it contains no
	// credentials or user secrets.
	RuntimeIdentity *RecoveryInstanceIdentity `json:"runtimeIdentity,omitempty"`
}

// Registry Provider 注册表
type Registry struct {
	providers map[string]func() Provider
	mu        sync.RWMutex
}

var globalRegistry = &Registry{
	providers: make(map[string]func() Provider),
}

// 初始化health包的Transport清理管理器引用（避免循环依赖）
func init() {
	health.GetTransportCleanupManager = func() interface {
		RegisterTransport(*http.Transport)
		RegisterTransportWithProvider(*http.Transport, uint)
	} {
		return GetTransportCleanupManager()
	}
}

// RegisterProvider 注册 Provider
func RegisterProvider(name string, factory func() Provider) {
	globalRegistry.mu.Lock()
	defer globalRegistry.mu.Unlock()
	globalRegistry.providers[name] = factory
}

// GetProvider 获取 Provider 实例
// 返回的是工厂创建的新实例
// 此方法仅用于创建新实例，不推荐直接使用
// 推荐使用 service/provider 包的 GetProviderInstanceByID 方法
func GetProvider(name string) (Provider, error) {
	globalRegistry.mu.RLock()
	factory, exists := globalRegistry.providers[name]
	globalRegistry.mu.RUnlock()

	if !exists {
		return nil, fmt.Errorf("provider %s not registered", name)
	}

	// 每次都创建新实例
	instance := factory()
	return instance, nil
}

// ListProviders 列出所有已注册的 Provider
func ListProviders() []string {
	globalRegistry.mu.RLock()
	defer globalRegistry.mu.RUnlock()

	var names []string
	for name := range globalRegistry.providers {
		names = append(names, name)
	}
	return names
}

// GetAllProviders 获取所有 Provider 类型的工厂函数
// 不再返回单例实例，而是返回可以创建Provider的工厂函数
func GetAllProviders() map[string]func() Provider {
	globalRegistry.mu.RLock()
	defer globalRegistry.mu.RUnlock()

	result := make(map[string]func() Provider)
	for name, factory := range globalRegistry.providers {
		result[name] = factory
	}
	return result
}
