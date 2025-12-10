package pmacct

import (
	"context"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"oneclickvirt/global"
	monitoringModel "oneclickvirt/model/monitoring"
	providerModel "oneclickvirt/model/provider"
	"oneclickvirt/provider"
	providerService "oneclickvirt/service/provider"
	"oneclickvirt/utils"

	"go.uber.org/zap"
)

// Service pmacct服务
type Service struct {
	ctx        context.Context
	providerID uint
	sshPool    *utils.SSHConnectionPool // SSH连接池
}

var (
	batchProcessor     *BatchProcessor
	batchProcessorOnce sync.Once
)

// NewService 创建pmacct服务实例（使用全局SSH连接池）
func NewService() *Service {
	return &Service{
		ctx:     global.APP_SHUTDOWN_CONTEXT,
		sshPool: utils.GetGlobalSSHPool(),
	}
}

// NewServiceWithContext 使用指定context创建pmacct服务实例（使用全局SSH连接池）
func NewServiceWithContext(ctx context.Context) *Service {
	return &Service{
		ctx:     ctx,
		sshPool: utils.GetGlobalSSHPool(),
	}
}

// SetProviderID 设置当前操作的ProviderID
func (s *Service) SetProviderID(providerID uint) {
	s.providerID = providerID
}

// InitializePmacctForInstance 为实例初始化流量监控
// 监控容器/虚拟机通过NAT映射的流量
// 优先使用PortIP（端口映射IP），如果没有则使用Endpoint（SSH连接IP）
func (s *Service) InitializePmacctForInstance(instanceID uint) error {
	var instance providerModel.Instance
	if err := global.APP_DB.First(&instance, instanceID).Error; err != nil {
		return fmt.Errorf("failed to find instance: %w", err)
	}

	// 获取provider配置
	var providerRecord providerModel.Provider
	if err := global.APP_DB.First(&providerRecord, instance.ProviderID).Error; err != nil {
		return fmt.Errorf("failed to find provider: %w", err)
	}

	// 检查provider是否启用了流量统计
	if !providerRecord.EnableTrafficControl {
		global.APP_LOG.Debug("Provider未启用流量统计，跳过pmacct监控初始化",
			zap.Uint("instanceID", instanceID),
			zap.String("instanceName", instance.Name),
			zap.Uint("providerID", providerRecord.ID),
			zap.String("providerName", providerRecord.Name))
		return nil
	}

	// 获取provider实例
	providerInstance, exists := providerService.GetProviderService().GetProviderByID(instance.ProviderID)
	if !exists {
		return fmt.Errorf("provider ID %d not found", instance.ProviderID)
	}

	s.SetProviderID(instance.ProviderID)

	global.APP_LOG.Info("开始初始化流量监控",
		zap.Uint("instanceID", instanceID),
		zap.String("instanceName", instance.Name),
		zap.String("providerType", providerInstance.GetType()))

	// 检查是否已存在监控记录（包括启用和停用的）
	var existingMonitor monitoringModel.PmacctMonitor
	if err := global.APP_DB.Where("instance_id = ?", instanceID).First(&existingMonitor).Error; err == nil {
		// 如果已存在且启用，说明是正常状态，跳过初始化
		if existingMonitor.IsEnabled {
			global.APP_LOG.Info("实例已存在启用的监控记录，跳过初始化",
				zap.Uint("instanceID", instanceID),
				zap.Uint("monitorID", existingMonitor.ID),
				zap.String("mappedIP", existingMonitor.MappedIP))
			return nil
		}

		// 如果已存在但停用，说明是重置等场景，先删除旧记录
		global.APP_LOG.Info("发现停用的监控记录，删除后重新创建",
			zap.Uint("instanceID", instanceID),
			zap.Uint("oldMonitorID", existingMonitor.ID),
			zap.Bool("oldIsEnabled", existingMonitor.IsEnabled))

		if err := global.APP_DB.Unscoped().Delete(&existingMonitor).Error; err != nil {
			return fmt.Errorf("删除旧监控记录失败: %w", err)
		}
	}

	// 确定要监控的IPv4地址
	// 优先使用PortIP（如果配置了端口映射专用IP）
	// 否则使用Endpoint（SSH连接的IP地址）
	var monitorIPv4 string
	var ipv4Source string

	if providerRecord.PortIP != "" {
		monitorIPv4 = providerRecord.PortIP
		ipv4Source = "PortIP"
	} else if providerRecord.Endpoint != "" {
		monitorIPv4 = providerRecord.Endpoint
		ipv4Source = "Endpoint"
	}

	// 如果IPv4包含端口，提取IP地址部分
	if monitorIPv4 != "" {
		if idx := strings.Index(monitorIPv4, ":"); idx != -1 {
			monitorIPv4 = monitorIPv4[:idx]
		}
	}

	// 确定要监控的IPv6地址
	var monitorIPv6 string
	var ipv6Source string

	// 检查是否有IPv6映射配置
	if instance.PublicIPv6 != "" {
		monitorIPv6 = instance.PublicIPv6
		ipv6Source = "PublicIPv6"
	} else if instance.IPv6Address != "" {
		monitorIPv6 = instance.IPv6Address
		ipv6Source = "IPv6Address"
	}

	// 至少需要一个IP地址（IPv4或IPv6）
	if monitorIPv4 == "" && monitorIPv6 == "" {
		return fmt.Errorf("provider has no IPv4 or IPv6 address configured")
	}

	global.APP_LOG.Info("确定pmacct监控IP",
		zap.Uint("instanceID", instanceID),
		zap.String("publicIPv4", monitorIPv4),
		zap.String("ipv4Source", ipv4Source),
		zap.String("publicIPv6", monitorIPv6),
		zap.String("ipv6Source", ipv6Source))

	// 在Provider宿主机上安装和配置pmacct
	if err := s.installPmacct(providerInstance); err != nil {
		return fmt.Errorf("failed to install pmacct: %w", err)
	}

	// 如果 PrivateIP 为空，尝试使用 Provider 的标准方法获取
	if instance.PrivateIP == "" {
		global.APP_LOG.Warn("实例PrivateIP为空，尝试通过Provider获取",
			zap.Uint("instanceID", instanceID),
			zap.String("instanceName", instance.Name),
			zap.String("providerType", providerInstance.GetType()))

		var privateIP string
		var err error

		// 使用 Provider 接口的标准方法获取私有IP
		switch prov := providerInstance.(type) {
		case interface{ GetInstanceIPv4(string) (string, error) }:
			// LXD Provider
			privateIP, err = prov.GetInstanceIPv4(instance.Name)
		case interface {
			GetInstanceIPv4(context.Context, string) (string, error)
		}:
			// Incus Provider
			ctx, cancel := context.WithTimeout(s.ctx, 30*time.Second)
			defer cancel()
			privateIP, err = prov.GetInstanceIPv4(ctx, instance.Name)
		default:
			global.APP_LOG.Debug("Provider不支持GetInstanceIPv4方法，跳过",
				zap.String("providerType", providerInstance.GetType()))
		}
		if err == nil && privateIP != "" {
			// 更新数据库中的 PrivateIP
			global.APP_DB.Model(&instance).Update("private_ip", privateIP)
			instance.PrivateIP = privateIP // 更新内存中的值
			global.APP_LOG.Info("成功通过Provider获取并更新实例PrivateIP",
				zap.String("instanceName", instance.Name),
				zap.String("privateIP", privateIP),
				zap.String("providerType", providerInstance.GetType()))
		} else if err != nil {
			global.APP_LOG.Warn("通过Provider获取PrivateIP失败",
				zap.String("instanceName", instance.Name),
				zap.Error(err))
		}
	}

	// 配置pmacct监控规则
	// 网络架构说明：
	// - NAT虚拟化（Docker/LXD/Incus）：BPF过滤器使用PrivateIP（内网IP），因为在veth接口上看到的是NAT前的地址
	// - 如果PrivateIP为空：不限制host，只过滤内网间流量（捕获所有通过该接口的外部流量）
	// - IPv6：直接使用公网IPv6（通常不经过NAT）

	// 确定用于BPF过滤器的IP
	// IPv4: 使用PrivateIP（NAT场景），如果为空则不限制host
	bpfIPv4 := instance.PrivateIP
	// IPv6: 直接使用公网IPv6（不经过NAT）
	bpfIPv6 := monitorIPv6

	// 即使 bpfIPv4 和 bpfIPv6 都为空，也可以继续（只过滤内网流量）
	if bpfIPv4 == "" && bpfIPv6 == "" {
		global.APP_LOG.Warn("无法获取实例的监控IP，将监控所有非内网流量",
			zap.String("instanceName", instance.Name),
			zap.String("PrivateIP", instance.PrivateIP),
			zap.String("MappedIP", monitorIPv4),
			zap.String("IPv6", monitorIPv6))
	}

	global.APP_LOG.Info("配置pmacct监控",
		zap.String("bpfIPv4", bpfIPv4),
		zap.String("bpfIPv6", bpfIPv6),
		zap.String("mappedIPv4", monitorIPv4),
		zap.String("mappedIPv6", monitorIPv6))

	// 配置pmacct
	if err := s.configurePmacctForIPs(providerInstance, instance.Name, bpfIPv4, bpfIPv6, monitorIPv4, monitorIPv6); err != nil {
		return fmt.Errorf("failed to configure pmacct: %w", err)
	}

	// 在数据库中创建监控记录（保存MappedIP和网络接口信息）
	// 网络接口信息会在configurePmacctForIPs中更新到instance表
	pmacctMonitor := &monitoringModel.PmacctMonitor{
		InstanceID:   instanceID,
		ProviderID:   instance.ProviderID,
		ProviderType: providerInstance.GetType(),
		MappedIP:     monitorIPv4, // 公网IPv4（用于显示）
		MappedIPv6:   monitorIPv6, // 公网IPv6（用于显示）
		IsEnabled:    true,
		LastSync:     time.Now(),
	}

	if err := global.APP_DB.Create(pmacctMonitor).Error; err != nil {
		return fmt.Errorf("failed to create pmacct monitor record: %w", err)
	}

	global.APP_LOG.Info("pmacct监控初始化成功",
		zap.Uint("instanceID", instanceID),
		zap.String("instanceName", instance.Name),
		zap.String("monitorIPv4", monitorIPv4),
		zap.String("ipv4Source", ipv4Source),
		zap.String("monitorIPv6", monitorIPv6),
		zap.String("ipv6Source", ipv6Source))

	return nil
}

