package constant

import (
	"errors"
	"fmt"
)

// 硬编码的实例配置选项，确保安全性
// 前端只能选择这些预定义的选项，不允许自定义输入
// 镜像列表从数据库动态获取，其他资源规格硬编码

// InstanceType 实例类型
type InstanceType string

const (
	InstanceTypeContainer InstanceType = "container"
	InstanceTypeVM        InstanceType = "vm"
)

// ProviderType Provider类型
type ProviderType string

const (
	ProviderTypeDocker  ProviderType = "docker"
	ProviderTypeLXD     ProviderType = "lxd"
	ProviderTypeIncus   ProviderType = "incus"
	ProviderTypeProxmox ProviderType = "proxmox"
)

// Architecture 架构类型
type Architecture string

const (
	ArchitectureAMD64 Architecture = "amd64"
	ArchitectureARM64 Architecture = "arm64"
)

// PortMappingMethod 端口映射方法
type PortMappingMethod string

const (
	PortMappingMethodDeviceProxy PortMappingMethod = "device_proxy" // LXD/Incus使用的设备代理方式
	PortMappingMethodIptables    PortMappingMethod = "iptables"     // 使用iptables进行端口映射
	PortMappingMethodNative      PortMappingMethod = "native"       // 原生实现（Docker, Proxmox独立IP）
)

// NetworkType 网络配置类型
type NetworkType string

const (
	NetworkTypeNATIPv4           NetworkType = "nat_ipv4"            // NAT IPv4
	NetworkTypeNATIPv4IPv6       NetworkType = "nat_ipv4_ipv6"       // NAT IPv4 + 独立IPv6
	NetworkTypeDedicatedIPv4     NetworkType = "dedicated_ipv4"      // 独立IPv4
	NetworkTypeDedicatedIPv4IPv6 NetworkType = "dedicated_ipv4_ipv6" // 独立IPv4 + 独立IPv6
	NetworkTypeIPv6Only          NetworkType = "ipv6_only"           // 纯IPv6
)

// HasIPv4 检查网络类型是否包含IPv4
func (nt NetworkType) HasIPv4() bool {
	return nt != NetworkTypeIPv6Only
}

// HasIPv6 检查网络类型是否包含IPv6
func (nt NetworkType) HasIPv6() bool {
	return nt == NetworkTypeNATIPv4IPv6 || nt == NetworkTypeDedicatedIPv4IPv6 || nt == NetworkTypeIPv6Only
}

// IsNAT 检查网络类型是否为NAT模式
func (nt NetworkType) IsNAT() bool {
	return nt == NetworkTypeNATIPv4 || nt == NetworkTypeNATIPv4IPv6
}

// IsDedicated 检查网络类型是否为独立IP模式
func (nt NetworkType) IsDedicated() bool {
	return nt == NetworkTypeDedicatedIPv4 || nt == NetworkTypeDedicatedIPv4IPv6
}

// GetLegacyValues 获取对应的旧格式值（用于向后兼容）
func (nt NetworkType) GetLegacyValues() (ipv4MappingType string, enableIPv6 bool) {
	switch nt {
	case NetworkTypeNATIPv4:
		return "nat", false
	case NetworkTypeNATIPv4IPv6:
		return "nat", true
	case NetworkTypeDedicatedIPv4:
		return "dedicated", false
	case NetworkTypeDedicatedIPv4IPv6:
		return "dedicated", true
	case NetworkTypeIPv6Only:
		return "ipv6_only", true
	default:
		return "nat", false
	}
}

// ExecutionRule 操作轮转规则（除了健康检测之外的所有任务和操作执行）
type ExecutionRule string

const (
	ExecutionRuleAuto    ExecutionRule = "auto"     // 自动切换（API不可用时自动切换SSH执行）
	ExecutionRuleAPIOnly ExecutionRule = "api_only" // 仅API执行
	ExecutionRuleSSHOnly ExecutionRule = "ssh_only" // 仅SSH执行
)

// CPUSpec CPU规格配置
type CPUSpec struct {
	ID    string `json:"id"`
	Cores int    `json:"cores"`
	Name  string `json:"name"`
}

// MemorySpec 内存规格配置
type MemorySpec struct {
	ID     string `json:"id"`
	SizeMB int    `json:"sizeMB"`
	Name   string `json:"name"`
}

// DiskSpec 磁盘规格配置
type DiskSpec struct {
	ID     string `json:"id"`
	SizeMB int    `json:"sizeMB"`
	Name   string `json:"name"`
}

// BandwidthSpec 带宽规格配置
type BandwidthSpec struct {
	ID        string `json:"id"`
	SpeedMbps int    `json:"speedMbps"`
	Name      string `json:"name"`
}

// 预定义的CPU规格 (1-20核)
var PredefinedCPUSpecs = []CPUSpec{}

// 预定义的内存规格
var PredefinedMemorySpecs = []MemorySpec{}

// 预定义的磁盘规格
var PredefinedDiskSpecs = []DiskSpec{}

// 预定义的带宽规格
var PredefinedBandwidthSpecs = []BandwidthSpec{}

