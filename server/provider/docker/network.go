package docker

import (
	"fmt"
	"strings"

	"oneclickvirt/global"

	"go.uber.org/zap"
)

// ensureIPv4OnHostInterface 确保独立 IPv4 地址已绑定到宿主机网络接口。
// 若尚未绑定，则自动将其以 /32 路由模式添加到宿主机主出口接口。
// 这是使用独立 IPv4（dedicated_ipv4 / dedicated_ipv4_ipv6）创建实例的前置条件检查。
func (d *DockerProvider) ensureIPv4OnHostInterface(ipv4 string) error {
	if ipv4 == "" {
		return nil
	}

	// 清理 IP 地址格式（去除 CIDR 前缀、多余空格等）
	cleanIP := strings.TrimSpace(ipv4)
	if idx := strings.IndexByte(cleanIP, '/'); idx != -1 {
		cleanIP = cleanIP[:idx]
	}
	if cleanIP == "" {
		return nil
	}

	global.APP_LOG.Debug("检查独立IPv4是否已绑定到宿主机网络接口",
		zap.String("ip", cleanIP))

	// 检查该 IP 是否已绑定到宿主机的任意网络接口
	checkCmd := fmt.Sprintf("ip addr show | grep -w '%s'", cleanIP)
	output, err := d.sshClient.Execute(checkCmd)
	if err == nil && strings.Contains(output, cleanIP) {
		global.APP_LOG.Debug("独立IPv4已绑定到宿主机接口，无需添加",
			zap.String("ip", cleanIP))
		return nil
	}

	global.APP_LOG.Debug("独立IPv4未绑定到宿主机接口，正在自动添加",
		zap.String("ip", cleanIP))

	// 获取宿主机出口网络接口（具有默认路由的接口）
	getPrimaryIfaceCmd := `ip route get 1.1.1.1 2>/dev/null | awk '{for(i=1;i<=NF;i++){if($i=="dev"){print $(i+1);exit}}}'`
	ifaceOutput, ifaceErr := d.sshClient.Execute(getPrimaryIfaceCmd)
	primaryIface := strings.TrimSpace(ifaceOutput)
	if ifaceErr != nil || primaryIface == "" {
		// 回退方案：取第一个全局 IPv4 地址所在接口（排除 loopback 与链路本地地址）
		fallbackCmd := `ip -o -4 addr show up | awk '$4!~/^127\./ && $4!~/^169\.254\./ {print $2; exit}'`
		fallbackOutput, fallbackErr := d.sshClient.Execute(fallbackCmd)
		if fallbackErr != nil || strings.TrimSpace(fallbackOutput) == "" {
			return fmt.Errorf("无法确定宿主机主网络接口，请手动将 %s/32 绑定到对应接口", cleanIP)
		}
		primaryIface = strings.TrimSpace(fallbackOutput)
	}

	// 以 /32 方式将独立 IPv4 添加到宿主机接口（路由模式，适合绝大多数云服务器场景）
	addCmd := fmt.Sprintf("ip addr add %s/32 dev %s", cleanIP, primaryIface)
	if _, addErr := d.sshClient.Execute(addCmd); addErr != nil {
		// 并发场景下可能已被其他操作添加，再次确认
		output2, checkErr2 := d.sshClient.Execute(checkCmd)
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