// installPmacct 在Provider宿主机上安装pmacct
func (s *Service) installPmacct(providerInstance provider.Provider) error {
	global.APP_LOG.Info("检查并安装pmacct", zap.String("providerType", providerInstance.GetType()))

	// 检查是否已安装pmacct
	checkCmd := "which pmacctd"
	ctx, cancel := context.WithTimeout(s.ctx, 30*time.Second)
	defer cancel()

	output, err := providerInstance.ExecuteSSHCommand(ctx, checkCmd)
	if err == nil && strings.Contains(output, "pmacctd") {
		// 检查pmacct版本
		if err := s.checkPmacctVersion(providerInstance); err != nil {
			global.APP_LOG.Warn("pmacct版本检查失败，将尝试重新安装", zap.Error(err))
			// 版本不符合要求，继续安装流程
		} else {
			global.APP_LOG.Info("pmacct已安装且版本符合要求")
			return nil
		}
	}

	// 安装pmacct
	installCmd := `
# 检测操作系统并安装pmacct
# 支持: Ubuntu 18+, Debian 8+, CentOS 7+, AlmaLinux 8.5+, OracleLinux 8+, RockyLinux 8+, Arch, Alpine

# Debian/Ubuntu系列
if [ -f /etc/debian_version ]; then
    echo "检测到Debian/Ubuntu系统，使用apt安装pmacct和sqlite3"
    apt-get update -qq || apt update -qq
    apt-get install -y pmacct sqlite3 || apt install -y pmacct sqlite3

# RHEL/CentOS/AlmaLinux/RockyLinux/Oracle Linux系列
elif [ -f /etc/redhat-release ] || [ -f /etc/centos-release ] || [ -f /etc/almalinux-release ] || [ -f /etc/rocky-release ] || [ -f /etc/oracle-release ]; then
    echo "检测到RHEL系列系统，使用yum/dnf安装pmacct"
    
    # 检测是否使用dnf（CentOS 8+, AlmaLinux, RockyLinux, OracleLinux 8+）
    if command -v dnf >/dev/null 2>&1; then
        # 先尝试启用EPEL
        dnf install -y epel-release 2>/dev/null || true
        # 对于某些系统可能需要启用PowerTools/CodeReady
        dnf config-manager --set-enabled powertools 2>/dev/null || \
        dnf config-manager --set-enabled PowerTools 2>/dev/null || \
        dnf config-manager --set-enabled crb 2>/dev/null || true
        dnf install -y pmacct sqlite
    else
        # CentOS 7使用yum
        yum install -y epel-release
        yum install -y pmacct sqlite
    fi

# Alpine Linux
elif [ -f /etc/alpine-release ]; then
    echo "检测到Alpine Linux，使用apk安装pmacct和sqlite"
    apk update
    apk add --no-cache pmacct sqlite

# Arch Linux
elif [ -f /etc/arch-release ] || command -v pacman >/dev/null 2>&1; then
    echo "检测到Arch Linux，使用pacman安装pmacct和sqlite"
    pacman -Sy --noconfirm --needed pmacct sqlite

else
    echo "错误：不支持的操作系统，无法自动安装pmacct"
    echo "支持的系统: Ubuntu 18+, Debian 8+, CentOS 7+, AlmaLinux 8.5+, OracleLinux 8+, RockyLinux 8+, Arch, Alpine"
    exit 1
fi

# 验证安装
if ! command -v pmacctd >/dev/null 2>&1; then
    echo "错误：pmacct安装失败，未找到pmacctd命令"
    exit 1
fi

if ! command -v sqlite3 >/dev/null 2>&1; then
    echo "错误：sqlite3安装失败，未找到sqlite3命令"
    exit 1
fi

echo "pmacct安装成功: $(pmacctd -V 2>&1 | head -1)"
echo "sqlite3安装成功: $(sqlite3 --version 2>&1 | head -1)"

# 确保pmacct默认服务停止（我们将手动管理配置）
systemctl stop pmacct 2>/dev/null || service pmacct stop 2>/dev/null || rc-service pmacct stop 2>/dev/null || true
systemctl disable pmacct 2>/dev/null || chkconfig pmacct off 2>/dev/null || rc-update del pmacct 2>/dev/null || true
`

	installCtx, installCancel := context.WithTimeout(s.ctx, 5*time.Minute)
	defer installCancel()

	output, err = providerInstance.ExecuteSSHCommand(installCtx, installCmd)
	if err != nil {
		return fmt.Errorf("pmacct installation failed: %w, output: %s", err, output)
	}

	global.APP_LOG.Info("pmacct安装成功")

	// 验证安装后的版本
	if err := s.checkPmacctVersion(providerInstance); err != nil {
		return fmt.Errorf("pmacct版本验证失败: %w", err)
	}

	return nil
}

