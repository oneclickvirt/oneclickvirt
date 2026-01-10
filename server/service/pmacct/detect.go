package pmacct

import (
	"context"
	"fmt"
	"oneclickvirt/global"
	providerModel "oneclickvirt/model/provider"
	"oneclickvirt/provider"
	"oneclickvirt/utils"
	"regexp"
	"strings"
	"time"

	"go.uber.org/zap"
)

// checkPmacctVersion 检查pmacct版本是否满足最低要求（>= 1.7.0）
func (s *Service) checkPmacctVersion(providerInstance provider.Provider) error {
	ctx, cancel := context.WithTimeout(s.ctx, 10*time.Second)
	defer cancel()

	// 获取pmacct版本信息
	versionCmd := "pmacctd -V 2>&1 | head -1"
	output, err := providerInstance.ExecuteSSHCommand(ctx, versionCmd)
	if err != nil {
		return fmt.Errorf("failed to get pmacct version: %w", err)
	}

	output = strings.TrimSpace(output)
	global.APP_LOG.Info("检测到pmacct版本", zap.String("version_output", output))

	// 从输出中提取版本号
	// 示例输出: "pmacctd (1.7.8)"
	// 或: "pmacctd 1.7.8"
	version, err := s.parsePmacctVersion(output)
	if err != nil {
		return fmt.Errorf("failed to parse pmacct version: %w", err)
	}

	// 检查版本是否满足最低要求 (>= 1.7.0)
	// 项目使用的功能在 1.7.0 版本即可满足（aggregate, sql_optimize_clauses, SQLite 插件等）
	minVersion := []int{1, 7, 0}
	if !s.compareVersion(version, minVersion) {
		return fmt.Errorf("pmacct版本过低: 当前版本 %s, 最低要求 1.7.0", s.versionToString(version))
	}

	global.APP_LOG.Info("pmacct版本符合要求",
		zap.String("current_version", s.versionToString(version)),
		zap.String("min_version", "1.7.0"))

	return nil
}

// detectNetworkInterface 检测宿主机的主网络接口
func (s *Service) detectNetworkInterface(providerInstance provider.Provider) (string, error) {
	// 尝试多种方法检测主网络接口
	// 方法1: 通过默认路由检测
	detectCmd := `
# 方法1: 通过默认路由获取主接口
DEFAULT_IF=$(ip route show default 2>/dev/null | awk '/default/ {print $5; exit}')
if [ -n "$DEFAULT_IF" ]; then
    echo "$DEFAULT_IF"
    exit 0
fi

# 方法2: 获取第一个非lo的活动接口
ACTIVE_IF=$(ip link show 2>/dev/null | grep -E '^[0-9]+: ' | grep -v 'lo:' | grep 'state UP' | head -n1 | awk -F': ' '{print $2}')
if [ -n "$ACTIVE_IF" ]; then
    echo "$ACTIVE_IF"
    exit 0
fi

# 方法3: 使用ifconfig（旧系统兼容）
if command -v ifconfig >/dev/null 2>&1; then
    IFCONFIG_IF=$(ifconfig 2>/dev/null | grep -E '^[a-z0-9]+' | grep -v '^lo' | head -n1 | awk '{print $1}' | sed 's/:$//')
    if [ -n "$IFCONFIG_IF" ]; then
        echo "$IFCONFIG_IF"
        exit 0
    fi
fi

# 方法4: 直接列出所有网络接口（排除lo、docker、veth等虚拟接口）
ALL_IF=$(ls /sys/class/net 2>/dev/null | grep -v '^lo$' | grep -v '^docker' | grep -v '^veth' | grep -v '^br-' | head -n1)
if [ -n "$ALL_IF" ]; then
    echo "$ALL_IF"
    exit 0
fi

# 如果都失败了，返回错误
echo "eth0"  # 使用默认值作为后备
`

	ctx, cancel := context.WithTimeout(s.ctx, 30*time.Second)
	defer cancel()

	output, err := providerInstance.ExecuteSSHCommand(ctx, detectCmd)
	if err != nil {
		global.APP_LOG.Warn("检测网络接口失败，使用默认值eth0", zap.Error(err))
		return "eth0", nil // 返回默认值而不是错误
	}

	networkInterface := utils.CleanCommandOutput(output)
	if networkInterface == "" {
		global.APP_LOG.Warn("检测到空接口名，使用默认值eth0")
		return "eth0", nil
	}

	// 验证接口名称格式（只包含字母、数字、下划线、点、短横线）
	if matched, _ := regexp.MatchString(`^[a-zA-Z0-9._-]+$`, networkInterface); !matched {
		global.APP_LOG.Warn("检测到的接口名称格式不正确，使用默认值eth0",
			zap.String("detected", networkInterface))
		return "eth0", nil
	}

	return networkInterface, nil
}

