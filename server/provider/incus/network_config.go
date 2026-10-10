package incus

import (
	"context"
	"fmt"
	"net"
	"strings"

	"oneclickvirt/global"
	providerModel "oneclickvirt/model/provider"
	"oneclickvirt/service/userquota"
	"oneclickvirt/utils"

	"go.uber.org/zap"
)

// stopInstanceForConfig stops an instance idempotently before changing
// network devices.  Agent transports can return a non-zero exit code when a
// guest is already stopped or does not respond to a graceful shutdown, so the
// state is checked before and after a bounded force-stop fallback.
func (i *IncusProvider) stopInstanceForConfig(ctx context.Context, instanceName string) error {
	global.APP_LOG.Debug("停止实例进行配置", zap.String("instanceName", instanceName))

	if ctx == nil {
		ctx = context.Background()
	}
	statusCmd := incusInstanceStatusCommand(instanceName)
	statusOutput, statusErr := utils.ExecuteShellCommandContext(ctx, i.sshClient, statusCmd)
	if statusErr == nil && strings.EqualFold(strings.TrimSpace(statusOutput), "stopped") {
		return nil
	}
	if err := ctx.Err(); err != nil {
		return err
	}

	stopCmd := fmt.Sprintf("incus stop %s --timeout=30", shellSingleQuote(instanceName))
	output, stopErr := utils.ExecuteShellCommandContext(ctx, i.sshClient, stopCmd)
	if stopErr != nil {
		if err := ctx.Err(); err != nil {
			return err
		}
		statusOutput, statusErr = utils.ExecuteShellCommandContext(ctx, i.sshClient, statusCmd)
		if statusErr == nil && strings.EqualFold(strings.TrimSpace(statusOutput), "stopped") {
			return nil
		}
		forceCmd := fmt.Sprintf("incus stop %s --force --timeout=30", shellSingleQuote(instanceName))
		forceOutput, forceErr := utils.ExecuteShellCommandContext(ctx, i.sshClient, forceCmd)
		if forceErr != nil {
			statusAfter, statusAfterErr := utils.ExecuteShellCommandContext(ctx, i.sshClient, statusCmd)
			if statusAfterErr == nil && strings.EqualFold(strings.TrimSpace(statusAfter), "stopped") {
				return nil
			}
			return fmt.Errorf("停止实例失败: graceful=%v output=%q; force=%v output=%q; status=%v output=%q", stopErr, utils.TruncateString(strings.TrimSpace(output), 1200), forceErr, utils.TruncateString(strings.TrimSpace(forceOutput), 1200), statusAfterErr, utils.TruncateString(strings.TrimSpace(statusAfter), 500))
		}
		global.APP_LOG.Debug("实例已通过强制停止完成", zap.String("instanceName", instanceName))
		return nil
	}
	global.APP_LOG.Debug("实例已安全停止", zap.String("instanceName", instanceName))
	return nil
}

// configureNetworkLimits 配置网络限速
func (i *IncusProvider) configureNetworkLimits(instanceName string, networkConfig NetworkConfig) error {
	global.APP_LOG.Debug("配置网络限速",
		zap.String("instanceName", instanceName),
		zap.Int("inSpeed", networkConfig.InSpeed),
		zap.Int("outSpeed", networkConfig.OutSpeed))

	var speedLimit int
	if networkConfig.InSpeed == networkConfig.OutSpeed {
		speedLimit = networkConfig.InSpeed
	} else {
		if networkConfig.InSpeed > networkConfig.OutSpeed {
			speedLimit = networkConfig.InSpeed
		} else {
			speedLimit = networkConfig.OutSpeed
		}
	}

	// 找到主网络接口
	cmd := fmt.Sprintf("incus config show %s | grep -A5 \"devices:\" | grep \"type: nic\" -B3 | grep \"^  \" | head -n1 | sed 's/://g'", shellSingleQuote(instanceName))
	output, err := i.sshClient.Execute(cmd)
	var targetInterface string
	if err == nil {
		targetInterface, _ = utils.ParseFirstNetworkInterfaceOutput(output)
	}
	if targetInterface == "" {
		targetInterface = "eth0" // 默认接口
	}

	// 设置网络限速
	inSpeedMbit := fmt.Sprintf("%dMbit", networkConfig.InSpeed)
	outSpeedMbit := fmt.Sprintf("%dMbit", networkConfig.OutSpeed)
	maxSpeedMbit := fmt.Sprintf("%dMbit", speedLimit)

	cmd = fmt.Sprintf("incus config device override %s %s limits.egress=%s limits.ingress=%s limits.max=%s",
		shellSingleQuote(instanceName), shellSingleQuote(targetInterface), shellSingleQuote(outSpeedMbit), shellSingleQuote(inSpeedMbit), shellSingleQuote(maxSpeedMbit))
	_, err = i.sshClient.Execute(cmd)
	if err != nil {
		global.APP_LOG.Error("网络限速配置失败",
			zap.String("instanceName", instanceName),
			zap.String("interface", targetInterface),
			zap.Error(err))
		return err
	}

	global.APP_LOG.Debug("网络限速配置成功",
		zap.String("instanceName", instanceName),
		zap.String("interface", targetInterface),
		zap.String("inSpeed", inSpeedMbit),
		zap.String("outSpeed", outSpeedMbit))

	return nil
}