// parsePmacctVersion 从pmacct版本输出中提取版本号
func (s *Service) parsePmacctVersion(output string) ([]int, error) {
	// 使用正则表达式提取版本号
	// 匹配: 1.7.8, 1.7.9, 2.0.0 等格式
	re := regexp.MustCompile(`(\d+)\.(\d+)\.(\d+)`)
	matches := re.FindStringSubmatch(output)

	if len(matches) < 4 {
		return nil, fmt.Errorf("无法从输出中提取版本号: %s", output)
	}

	major, err1 := strconv.Atoi(matches[1])
	minor, err2 := strconv.Atoi(matches[2])
	patch, err3 := strconv.Atoi(matches[3])

	if err1 != nil || err2 != nil || err3 != nil {
		return nil, fmt.Errorf("版本号转换失败: %s", output)
	}

	return []int{major, minor, patch}, nil
}

// compareVersion 比较版本号，如果current >= min则返回true
func (s *Service) compareVersion(current, min []int) bool {
	if len(current) != 3 || len(min) != 3 {
		return false
	}

	// 比较主版本号
	if current[0] > min[0] {
		return true
	}
	if current[0] < min[0] {
		return false
	}

	// 主版本号相同，比较次版本号
	if current[1] > min[1] {
		return true
	}
	if current[1] < min[1] {
		return false
	}

	// 主版本号和次版本号都相同，比较补丁版本号
	return current[2] >= min[2]
}

