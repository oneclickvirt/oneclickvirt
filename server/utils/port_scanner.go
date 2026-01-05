package utils

import (
	"bufio"
	"context"
	"fmt"
	"oneclickvirt/global"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"

	"go.uber.org/zap"
)

// PortScannerType 表示使用的端口扫描工具类型
type PortScannerType string

const (
	ScannerSS      PortScannerType = "ss"      // 优先使用ss命令（现代Linux）
	ScannerNetstat PortScannerType = "netstat" // 备选netstat命令（兼容旧系统）
)

// PortScanResult 端口扫描结果
type PortScanResult struct {
	OccupiedPorts map[int]bool // 被占用的端口集合
	ScannerType   PortScannerType
	Error         error
}

// detectPortScannerOnHost 在远程主机上检测可用的端口扫描工具
// 参数: sshClient - SSH客户端（如果为nil则在本地检测）
func detectPortScannerOnHost(sshClient *SSHClient) PortScannerType {
	// 优先检测ss命令
	checkCmd := "command -v ss >/dev/null 2>&1 && echo 'found' || echo 'notfound'"
	var output string
	var err error

	if sshClient != nil {
		output, err = sshClient.Execute(checkCmd)
	} else {
		output, err = ExecuteShellCommand(checkCmd, 5*time.Second)
	}

	if err == nil && strings.TrimSpace(output) == "found" {
		return ScannerSS
	}

	// 检测netstat命令
	checkCmd = "command -v netstat >/dev/null 2>&1 && echo 'found' || echo 'notfound'"
	if sshClient != nil {
		output, err = sshClient.Execute(checkCmd)
	} else {
		output, err = ExecuteShellCommand(checkCmd, 5*time.Second)
	}

	if err == nil && strings.TrimSpace(output) == "found" {
		return ScannerNetstat
	}

	// 如果都没有，尝试自动安装
	global.APP_LOG.Warn("远程系统中未找到ss或netstat命令，尝试自动安装")
	if installNetworkToolsOnHost(sshClient) {
		// 安装后再次检测ss
		checkCmd = "command -v ss >/dev/null 2>&1 && echo 'found' || echo 'notfound'"
		if sshClient != nil {
			output, err = sshClient.Execute(checkCmd)
		} else {
			output, err = ExecuteShellCommand(checkCmd, 5*time.Second)
		}
		if err == nil && strings.TrimSpace(output) == "found" {
			return ScannerSS
		}

		// 检测netstat
		checkCmd = "command -v netstat >/dev/null 2>&1 && echo 'found' || echo 'notfound'"
		if sshClient != nil {
			output, err = sshClient.Execute(checkCmd)
		} else {
			output, err = ExecuteShellCommand(checkCmd, 5*time.Second)
		}
		if err == nil && strings.TrimSpace(output) == "found" {
			return ScannerNetstat
		}
	}

	return ""
}

// installNetworkToolsOnHost 在远程主机上自动安装网络工具
// 支持 Debian/Ubuntu/RHEL/CentOS/Fedora/RockyLinux/AlmaLinux/Alpine/Arch 系统
func installNetworkToolsOnHost(sshClient *SSHClient) bool {
	// 检测包管理器并安装对应的网络工具包
	installCommands := []struct {
		detector string // 检测命令
		install  string // 安装命令
		desc     string // 描述
	}{
		// Debian/Ubuntu系列
		{
			detector: "command -v apt-get >/dev/null 2>&1 && echo 'found' || echo 'notfound'",
			install:  "apt-get update -qq && apt-get install -y -qq iproute2 net-tools 2>&1",
			desc:     "Debian/Ubuntu",
		},
		// RHEL/CentOS/RockyLinux/AlmaLinux系列 (使用yum)
		{
			detector: "command -v yum >/dev/null 2>&1 && echo 'found' || echo 'notfound'",
			install:  "yum install -y -q iproute net-tools 2>&1",
			desc:     "RHEL/CentOS/Rocky/Alma",
		},
		// Fedora系列 (使用dnf)
		{
			detector: "command -v dnf >/dev/null 2>&1 && echo 'found' || echo 'notfound'",
			install:  "dnf install -y -q iproute net-tools 2>&1",
			desc:     "Fedora",
		},
		// Alpine系列
		{
			detector: "command -v apk >/dev/null 2>&1 && echo 'found' || echo 'notfound'",
			install:  "apk add --no-cache iproute2 net-tools 2>&1",
			desc:     "Alpine",
		},
		// Arch系列
		{
			detector: "command -v pacman >/dev/null 2>&1 && echo 'found' || echo 'notfound'",
			install:  "pacman -S --noconfirm --needed iproute2 net-tools 2>&1",
			desc:     "Arch",
		},
	}

	for _, cmd := range installCommands {
		var output string
		var err error

		// 检测包管理器
		if sshClient != nil {
			output, err = sshClient.Execute(cmd.detector)
		} else {
			output, err = ExecuteShellCommand(cmd.detector, 5*time.Second)
		}

		if err == nil && strings.TrimSpace(output) == "found" {
			global.APP_LOG.Info("检测到包管理器，尝试安装网络工具", zap.String("distro", cmd.desc))

			// 执行安装命令
			if sshClient != nil {
				output, err = sshClient.Execute(cmd.install)
			} else {
				output, err = ExecuteShellCommand(cmd.install, 60*time.Second)
			}

			if err != nil {
				global.APP_LOG.Error("安装网络工具失败",
					zap.String("distro", cmd.desc),
					zap.Error(err),
					zap.String("output", output))
				continue
			}

			global.APP_LOG.Info("网络工具安装成功", zap.String("distro", cmd.desc))
			return true
		}
	}

	global.APP_LOG.Error("无法自动安装网络工具，请手动安装 iproute2 或 net-tools 包")
	return false
}