// verifyInterfaceExists 验证网络接口是否存在于宿主机上
// 用于检查数据库中保存的网卡是否仍然有效（避免容器重启后网卡名变化）
func (s *Service) verifyInterfaceExists(providerInstance provider.Provider, interfaceName string) bool {
	if interfaceName == "" {
		return false
	}

	// 执行快速检查命令
	checkCmd := fmt.Sprintf("ip link show %s >/dev/null 2>&1 && echo 'EXISTS' || echo 'NOT_FOUND'", interfaceName)

	ctx, cancel := context.WithTimeout(s.ctx, 5*time.Second)
	defer cancel()

	output, err := providerInstance.ExecuteSSHCommand(ctx, checkCmd)
	if err != nil {
		global.APP_LOG.Warn("验证网络接口存在性时执行命令失败",
			zap.String("interface", interfaceName),
			zap.Error(err))
		return false
	}

	output = utils.CleanCommandOutput(output)
	exists := strings.Contains(output, "EXISTS")

	if !exists {
		global.APP_LOG.Warn("网络接口已不存在",
			zap.String("interface", interfaceName))
	}

	return exists
}

// detectVethInterface 检测容器对应的veth接口（用于Docker/LXD/Incus）
// 对于LXD/Incus，优先使用config show方法获取volatile.eth0.host_name
func (s *Service) detectVethInterface(providerInstance provider.Provider, instanceName string) (string, error) {
	providerType := providerInstance.GetType()

	// 对于LXD/Incus，优先使用Provider的GetVethInterfaceName方法
	if providerType == "lxd" {
		if lxdProv, ok := providerInstance.(interface {
			GetVethInterfaceName(string) (string, error)
		}); ok {
			vethName, err := lxdProv.GetVethInterfaceName(instanceName)
			if err == nil && vethName != "" {
				global.APP_LOG.Info("通过LXD Provider方法成功获取veth接口",
					zap.String("instance", instanceName),
					zap.String("veth", vethName))
				return vethName, nil
			}
			global.APP_LOG.Warn("LXD Provider方法获取veth接口失败，使用备用方法",
				zap.String("instance", instanceName),
				zap.Error(err))
		}
	} else if providerType == "incus" {
		if incusProv, ok := providerInstance.(interface {
			GetVethInterfaceName(context.Context, string) (string, error)
		}); ok {
			ctx, cancel := context.WithTimeout(s.ctx, 10*time.Second)
			defer cancel()
			vethName, err := incusProv.GetVethInterfaceName(ctx, instanceName)
			if err == nil && vethName != "" {
				global.APP_LOG.Info("通过Incus Provider方法成功获取veth接口",
					zap.String("instance", instanceName),
					zap.String("veth", vethName))
				return vethName, nil
			}
			global.APP_LOG.Warn("Incus Provider方法获取veth接口失败，使用备用方法",
				zap.String("instance", instanceName),
				zap.Error(err))
		}
	}

	// 备用方法：通过进程和网络命名空间检测（适用于所有虚拟化类型）
	var detectCmd string
	if providerType == "docker" {
		// Docker容器veth接口检测
		detectCmd = fmt.Sprintf(`
# 检测Docker容器对应的veth接口
CONTAINER_NAME='%s'

# 1. 获取容器PID
CONTAINER_PID=$(docker inspect -f '{{.State.Pid}}' "$CONTAINER_NAME" 2>/dev/null)
if [ -z "$CONTAINER_PID" ] || [ "$CONTAINER_PID" = "0" ]; then
    echo "ERROR: 容器未运行或PID为0" >&2
    exit 1
fi

# 2. 获取容器内eth0的peer ifindex（即宿主机上对应的veth接口的ifindex）
# 容器内的 eth0@ifXXX 中的 ifXXX 就是宿主机上veth的ifindex
HOST_VETH_IFINDEX=$(nsenter -t $CONTAINER_PID -n ip link show eth0 2>/dev/null | head -n1 | sed -n 's/.*@if\([0-9]\+\).*/\1/p')
if [ -z "$HOST_VETH_IFINDEX" ]; then
    echo "ERROR: 无法获取宿主机veth接口索引" >&2
    exit 1
fi

# 3. 在宿主机上根据ifindex找到对应的veth接口名称
VETH_NAME=$(ip -o link show 2>/dev/null | awk -v idx="$HOST_VETH_IFINDEX" -F': ' '$1 == idx {print $2}' | cut -d'@' -f1)

if [ -n "$VETH_NAME" ]; then
    # 验证这确实是一个veth接口
    if echo "$VETH_NAME" | grep -q "^veth"; then
        echo "$VETH_NAME"
        exit 0
    fi
fi

echo "ERROR: 无法找到有效的veth接口" >&2
exit 1
`, instanceName)
	} else if providerType == "lxd" || providerType == "incus" {
		// LXD/Incus容器veth接口检测（备用方法）
		cmd := "lxc"
		if providerType == "incus" {
			cmd = "incus"
		}
		detectCmd = fmt.Sprintf(`
# 检测LXD/Incus容器对应的veth接口
CONTAINER_NAME='%s'

# 1. 获取容器PID
CONTAINER_PID=$(%s info "$CONTAINER_NAME" 2>/dev/null | grep -i 'PID:' | awk '{print $2}')
if [ -z "$CONTAINER_PID" ] || [ "$CONTAINER_PID" = "0" ]; then
    echo "ERROR: 容器未运行或PID为0" >&2
    exit 1
fi

# 2. 获取容器内eth0的peer ifindex（即宿主机上对应的veth接口的ifindex）
# 容器内的 eth0@ifXXX 中的 ifXXX 就是宿主机上veth的ifindex
HOST_VETH_IFINDEX=$(nsenter -t $CONTAINER_PID -n ip link show eth0 2>/dev/null | head -n1 | sed -n 's/.*@if\([0-9]\+\).*/\1/p')
if [ -z "$HOST_VETH_IFINDEX" ]; then
    echo "ERROR: 无法获取宿主机veth接口索引" >&2
    exit 1
fi

# 3. 在宿主机上根据ifindex找到对应的veth接口名称
VETH_NAME=$(ip -o link show 2>/dev/null | awk -v idx="$HOST_VETH_IFINDEX" -F': ' '$1 == idx {print $2}' | cut -d'@' -f1)

if [ -n "$VETH_NAME" ]; then
    # 验证这确实是一个veth接口
    if echo "$VETH_NAME" | grep -q "^veth"; then
        echo "$VETH_NAME"
        exit 0
    fi
fi

echo "ERROR: 无法找到有效的veth接口" >&2
exit 1
`, instanceName, cmd)
	} else {
		return "", fmt.Errorf("unsupported provider type for veth detection: %s", providerType)
	}

	ctx, cancel := context.WithTimeout(s.ctx, 30*time.Second)
	defer cancel()

	output, err := providerInstance.ExecuteSSHCommand(ctx, detectCmd)
	if err != nil {
		global.APP_LOG.Error("执行veth检测命令失败",
			zap.String("instance", instanceName),
			zap.String("providerType", providerType),
			zap.Error(err),
			zap.String("output", output))
		return "", fmt.Errorf("failed to execute veth detection command: %w", err)
	}

	vethName := utils.CleanCommandOutput(output)
	if vethName == "" || strings.HasPrefix(vethName, "ERROR:") {
		return "", fmt.Errorf("无法检测容器 %s 的veth接口: %s", instanceName, vethName)
	}

	// 验证veth接口名称格式
	if matched, _ := regexp.MatchString(`^veth[a-zA-Z0-9]+$`, vethName); !matched {
		global.APP_LOG.Warn("检测到的veth接口名称格式不正确",
			zap.String("instance", instanceName),
			zap.String("detected", vethName))
		return "", fmt.Errorf("invalid veth interface name: %s", vethName)
	}

	global.APP_LOG.Info("成功检测到容器veth接口",
		zap.String("instance", instanceName),
		zap.String("providerType", providerType),
		zap.String("veth", vethName))

	return vethName, nil
}

