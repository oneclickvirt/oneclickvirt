package provider

import (
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

// TrafficStatsMode 流量统计性能模式
const (
	TrafficStatsModeHigh     = "high"     // 高性能模式（8核+独立服务器）
	TrafficStatsModeStandard = "standard" // 标准模式（4-8核独立服务器）
	TrafficStatsModeLight    = "light"    // 轻量模式（2-4核独立服务器，默认）
	TrafficStatsModeMinimal  = "minimal"  // 最小模式（共享VPS/无独享内核）
	TrafficStatsModeCustom   = "custom"   // 自定义模式
)

// TrafficStatsPreset 流量统计预设配置
type TrafficStatsPreset struct {
	SQLiteCollectInterval int // SQLite采集间隔（秒），采集后自动同步统计
	CollectBatchSize      int // 采集批量大小（每次处理的实例数）
	LimitCheckInterval    int // 流量限制检测间隔（秒）
	LimitCheckBatchSize   int // 流量限制检测批量大小
	AutoResetInterval     int // 自动重置检查间隔（秒）
	AutoResetBatchSize    int // 自动重置批量大小
}

// GetTrafficStatsPreset 根据模式获取预设配置
func GetTrafficStatsPreset(mode string) TrafficStatsPreset {
	switch mode {
	case TrafficStatsModeHigh:
		// 高性能模式（8核+）- CPU占用10-15%, 响应快
		return TrafficStatsPreset{
			SQLiteCollectInterval: 30,  // 0.5分钟采集+统计
			CollectBatchSize:      20,  // 批量20个
			LimitCheckInterval:    30,  // 30秒检测
			LimitCheckBatchSize:   20,  // 批量20个
			AutoResetInterval:     600, // 10分钟检查
			AutoResetBatchSize:    20,  // 批量20个
		}
	case TrafficStatsModeStandard:
		// 标准模式（4-8核）- CPU占用5-10%, 响应正常
		return TrafficStatsPreset{
			SQLiteCollectInterval: 60,  // 1分钟采集+统计
			CollectBatchSize:      15,  // 批量15个
			LimitCheckInterval:    60,  // 1分钟检测
			LimitCheckBatchSize:   15,  // 批量15个
			AutoResetInterval:     900, // 15分钟检查
			AutoResetBatchSize:    15,  // 批量15个
		}
	case TrafficStatsModeLight:
		// 轻量模式（2-4核，默认）- CPU占用2-5%, 资源友好
		return TrafficStatsPreset{
			SQLiteCollectInterval: 90,   // 1.5分钟采集+统计
			CollectBatchSize:      10,   // 批量10个
			LimitCheckInterval:    90,   // 1.5分钟检测
			LimitCheckBatchSize:   10,   // 批量10个
			AutoResetInterval:     1800, // 30分钟检查
			AutoResetBatchSize:    10,   // 批量10个
		}
	case TrafficStatsModeMinimal:
		// 最小模式（共享VPS）- CPU占用0.5-2%, 极低负载
		return TrafficStatsPreset{
			SQLiteCollectInterval: 120,  // 2分钟采集+统计
			CollectBatchSize:      5,    // 批量5个
			LimitCheckInterval:    120,  // 2分钟检测
			LimitCheckBatchSize:   5,    // 批量5个
			AutoResetInterval:     3600, // 60分钟检查
			AutoResetBatchSize:    5,    // 批量5个
		}
	default:
		// 返回轻量模式作为默认
		return GetTrafficStatsPreset(TrafficStatsModeLight)
	}
}

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

	// 存储配置（所有Provider类型通用）
	StoragePool     string `json:"storagePool" gorm:"size:64;default:local"`   // 存储池名称，用于存储虚拟机磁盘和容器
	StoragePoolPath string `json:"storagePoolPath" gorm:"size:255;default:''"` // 存储池实际挂载路径，用于准确获取硬盘大小

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
	EnableTrafficControl bool       `json:"enableTrafficControl" gorm:"default:false"`    // 是否启用流量统计和限制，默认不启用
	MaxTraffic           int64      `json:"maxTraffic" gorm:"default:1048576"`            // 最大流量限制（默认1TB=1048576MB）
	TrafficLimited       bool       `json:"trafficLimited" gorm:"default:false"`          // 是否因流量超限被限制
	TrafficResetAt       *time.Time `json:"trafficResetAt"`                               // 流量重置时间
	TrafficCountMode     string     `json:"trafficCountMode" gorm:"default:both;size:16"` // 流量统计模式：both(双向), out(仅出向), in(仅入向)
	TrafficMultiplier    float64    `json:"trafficMultiplier" gorm:"default:1.0"`         // 流量计费倍率（例如：入向0.5倍，出向1倍）

	// 流量统计性能配置
	TrafficStatsMode           string `json:"trafficStatsMode" gorm:"default:light;size:16"`                               // 流量统计性能模式：high(高性能), standard(标准), light(轻量), minimal(最小), custom(自定义)
	TrafficCollectInterval     int    `json:"trafficCollectInterval" gorm:"column:traffic_collect_interval;default:300"`   // 流量采集间隔（秒），采集后自动统计，默认300秒（5分钟）
	TrafficCollectBatchSize    int    `json:"trafficCollectBatchSize" gorm:"column:traffic_collect_batch_size;default:10"` // 流量采集批量大小，默认10个
	TrafficLimitCheckInterval  int    `json:"trafficLimitCheckInterval" gorm:"default:600"`                                // 流量限制检测间隔（秒），默认600秒（10分钟）
	TrafficLimitCheckBatchSize int    `json:"trafficLimitCheckBatchSize" gorm:"default:10"`                                // 流量限制检测批量大小，默认10个
	TrafficAutoResetInterval   int    `json:"trafficAutoResetInterval" gorm:"default:1800"`                                // 流量自动重置检查间隔（秒），默认1800秒（30分钟）
	TrafficAutoResetBatchSize  int    `json:"trafficAutoResetBatchSize" gorm:"default:10"`                                 // 流量自动重置批量大小，默认10个

	// 资源占用统计（基于实际创建的实例计算）
	UsedCPUCores     int        `json:"usedCpuCores" gorm:"default:0"`       // 已占用的CPU核心数
	UsedMemory       int64      `json:"usedMemory" gorm:"default:0"`         // 已占用的内存大小（MB）
	UsedDisk         int64      `json:"usedDisk" gorm:"default:0"`           // 已占用的磁盘空间（MB）
	ContainerCount   int        `json:"containerCount" gorm:"default:0"`     // 当前运行的容器实例数量（缓存值，定期更新）
	VMCount          int        `json:"vmCount" gorm:"default:0"`            // 当前运行的虚拟机实例数量（缓存值，定期更新）
	ResourceSynced   bool       `json:"resourceSynced" gorm:"default:false"` // 资源信息是否已同步
	ResourceSyncedAt *time.Time `json:"resourceSyncedAt"`                    // 资源信息最后同步时间
	CountCacheExpiry *time.Time `json:"countCacheExpiry"`                    // 数量缓存过期时间（避免频繁查询数据库）

	// 可用资源统计（动态计算得出）
	AvailableCPUCores int   `json:"availableCpuCores" gorm:"default:0"` // 可用的CPU核心数（NodeCPUCores - UsedCPUCores）
	AvailableMemory   int64 `json:"availableMemory" gorm:"default:0"`   // 可用的内存大小（NodeMemoryTotal - UsedMemory）
	UsedInstances     int   `json:"usedInstances" gorm:"default:0"`     // 已使用的实例总数（ContainerCount + VMCount）

	// 节点级别的等级限制配置（JSON格式存储）
	// 用于限制该节点上不同等级用户能创建的最大资源，与全局等级配置类似但仅对当前节点生效
	// 该字段会与用户全局等级限制进行比较，取两者的最小值作为实际限制
	LevelLimits string `json:"levelLimits" gorm:"type:text"` // JSON格式: map[int]config.LevelLimitInfo

	// 节点标识信息（用于区分多个hostname相同的节点）
	HostName string `json:"hostName" gorm:"size:128"` // 节点主机名（hostname），由健康检查自动更新

	// 容器特殊配置选项（仅适用于 LXD 和 Incus 的容器实例）
	ContainerPrivileged   bool   `json:"containerPrivileged" gorm:"default:false"`          // 容器特权模式：允许容器访问宿主机资源
	ContainerAllowNesting bool   `json:"containerAllowNesting" gorm:"default:false"`        // 容器嵌套：允许在容器内运行容器
	ContainerEnableLXCFS  bool   `json:"containerEnableLxcfs" gorm:"default:true"`          // LXCFS资源视图：显示真实资源限制
	ContainerCPUAllowance string `json:"containerCpuAllowance" gorm:"default:100%;size:16"` // CPU限制：例如 "100%" 或 "50%"
	ContainerMemorySwap   bool   `json:"containerMemorySwap" gorm:"default:true"`           // 内存交换：允许使用swap空间
	ContainerMaxProcesses int    `json:"containerMaxProcesses" gorm:"default:0"`            // 最大进程数：0表示不限制
	ContainerDiskIOLimit  string `json:"containerDiskIoLimit" gorm:"size:32"`               // 磁盘IO限制：例如 "10MB" 或 "100iops"
}

