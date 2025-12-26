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

// initCPUSpecs 初始化CPU规格配置 (1-10240核，渐进式步长)
func initCPUSpecs() {
	// 定义各个范围及其步长
	ranges := []struct {
		start int
		end   int
		step  int
	}{
		{1, 16, 1},          // 1~16: 步长1
		{18, 32, 2},         // 17~32: 步长2
		{36, 64, 4},         // 33~64: 步长4
		{72, 128, 8},        // 65~128: 步长8
		{144, 256, 16},      // 129~256: 步长16
		{288, 512, 32},      // 257~512: 步长32
		{576, 1024, 64},     // 513~1024: 步长64
		{1152, 2048, 128},   // 1025~2048: 步长128
		{2304, 4096, 256},   // 2049~4096: 步长256
		{4608, 8192, 512},   // 4097~8192: 步长512
		{9216, 10240, 1024}, // 8193~10240: 步长1024
	}

	for _, r := range ranges {
		for i := r.start; i <= r.end; i += r.step {
			spec := CPUSpec{
				ID:    fmt.Sprintf("cpu-%d", i),
				Cores: i,
				Name:  fmt.Sprintf("%d核", i),
			}
			PredefinedCPUSpecs = append(PredefinedCPUSpecs, spec)
		}
	}
}

