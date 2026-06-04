package pmacct

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"time"

	"oneclickvirt/global"
	"oneclickvirt/provider"
	"oneclickvirt/utils"

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
	global.APP_LOG.Debug("检测到pmacct版本", zap.String("version_output", output))

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

	global.APP_LOG.Debug("pmacct版本符合要求",
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
				global.APP_LOG.Debug("通过LXD Provider方法成功获取veth接口",
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
				global.APP_LOG.Debug("通过Incus Provider方法成功获取veth接口",
					zap.String("instance", instanceName),
					zap.String("veth", vethName))
				return vethName, nil
			}
			global.APP_LOG.Warn("Incus Provider方法获取veth接口失败，使用备用方法",
				zap.String("instance", instanceName),
				zap.Error(err))
		}
	}

	// 备用方法：通过进程和网络命名空间检测（适用于所有容器虚拟化类型）
	var detectCmd string
	var runtimeCmd string
	switch providerType {
	case "docker", "orbstack":
		runtimeCmd = "docker"
	case "podman":
		runtimeCmd = "podman"
	case "containerd":
		runtimeCmd = "nerdctl"
	case "lxd", "incus":
		// LXD/Incus 使用独立的检测逻辑
	default:
		return "", fmt.Errorf("unsupported provider type for veth detection: %s", providerType)
	}

	if utils.IsDockerFamilyProvider(providerType) {
		// Docker/Podman/Containerd/Orbstack 容器veth接口检测
		detectCmd = fmt.Sprintf(`
# 检测容器对应的veth接口
CONTAINER_NAME='%s'

# 1. 获取容器PID
CONTAINER_PID=$(%s inspect -f '{{.State.Pid}}' "$CONTAINER_NAME" 2>/dev/null)
if [ -z "$CONTAINER_PID" ] || [ "$CONTAINER_PID" = "0" ]; then
    echo "ERROR: 容器未运行或PID为0" >&2
    exit 1
fi

# 2. 获取容器内eth0的peer ifindex（即宿主机上对应的veth接口的ifindex）
HOST_VETH_IFINDEX=$(nsenter -t $CONTAINER_PID -n ip link show eth0 2>/dev/null | head -n1 | sed -n 's/.*@if\([0-9]\+\).*/\1/p')
if [ -z "$HOST_VETH_IFINDEX" ]; then
    echo "ERROR: 无法获取宿主机veth接口索引" >&2
    exit 1
fi

# 3. 在宿主机上根据ifindex找到对应的veth接口名称
VETH_NAME=$(ip -o link show 2>/dev/null | awk -v idx="$HOST_VETH_IFINDEX" -F': ' '$1 == idx {print $2}' | cut -d'@' -f1)

if [ -n "$VETH_NAME" ]; then
    echo "$VETH_NAME"
    exit 0
fi

echo "ERROR: 无法找到有效的veth接口" >&2
exit 1
`, instanceName, runtimeCmd)
	} else {
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

# 2. 获取容器内eth0的peer ifindex
HOST_VETH_IFINDEX=$(nsenter -t $CONTAINER_PID -n ip link show eth0 2>/dev/null | head -n1 | sed -n 's/.*@if\([0-9]\+\).*/\1/p')
if [ -z "$HOST_VETH_IFINDEX" ]; then
    echo "ERROR: 无法获取宿主机veth接口索引" >&2
    exit 1
fi

# 3. 在宿主机上根据ifindex找到对应的veth接口名称
VETH_NAME=$(ip -o link show 2>/dev/null | awk -v idx="$HOST_VETH_IFINDEX" -F': ' '$1 == idx {print $2}' | cut -d'@' -f1)

if [ -n "$VETH_NAME" ]; then
    echo "$VETH_NAME"
    exit 0
fi

echo "ERROR: 无法找到有效的veth接口" >&2
exit 1
`, instanceName, cmd)
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

	// 验证网络接口名称格式（允许 veth、eth、cali、br 等各种前缀）
	if matched, _ := regexp.MatchString(`^[a-zA-Z][a-zA-Z0-9._-]*$`, vethName); !matched {
		global.APP_LOG.Warn("检测到的网络接口名称格式不正确",
			zap.String("instance", instanceName),
			zap.String("detected", vethName))
		return "", fmt.Errorf("invalid network interface name: %s", vethName)
	}

	global.APP_LOG.Debug("成功检测到容器veth接口",
		zap.String("instance", instanceName),
		zap.String("providerType", providerType),
		zap.String("veth", vethName))

	return vethName, nil
}