func (p *Provider) BeforeCreate(tx *gorm.DB) error {
	p.UUID = uuid.New().String()

	// 如果没有设置流量统计模式，使用默认轻量模式
	if p.TrafficStatsMode == "" {
		p.TrafficStatsMode = TrafficStatsModeLight
	}

	// 应用预设配置（如果不是自定义模式且配置值为0）
	if p.TrafficStatsMode != TrafficStatsModeCustom {
		p.ApplyTrafficStatsPreset()
	}

	return nil
}

// ApplyTrafficStatsPreset 应用流量统计预设配置
// 强制应用所有预设值（不保留旧值）
func (p *Provider) ApplyTrafficStatsPreset() {
	preset := GetTrafficStatsPreset(p.TrafficStatsMode)

	// 强制应用预设配置的所有值
	p.TrafficCollectInterval = preset.SQLiteCollectInterval
	p.TrafficCollectBatchSize = preset.CollectBatchSize
	p.TrafficLimitCheckInterval = preset.LimitCheckInterval
	p.TrafficLimitCheckBatchSize = preset.LimitCheckBatchSize
	p.TrafficAutoResetInterval = preset.AutoResetInterval
	p.TrafficAutoResetBatchSize = preset.AutoResetBatchSize
}

// GetTrafficStatsConfig 获取流量统计配置
func (p *Provider) GetTrafficStatsConfig() TrafficStatsPreset {
	return TrafficStatsPreset{
		SQLiteCollectInterval: p.TrafficCollectInterval,
		CollectBatchSize:      p.TrafficCollectBatchSize,
		LimitCheckInterval:    p.TrafficLimitCheckInterval,
		LimitCheckBatchSize:   p.TrafficLimitCheckBatchSize,
		AutoResetInterval:     p.TrafficAutoResetInterval,
		AutoResetBatchSize:    p.TrafficAutoResetBatchSize,
	}
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
	Name         string `json:"name" gorm:"uniqueIndex:idx_instance_name_provider;not null;size:128"`                                   // 实例名称（与provider_id组合唯一）
	Provider     string `json:"provider" gorm:"not null;size:32"`                                                                       // Provider名称
	ProviderID   uint   `json:"providerId" gorm:"uniqueIndex:idx_instance_name_provider;index:idx_provider_status,priority:1;not null"` // 关联的Provider ID（与name组合唯一）
	Status       string `json:"status" gorm:"size:32;index:idx_provider_status,priority:2"`                                             // 实例状态：creating, running, stopped, failed等
	Image        string `json:"image" gorm:"size:128"`                                                                                  // 使用的镜像名称
	InstanceType string `json:"instance_type" gorm:"size:16;default:container"`                                                         // 实例类型：container, vm

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

	// 流量统计（实例层面）
	MaxTraffic         int64  `json:"maxTraffic" gorm:"default:0"`                  // 实例流量限制（MB），0表示不限制，从用户等级继承
	TrafficLimited     bool   `json:"trafficLimited" gorm:"default:false"`          // 是否因流量超限被停机
	TrafficLimitReason string `json:"trafficLimitReason" gorm:"size:16;default:''"` // 流量限制原因：instance(实例超限), user(用户超限), provider(Provider超限)
	PmacctInterfaceV4  string `json:"pmacctInterfaceV4" gorm:"size:32"`             // pmacct 监控的IPv4网络接口名称
	PmacctInterfaceV6  string `json:"pmacctInterfaceV6" gorm:"size:32"`             // pmacct 监控的IPv6网络接口名称

	// 生命周期
	ExpiredAt time.Time `json:"expiredAt" gorm:"column:expired_at"` // 实例到期时间

	// 关联关系
	UserID uint `json:"userId" gorm:"index:idx_user_status,priority:1"` // 所属用户ID
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
	InstanceID   uint   `json:"instanceId"`                                   // 关联的实例ID
	ProviderID   uint   `json:"providerId"`                                   // 关联的Provider ID
	HostPort     int    `json:"hostPort" gorm:"not null"`                     // 宿主机端口（起始端口）
	HostPortEnd  int    `json:"hostPortEnd" gorm:"default:0"`                 // 宿主机端口结束（0表示单端口）
	GuestPort    int    `json:"guestPort" gorm:"not null"`                    // 容器/虚拟机内部端口（起始端口）
	GuestPortEnd int    `json:"guestPortEnd" gorm:"default:0"`                // 容器/虚拟机内部端口结束（0表示单端口）
	PortCount    int    `json:"portCount" gorm:"default:1"`                   // 端口数量（端口段包含的端口个数）
	Protocol     string `json:"protocol" gorm:"default:both;size:8"`          // 协议类型：tcp, udp, both
	Status       string `json:"status" gorm:"default:active;size:16"`         // 映射状态：active, inactive
	Description  string `json:"description" gorm:"size:256"`                  // 端口用途描述（支持更长描述）
	IsSSH        bool   `json:"isSsh" gorm:"default:false"`                   // 是否为SSH端口
	IsAutomatic  bool   `json:"isAutomatic" gorm:"default:true"`              // 是否为自动分配的端口
	PortType     string `json:"portType" gorm:"default:range_mapped;size:16"` // 端口类型：range_mapped(区间映射), manual(手动添加), batch(批量添加)

	// IPv6支持
	IPv6Enabled   bool   `json:"ipv6Enabled" gorm:"default:false"`            // 是否启用IPv6映射
	IPv6Address   string `json:"ipv6Address" gorm:"size:64"`                  // IPv6映射地址
	MappingMethod string `json:"mappingMethod" gorm:"size:32;default:native"` // 映射方法：native, iptables, firewall
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

	// 容器特殊配置选项（仅适用于 LXD 和 Incus 的容器实例）
	Privileged   *bool   `json:"privileged,omitempty"`   // 容器特权模式，使用指针以区分 false 和未设置
	AllowNesting *bool   `json:"allowNesting,omitempty"` // 容器嵌套
	EnableLXCFS  *bool   `json:"enableLxcfs,omitempty"`  // LXCFS资源视图
	CPUAllowance *string `json:"cpuAllowance,omitempty"` // CPU限制
	MemorySwap   *bool   `json:"memorySwap,omitempty"`   // 内存交换
	MaxProcesses *int    `json:"maxProcesses,omitempty"` // 最大进程数
	DiskIOLimit  *string `json:"diskIoLimit,omitempty"`  // 磁盘IO限制
}