// initMemorySpecs 初始化内存规格配置
func initMemorySpecs() {
	// 统一使用MB作为单位
	mbSpecs := []struct {
		sizeMB int
	}{
		// 小内存: 64MB - 1GB (步长较小)
		{64}, {128}, {192}, {256}, {320}, {384}, {448}, {512}, {576}, {640}, {704}, {768}, {832}, {896}, {960}, {1024},
		// 1GB - 4GB (256MB步长)
		{1280}, {1536}, {1792}, {2048}, {2304}, {2560}, {2816}, {3072}, {3328}, {3584}, {3840}, {4096},
		// 4GB - 10GB (512MB步长)
		{4608}, {5120}, {5632}, {6144}, {6656}, {7168}, {7680}, {8192}, {8704}, {9216}, {9728}, {10240},
		// 10GB - 32GB (1GB步长)
		{11264}, {12288}, {13312}, {14336}, {15360}, {16384}, {17408}, {18432}, {19456}, {20480},
		{21504}, {22528}, {23552}, {24576}, {25600}, {26624}, {27648}, {28672}, {29696}, {30720}, {31744}, {32768},
		// 32GB - 64GB (2GB步长)
		{34816}, {36864}, {38912}, {40960}, {43008}, {45056}, {47104}, {49152}, {51200}, {53248}, {55296}, {57344}, {59392}, {61440}, {63488}, {65536},
		// 64GB - 128GB (4GB步长)
		{69632}, {73728}, {77824}, {81920}, {86016}, {90112}, {94208}, {98304}, {102400}, {106496}, {110592}, {114688}, {118784}, {122880}, {126976}, {131072},
		// 128GB - 256GB (8GB步长)
		{139264}, {147456}, {155648}, {163840}, {172032}, {180224}, {188416}, {196608}, {204800}, {212992}, {221184}, {229376}, {237568}, {245760}, {253952}, {262144},
		// 256GB - 512GB (16GB步长)
		{278528}, {294912}, {311296}, {327680}, {344064}, {360448}, {376832}, {393216}, {409600}, {425984}, {442368}, {458752}, {475136}, {491520}, {507904}, {524288},
		// 512GB - 1TB (32GB步长)
		{557056}, {589824}, {622592}, {655360}, {688128}, {720896}, {753664}, {786432}, {819200}, {851968}, {884736}, {917504}, {950272}, {983040}, {1015808}, {1048576},
		// 1TB - 2TB (64GB步长)
		{1114112}, {1179648}, {1245184}, {1310720}, {1376256}, {1441792}, {1507328}, {1572864}, {1638400}, {1703936}, {1769472}, {1835008}, {1900544}, {1966080}, {2031616}, {2097152},
		// 2TB - 4TB (128GB步长)
		{2228224}, {2359296}, {2490368}, {2621440}, {2752512}, {2883584}, {3014656}, {3145728}, {3276800}, {3407872}, {3538944}, {3670016}, {3801088}, {3932160}, {4063232}, {4194304},
		// 4TB - 8TB (256GB步长)
		{4456448}, {4718592}, {4980736}, {5242880}, {5505024}, {5767168}, {6029312}, {6291456}, {6553600}, {6815744}, {7077888}, {7340032}, {7602176}, {7864320}, {8126464}, {8388608},
		// 8TB - 10TB (512GB步长)
		{8912896}, {9437184}, {9961472}, {10485760},
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
		// 极小容量: 50MB - 512MB
		{50}, {100}, {150}, {200}, {256}, {300}, {350}, {400}, {450}, {512},
		// 小容量: 512MB - 5GB (512MB步长)
		{1024}, {1536}, {2048}, {2560}, {3072}, {3584}, {4096}, {4608}, {5120},
		// 5GB - 20GB (1GB步长)
		{6144}, {7168}, {8192}, {9216}, {10240}, {11264}, {12288}, {13312}, {14336}, {15360}, {16384}, {17408}, {18432}, {19456}, {20480},
		// 20GB - 50GB (2GB步长)
		{22528}, {24576}, {26624}, {28672}, {30720}, {32768}, {34816}, {36864}, {38912}, {40960}, {43008}, {45056}, {47104}, {49152}, {51200},
		// 50GB - 100GB (5GB步长)
		{56320}, {61440}, {66560}, {71680}, {76800}, {81920}, {87040}, {92160}, {97280}, {102400},
		// 100GB - 200GB (10GB步长)
		{112640}, {122880}, {133120}, {143360}, {153600}, {163840}, {174080}, {184320}, {194560}, {204800},
		// 200GB - 500GB (20GB步长)
		{225280}, {245760}, {266240}, {286720}, {307200}, {327680}, {348160}, {368640}, {389120}, {409600}, {430080}, {450560}, {471040}, {491520}, {512000},
		// 500GB - 1TB (50GB步长)
		{563200}, {614400}, {665600}, {716800}, {768000}, {819200}, {870400}, {921600}, {972800}, {1024000},
		// 1TB - 2TB (100GB步长)
		{1126400}, {1228800}, {1331200}, {1433600}, {1536000}, {1638400}, {1740800}, {1843200}, {1945600}, {2048000},
		// 2TB - 5TB (200GB步长)
		{2252800}, {2457600}, {2662400}, {2867200}, {3072000}, {3276800}, {3481600}, {3686400}, {3891200}, {4096000}, {4300800}, {4505600}, {4710400}, {4915200}, {5120000},
		// 5TB - 10TB (500GB步长)
		{5632000}, {6144000}, {6656000}, {7168000}, {7680000}, {8192000}, {8704000}, {9216000}, {9728000}, {10240000},
		// 10TB - 20TB (1TB步长)
		{11264000}, {12288000}, {13312000}, {14336000}, {15360000}, {16384000}, {17408000}, {18432000}, {19456000}, {20480000},
		// 20TB - 50TB (2TB步长)
		{22528000}, {24576000}, {26624000}, {28672000}, {30720000}, {32768000}, {34816000}, {36864000}, {38912000}, {40960000},
		{43008000}, {45056000}, {47104000}, {49152000}, {51200000},
		// 50TB - 100TB (5TB步长)
		{56320000}, {61440000}, {66560000}, {71680000}, {76800000}, {81920000}, {87040000}, {92160000}, {97280000}, {102400000},
		// 100TB - 200TB (10TB步长)
		{112640000}, {122880000}, {133120000}, {143360000}, {153600000}, {163840000}, {174080000}, {184320000}, {194560000}, {204800000},
		// 200TB - 500TB (25TB步长)
		{230400000}, {256000000}, {281600000}, {307200000}, {332800000}, {358400000}, {384000000}, {409600000}, {435200000}, {460800000}, {486400000}, {512000000},
		// 500TB - 1PB (50TB步长)
		{563200000}, {614400000}, {665600000}, {716800000}, {768000000}, {819200000}, {870400000}, {921600000}, {972800000}, {1024000000},
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
	// 统一使用Mbps作为单位，从1Mbps到1000000Mbps (1Tbps)
	bandwidthSpecs := []struct {
		speedMbps int
	}{
		// 小带宽: 1-100Mbps (灵活步长)
		{1}, {2}, {3}, {4}, {5}, {6}, {7}, {8}, {9}, {10},
		{15}, {20}, {25}, {30}, {35}, {40}, {45}, {50}, {60}, {70}, {80}, {90}, {100},
		// 100-1000Mbps (50Mbps步长)
		{150}, {200}, {250}, {300}, {350}, {400}, {450}, {500}, {550}, {600}, {650}, {700}, {750}, {800}, {850}, {900}, {950}, {1000},
		// 1-5Gbps (100Mbps步长)
		{1100}, {1200}, {1300}, {1400}, {1500}, {1600}, {1700}, {1800}, {1900}, {2000},
		{2100}, {2200}, {2300}, {2400}, {2500}, {2600}, {2700}, {2800}, {2900}, {3000},
		{3100}, {3200}, {3300}, {3400}, {3500}, {3600}, {3700}, {3800}, {3900}, {4000},
		{4100}, {4200}, {4300}, {4400}, {4500}, {4600}, {4700}, {4800}, {4900}, {5000},
		// 5-10Gbps (200Mbps步长)
		{5200}, {5400}, {5600}, {5800}, {6000}, {6200}, {6400}, {6600}, {6800}, {7000},
		{7200}, {7400}, {7600}, {7800}, {8000}, {8200}, {8400}, {8600}, {8800}, {9000},
		{9200}, {9400}, {9600}, {9800}, {10000},
		// 10-20Gbps (500Mbps步长)
		{10500}, {11000}, {11500}, {12000}, {12500}, {13000}, {13500}, {14000}, {14500}, {15000},
		{15500}, {16000}, {16500}, {17000}, {17500}, {18000}, {18500}, {19000}, {19500}, {20000},
		// 20-50Gbps (1Gbps步长)
		{21000}, {22000}, {23000}, {24000}, {25000}, {26000}, {27000}, {28000}, {29000}, {30000},
		{31000}, {32000}, {33000}, {34000}, {35000}, {36000}, {37000}, {38000}, {39000}, {40000},
		{41000}, {42000}, {43000}, {44000}, {45000}, {46000}, {47000}, {48000}, {49000}, {50000},
		// 50-100Gbps (2Gbps步长)
		{52000}, {54000}, {56000}, {58000}, {60000}, {62000}, {64000}, {66000}, {68000}, {70000},
		{72000}, {74000}, {76000}, {78000}, {80000}, {82000}, {84000}, {86000}, {88000}, {90000},
		{92000}, {94000}, {96000}, {98000}, {100000},
		// 100-200Gbps (5Gbps步长)
		{105000}, {110000}, {115000}, {120000}, {125000}, {130000}, {135000}, {140000}, {145000}, {150000},
		{155000}, {160000}, {165000}, {170000}, {175000}, {180000}, {185000}, {190000}, {195000}, {200000},
		// 200-500Gbps (10Gbps步长)
		{210000}, {220000}, {230000}, {240000}, {250000}, {260000}, {270000}, {280000}, {290000}, {300000},
		{310000}, {320000}, {330000}, {340000}, {350000}, {360000}, {370000}, {380000}, {390000}, {400000},
		{410000}, {420000}, {430000}, {440000}, {450000}, {460000}, {470000}, {480000}, {490000}, {500000},
		// 500Gbps-1Tbps (20Gbps步长)
		{520000}, {540000}, {560000}, {580000}, {600000}, {620000}, {640000}, {660000}, {680000}, {700000},
		{720000}, {740000}, {760000}, {780000}, {800000}, {820000}, {840000}, {860000}, {880000}, {900000},
		{920000}, {940000}, {960000}, {980000}, {1000000},
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