// getBandwidthFromProvider 从Provider配置获取带宽设置，并结合用户等级限制
func (i *IncusProvider) getBandwidthFromProvider(userLevel int) (inSpeed, outSpeed int, err error) {
	// 获取Provider信息
	var providerInfo providerModel.Provider
	if global.APP_DB == nil {
		return 300, 300, nil
	}
	if err := global.APP_DB.Where("id = ?", i.config.ID).First(&providerInfo).Error; err != nil {
		// 如果获取Provider失败，使用默认值
		global.APP_LOG.Warn("无法获取Provider配置，使用默认带宽",
			zap.Uint("provider_id", i.config.ID),
			zap.String("provider", i.config.Name),
			zap.Error(err))
		return 300, 300, nil // 默认300Mbps
	}

	// 基础带宽配置（来自Provider）
	providerInSpeed := providerInfo.DefaultInboundBandwidth
	providerOutSpeed := providerInfo.DefaultOutboundBandwidth

	// 获取用户等级对应的带宽限制
	userBandwidthLimit := i.getUserLevelBandwidth(userLevel)

	// 选择更小的值作为实际带宽限制（用户等级限制 vs Provider默认值）
	inSpeed = providerInSpeed
	if userBandwidthLimit > 0 && userBandwidthLimit < providerInSpeed {
		inSpeed = userBandwidthLimit
	}

	outSpeed = providerOutSpeed
	if userBandwidthLimit > 0 && userBandwidthLimit < providerOutSpeed {
		outSpeed = userBandwidthLimit
	}

	// 设置默认值（如果配置为0）
	if inSpeed <= 0 {
		inSpeed = 300 // 默认300Mbps
	}
	if outSpeed <= 0 {
		outSpeed = 300 // 默认300Mbps
	}

	// 确保不超过Provider的最大限制
	if providerInfo.MaxInboundBandwidth > 0 && inSpeed > providerInfo.MaxInboundBandwidth {
		inSpeed = providerInfo.MaxInboundBandwidth
	}
	if providerInfo.MaxOutboundBandwidth > 0 && outSpeed > providerInfo.MaxOutboundBandwidth {
		outSpeed = providerInfo.MaxOutboundBandwidth
	}

	global.APP_LOG.Debug("从Provider配置和用户等级获取带宽设置",
		zap.String("provider", i.config.Name),
		zap.Int("inSpeed", inSpeed),
		zap.Int("outSpeed", outSpeed),
		zap.Int("userLevel", userLevel),
		zap.Int("userBandwidthLimit", userBandwidthLimit),
		zap.Int("providerDefault", providerInSpeed))

	return inSpeed, outSpeed, nil
}

// getUserLevelBandwidth 根据用户等级获取带宽限制
func (i *IncusProvider) getUserLevelBandwidth(userLevel int) int {
	levelLimit, err := userquota.ResolveLevelLimit(userLevel)
	if err != nil {
		global.APP_LOG.Warn("获取用户等级带宽限制失败，使用等级1默认值",
			zap.Int("userLevel", userLevel),
			zap.Error(err))
		levelLimit, _ = userquota.ResolveLevelLimit(1)
	}
	bandwidth := userquota.ResourceInt(levelLimit.MaxResources, "bandwidth")
	if bandwidth <= 0 {
		return 100
	}
	return bandwidth
}