// ProviderNodeConfig 节点配置
type ProviderNodeConfig struct {
	ID                    uint     `json:"id"` // Provider ID，用于资源清理
	UUID                  string   `json:"uuid"`
	Name                  string   `json:"name"`
	Host                  string   `json:"host"`
	PortIP                string   `json:"port_ip"` // 端口映射使用的公网IP（非必填，若为空则使用Host）
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

	// 节点标识（用于区分多个相同hostname的节点）
	HostName string `json:"host_name"` // 节点主机名（hostname），用于Proxmox等需要节点名的Provider

	// 容器特殊配置选项（仅适用于 LXD 和 Incus 的容器实例）
	ContainerPrivileged   bool   `json:"containerPrivileged"`   // 容器特权模式
	ContainerAllowNesting bool   `json:"containerAllowNesting"` // 容器嵌套
	ContainerEnableLXCFS  bool   `json:"containerEnableLxcfs"`  // LXCFS资源视图
	ContainerCPUAllowance string `json:"containerCpuAllowance"` // CPU限制
	ContainerMemorySwap   bool   `json:"containerMemorySwap"`   // 内存交换
	ContainerMaxProcesses int    `json:"containerMaxProcesses"` // 最大进程数
	ContainerDiskIOLimit  string `json:"containerDiskIoLimit"`  // 磁盘IO限制
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
