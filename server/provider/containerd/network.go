package containerd

import (
	"fmt"
	"strings"

	"oneclickvirt/global"

	"go.uber.org/zap"
)

// ensureIPv4OnHostInterface 确保独立 IPv4 地址已绑定到宿主机网络接口
func (c *ContainerdProvider) ensureIPv4OnHostInterface(ipv4 string) error {
	if ipv4 == "" {
		return nil
	}

	cleanIP := strings.TrimSpace(ipv4)
	if idx := strings.IndexByte(cleanIP, '/'); idx != -1 {
		cleanIP = cleanIP[:idx]
	}
	if cleanIP == "" {
		return nil
	}

	global.APP_LOG.Debug("检查独立IPv4是否已绑定到宿主机网络接口",
		zap.String("ip", cleanIP))

	checkCmd := fmt.Sprintf("ip addr show | grep -w '%s'", cleanIP)
	output, err := c.sshClient.Execute(checkCmd)
	if err == nil && strings.Contains(output, cleanIP) {
		return nil
	}

	getPrimaryIfaceCmd := `ip route get 1.1.1.1 2>/dev/null | awk '{for(i=1;i<=NF;i++){if($i=="dev"){print $(i+1);exit}}}'`
	ifaceOutput, ifaceErr := c.sshClient.Execute(getPrimaryIfaceCmd)
	primaryIface := strings.TrimSpace(ifaceOutput)
	if ifaceErr != nil || primaryIface == "" {
		fallbackCmd := `ip -o -4 addr show up | awk '$4!~/^127\./ && $4!~/^169\.254\./ {print $2; exit}'`
		fallbackOutput, fallbackErr := c.sshClient.Execute(fallbackCmd)
		if fallbackErr != nil || strings.TrimSpace(fallbackOutput) == "" {
			return fmt.Errorf("无法确定宿主机主网络接口，请手动将 %s/32 绑定到对应接口", cleanIP)
		}
		primaryIface = strings.TrimSpace(fallbackOutput)
	}

	addCmd := fmt.Sprintf("ip addr add %s/32 dev %s", cleanIP, primaryIface)
	if _, addErr := c.sshClient.Execute(addCmd); addErr != nil {
		output2, checkErr2 := c.sshClient.Execute(checkCmd)
		if checkErr2 == nil && strings.Contains(output2, cleanIP) {
			return nil
		}
		return fmt.Errorf("自动绑定独立IPv4 %s 到宿主机接口 %s 失败: %w", cleanIP, primaryIface, addErr)
	}

	global.APP_LOG.Debug("成功将独立IPv4绑定到宿主机接口",
		zap.String("ip", cleanIP),
		zap.String("interface", primaryIface))
	return nil
}