// DetectProxmoxNetworkInterface 导出方法，供Proxmox Provider调用
// 检测 Proxmox VE 实例的网络接口
// 根据接口命名规则精确识别：
// - LXC容器：veth<ctid>i0 格式（如 veth178i0）
// - KVM虚拟机：tap<vmid>i0 格式（如 tap101i0）
func (s *Service) DetectProxmoxNetworkInterface(providerInstance provider.Provider, instanceName string, instanceID string) (string, error) {
	return s.detectProxmoxNetworkInterface(providerInstance, instanceName, instanceID)
}

// detectProxmoxNetworkInterface 检测 Proxmox VE 实例的网络接口（内部方法）
// 根据接口命名规则精确识别：
// - LXC容器：veth<ctid>i0 格式（如 veth178i0）
// - KVM虚拟机：tap<vmid>i0 格式（如 tap101i0）
func (s *Service) detectProxmoxNetworkInterface(providerInstance provider.Provider, instanceName string, instanceID string) (string, error) {
	global.APP_LOG.Info("开始检测Proxmox网络接口",
		zap.String("instance", instanceName),
		zap.String("instanceID", instanceID))

	// Proxmox 实例命名格式检测
	// LXC: veth<ctid>i0 (容器)
	// KVM: tap<vmid>i0 (虚拟机)
	detectCmd := fmt.Sprintf(`
# 检测 Proxmox 网络接口
# 参数: 实例ID
INSTANCE_ID='%s'

# 方法1: 通过接口名直接匹配
# LXC 容器: veth<ctid>i0
if ip link show veth${INSTANCE_ID}i0 >/dev/null 2>&1; then
    echo "veth${INSTANCE_ID}i0"
    exit 0
fi

# KVM 虚拟机: tap<vmid>i0
if ip link show tap${INSTANCE_ID}i0 >/dev/null 2>&1; then
    echo "tap${INSTANCE_ID}i0"
    exit 0
fi

# 方法2: 通过 pct/qm 命令查询
# 检测是否为 LXC 容器
if command -v pct >/dev/null 2>&1; then
    if pct status ${INSTANCE_ID} >/dev/null 2>&1; then
        # 是容器，查找 veth 接口
        VETH=$(ip link | grep -o "veth${INSTANCE_ID}i[0-9]" | head -n1)
        if [ -n "$VETH" ]; then
            echo "$VETH"
            exit 0
        fi
    fi
fi

# 检测是否为 KVM 虚拟机
if command -v qm >/dev/null 2>&1; then
    if qm status ${INSTANCE_ID} >/dev/null 2>&1; then
        # 是虚拟机，查找 tap 接口
        TAP=$(ip link | grep -o "tap${INSTANCE_ID}i[0-9]" | head -n1)
        if [ -n "$TAP" ]; then
            echo "$TAP"
            exit 0
        fi
    fi
fi

# 方法3: 通过 bridge 查询
# 列出所有 veth/tap 接口，匹配实例ID
INTERFACE=$(bridge link | grep -E "(veth|tap)${INSTANCE_ID}i[0-9]" | awk '{print $2}' | cut -d'@' -f1 | head -n1)
if [ -n "$INTERFACE" ]; then
    echo "$INTERFACE"
    exit 0
fi

echo "ERROR: 无法找到实例 ${INSTANCE_ID} 的网络接口" >&2
exit 1
`, instanceID)

	ctx, cancel := context.WithTimeout(s.ctx, 30*time.Second)
	defer cancel()

	output, err := providerInstance.ExecuteSSHCommand(ctx, detectCmd)
	if err != nil {
		global.APP_LOG.Error("执行Proxmox网络接口检测命令失败",
			zap.String("instance", instanceName),
			zap.String("instanceID", instanceID),
			zap.Error(err),
			zap.String("output", output))
		return "", fmt.Errorf("failed to execute Proxmox interface detection: %w", err)
	}

	interfaceName := utils.CleanCommandOutput(output)
	if interfaceName == "" || strings.HasPrefix(interfaceName, "ERROR:") {
		return "", fmt.Errorf("无法检测Proxmox实例 %s (ID: %s) 的网络接口: %s", instanceName, instanceID, interfaceName)
	}

	// 验证接口名称格式
	// LXC: veth<ctid>i<n>
	// KVM: tap<vmid>i<n>
	if matched, _ := regexp.MatchString(`^(veth|tap)\d+i\d+$`, interfaceName); !matched {
		global.APP_LOG.Warn("检测到的Proxmox接口名称格式异常",
			zap.String("instance", instanceName),
			zap.String("instanceID", instanceID),
			zap.String("detected", interfaceName))
		return "", fmt.Errorf("invalid Proxmox interface name: %s", interfaceName)
	}

	// 判断接口类型
	interfaceType := "unknown"
	if strings.HasPrefix(interfaceName, "veth") {
		interfaceType = "LXC容器"
	} else if strings.HasPrefix(interfaceName, "tap") {
		interfaceType = "KVM虚拟机"
	}

	global.APP_LOG.Info("成功检测到Proxmox网络接口",
		zap.String("instance", instanceName),
		zap.String("instanceID", instanceID),
		zap.String("interface", interfaceName),
		zap.String("type", interfaceType))

	return interfaceName, nil
}

