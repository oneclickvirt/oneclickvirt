package pmacct

import (
	"context"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"oneclickvirt/global"
	"oneclickvirt/provider"

	"go.uber.org/zap"
)

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

# 确保pmacct默认服务停止（将手动管理配置）
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
