package pmacct

import (
	"context"
	"fmt"
	"strings"
	"time"

	"oneclickvirt/global"
	providerModel "oneclickvirt/model/provider"
	"oneclickvirt/provider"

	"go.uber.org/zap"
)

// configurePmacctForIPs 配置pmacct监控特定IP的流量（支持IPv4和IPv6）
// bpfIPv4/bpfIPv6: BPF过滤器使用的IP（容器用内网IP，虚拟机用公网IP）
// publicIPv4/publicIPv6: 记录用的公网IP（用于数据库存储和显示）
func (s *Service) configurePmacctForIPs(providerInstance provider.Provider, instanceName, bpfIPv4, bpfIPv6, publicIPv4, publicIPv6 string) error {
	global.APP_LOG.Debug("配置pmacct监控",
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

	global.APP_LOG.Debug("检测到网络接口",
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
	global.APP_LOG.Debug("检测到init系统", zap.String("initSystem", initSystem))

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