// BatchCheckPortsOccupied 批量检查宿主机上哪些端口被占用
// 使用ss或netstat命令一次性获取所有监听端口
// 参数: sshClient - SSH客户端（用于连接远程主机），ports - 需要检查的端口列表
// 返回: 被占用的端口集合
func BatchCheckPortsOccupied(sshClient *SSHClient, ports []int) *PortScanResult {
	if len(ports) == 0 {
		return &PortScanResult{
			OccupiedPorts: make(map[int]bool),
			Error:         nil,
		}
	}

	scanner := detectPortScannerOnHost(sshClient)
	if scanner == "" {
		global.APP_LOG.Error("未找到可用的端口扫描工具（ss或netstat）")
		return &PortScanResult{
			OccupiedPorts: make(map[int]bool),
			Error:         fmt.Errorf("未找到可用的端口扫描工具"),
		}
	}

	var result *PortScanResult
	switch scanner {
	case ScannerSS:
		result = batchCheckWithSS(sshClient, ports)
	case ScannerNetstat:
		result = batchCheckWithNetstat(sshClient, ports)
	}

	return result
}

// batchCheckWithSS 使用ss命令批量检查端口占用情况
func batchCheckWithSS(sshClient *SSHClient, ports []int) *PortScanResult {
	result := &PortScanResult{
		OccupiedPorts: make(map[int]bool),
		ScannerType:   ScannerSS,
	}

	// 构建ss命令
	// ss -tuln 显示所有TCP和UDP的监听端口（不解析域名）
	// -t: TCP, -u: UDP, -l: listening, -n: numeric（不解析服务名）
	cmd := "ss -tuln 2>/dev/null || ss -tln 2>/dev/null"

	var output string
	var err error
	if sshClient != nil {
		output, err = sshClient.Execute(cmd)
	} else {
		output, err = ExecuteShellCommand(cmd, 10*time.Second)
	}

	if err != nil {
		global.APP_LOG.Error("执行ss命令失败", zap.Error(err), zap.String("output", output))
		result.Error = fmt.Errorf("执行ss命令失败: %v", err)
		return result
	}

	// 解析ss输出
	// ss输出格式示例：
	// State   Recv-Q   Send-Q     Local Address:Port      Peer Address:Port
	// LISTEN  0        128              0.0.0.0:22             0.0.0.0:*
	// LISTEN  0        128                    *:80                   *:*

	occupiedPorts := parseSSOutput(output)

	// 筛选出关心的端口
	for _, port := range ports {
		if occupiedPorts[port] {
			result.OccupiedPorts[port] = true
		}
	}

	global.APP_LOG.Debug("使用ss命令批量检查端口完成",
		zap.Int("检查端口数", len(ports)),
		zap.Int("占用端口数", len(result.OccupiedPorts)))

	return result
}

// batchCheckWithNetstat 使用netstat命令批量检查端口占用情况
func batchCheckWithNetstat(sshClient *SSHClient, ports []int) *PortScanResult {
	result := &PortScanResult{
		OccupiedPorts: make(map[int]bool),
		ScannerType:   ScannerNetstat,
	}

	// 构建netstat命令
	// netstat -tuln 显示所有TCP和UDP的监听端口
	// -t: TCP, -u: UDP, -l: listening, -n: numeric
	cmd := "netstat -tuln 2>/dev/null || netstat -tln 2>/dev/null"

	var output string
	var err error
	if sshClient != nil {
		output, err = sshClient.Execute(cmd)
	} else {
		output, err = ExecuteShellCommand(cmd, 10*time.Second)
	}

	if err != nil {
		global.APP_LOG.Error("执行netstat命令失败", zap.Error(err), zap.String("output", output))
		result.Error = fmt.Errorf("执行netstat命令失败: %v", err)
		return result
	}

	// 解析netstat输出
	occupiedPorts := parseNetstatOutput(output)

	// 筛选出关心的端口
	for _, port := range ports {
		if occupiedPorts[port] {
			result.OccupiedPorts[port] = true
		}
	}

	global.APP_LOG.Debug("使用netstat命令批量检查端口完成",
		zap.Int("检查端口数", len(ports)),
		zap.Int("占用端口数", len(result.OccupiedPorts)))

	return result
}