// versionToString 将版本号数组转换为字符串
func (s *Service) versionToString(version []int) string {
	if len(version) != 3 {
		return "unknown"
	}
	return fmt.Sprintf("%d.%d.%d", version[0], version[1], version[2])
}

// configurePmacctForIPs 配置pmacct监控特定IP的流量（支持IPv4和IPv6）
// bpfIPv4/bpfIPv6: BPF过滤器使用的IP（容器用内网IP，虚拟机用公网IP）
// publicIPv4/publicIPv6: 记录用的公网IP（用于数据库存储和显示）
func (s *Service) configurePmacctForIPs(providerInstance provider.Provider, instanceName, bpfIPv4, bpfIPv6, publicIPv4, publicIPv6 string) error {
	global.APP_LOG.Info("配置pmacct监控",
		zap.String("instance", instanceName),
		zap.String("bpfIPv4", bpfIPv4),
		zap.String("bpfIPv6", bpfIPv6),
		zap.String("publicIPv4", publicIPv4),
		zap.String("publicIPv6", publicIPv6))

	// 检测网络接口（支持IPv4和IPv6）
	hasIPv6 := bpfIPv6 != "" || publicIPv6 != ""

	// 从数据库获取实例信息（用于获取已保存的网络接口）
	var instance providerModel.Instance
	if err := global.APP_DB.Where("name = ?", instanceName).First(&instance).Error; err != nil {
		global.APP_LOG.Warn("无法从数据库获取实例信息",
			zap.String("instance", instanceName),
			zap.Error(err))
	}

	networkInterfaces, err := s.detectNetworkInterfaces(providerInstance, instanceName, &instance, hasIPv6)
	if err != nil {
		return fmt.Errorf("failed to detect network interfaces: %w", err)
	}

	global.APP_LOG.Info("检测到网络接口",
		zap.String("instance", instanceName),
		zap.String("ipv4Interface", networkInterfaces.IPv4Interface),
		zap.String("ipv6Interface", networkInterfaces.IPv6Interface))

	// 如果实例信息已获取但带宽为0，使用默认值
	if instance.Bandwidth == 0 {
		global.APP_LOG.Warn("实例带宽配置为0，使用默认值",
			zap.String("instance", instanceName))
		instance.Bandwidth = 100 // 默认100Mbps
	}

	// 根据实例带宽动态计算缓冲区大小和缓存条目数
	pluginBufferSize, pluginPipeSize, _, sqlCacheEntries := s.calculatePmacctBufferSizes(instance.Bandwidth)

	// 确定监控使用的网络接口
	// 对于容器，IPv4和IPv6通常使用同一个veth接口
	// 对于虚拟机，可能使用同一个物理接口或不同接口
	networkInterface := networkInterfaces.IPv4Interface
	if networkInterface == "" && networkInterfaces.IPv6Interface != "" {
		// 如果只有IPv6接口，使用IPv6接口
		networkInterface = networkInterfaces.IPv6Interface
	}

	// 创建pmacct配置文件
	configDir := fmt.Sprintf("/var/lib/pmacct/%s", instanceName)
	configFile := fmt.Sprintf("%s/pmacctd.conf", configDir)
	dataFile := fmt.Sprintf("%s/traffic.db", configDir)

	// 构建监控信息
	monitorInfo := ""
	if publicIPv4 != "" && publicIPv6 != "" {
		monitorInfo = fmt.Sprintf("Public IPv4: %s, Public IPv6: %s", publicIPv4, publicIPv6)
	} else if publicIPv4 != "" {
		monitorInfo = fmt.Sprintf("Public IPv4: %s", publicIPv4)
	} else if publicIPv6 != "" {
		monitorInfo = fmt.Sprintf("Public IPv6: %s", publicIPv6)
	}

	if bpfIPv4 != "" && publicIPv4 != "" && bpfIPv4 != publicIPv4 {
		monitorInfo += fmt.Sprintf(" (BPF Monitor: %s)", bpfIPv4)
	}

	// 构建BPF过滤器
	var bpfFilter string
	internalNetFilter := "not ((src net 10.0.0.0/8 and dst net 10.0.0.0/8) or " +
		"(src net 172.16.0.0/12 and dst net 172.16.0.0/12) or " +
		"(src net 192.168.0.0/16 and dst net 192.168.0.0/16) or " +
		"(src net 127.0.0.0/8 and dst net 127.0.0.0/8) or " +
		"(dst net 224.0.0.0/4) or " +
		"(dst host 255.255.255.255) or " +
		"(src net 169.254.0.0/16 or dst net 169.254.0.0/16))"

	if bpfIPv4 != "" && bpfIPv6 != "" {
		bpfFilter = fmt.Sprintf(
			"(host %s and %s) or (host %s)",
			bpfIPv4, internalNetFilter, bpfIPv6)
	} else if bpfIPv4 != "" {
		bpfFilter = fmt.Sprintf("host %s and %s", bpfIPv4, internalNetFilter)
	} else if bpfIPv6 != "" {
		bpfFilter = fmt.Sprintf("host %s", bpfIPv6)
	} else {
		bpfFilter = internalNetFilter
		global.APP_LOG.Warn("BPF过滤器未指定监控IP，将捕获所有非内网流量",
			zap.String("instance", instanceName))
	}

	config := fmt.Sprintf(`# pmacct configuration for instance: %s
# Monitoring: %s
# Bandwidth: %d Mbps

# 前台运行模式
daemonize: false
# PID文件路径
pidfile: %s/pmacctd.pid
# 日志输出到syslog
syslog: daemon

# 监听的网络接口
pcap_interface: %s

# BPF过滤器：捕获外部流量，排除内网通信（10.x, 172.16-31.x, 192.168.x, 224.x多播, 255.255.255.255广播）
pcap_filter: %s

# 聚合方式：仅按源IP和目标IP聚合
aggregate: src_host, dst_host

# 插件配置：使用SQLite本地存储
plugins: sqlite3[sqlite]

# SQLite数据库文件路径
sql_db[sqlite]: %s
# 数据表名称
sql_table[sqlite]: acct_v9
# 仅插入aggregate中指定的字段
sql_optimize_clauses[sqlite]: true
# 刷新间隔：60秒从内存写入SQLite (累计式)
sql_refresh_time[sqlite]: 60
# 历史记录时间窗口：1分钟
sql_history[sqlite]: 1m
# 时间戳对齐方式：按分钟对齐
sql_history_roundoff[sqlite]: m
# 直接插入模式：不更新已存在记录
sql_dont_try_update[sqlite]: true

# 内存缓存条目数（根据带宽动态调整：50M=32, 100M=64, 200M=128, 500M=256, 1G=512, 2G=768, >2G=1024）
sql_cache_entries[sqlite]: %d
# 插件缓冲区大小（字节）
plugin_buffer_size[sqlite]: %d
# 插件管道大小（字节）
plugin_pipe_size[sqlite]: %d
`, instanceName, monitorInfo, instance.Bandwidth, configDir, networkInterface,
		bpfFilter,
		dataFile,
		sqlCacheEntries, pluginBufferSize, pluginPipeSize)
	// systemd服务文件内容
	systemdService := fmt.Sprintf(`[Unit]
Description=pmacct daemon for instance %s
Documentation=man:pmacctd(8)
After=network.target

[Service]
Type=simple
ExecStart=/usr/sbin/pmacctd -f %s
ExecReload=/bin/kill -HUP $MAINPID
Restart=on-failure
RestartSec=5s
StandardOutput=journal
StandardError=journal

[Install]
WantedBy=multi-user.target
`, instanceName, configFile)

	// 步骤1: 创建配置目录
	mkdirCmd := fmt.Sprintf("mkdir -p %s && chmod 755 %s", configDir, configDir)
	mkdirCtx, mkdirCancel := context.WithTimeout(s.ctx, 30*time.Second)
	defer mkdirCancel()

	if _, err := providerInstance.ExecuteSSHCommand(mkdirCtx, mkdirCmd); err != nil {
		return fmt.Errorf("failed to create pmacct config directory: %w", err)
	}

	// 步骤2: 使用SFTP上传pmacct配置文件
	if err := s.uploadFileViaSFTP(providerInstance, config, configFile, 0644); err != nil {
		return fmt.Errorf("failed to upload pmacct config file: %w", err)
	}

	// 步骤3: 初始化SQLite数据库表结构
	// pmacct不会自动创建表，需要手动创建acct_v9表
	if err := s.initializePmacctDatabase(providerInstance, dataFile); err != nil {
		return fmt.Errorf("failed to initialize pmacct database: %w", err)
	}

	// 检测宿主机是否支持systemd，并创建相应的服务
	detectCmd := `
# 检测init系统类型
# 支持: systemd, SysVinit, OpenRC (Alpine)
if command -v systemctl >/dev/null 2>&1 && [ -d /etc/systemd/system ]; then
    echo "systemd"
elif command -v rc-service >/dev/null 2>&1 && [ -d /etc/init.d ]; then
    # Alpine Linux使用OpenRC
    echo "openrc"
elif command -v service >/dev/null 2>&1 && [ -d /etc/init.d ]; then
    # 传统SysVinit
    echo "sysvinit"
else
    echo "none"
fi
`

	detectCtx, detectCancel := context.WithTimeout(s.ctx, 10*time.Second)
	defer detectCancel()

	initSystem, err := providerInstance.ExecuteSSHCommand(detectCtx, detectCmd)
	if err != nil {
		return fmt.Errorf("failed to detect init system: %w", err)
	}

	initSystem = strings.TrimSpace(initSystem)
	global.APP_LOG.Info("检测到init系统", zap.String("initSystem", initSystem))

	// 根据init系统类型创建服务
	switch initSystem {
	case "systemd":
		return s.setupSystemdService(providerInstance, instanceName, networkInterface, configFile, configDir, systemdService, networkInterfaces)
	case "openrc":
		return s.setupOpenRCService(providerInstance, instanceName, networkInterface, configFile, configDir, networkInterfaces)
	case "sysvinit":
		return s.setupSysVService(providerInstance, instanceName, networkInterface, configFile, configDir, networkInterfaces)
	default:
		// 降级到nohup方式（不推荐）
		global.APP_LOG.Warn("未检测到支持的init系统，使用nohup启动（重启后需要手动重启）",
			zap.String("detectedSystem", initSystem))
		return s.startWithNohup(providerInstance, instanceName, networkInterface, configFile, configDir, networkInterfaces)
	}
}