// detectProxmoxInterfaceByMAC 通过MAC地址匹配Proxmox网络接口
// 适用于无法通过实例ID直接匹配的场景
func (s *Service) detectProxmoxInterfaceByMAC(providerInstance provider.Provider, instanceName, macAddress string) (string, error) {
	if macAddress == "" {
		return "", fmt.Errorf("MAC地址为空")
	}

	global.APP_LOG.Info("通过MAC地址检测Proxmox网络接口",
		zap.String("instance", instanceName),
		zap.String("mac", macAddress))

	detectCmd := fmt.Sprintf(`
# 通过MAC地址查找对应的网络接口
MAC='%s'

# 查找具有该MAC地址的接口
INTERFACE=$(ip link | grep -B1 "$MAC" | head -n1 | awk '{print $2}' | cut -d':' -f1 | cut -d'@' -f1)

if [ -n "$INTERFACE" ]; then
    # 验证是 veth 或 tap 接口
    if echo "$INTERFACE" | grep -qE '^(veth|tap)'; then
        echo "$INTERFACE"
        exit 0
    fi
fi

echo "ERROR: 未找到MAC地址 $MAC 对应的veth/tap接口" >&2
exit 1
`, macAddress)

	ctx, cancel := context.WithTimeout(s.ctx, 15*time.Second)
	defer cancel()

	output, err := providerInstance.ExecuteSSHCommand(ctx, detectCmd)
	if err != nil {
		global.APP_LOG.Error("通过MAC地址检测接口失败",
			zap.String("instance", instanceName),
			zap.String("mac", macAddress),
			zap.Error(err))
		return "", fmt.Errorf("failed to detect interface by MAC: %w", err)
	}

	interfaceName := utils.CleanCommandOutput(output)
	if interfaceName == "" || strings.HasPrefix(interfaceName, "ERROR:") {
		return "", fmt.Errorf("无法通过MAC地址 %s 找到接口", macAddress)
	}

	global.APP_LOG.Info("通过MAC地址成功检测到网络接口",
		zap.String("instance", instanceName),
		zap.String("mac", macAddress),
		zap.String("interface", interfaceName))

	return interfaceName, nil
}