// parseSSOutput 解析ss命令的输出
func parseSSOutput(output string) map[int]bool {
	occupiedPorts := make(map[int]bool)

	// 正则表达式匹配端口号
	// 匹配格式如: 0.0.0.0:22 或 *:80 或 :::8080
	portRegex := regexp.MustCompile(`[:\s](\d+)\s`)

	scanner := bufio.NewScanner(strings.NewReader(output))
	for scanner.Scan() {
		line := scanner.Text()

		// 跳过标题行
		if strings.Contains(line, "Local Address") || strings.Contains(line, "Netid") {
			continue
		}

		// 查找端口号
		matches := portRegex.FindAllStringSubmatch(line, -1)
		for _, match := range matches {
			if len(match) > 1 {
				if port, err := strconv.Atoi(match[1]); err == nil {
					occupiedPorts[port] = true
				}
			}
		}
	}

	return occupiedPorts
}

// parseNetstatOutput 解析netstat命令的输出
func parseNetstatOutput(output string) map[int]bool {
	occupiedPorts := make(map[int]bool)

	// 正则表达式匹配端口号
	// 匹配格式如: 0.0.0.0:22 或 :::8080
	portRegex := regexp.MustCompile(`[:\s](\d+)\s`)

	scanner := bufio.NewScanner(strings.NewReader(output))
	for scanner.Scan() {
		line := scanner.Text()

		// 跳过标题行
		if strings.Contains(line, "Local Address") || strings.Contains(line, "Proto") {
			continue
		}

		// 只处理LISTEN状态的行
		if !strings.Contains(line, "LISTEN") {
			continue
		}

		// 查找端口号
		matches := portRegex.FindAllStringSubmatch(line, -1)
		for _, match := range matches {
			if len(match) > 1 {
				if port, err := strconv.Atoi(match[1]); err == nil {
					occupiedPorts[port] = true
				}
			}
		}
	}

	return occupiedPorts
}

// CheckPortOccupiedOnHost 检查指定主机上的单个端口是否被占用
// 这是一个便捷方法，内部调用批量检查
// 参数: sshClient - SSH客户端（用于连接远程主机）
func CheckPortOccupiedOnHost(sshClient *SSHClient, port int) bool {
	result := BatchCheckPortsOccupied(sshClient, []int{port})
	if result.Error != nil {
		global.APP_LOG.Error("检查端口占用失败",
			zap.Int("port", port),
			zap.Error(result.Error))
		// 出错时保守返回已占用
		return true
	}
	return result.OccupiedPorts[port]
}

// GetAllListeningPorts 获取宿主机上所有监听的端口
// 参数: sshClient - SSH客户端（用于连接远程主机）
func GetAllListeningPorts(sshClient *SSHClient) ([]int, error) {
	scanner := detectPortScannerOnHost(sshClient)
	if scanner == "" {
		return nil, fmt.Errorf("未找到可用的端口扫描工具")
	}

	var cmd string
	if scanner == ScannerSS {
		cmd = "ss -tuln 2>/dev/null || ss -tln 2>/dev/null"
	} else {
		cmd = "netstat -tuln 2>/dev/null || netstat -tln 2>/dev/null"
	}

	var output string
	var err error
	if sshClient != nil {
		output, err = sshClient.Execute(cmd)
	} else {
		output, err = ExecuteShellCommand(cmd, 10*time.Second)
	}

	if err != nil {
		return nil, fmt.Errorf("执行命令失败: %v", err)
	}

	var occupiedPorts map[int]bool
	if scanner == ScannerSS {
		occupiedPorts = parseSSOutput(output)
	} else {
		occupiedPorts = parseNetstatOutput(output)
	}

	// 转换为端口列表
	ports := make([]int, 0, len(occupiedPorts))
	for port := range occupiedPorts {
		ports = append(ports, port)
	}

	return ports, nil
}

// ExecuteShellCommand 在本地执行shell命令（用于测试或本地操作）
func ExecuteShellCommand(command string, timeout time.Duration) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, "sh", "-c", command)
	output, err := cmd.CombinedOutput()

	return strings.TrimSpace(string(output)), err
}