// formatMemorySize 格式化内存大小显示
func formatMemorySize(sizeMB int) string {
	if sizeMB < 1024 {
		return fmt.Sprintf("%dMB", sizeMB)
	}
	sizeGB := float64(sizeMB) / 1024
	if sizeGB == float64(int(sizeGB)) {
		return fmt.Sprintf("%dGB", int(sizeGB))
	}
	return fmt.Sprintf("%.1fGB", sizeGB)
}

// formatDiskSize 格式化磁盘大小显示
func formatDiskSize(sizeMB int) string {
	if sizeMB < 1024 {
		return fmt.Sprintf("%dMB", sizeMB)
	}
	sizeGB := float64(sizeMB) / 1024
	if sizeGB == float64(int(sizeGB)) {
		return fmt.Sprintf("%dGB", int(sizeGB))
	}
	return fmt.Sprintf("%.1fGB", sizeGB)
}

// init 初始化所有硬编码配置
func init() {
	initCPUSpecs()
	initMemorySpecs()
	initDiskSpecs()
	initBandwidthSpecs()
}

// initCPUSpecs 初始化CPU规格配置 (1-20核)
func initCPUSpecs() {
	for i := 1; i <= 20; i++ {
		spec := CPUSpec{
			ID:    fmt.Sprintf("cpu-%d", i),
			Cores: i,
			Name:  fmt.Sprintf("%d核", i),
		}
		PredefinedCPUSpecs = append(PredefinedCPUSpecs, spec)
	}
}

// initMemorySpecs 初始化内存规格配置
func initMemorySpecs() {
	// 统一使用MB作为单位
	mbSpecs := []struct {
		sizeMB int
	}{
		{64}, {128}, {256}, {326}, {512},
		{1024}, {1248}, {1512}, {1718}, {2048},
		{2512}, {3072}, {4096}, {5120}, {6144}, {7168}, {8192}, {9216}, {10240},
	}
	for _, spec := range mbSpecs {
		memSpec := MemorySpec{
			ID:     fmt.Sprintf("mem-%dmb", spec.sizeMB),
			SizeMB: spec.sizeMB,
			Name:   formatMemorySize(spec.sizeMB),
		}
		PredefinedMemorySpecs = append(PredefinedMemorySpecs, memSpec)
	}
}

// initDiskSpecs 初始化磁盘规格配置
func initDiskSpecs() {
	// 统一使用MB作为单位
	mbSpecs := []struct {
		sizeMB int
	}{
		{50}, {512}, {1024}, {1512}, {2048},
		{2512}, {3072}, {4096}, {5120}, {6144}, {7168}, {8192}, {9216}, {10240},
		{15360}, {20480}, {25600}, {30720}, {40960}, {51200}, {61440}, {71680}, {81920}, {92160}, {102400},
	}

	for _, spec := range mbSpecs {
		diskSpec := DiskSpec{
			ID:     fmt.Sprintf("disk-%dmb", spec.sizeMB),
			SizeMB: spec.sizeMB,
			Name:   formatDiskSize(spec.sizeMB),
		}
		PredefinedDiskSpecs = append(PredefinedDiskSpecs, diskSpec)
	}
}

// initBandwidthSpecs 初始化带宽规格配置
func initBandwidthSpecs() {
	// 统一使用Mbps作为单位，从10Mbps到10000Mbps
	bandwidthSpecs := []struct {
		speedMbps int
	}{
		{10}, {20}, {50}, {100},
		{200}, {300}, {400}, {500}, {600}, {700}, {800}, {900}, {1000},
		{1100}, {1200}, {1300}, {1400}, {1500}, {1600}, {1700}, {1800}, {1900}, {2000},
		{2500}, {3000}, {3500}, {4000}, {4500}, {5000}, {6000}, {7000}, {8000}, {9000}, {10000},
	}

	for _, spec := range bandwidthSpecs {
		PredefinedBandwidthSpecs = append(PredefinedBandwidthSpecs, BandwidthSpec{
			ID:        fmt.Sprintf("bw-%dmbps", spec.speedMbps),
			SpeedMbps: spec.speedMbps,
			Name:      fmt.Sprintf("%dMbps", spec.speedMbps),
		})
	}
}

// GetCPUSpecByID 根据ID获取CPU规格配置
func GetCPUSpecByID(id string) (*CPUSpec, error) {
	for _, config := range PredefinedCPUSpecs {
		if config.ID == id {
			return &config, nil
		}
	}
	return nil, errors.New("CPU规格配置未找到")
}

// GetMemorySpecByID 根据ID获取内存规格配置
func GetMemorySpecByID(id string) (*MemorySpec, error) {
	for _, config := range PredefinedMemorySpecs {
		if config.ID == id {
			return &config, nil
		}
	}
	return nil, errors.New("内存规格配置未找到")
}

// GetDiskSpecByID 根据ID获取磁盘规格配置
func GetDiskSpecByID(id string) (*DiskSpec, error) {
	for _, config := range PredefinedDiskSpecs {
		if config.ID == id {
			return &config, nil
		}
	}
	return nil, errors.New("磁盘规格配置未找到")
}

// GetBandwidthSpecByID 根据ID获取带宽规格配置
func GetBandwidthSpecByID(id string) (*BandwidthSpec, error) {
	for _, config := range PredefinedBandwidthSpecs {
		if config.ID == id {
			return &config, nil
		}
	}
	return nil, errors.New("带宽规格配置未找到")
}