// setupSystemdService 使用systemd管理pmacct服务
func (s *Service) setupSystemdService(providerInstance provider.Provider, instanceName, networkInterface, configFile, configDir, serviceContent string, networkInterfaces *NetworkInterfaceInfo) error {
	serviceFile := fmt.Sprintf("/etc/systemd/system/pmacctd-%s.service", instanceName)

	// 步骤1: 使用SFTP上传systemd服务文件
	if err := s.uploadFileViaSFTP(providerInstance, serviceContent, serviceFile, 0644); err != nil {
		return fmt.Errorf("failed to upload systemd service file: %w", err)
	}

	// 步骤2: 生成启动脚本并上传（包含停止旧服务逻辑）
	startScript := fmt.Sprintf(`#!/bin/bash
set -e

# 停止可能存在的旧进程（在脚本内执行，避免SSH会话中断）
systemctl stop pmacctd-%s 2>/dev/null || true
pkill -f "pmacctd.*%s" 2>/dev/null || true
sleep 1

# 重载systemd配置
systemctl daemon-reload

# 启用并启动服务
systemctl enable pmacctd-%s
systemctl start pmacctd-%s

# 验证服务状态
if systemctl is-active --quiet pmacctd-%s; then
    echo "pmacct service started successfully"
    exit 0
else
    echo "Failed to start pmacct service"
    systemctl status pmacctd-%s --no-pager || true
    exit 1
fi
`, instanceName, configFile, instanceName, instanceName, instanceName, instanceName)

	startScriptPath := fmt.Sprintf("/tmp/pmacct_start_%s.sh", instanceName)
	if err := s.uploadFileViaSFTP(providerInstance, startScript, startScriptPath, 0755); err != nil {
		return fmt.Errorf("failed to upload start script: %w", err)
	}

	// 步骤3: 执行启动脚本
	execCtx, execCancel := context.WithTimeout(s.ctx, 60*time.Second)
	defer execCancel()

	output, err := providerInstance.ExecuteSSHCommand(execCtx, startScriptPath)
	if err != nil {
		return fmt.Errorf("failed to start systemd service: %w, output: %s", err, output)
	}

	// 步骤4: 清理临时脚本
	cleanupCtx, cleanupCancel := context.WithTimeout(s.ctx, 10*time.Second)
	defer cleanupCancel()
	providerInstance.ExecuteSSHCommand(cleanupCtx, fmt.Sprintf("rm -f %s", startScriptPath))

	global.APP_LOG.Info("pmacct systemd服务配置并启动成功",
		zap.String("instance", instanceName),
		zap.String("serviceFile", serviceFile))

	// 配置成功后，更新实例的网络接口信息到数据库
	s.updateInstanceNetworkInterfaces(instanceName, networkInterfaces.IPv4Interface, networkInterfaces.IPv6Interface)

	return nil
}