// setIPAddressBinding 设置IP地址绑定
func (i *IncusProvider) setIPAddressBinding(instanceName, instanceIP string) error {
	if i.sshClient == nil {
		return fmt.Errorf("SSH client不可用，无法固定实例IP地址")
	}
	return utils.SetLXCIPv4Binding(i.sshClient, "incus", instanceName, instanceIP)
}

// ensureIPv4OnHostInterface 确保独立 IPv4 地址已绑定到宿主机网络接口。
// 若尚未绑定，则自动将其以 /32 路由模式添加到宿主机主出口接口。
// 这是使用独立 IPv4（dedicated_ipv4 / dedicated_ipv4_ipv6）创建实例的前置条件检查。
func (i *IncusProvider) ensureIPv4OnHostInterface(ipv4 string) error {
	if ipv4 == "" {
		return nil
	}

	// 清理 IP 地址格式（去除 CIDR 前缀、多余空格等）
	cleanIP := strings.TrimSpace(ipv4)
	if idx := strings.IndexByte(cleanIP, '/'); idx != -1 {
		cleanIP = cleanIP[:idx]
	}
	if parsed := net.ParseIP(cleanIP); parsed == nil || parsed.To4() == nil {
		return fmt.Errorf("无效的独立IPv4地址: %s", cleanIP)
	}

	global.APP_LOG.Debug("检查独立IPv4是否已绑定到宿主机网络接口",
		zap.String("ip", cleanIP))

	// 检查该 IP 是否已绑定到宿主机的任意网络接口
	checkCmd := fmt.Sprintf("ip addr show | grep -w %s", shellSingleQuote(cleanIP))
	output, err := i.sshClient.Execute(checkCmd)
	if err == nil && strings.Contains(output, cleanIP) {
		global.APP_LOG.Debug("独立IPv4已绑定到宿主机接口，无需添加",
			zap.String("ip", cleanIP))
		return nil
	}

	global.APP_LOG.Debug("独立IPv4未绑定到宿主机接口，正在自动添加",
		zap.String("ip", cleanIP))

	// 获取宿主机出口网络接口（具有默认路由的接口）
	getPrimaryIfaceCmd := `ip route get 1.1.1.1 2>/dev/null | awk '{for(i=1;i<=NF;i++){if($i=="dev"){print $(i+1);exit}}}'`
	ifaceOutput, ifaceErr := i.sshClient.Execute(getPrimaryIfaceCmd)
	primaryIface := strings.TrimSpace(ifaceOutput)
	if ifaceErr != nil || primaryIface == "" {
		// 回退方案：取第一个全局 IPv4 地址所在接口（排除 loopback 与链路本地地址）
		fallbackCmd := `ip -o -4 addr show up | awk '$4!~/^127\./ && $4!~/^169\.254\./ {print $2; exit}'`
		fallbackOutput, fallbackErr := i.sshClient.Execute(fallbackCmd)
		if fallbackErr != nil || strings.TrimSpace(fallbackOutput) == "" {
			return fmt.Errorf("无法确定宿主机主网络接口，请手动将 %s/32 绑定到对应接口", cleanIP)
		}
		primaryIface = strings.TrimSpace(fallbackOutput)
	}

	// 以 /32 方式将独立 IPv4 添加到宿主机接口（路由模式，适合绝大多数云服务器场景）
	addCmd := fmt.Sprintf("ip addr add %s/32 dev %s", shellSingleQuote(cleanIP), shellSingleQuote(primaryIface))
	if _, addErr := i.sshClient.Execute(addCmd); addErr != nil {
		// 并发场景下可能已被其他操作添加，再次确认
		output2, checkErr2 := i.sshClient.Execute(checkCmd)
		if checkErr2 == nil && strings.Contains(output2, cleanIP) {
			global.APP_LOG.Debug("独立IPv4已由并发操作绑定，跳过",
				zap.String("ip", cleanIP))
			return nil
		}
		return fmt.Errorf("自动绑定独立IPv4 %s 到宿主机接口 %s 失败: %w", cleanIP, primaryIface, addErr)
	}

	global.APP_LOG.Debug("成功将独立IPv4绑定到宿主机接口",
		zap.String("ip", cleanIP),
		zap.String("interface", primaryIface))
	return nil
}
