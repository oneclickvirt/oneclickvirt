package provider

import (
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

type Provider struct {
	// 基础字段
	ID        uint      `json:"id" gorm:"primarykey"`                     // 主键ID
	UUID      string    `json:"uuid" gorm:"uniqueIndex;not null;size:36"` // 唯一标识符
	CreatedAt time.Time `json:"createdAt"`                                // 创建时间
	UpdatedAt time.Time `json:"updatedAt"`                                // 更新时间

	// 基本信息
	Name     string `json:"name" gorm:"uniqueIndex;not null;size:64"` // Provider名称（唯一）
	Type     string `json:"type" gorm:"not null;size:32"`             // Provider类型：docker, lxd, incus, proxmox
	Endpoint string `json:"endpoint" gorm:"size:255"`                 // SSH连接端点地址
	PortIP   string `json:"portIP" gorm:"size:255"`                   // 端口映射使用的公网IP（非必填，若为空则使用Endpoint）
	SSHPort  int    `json:"sshPort" gorm:"default:22"`                // SSH连接端口
	Username string `json:"username" gorm:"size:128"`                 // SSH连接用户名
	Password string `json:"-" gorm:"size:255"`                        // SSH连接密码（不返回给前端）
	SSHKey   string `json:"-" gorm:"type:text"`                       // SSH私钥（不返回给前端，优先于密码使用）
	Token    string `json:"-" gorm:"size:255"`                        // API访问令牌（不返回给前端）
	Config   string `json:"config" gorm:"type:text"`                  // 额外配置信息（JSON格式）

	// 状态和地理信息
	Status      string `json:"status" gorm:"default:active;size:16"` // Provider状态：active, inactive
	Region      string `json:"region" gorm:"size:64"`                // 地区
	Country     string `json:"country" gorm:"size:64"`               // 国家
	CountryCode string `json:"countryCode" gorm:"size:8"`            // 国家代码
	City        string `json:"city" gorm:"size:64"`                  // 城市（可选）

	// 功能支持
	ContainerEnabled      bool   `json:"container_enabled" gorm:"default:true"` // 是否支持容器实例
	VirtualMachineEnabled bool   `json:"vm_enabled" gorm:"default:false"`       // 是否支持虚拟机实例
	SupportedTypes        string `json:"supported_types" gorm:"size:128"`       // 支持的实例类型列表
	AllowClaim            bool   `json:"allowClaim" gorm:"default:true"`        // 是否允许用户使用此Provider

	// 端口映射配置
	IPv4PortMappingMethod string `json:"ipv4PortMappingMethod" gorm:"size:16;default:device_proxy"` // IPv4端口映射方式：device_proxy, iptables, native
	IPv6PortMappingMethod string `json:"ipv6PortMappingMethod" gorm:"size:16;default:device_proxy"` // IPv6端口映射方式：device_proxy, iptables, native

	// 配额管理
	UsedQuota    int        `json:"usedQuota" gorm:"default:0"`                // 已使用配额（传统字段，兼容性保留）
	TotalQuota   int        `json:"totalQuota" gorm:"default:0"`               // 总配额（传统字段，兼容性保留）
	Architecture string     `json:"architecture" gorm:"size:16;default:amd64"` // CPU架构：amd64, arm64, s390x等
	ExpiresAt    *time.Time `json:"expiresAt" gorm:"index;column:expires_at"`  // Provider过期时间
	IsFrozen     bool       `json:"isFrozen" gorm:"default:false"`             // 是否被冻结（冻结后无法使用）

	// 存储配置（ProxmoxVE专用）
	StoragePool string `json:"storagePool" gorm:"size:64;default:local"` // 存储池名称，用于存储虚拟机磁盘和容器

	// 证书相关字段（用于TLS连接）
	CertPath        string     `json:"certPath" gorm:"size:512"`                 // 客户端证书文件路径
	KeyPath         string     `json:"keyPath" gorm:"size:512"`                  // 客户端私钥文件路径
	CACertPath      string     `json:"caCertPath" gorm:"size:512"`               // CA证书文件路径
	CertFingerprint string     `json:"certFingerprint" gorm:"size:128"`          // 证书指纹
	APIStatus       string     `json:"apiStatus" gorm:"default:unknown;size:16"` // API连接状态：online, offline, unknown
	SSHStatus       string     `json:"sshStatus" gorm:"default:unknown;size:16"` // SSH连接状态：online, offline, unknown
	LastAPICheck    *time.Time `json:"lastApiCheck"`                             // 最后一次API健康检查时间
	LastSSHCheck    *time.Time `json:"lastSshCheck"`                             // 最后一次SSH健康检查时间

	// 配置管理字段
	AuthConfig       string     `json:"-" gorm:"type:text"`                  // 完整认证配置JSON（不返回给前端）
	ConfigVersion    int        `json:"configVersion" gorm:"default:0"`      // 配置版本号
	AutoConfigured   bool       `json:"autoConfigured" gorm:"default:false"` // 是否已经自动配置完成
	LastConfigUpdate *time.Time `json:"lastConfigUpdate"`                    // 最后一次配置更新时间
	ConfigBackupPath string     `json:"configBackupPath" gorm:"size:512"`    // 配置备份文件路径
	CertContent      string     `json:"-" gorm:"type:text"`                  // 证书内容（不返回给前端）
	KeyContent       string     `json:"-" gorm:"type:text"`                  // 私钥内容（不返回给前端）
	TokenContent     string     `json:"-" gorm:"type:text"`                  // Token内容JSON格式（不返回给前端）

	// 节点硬件资源信息（通过SSH查询获得）
	NodeCPUCores    int   `json:"nodeCpuCores" gorm:"default:0"`    // 节点总CPU核心数
	NodeMemoryTotal int64 `json:"nodeMemoryTotal" gorm:"default:0"` // 节点总内存大小（MB）
	NodeDiskTotal   int64 `json:"nodeDiskTotal" gorm:"default:0"`   // 节点总磁盘空间（MB）

	// 并发控制配置
	AllowConcurrentTasks bool `json:"allowConcurrentTasks" gorm:"default:false"` // 是否允许并发执行任务
	MaxConcurrentTasks   int  `json:"maxConcurrentTasks" gorm:"default:1"`       // 最大并发任务数量

	// SSH连接配置
	SSHConnectTimeout int `json:"sshConnectTimeout" gorm:"default:30"`  // SSH连接超时时间（秒），默认30秒
	SSHExecuteTimeout int `json:"sshExecuteTimeout" gorm:"default:300"` // SSH命令执行超时时间（秒），默认300秒

	// 任务调度配置
	TaskPollInterval  int  `json:"taskPollInterval" gorm:"default:60"`    // 任务轮询间隔（秒）
	EnableTaskPolling bool `json:"enableTaskPolling" gorm:"default:true"` // 是否启用任务轮询机制

	// 操作执行配置
	ExecutionRule string `json:"executionRule" gorm:"default:auto;size:16"` // 操作轮转规则：auto(自动切换), api_only(仅API), ssh_only(仅SSH)

	// 实例数量限制配置
	MaxContainerInstances int `json:"maxContainerInstances" gorm:"default:0"` // 最大容器实例数量（0表示无限制）
	MaxVMInstances        int `json:"maxVMInstances" gorm:"default:0"`        // 最大虚拟机实例数量（0表示无限制）

	// 容器资源配额管理配置（Provider层面）
	// 这些配置决定该资源是否计入Provider总量预算，不影响实例创建时的资源参数设置
	// false=允许超分配（不计入总量），true=严格限制（计入总量）
	ContainerLimitCPU    bool `json:"containerLimitCpu" gorm:"default:false"`    // 容器CPU是否计入Provider总量预算，默认false（允许超分配）
	ContainerLimitMemory bool `json:"containerLimitMemory" gorm:"default:false"` // 容器内存是否计入Provider总量预算，默认false（允许超分配）
	ContainerLimitDisk   bool `json:"containerLimitDisk" gorm:"default:true"`    // 容器硬盘是否计入Provider总量预算，默认true（严格限制）

	// 虚拟机资源配额管理配置（Provider层面）
	// 这些配置决定该资源是否计入Provider总量预算，不影响实例创建时的资源参数设置
	// false=允许超分配（不计入总量），true=严格限制（计入总量）
	VMLimitCPU    bool `json:"vmLimitCpu" gorm:"default:true"`    // 虚拟机CPU是否计入Provider总量预算，默认true（严格限制）
	VMLimitMemory bool `json:"vmLimitMemory" gorm:"default:true"` // 虚拟机内存是否计入Provider总量预算，默认true（严格限制）
	VMLimitDisk   bool `json:"vmLimitDisk" gorm:"default:true"`   // 虚拟机硬盘是否计入Provider总量预算，默认true（严格限制）

	// 端口映射配置
	DefaultPortCount  int    `json:"defaultPortCount" gorm:"default:10"`                   // 每个实例默认映射端口数量
	PortRangeStart    int    `json:"portRangeStart" gorm:"default:10000"`                  // 端口映射范围起始
	PortRangeEnd      int    `json:"portRangeEnd" gorm:"default:65535"`                    // 端口映射范围结束
	NextAvailablePort int    `json:"nextAvailablePort" gorm:"default:10000"`               // 下一个可用端口
	NetworkType       string `json:"networkType" gorm:"default:nat_ipv4;size:32;not null"` // 网络配置类型：nat_ipv4, nat_ipv4_ipv6, dedicated_ipv4, dedicated_ipv4_ipv6, ipv6_only

	// 带宽配置（Mbps为单位）
	DefaultInboundBandwidth  int `json:"defaultInboundBandwidth" gorm:"default:300"`  // 默认入站带宽限制（Mbps）
	DefaultOutboundBandwidth int `json:"defaultOutboundBandwidth" gorm:"default:300"` // 默认出站带宽限制（Mbps）
	MaxInboundBandwidth      int `json:"maxInboundBandwidth" gorm:"default:1000"`     // 最大入站带宽限制（Mbps）
	MaxOutboundBandwidth     int `json:"maxOutboundBandwidth" gorm:"default:1000"`    // 最大出站带宽限制（Mbps）

	// 流量管理（MB为单位）
	EnableTrafficControl bool       `json:"enableTrafficControl" gorm:"default:true"`     // 是否启用流量统计和限制，默认启用
	MaxTraffic           int64      `json:"maxTraffic" gorm:"default:1048576"`            // 最大流量限制（默认1TB=1048576MB）
	UsedTraffic          int64      `json:"usedTraffic" gorm:"default:0"`                 // 当月已使用流量（MB）
	TrafficLimited       bool       `json:"trafficLimited" gorm:"default:false"`          // 是否因流量超限被限制
	TrafficResetAt       *time.Time `json:"trafficResetAt"`                               // 流量重置时间
	TrafficCountMode     string     `json:"trafficCountMode" gorm:"default:both;size:16"` // 流量统计模式：both(双向), out(仅出向), in(仅入向)
	TrafficMultiplier    float64    `json:"trafficMultiplier" gorm:"default:1.0"`         // 流量计费倍率（例如：入向0.5倍，出向1倍）

	// 资源占用统计（基于实际创建的实例计算）
	UsedCPUCores     int        `json:"usedCpuCores" gorm:"default:0"`       // 已占用的CPU核心数
	UsedMemory       int64      `json:"usedMemory" gorm:"default:0"`         // 已占用的内存大小（MB）
	UsedDisk         int64      `json:"usedDisk" gorm:"default:0"`           // 已占用的磁盘空间（MB）
	ContainerCount   int        `json:"containerCount" gorm:"default:0"`     // 当前运行的容器实例数量
	VMCount          int        `json:"vmCount" gorm:"default:0"`            // 当前运行的虚拟机实例数量
	ResourceSynced   bool       `json:"resourceSynced" gorm:"default:false"` // 资源信息是否已同步
	ResourceSyncedAt *time.Time `json:"resourceSyncedAt"`                    // 资源信息最后同步时间

	// 可用资源统计（动态计算得出）
	AvailableCPUCores int   `json:"availableCpuCores" gorm:"default:0"` // 可用的CPU核心数（NodeCPUCores - UsedCPUCores）
	AvailableMemory   int64 `json:"availableMemory" gorm:"default:0"`   // 可用的内存大小（NodeMemoryTotal - UsedMemory）
	UsedInstances     int   `json:"usedInstances" gorm:"default:0"`     // 已使用的实例总数（ContainerCount + VMCount）

	// 节点级别的等级限制配置（JSON格式存储）
	// 用于限制该节点上不同等级用户能创建的最大资源，与全局等级配置类似但仅对当前节点生效
	// 该字段会与用户全局等级限制进行比较，取两者的最小值作为实际限制
	LevelLimits string `json:"levelLimits" gorm:"type:text"` // JSON格式: map[int]config.LevelLimitInfo
}

func (p *Provider) BeforeCreate(tx *gorm.DB) error {
	p.UUID = uuid.New().String()
	return nil
}

// GetAuthMethod 返回当前使用的认证方式
// 返回 "password" 或 "sshKey"
func (p *Provider) GetAuthMethod() string {
	// SSH密钥优先
	if p.SSHKey != "" {
		return "sshKey"
	}
	if p.Password != "" {
		return "password"
	}
	// 默认返回password（理论上不应该出现两者都为空的情况）
	return "password"
}

// Instance 实例模型
type Instance struct {
	// 基础字段
	ID        uint           `json:"id" gorm:"primarykey"`                     // 实例主键ID
	UUID      string         `json:"uuid" gorm:"uniqueIndex;not null;size:36"` // 实例唯一标识符
	CreatedAt time.Time      `json:"createdAt"`                                // 实例创建时间
	UpdatedAt time.Time      `json:"updatedAt"`                                // 实例信息更新时间
	DeletedAt gorm.DeletedAt `json:"-" gorm:"index"`                           // 软删除时间

	// 基本信息
	Name         string `json:"name" gorm:"uniqueIndex;not null;size:128"`      // 实例名称（唯一）
	Provider     string `json:"provider" gorm:"not null;size:32"`               // Provider名称
	ProviderID   uint   `json:"providerId" gorm:"not null"`                     // 关联的Provider ID
	Status       string `json:"status" gorm:"size:32"`                          // 实例状态：creating, running, stopped, failed等
	Image        string `json:"image" gorm:"size:128"`                          // 使用的镜像名称
	InstanceType string `json:"instance_type" gorm:"size:16;default:container"` // 实例类型：container, vm

	// 资源配置
	CPU       int   `json:"cpu" gorm:"default:1"`        // CPU核心数
	Memory    int64 `json:"memory" gorm:"default:512"`   // 内存大小（MB）
	Disk      int64 `json:"disk" gorm:"default:10240"`   // 磁盘大小（MB）
	Bandwidth int   `json:"bandwidth" gorm:"default:10"` // 网络带宽（Mbps）

	// 网络配置
	Network        string `json:"network" gorm:"size:64"`      // 网络名称或配置
	PrivateIP      string `json:"privateIP" gorm:"size:64"`    // 内网/私有IPv4地址
	PublicIP       string `json:"publicIP" gorm:"size:64"`     // 公网IPv4地址
	IPv6Address    string `json:"ipv6Address" gorm:"size:128"` // 内网IPv6地址
	PublicIPv6     string `json:"publicIPv6" gorm:"size:128"`  // 公网IPv6地址
	SSHPort        int    `json:"sshPort" gorm:"default:22"`   // SSH访问端口
	PortRangeStart int    `json:"portRangeStart"`              // 端口映射范围起始
	PortRangeEnd   int    `json:"portRangeEnd"`                // 端口映射范围结束

	// 访问凭据
	Username string `json:"username" gorm:"size:64"`  // 登录用户名
	Password string `json:"password" gorm:"size:128"` // 登录密码

	// 系统信息
	OSType string `json:"osType" gorm:"size:64"` // 操作系统类型：ubuntu, centos, debian等
	Region string `json:"region" gorm:"size:64"` // 所在地区

	// 流量统计（MB为单位）
	UsedTrafficIn      int64  `json:"usedTrafficIn" gorm:"default:0"`               // 入站流量（MB）
	UsedTrafficOut     int64  `json:"usedTrafficOut" gorm:"default:0"`              // 出站流量（MB）
	UsedTraffic        int64  `json:"usedTraffic" gorm:"default:0"`                 // 实例当月总流量（MB）= UsedTrafficIn + UsedTrafficOut
	MaxTraffic         int64  `json:"maxTraffic" gorm:"default:0"`                  // 实例流量限制（MB），0表示不限制，从用户等级继承
	TrafficLimited     bool   `json:"trafficLimited" gorm:"default:false"`          // 是否因流量超限被停机
	TrafficLimitReason string `json:"trafficLimitReason" gorm:"size:16;default:''"` // 流量限制原因：instance(实例超限), user(用户超限), provider(Provider超限)
	VnstatInterface    string `json:"vnstatInterface" gorm:"size:32"`               // vnstat监控的网络接口名称

	// 生命周期
	ExpiredAt time.Time `json:"expiredAt" gorm:"column:expired_at"` // 实例到期时间

	// 关联关系
	UserID uint `json:"userId"` // 所属用户ID

	// 其他标志
	SoftDeleted bool `json:"softDeleted" gorm:"default:false"` // 是否软删除
}

func (i *Instance) BeforeCreate(tx *gorm.DB) error {
	i.UUID = uuid.New().String()
	return nil
}

// Port 端口映射模型
type Port struct {
	// 基础字段
	ID        uint           `json:"id" gorm:"primarykey"` // 端口映射主键ID
	CreatedAt time.Time      `json:"createdAt"`            // 创建时间
	UpdatedAt time.Time      `json:"updatedAt"`            // 更新时间
	DeletedAt gorm.DeletedAt `json:"-" gorm:"index"`       // 软删除时间

	// 端口映射信息
	InstanceID  uint   `json:"instanceId"`                                   // 关联的实例ID
	ProviderID  uint   `json:"providerId"`                                   // 关联的Provider ID
	HostPort    int    `json:"hostPort" gorm:"not null"`                     // 宿主机端口
	GuestPort   int    `json:"guestPort" gorm:"not null"`                    // 容器/虚拟机内部端口
	Protocol    string `json:"protocol" gorm:"default:both;size:8"`          // 协议类型：tcp, udp, both
	Status      string `json:"status" gorm:"default:active;size:16"`         // 映射状态：active, inactive
	Description string `json:"description" gorm:"size:128"`                  // 端口用途描述
	IsSSH       bool   `json:"isSsh" gorm:"default:false"`                   // 是否为SSH端口
	IsAutomatic bool   `json:"isAutomatic" gorm:"default:true"`              // 是否为自动分配的端口
	PortType    string `json:"portType" gorm:"default:range_mapped;size:16"` // 端口类型：range_mapped(区间映射), manual(手动添加)

	// IPv6支持
	IPv6Enabled   bool   `json:"ipv6Enabled" gorm:"default:false"`            // 是否启用IPv6映射
	IPv6Address   string `json:"ipv6Address" gorm:"size:64"`                  // IPv6映射地址
	MappingMethod string `json:"mappingMethod" gorm:"size:32;default:native"` // 映射方法：native, iptables, gost, firewall
}

// PendingDeletion 待删除资源模型
type PendingDeletion struct {
	ID           uint      `json:"id" gorm:"primarykey"`
	CreatedAt    time.Time `json:"createdAt"`
	ResourceType string    `json:"resourceType" gorm:"not null;size:32"`
	ResourceID   uint      `json:"resourceId" gorm:"not null"`
	ResourceUUID string    `json:"resourceUuid" gorm:"not null;size:36"`
	ScheduledAt  time.Time `json:"scheduledAt"`
	Status       string    `json:"status" gorm:"default:pending;size:16"`
}

// 以下是业务层结构体（不是数据库模型）

// ProviderInstance 实例信息
type ProviderInstance struct {
	ID          string            `json:"id"`
	Name        string            `json:"name"`
	Status      string            `json:"status"`
	Type        string            `json:"type"`
	Image       string            `json:"image"`
	IP          string            `json:"ip"`          // 内网IP地址（向后兼容）
	PrivateIP   string            `json:"privateIP"`   // 内网/私有IP地址
	PublicIP    string            `json:"publicIP"`    // 公网IP地址
	IPv6Address string            `json:"ipv6Address"` // IPv6地址
	CPU         string            `json:"cpu"`
	Memory      string            `json:"memory"`
	Disk        string            `json:"disk"`
	Created     time.Time         `json:"created"`
	Metadata    map[string]string `json:"metadata"`
}

// ProviderImage 镜像信息
type ProviderImage struct {
	ID          string            `json:"id"`
	Name        string            `json:"name"`
	Tag         string            `json:"tag"`
	Size        string            `json:"size"`
	Created     time.Time         `json:"created"`
	Description string            `json:"description"`
	Metadata    map[string]string `json:"metadata"`
}

// ProviderInstanceConfig 实例配置
type ProviderInstanceConfig struct {
	Name         string            `json:"name"`
	Image        string            `json:"image"`
	ImageURL     string            `json:"image_url"`  // 镜像下载URL
	ImagePath    string            `json:"image_path"` // 镜像文件路径
	UseCDN       bool              `json:"use_cdn"`    // 是否使用CDN加速下载镜像
	CPU          string            `json:"cpu"`
	Memory       string            `json:"memory"`
	Disk         string            `json:"disk"`
	Network      string            `json:"network"`
	Ports        []string          `json:"ports"`
	Env          map[string]string `json:"env"`
	Metadata     map[string]string `json:"metadata"`
	InstanceType string            `json:"instance_type"` // container 或 vm
}

// ProviderNodeConfig 节点配置
type ProviderNodeConfig struct {
	UUID                  string   `json:"uuid"`
	Name                  string   `json:"name"`
	Host                  string   `json:"host"`
	Port                  int      `json:"port"`
	Username              string   `json:"username"`
	Password              string   `json:"password"`
	PrivateKey            string   `json:"private_key"` // SSH私钥内容，优先于密码使用
	Token                 string   `json:"token"`       // API Token Secret，用于ProxmoxVE等
	TokenID               string   `json:"token_id"`    // API Token ID，用于ProxmoxVE等 (USER@REALM!TOKENID)
	CertPath              string   `json:"cert_path"`
	KeyPath               string   `json:"key_path"`
	Country               string   `json:"country"`             // Provider所在国家，用于CDN选择
	City                  string   `json:"city"`                // Provider所在城市（可选）
	Architecture          string   `json:"architecture"`        // 架构类型，如amd64, arm64等
	Type                  string   `json:"type"`                // docker, lxd, incus, proxmox
	SupportedTypes        []string `json:"supported_types"`     // 支持的实例类型: container, vm, both
	ContainerEnabled      bool     `json:"container_enabled"`   // 是否支持容器
	VirtualMachineEnabled bool     `json:"vm_enabled"`          // 是否支持虚拟机
	SSHConnectTimeout     int      `json:"ssh_connect_timeout"` // SSH连接超时时间（秒）
	SSHExecuteTimeout     int      `json:"ssh_execute_timeout"` // SSH命令执行超时时间（秒）
	ExecutionRule         string   `json:"execution_rule"`      // 操作轮转规则：auto, api_only, ssh_only
	NetworkType           string   `json:"networkType"`         // 网络配置类型：nat_ipv4, nat_ipv4_ipv6, dedicated_ipv4, dedicated_ipv4_ipv6, ipv6_only

	// 容器资源限制配置（Provider层面）
	ContainerLimitCPU    bool `json:"containerLimitCpu"`    // 容器是否限制CPU数量，默认不限制
	ContainerLimitMemory bool `json:"containerLimitMemory"` // 容器是否限制内存大小，默认不限制
	ContainerLimitDisk   bool `json:"containerLimitDisk"`   // 容器是否限制硬盘大小，默认限制

	// 虚拟机资源限制配置（Provider层面）
	VMLimitCPU    bool `json:"vmLimitCpu"`    // 虚拟机是否限制CPU数量，默认限制
	VMLimitMemory bool `json:"vmLimitMemory"` // 虚拟机是否限制内存大小，默认限制
	VMLimitDisk   bool `json:"vmLimitDisk"`   // 虚拟机是否限制硬盘大小，默认限制
}

// ProviderResponse 用于返回给前端的Provider响应结构
// 包含认证方式标识，但不包含敏感的密码和SSH密钥内容
type ProviderResponse struct {
	Provider
	AuthMethod string `json:"authMethod"` // 当前使用的认证方式: "password" 或 "sshKey"
}

// ToResponse 将Provider转换为ProviderResponse
func (p *Provider) ToResponse() ProviderResponse {
	return ProviderResponse{
		Provider:   *p,
		AuthMethod: p.GetAuthMethod(),
	}
}