// setupSysVService 使用SysV init管理pmacct服务
func (s *Service) setupSysVService(providerInstance provider.Provider, instanceName, networkInterface, configFile, configDir string, networkInterfaces *NetworkInterfaceInfo) error {
	initScript := fmt.Sprintf("/etc/init.d/pmacctd-%s", instanceName)

	// 步骤1: 生成init脚本内容
	scriptContent := fmt.Sprintf(`#!/bin/bash
### BEGIN INIT INFO
# Provides:          pmacctd-%s
# Required-Start:    $network $local_fs $remote_fs
# Required-Stop:     $network $local_fs $remote_fs
# Default-Start:     2 3 4 5
# Default-Stop:      0 1 6
# Short-Description: pmacct daemon for instance %s
### END INIT INFO

DAEMON=/usr/sbin/pmacctd
CONFIG=%s
PIDFILE=%s/pmacctd.pid
NAME=pmacctd-%s

case "$1" in
  start)
    echo "Starting $NAME..."
    $DAEMON -f $CONFIG
    ;;
  stop)
    echo "Stopping $NAME..."
    if [ -f $PIDFILE ]; then
      kill $(cat $PIDFILE)
    fi
    pkill -f "pmacctd.*$CONFIG"
    ;;
  restart)
    $0 stop
    sleep 2
    $0 start
    ;;
  status)
    if pgrep -f "pmacctd.*$CONFIG" > /dev/null; then
      echo "$NAME is running"
      exit 0
    else
      echo "$NAME is not running"
      exit 1
    fi
    ;;
  *)
    echo "Usage: $0 {start|stop|restart|status}"
    exit 1
    ;;
esac

exit 0
`, instanceName, instanceName, configFile, configDir, instanceName)

	// 步骤1: 使用SFTP上传init脚本
	if err := s.uploadFileViaSFTP(providerInstance, scriptContent, initScript, 0755); err != nil {
		return fmt.Errorf("failed to upload init script: %w", err)
	}

	// 步骤2: 生成启用服务的脚本并上传（包含停止逻辑）
	enableScript := fmt.Sprintf(`#!/bin/bash
set -e

# 停止可能存在的旧进程
if [ -f /etc/init.d/pmacctd-%s ]; then
    /etc/init.d/pmacctd-%s stop 2>/dev/null || true
fi
pkill -f "pmacctd.*%s" 2>/dev/null || true
sleep 1

# 启用服务（支持多种init系统）
# Debian/Ubuntu使用update-rc.d
if command -v update-rc.d >/dev/null 2>&1; then
    update-rc.d pmacctd-%s defaults
# RHEL/CentOS使用chkconfig
elif command -v chkconfig >/dev/null 2>&1; then
    chkconfig --add pmacctd-%s
    chkconfig pmacctd-%s on
# Alpine使用rc-update
elif command -v rc-update >/dev/null 2>&1; then
    rc-update add pmacctd-%s default 2>/dev/null || true
fi

# 启动服务
%s start

# 验证服务状态
sleep 2
if pgrep -f "pmacctd.*%s" > /dev/null; then
    echo "pmacct service started successfully"
    exit 0
else
    echo "Failed to start pmacct service"
    exit 1
fi
`, instanceName, instanceName, configFile, instanceName, instanceName, instanceName, instanceName, initScript, configFile)

	enableScriptPath := fmt.Sprintf("/tmp/pmacct_enable_%s.sh", instanceName)
	if err := s.uploadFileViaSFTP(providerInstance, enableScript, enableScriptPath, 0755); err != nil {
		return fmt.Errorf("failed to upload enable script: %w", err)
	}

	// 步骤3: 执行启用脚本
	execCtx, execCancel := context.WithTimeout(s.ctx, 60*time.Second)
	defer execCancel()

	output, err := providerInstance.ExecuteSSHCommand(execCtx, enableScriptPath)
	if err != nil {
		return fmt.Errorf("failed to enable sysv service: %w, output: %s", err, output)
	}

	// 步骤4: 清理临时脚本
	cleanupCtx, cleanupCancel := context.WithTimeout(s.ctx, 10*time.Second)
	defer cleanupCancel()
	providerInstance.ExecuteSSHCommand(cleanupCtx, fmt.Sprintf("rm -f %s", enableScriptPath))

	global.APP_LOG.Info("pmacct SysV服务配置并启动成功",
		zap.String("instance", instanceName),
		zap.String("initScript", initScript))

	// 配置成功后，更新实例的网络接口信息到数据库
	s.updateInstanceNetworkInterfaces(instanceName, networkInterfaces.IPv4Interface, networkInterfaces.IPv6Interface)

	return nil
}

