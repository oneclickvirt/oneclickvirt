package provider

import (
	"context"
	"fmt"
	"net/http"
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

// DiscoveredInstance 发现的实例信息结构体
type DiscoveredInstance struct {
	// 基本标识
	UUID         string `json:"uuid"`         // 实例唯一标识
	Name         string `json:"name"`         // 实例名称
	Status       string `json:"status"`       // 实例状态（running, stopped等）
	InstanceType string `json:"instanceType"` // 实例类型（container或vm）

	// 资源配置
	CPU    int   `json:"cpu"`    // CPU核心数
	Memory int64 `json:"memory"` // 内存大小（MB）
	Disk   int64 `json:"disk"`   // 磁盘大小（MB）

	// 网络配置
	PrivateIP   string `json:"privateIP"`   // 内网IPv4地址
	PublicIP    string `json:"publicIP"`    // 公网IPv4地址
	IPv6Address string `json:"ipv6Address"` // IPv6地址
	SSHPort     int    `json:"sshPort"`     // SSH端口
	ExtraPorts  []int  `json:"extraPorts"`  // 其他开放端口
	MACAddress  string `json:"macAddress"`  // MAC地址

	// 系统信息
	Image  string `json:"image"`  // 使用的镜像
	OSType string `json:"osType"` // 操作系统类型

	// 原始数据（用于调试）
	RawData interface{} `json:"rawData"` // provider特定的原始数据
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
