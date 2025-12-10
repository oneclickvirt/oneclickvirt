package health

import (
	"context"
	"time"
)

// HealthChecker 健康检测接口
type HealthChecker interface {
	// CheckHealth 执行健康检查
	CheckHealth(ctx context.Context) (*HealthResult, error)

	// GetHealthStatus 获取健康状态
	GetHealthStatus() HealthStatus

	// SetConfig 设置配置
	SetConfig(config HealthConfig)
}

// HealthStatus 健康状态枚举
type HealthStatus string

const (
	HealthStatusUnknown   HealthStatus = "unknown"
	HealthStatusHealthy   HealthStatus = "healthy"
	HealthStatusUnhealthy HealthStatus = "unhealthy"
	HealthStatusPartial   HealthStatus = "partial"
)

// HealthResult 健康检查结果
type HealthResult struct {
	Status        HealthStatus           `json:"status"`
	Timestamp     time.Time              `json:"timestamp"`
	Duration      time.Duration          `json:"duration"`
	SSHStatus     string                 `json:"ssh_status"`
	APIStatus     string                 `json:"api_status"`
	ServiceStatus string                 `json:"service_status"`
	Errors        []string               `json:"errors,omitempty"`
	Details       map[string]interface{} `json:"details,omitempty"`
	// 资源信息字段
	ResourceInfo *ResourceInfo `json:"resource_info,omitempty"`
	// 节点标识信息
	HostName string `json:"host_name,omitempty"` // 节点主机名（hostname）
}

// ResourceInfo 节点资源信息
type ResourceInfo struct {
	CPUCores    int        `json:"cpu_cores"`    // CPU核心数
	MemoryTotal int64      `json:"memory_total"` // 总内存（MB）
	SwapTotal   int64      `json:"swap_total"`   // 总交换空间（MB）
	DiskTotal   int64      `json:"disk_total"`   // 总磁盘空间（MB）
	DiskFree    int64      `json:"disk_free"`    // 可用磁盘空间（MB）
	StoragePoolPath string     `json:"storage_pool_path"` // 存储池实际挂载路径
	Synced      bool       `json:"synced"`       // 是否已同步
	SyncedAt    *time.Time `json:"synced_at"`    // 同步时间
	HostName    string     `json:"host_name"`    // 节点主机名（hostname），用于区分多个节点
}

// HealthConfig 健康检查配置
type HealthConfig struct {
	// 追踪标识（用于日志和调试）
	ProviderID   uint   `json:"provider_id"`   // Provider数据库ID
	ProviderName string `json:"provider_name"` // Provider名称

	// 基础连接配置
	Host       string `json:"host"`
	Port       int    `json:"port"`
	Username   string `json:"username"`
	Password   string `json:"password"`
	PrivateKey string `json:"private_key"` // SSH私钥，优先于密码使用

	// API配置
	APIEnabled    bool   `json:"api_enabled"`
	APIPort       int    `json:"api_port"`
	APIScheme     string `json:"api_scheme"`      // http, https
	SkipTLSVerify bool   `json:"skip_tls_verify"` // 跳过TLS证书验证
	Token         string `json:"token"`
	TokenID       string `json:"token_id"`
	CertPath      string `json:"cert_path"`
	KeyPath       string `json:"key_path"`
	CertContent   string `json:"cert_content"` // 证书内容（优先于CertPath）
	KeyContent    string `json:"key_content"`  // 私钥内容（优先于KeyPath）

	// 检查配置
	Timeout        time.Duration `json:"timeout"`
	SSHEnabled     bool          `json:"ssh_enabled"`
	ServiceChecks  []string      `json:"service_checks"`  // 要检查的服务列表
	CustomCommands []string      `json:"custom_commands"` // 自定义检查命令
}

// DeepCopy 创建 HealthConfig 的深拷贝，避免并发修改问题
func (c HealthConfig) DeepCopy() HealthConfig {
	// 拷贝切片
	serviceChecks := make([]string, len(c.ServiceChecks))
	copy(serviceChecks, c.ServiceChecks)

	customCommands := make([]string, len(c.CustomCommands))
	copy(customCommands, c.CustomCommands)

	return HealthConfig{
		ProviderID:     c.ProviderID,
		ProviderName:   c.ProviderName,
		Host:           c.Host,
		Port:           c.Port,
		Username:       c.Username,
		Password:       c.Password,
		PrivateKey:     c.PrivateKey,
		APIEnabled:     c.APIEnabled,
		APIPort:        c.APIPort,
		APIScheme:      c.APIScheme,
		SkipTLSVerify:  c.SkipTLSVerify,
		Token:          c.Token,
		TokenID:        c.TokenID,
		CertPath:       c.CertPath,
		KeyPath:        c.KeyPath,
		CertContent:    c.CertContent,
		KeyContent:     c.KeyContent,
		Timeout:        c.Timeout,
		SSHEnabled:     c.SSHEnabled,
		ServiceChecks:  serviceChecks,
		CustomCommands: customCommands,
	}
}

// CheckType 检查类型
type CheckType string

const (
	CheckTypeSSH     CheckType = "ssh"
	CheckTypeAPI     CheckType = "api"
	CheckTypeService CheckType = "service"
	CheckTypeCustom  CheckType = "custom"
)

// CheckResult 单个检查结果
type CheckResult struct {
	Type     CheckType              `json:"type"`
	Success  bool                   `json:"success"`
	Duration time.Duration          `json:"duration"`
	Error    string                 `json:"error,omitempty"`
	Details  map[string]interface{} `json:"details,omitempty"`
}