// setupOpenRCService 使用OpenRC管理pmacct服务（Alpine Linux）
func (s *Service) setupOpenRCService(providerInstance provider.Provider, instanceName, networkInterface, configFile, configDir string, networkInterfaces *NetworkInterfaceInfo) error {
	initScript := fmt.Sprintf("/etc/init.d/pmacctd-%s", instanceName)

	// 步骤1: 生成OpenRC init脚本
	scriptContent := fmt.Sprintf(`#!/sbin/openrc-run
# OpenRC service script for pmacct instance: %s

name="pmacctd-%s"
description="pmacct daemon for instance %s"

command="/usr/sbin/pmacctd"
command_args="-f %s"
pidfile="%s/pmacctd.pid"
command_background=true

depend() {
    need net
    after firewall
}

start_pre() {
    checkpath --directory --mode 0755 %s
}

stop_post() {
    rm -f "$pidfile"
}
`, instanceName, instanceName, instanceName, configFile, configDir, configDir)

	// 步骤2: 使用SFTP上传OpenRC init脚本
	if err := s.uploadFileViaSFTP(providerInstance, scriptContent, initScript, 0755); err != nil {
		return fmt.Errorf("failed to upload openrc script: %w", err)
	}

	// 步骤3: 生成启用服务的脚本并上传（包含停止逻辑）
	enableScript := fmt.Sprintf(`#!/bin/sh
set -e

# 停止可能存在的旧进程
if [ -f /etc/init.d/pmacctd-%s ]; then
    rc-service pmacctd-%s stop 2>/dev/null || true
fi
pkill -f "pmacctd.*%s" 2>/dev/null || true
sleep 1

# 添加到默认运行级别
rc-update add pmacctd-%s default 2>/dev/null || true

# 启动服务
rc-service pmacctd-%s start

# 验证服务状态
sleep 2
if pgrep -f "pmacctd.*%s" > /dev/null; then
    echo "pmacct service started successfully"
    exit 0
else
    echo "Failed to start pmacct service"
    exit 1
fi
`, instanceName, instanceName, configFile, instanceName, instanceName, configFile)

	enableScriptPath := fmt.Sprintf("/tmp/pmacct_enable_%s.sh", instanceName)
	if err := s.uploadFileViaSFTP(providerInstance, enableScript, enableScriptPath, 0755); err != nil {
		return fmt.Errorf("failed to upload enable script: %w", err)
	}

	// 步骤3: 执行启用脚本
	execCtx, execCancel := context.WithTimeout(s.ctx, 60*time.Second)
	defer execCancel()

	output, err := providerInstance.ExecuteSSHCommand(execCtx, enableScriptPath)
	if err != nil {
		return fmt.Errorf("failed to enable openrc service: %w, output: %s", err, output)
	}

	// 步骤4: 清理临时脚本
	cleanupCtx, cleanupCancel := context.WithTimeout(s.ctx, 10*time.Second)
	defer cleanupCancel()
	providerInstance.ExecuteSSHCommand(cleanupCtx, fmt.Sprintf("rm -f %s", enableScriptPath))

	global.APP_LOG.Info("pmacct OpenRC服务配置并启动成功",
		zap.String("instance", instanceName),
		zap.String("initScript", initScript))

	// 配置成功后，更新实例的网络接口信息到数据库
	s.updateInstanceNetworkInterfaces(instanceName, networkInterfaces.IPv4Interface, networkInterfaces.IPv6Interface)

	return nil
}