// NetworkInterfaceInfo 网络接口信息
type NetworkInterfaceInfo struct {
	IPv4Interface string // IPv4流量监控的网络接口
	IPv6Interface string // IPv6流量监控的网络接口（可能与IPv4相同或不同）
}

// detectNetworkInterfaces 检测支持IPv4和IPv6的网络接口
// 优先从数据库中获取已保存的接口信息，如果不存在则动态检测
// 对于容器(Docker/LXD/Incus): 优先检测veth接口，两个协议通常使用同一个veth
// 对于虚拟机(Proxmox): 使用主网络接口，可能有独立的IPv6接口
func (s *Service) detectNetworkInterfaces(providerInstance provider.Provider, instanceName string, instance *providerModel.Instance, hasIPv6 bool) (*NetworkInterfaceInfo, error) {
	providerType := providerInstance.GetType()
	info := &NetworkInterfaceInfo{}
	needRedetect := false

	// 优先从数据库中获取已保存的网络接口信息
	if instance.PmacctInterfaceV4 != "" {
		// 验证保存的网卡是否仍然存在（避免容器重启后网卡名变化）
		if s.verifyInterfaceExists(providerInstance, instance.PmacctInterfaceV4) {
			info.IPv4Interface = instance.PmacctInterfaceV4
			global.APP_LOG.Info("使用数据库中保存的IPv4网络接口（已验证存在）",
				zap.String("instance", instanceName),
				zap.String("interfaceV4", info.IPv4Interface))
		} else {
			global.APP_LOG.Warn("数据库中保存的IPv4网络接口已不存在，将重新检测",
				zap.String("instance", instanceName),
				zap.String("oldInterfaceV4", instance.PmacctInterfaceV4))
			needRedetect = true
		}
	}
	if hasIPv6 && instance.PmacctInterfaceV6 != "" {
		// 验证保存的网卡是否仍然存在
		if s.verifyInterfaceExists(providerInstance, instance.PmacctInterfaceV6) {
			info.IPv6Interface = instance.PmacctInterfaceV6
			global.APP_LOG.Info("使用数据库中保存的IPv6网络接口（已验证存在）",
				zap.String("instance", instanceName),
				zap.String("interfaceV6", info.IPv6Interface))
		} else {
			global.APP_LOG.Warn("数据库中保存的IPv6网络接口已不存在，将重新检测",
				zap.String("instance", instanceName),
				zap.String("oldInterfaceV6", instance.PmacctInterfaceV6))
			needRedetect = true
		}
	}

	// 如果数据库中已有完整的接口信息且都验证通过，直接返回
	if !needRedetect && info.IPv4Interface != "" && (!hasIPv6 || info.IPv6Interface != "") {
		return info, nil
	}

	// 否则进行动态检测
	global.APP_LOG.Info("数据库中无完整网络接口信息，开始动态检测",
		zap.String("instance", instanceName),
		zap.Bool("hasIPv6", hasIPv6))

	// Docker/LXD/Incus 容器: 优先检测veth接口
	if providerType == "docker" || providerType == "lxd" || providerType == "incus" {
		// 尝试检测veth接口
		vethInterface, err := s.detectVethInterface(providerInstance, instanceName)
		if err != nil {
			global.APP_LOG.Warn("检测veth接口失败，回退到主网络接口",
				zap.String("instance", instanceName),
				zap.String("providerType", providerType),
				zap.Error(err))
			// 回退到主网络接口
			mainInterface, err := s.detectNetworkInterface(providerInstance)
			if err != nil {
				return nil, fmt.Errorf("failed to detect network interface: %w", err)
			}
			if info.IPv4Interface == "" {
				info.IPv4Interface = mainInterface
			}
			if hasIPv6 && info.IPv6Interface == "" {
				info.IPv6Interface = mainInterface // 容器通常使用同一个接口
			}
		} else {
			// 容器的IPv4和IPv6流量通常经过同一个veth接口
			info.IPv4Interface = vethInterface
			if hasIPv6 {
				info.IPv6Interface = vethInterface
			}
		}
	} else if providerType == "proxmox" {
		// Proxmox VE: 使用专门的检测方法
		// 通过实例ID或MAC地址精确识别 veth/tap 接口
		var proxmoxInterface string
		var err error

		// 方法1: 通过实例ID检测（最可靠）
		// 假设 instanceName 包含 VMID/CTID
		// 例如: "vm-101" 或 "lxc-178" 或直接 "101"
		instanceID := s.extractProxmoxInstanceID(instanceName)
		if instanceID != "" {
			proxmoxInterface, err = s.detectProxmoxNetworkInterface(providerInstance, instanceName, instanceID)
			if err != nil {
				global.APP_LOG.Warn("通过实例ID检测Proxmox接口失败，尝试备用方法",
					zap.String("instance", instanceName),
					zap.String("instanceID", instanceID),
					zap.Error(err))
			}
		}

		// 方法2: 如果方法1失败，回退到主网络接口检测
		if proxmoxInterface == "" {
			global.APP_LOG.Info("使用通用方法检测Proxmox网络接口",
				zap.String("instance", instanceName))
			mainInterface, err := s.detectNetworkInterface(providerInstance)
			if err != nil {
				return nil, fmt.Errorf("failed to detect network interface: %w", err)
			}
			proxmoxInterface = mainInterface
		}

		if info.IPv4Interface == "" {
			info.IPv4Interface = proxmoxInterface
		}
		if hasIPv6 && info.IPv6Interface == "" {
			// Proxmox 的 IPv4 和 IPv6 通常使用同一个 tap/veth 接口
			info.IPv6Interface = proxmoxInterface
		}
	} else {
		// 其他虚拟化类型: 使用主网络接口
		mainInterface, err := s.detectNetworkInterface(providerInstance)
		if err != nil {
			return nil, fmt.Errorf("failed to detect network interface: %w", err)
		}
		if info.IPv4Interface == "" {
			info.IPv4Interface = mainInterface
		}
		if hasIPv6 && info.IPv6Interface == "" {
			info.IPv6Interface = mainInterface
		}
	}

	global.APP_LOG.Info("检测到网络接口",
		zap.String("instance", instanceName),
		zap.String("providerType", providerType),
		zap.String("ipv4Interface", info.IPv4Interface),
		zap.String("ipv6Interface", info.IPv6Interface),
		zap.Bool("hasIPv6", hasIPv6))

	return info, nil
}

// extractProxmoxInstanceID 从实例名称中提取 Proxmox VMID/CTID
// 支持的格式:
// - "vm-101" -> "101"
// - "lxc-178" -> "178"
// - "pve-101" -> "101"
// - "101" -> "101"
func (s *Service) extractProxmoxInstanceID(instanceName string) string {
	// 匹配模式: vm-<id>, lxc-<id>, pve-<id>, 或纯数字
	patterns := []string{
		`vm-(\d+)`,
		`lxc-(\d+)`,
		`pve-(\d+)`,
		`container-(\d+)`,
		`^(\d+)$`, // 纯数字
	}

	for _, pattern := range patterns {
		re := regexp.MustCompile(pattern)
		if matches := re.FindStringSubmatch(instanceName); len(matches) > 1 {
			global.APP_LOG.Debug("从实例名称提取到Proxmox ID",
				zap.String("instanceName", instanceName),
				zap.String("instanceID", matches[1]),
				zap.String("pattern", pattern))
			return matches[1]
		}
	}

	global.APP_LOG.Debug("无法从实例名称提取Proxmox ID",
		zap.String("instanceName", instanceName))
	return ""
}