// startWithNohup 使用nohup启动pmacct（降级方案）
func (s *Service) startWithNohup(providerInstance provider.Provider, instanceName, networkInterface, configFile, configDir string, networkInterfaces *NetworkInterfaceInfo) error {
	// 启动pmacct进程（后台运行）
	startCmd := fmt.Sprintf(`
# 停止可能存在的旧进程
if [ -f %s/pmacctd.pid ]; then
    OLD_PID=$(cat %s/pmacctd.pid)
    kill $OLD_PID 2>/dev/null || true
    sleep 1
fi

# 启动新的pmacct进程
nohup pmacctd -f %s > %s/pmacctd.log 2>&1 &
sleep 2

# 验证进程是否启动
if pgrep -f "pmacctd.*%s" > /dev/null; then
    echo "pmacct started successfully"
else
    echo "Failed to start pmacct"
    exit 1
fi
`, configDir, configDir, configFile, configDir, configFile)

	startCtx, startCancel := context.WithTimeout(s.ctx, 30*time.Second)
	defer startCancel()

	output, err := providerInstance.ExecuteSSHCommand(startCtx, startCmd)
	if err != nil {
		return fmt.Errorf("failed to start pmacct: %w, output: %s", err, output)
	}

	global.APP_LOG.Info("pmacct配置并启动成功（nohup方式）",
		zap.String("instance", instanceName),
		zap.String("configFile", configFile))

	// 配置成功后，更新实例的网络接口信息到数据库
	s.updateInstanceNetworkInterfaces(instanceName, networkInterfaces.IPv4Interface, networkInterfaces.IPv6Interface)

	return nil
}

// GetPmacctSummary 获取实例的pmacct流量汇总
func (s *Service) GetPmacctSummary(instanceID uint) (*monitoringModel.PmacctSummary, error) {
	var monitor monitoringModel.PmacctMonitor
	if err := global.APP_DB.Where("instance_id = ? AND is_enabled = ?", instanceID, true).First(&monitor).Error; err != nil {
		return nil, fmt.Errorf("pmacct monitor not found: %w", err)
	}

	// 检查Provider是否启用了流量统计
	var instance providerModel.Instance
	if err := global.APP_DB.Select("provider_id").First(&instance, instanceID).Error; err != nil {
		return nil, fmt.Errorf("instance not found: %w", err)
	}

	var providerRecord providerModel.Provider
	if err := global.APP_DB.Select("enable_traffic_control").First(&providerRecord, instance.ProviderID).Error; err != nil {
		return nil, fmt.Errorf("provider not found: %w", err)
	}

	// 如果Provider未启用流量统计，返回空数据
	if !providerRecord.EnableTrafficControl {
		return &monitoringModel.PmacctSummary{
			InstanceID: instanceID,
			MappedIP:   monitor.MappedIP,
			MappedIPv6: monitor.MappedIPv6,
			Today: &monitoringModel.PmacctTrafficRecord{
				InstanceID: instanceID,
				RxBytes:    0,
				TxBytes:    0,
				TotalBytes: 0,
			},
			ThisMonth: &monitoringModel.PmacctTrafficRecord{
				InstanceID: instanceID,
				RxBytes:    0,
				TxBytes:    0,
				TotalBytes: 0,
			},
			AllTime: &monitoringModel.PmacctTrafficRecord{
				InstanceID: instanceID,
				RxBytes:    0,
				TxBytes:    0,
				TotalBytes: 0,
			},
			History: []*monitoringModel.PmacctTrafficRecord{},
		}, nil
	}

	now := time.Now()
	year := now.Year()
	month := int(now.Month())
	day := now.Day()

	// 获取今日流量
	today := s.aggregateTrafficRecords(instanceID, year, month, day, 0)

	// 获取本月流量
	thisMonth := s.aggregateTrafficRecords(instanceID, year, month, 0, 0)

	// 获取总流量
	allTime := s.aggregateTrafficRecords(instanceID, 0, 0, 0, 0)

	// 获取历史记录（最近30天）
	history := s.getAggregatedHistory(instanceID, 30)

	return &monitoringModel.PmacctSummary{
		InstanceID: instanceID,
		MappedIP:   monitor.MappedIP,
		MappedIPv6: monitor.MappedIPv6,
		Today:      today,
		ThisMonth:  thisMonth,
		AllTime:    allTime,
		History:    history,
	}, nil
}

// updateInstanceNetworkInterfaces 更新实例的网络接口信息到数据库
// 这个方法接收的是检测到的实际接口，需要从configurePmacctForIPs传递正确的IPv4/IPv6接口
func (s *Service) updateInstanceNetworkInterfaces(instanceName, ipv4Interface, ipv6Interface string) {
	updateData := map[string]interface{}{}
	if ipv4Interface != "" {
		updateData["pmacct_interface_v4"] = ipv4Interface
	}
	if ipv6Interface != "" {
		updateData["pmacct_interface_v6"] = ipv6Interface
	}

	if len(updateData) > 0 {
		if err := global.APP_DB.Model(&providerModel.Instance{}).Where("name = ?", instanceName).Updates(updateData).Error; err != nil {
			global.APP_LOG.Warn("更新实例网络接口信息失败",
				zap.String("instance", instanceName),
				zap.Error(err))
		} else {
			global.APP_LOG.Info("成功更新实例网络接口信息",
				zap.String("instance", instanceName),
				zap.String("ipv4Interface", ipv4Interface),
				zap.String("ipv6Interface", ipv6Interface))
		}
	}
}
