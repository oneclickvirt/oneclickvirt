package qemu

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"oneclickvirt/global"
	"oneclickvirt/provider"
	"oneclickvirt/utils"

	"go.uber.org/zap"
)

func (p *QEMUProvider) ListInstances(ctx context.Context) ([]provider.Instance, error) {
	if !p.connected {
		return nil, fmt.Errorf("not connected")
	}
	return p.sshListInstances(ctx)
}

func (p *QEMUProvider) CreateInstance(ctx context.Context, config provider.InstanceConfig) error {
	if !p.connected {
		return fmt.Errorf("not connected")
	}
	return p.sshCreateInstance(ctx, config, nil)
}

func (p *QEMUProvider) CreateInstanceWithProgress(ctx context.Context, config provider.InstanceConfig, progressCallback provider.ProgressCallback) error {
	if !p.connected {
		return fmt.Errorf("not connected")
	}
	return p.sshCreateInstance(ctx, config, progressCallback)
}

func (p *QEMUProvider) GetInstance(ctx context.Context, id string) (*provider.Instance, error) {
	if !p.connected {
		return nil, fmt.Errorf("not connected")
	}
	instances, err := p.sshListInstances(ctx)
	if err != nil {
		return nil, err
	}
	for _, inst := range instances {
		if inst.Name == id || inst.ID == id {
			return &inst, nil
		}
	}
	return nil, fmt.Errorf("instance %s not found", id)
}

// sshListInstances 通过virsh列出所有虚拟机
func (p *QEMUProvider) sshListInstances(ctx context.Context) ([]provider.Instance, error) {
	// virsh list --all 输出格式:
	//  Id   Name    State
	// ----------------------
	//  1    vm1     running
	//  -    vm2     shut off
	output, err := p.sshClient.Execute("virsh list --all --name 2>/dev/null | grep -v '^$'")
	if err != nil {
		return nil, fmt.Errorf("failed to list VMs: %w", err)
	}

	var instances []provider.Instance
	names := strings.Split(strings.TrimSpace(output), "\n")
	for _, name := range names {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		inst, err := p.getInstanceInfo(ctx, name)
		if err != nil {
			global.APP_LOG.Warn("获取VM信息失败", zap.String("name", name), zap.Error(err))
			continue
		}
		instances = append(instances, *inst)
	}
	return instances, nil
}

// getInstanceInfo 获取单个VM详细信息
func (p *QEMUProvider) getInstanceInfo(ctx context.Context, name string) (*provider.Instance, error) {
	// 获取dominfo
	output, err := p.sshClient.Execute(fmt.Sprintf("virsh dominfo '%s' 2>/dev/null", name))
	if err != nil {
		return nil, fmt.Errorf("failed to get VM info: %w", err)
	}

	inst := &provider.Instance{
		Name:   name,
		ID:     name,
		Type:   "vm",
		Status: "unknown",
	}

	// 解析dominfo
	for _, line := range strings.Split(output, "\n") {
		parts := strings.SplitN(line, ":", 2)
		if len(parts) != 2 {
			continue
		}
		key := strings.TrimSpace(parts[0])
		value := strings.TrimSpace(parts[1])

		switch key {
		case "State":
			inst.Status = mapVirshStatus(value)
		case "CPU(s)":
			inst.CPU = value
		case "Max memory":
			// 格式: "1048576 KiB" → 转为MB
			if memKB, err := parseKiBValue(value); err == nil {
				inst.Memory = fmt.Sprintf("%d MB", memKB/1024)
			}
		case "UUID":
			inst.ID = value
		}
	}

	// 尝试获取IP地址
	if ip := p.getVMIPAddress(ctx, name); ip != "" {
		inst.IP = ip
		inst.PrivateIP = ip
	}

	return inst, nil
}

// sshCreateInstance 创建QEMU虚拟机
func (p *QEMUProvider) sshCreateInstance(ctx context.Context, config provider.InstanceConfig, progressCallback provider.ProgressCallback) error {
	updateProgress := func(pct int, msg string) {
		if progressCallback != nil {
			progressCallback(pct, msg)
		}
		global.APP_LOG.Debug(msg, zap.String("instance", config.Name), zap.Int("progress", pct))
	}

	updateProgress(5, "开始创建QEMU虚拟机")

	// 解析资源配置
	cpu, _ := strconv.Atoi(config.CPU)
	if cpu <= 0 {
		cpu = 1
	}
	memoryMB, _ := strconv.Atoi(config.Memory)
	if memoryMB <= 0 {
		memoryMB = 512
	}
	diskGB, _ := strconv.Atoi(config.Disk)
	if diskGB <= 0 {
		diskGB = 10
	}

	// 获取密码
	password := "password"
	if pw, ok := config.Metadata["password"]; ok && pw != "" {
		password = pw
	}

	// 解析端口
	sshPort := 0
	startPort := 0
	endPort := 0
	if len(config.Ports) >= 1 {
		sshPort, _ = strconv.Atoi(config.Ports[0])
	}
	if len(config.Ports) >= 2 {
		startPort, _ = strconv.Atoi(config.Ports[1])
	}
	if len(config.Ports) >= 3 {
		endPort, _ = strconv.Atoi(config.Ports[2])
	}

	// 获取系统镜像名
	system := config.Image
	if system == "" {
		system = "debian12"
	}

	updateProgress(10, "检查并准备镜像")

	// 确保脚本可用
	if err := p.ensureScriptsAvailable(ctx); err != nil {
		return fmt.Errorf("failed to ensure scripts: %w", err)
	}

	updateProgress(20, "准备创建脚本")

	// 确保镜像目录存在
	p.sshClient.Execute(fmt.Sprintf("mkdir -p %s", ImageDir))
	p.sshClient.Execute(fmt.Sprintf("mkdir -p %s", VMLogDir))

	updateProgress(30, "执行虚拟机创建命令")

	// 使用 oneqemu.sh 脚本创建VM
	// 用法: ./oneqemu.sh <name> <cpu> <memory_mb> <disk_gb> <password> <sshport> <startport> <endport> [system]
	createCmd := fmt.Sprintf("cd %s && bash oneqemu.sh '%s' %d %d %d '%s' %d %d %d '%s' 2>&1",
		ScriptDir,
		config.Name,
		cpu,
		memoryMB,
		diskGB,
		password,
		sshPort,
		startPort,
		endPort,
		system,
	)

	output, err := p.sshClient.Execute(createCmd)
	if err != nil {
		global.APP_LOG.Error("QEMU虚拟机创建失败",
			zap.String("name", config.Name),
			zap.String("output", utils.TruncateString(output, 1000)),
			zap.Error(err))
		return fmt.Errorf("failed to create VM: %w", err)
	}

	updateProgress(80, "等待虚拟机启动")

	// 等待VM启动
	var vmStarted bool
	for i := 0; i < 30; i++ {
		statusOutput, err := p.sshClient.Execute(fmt.Sprintf("virsh domstate '%s' 2>/dev/null", config.Name))
		if err == nil && strings.Contains(strings.TrimSpace(statusOutput), "running") {
			vmStarted = true
			break
		}
		time.Sleep(2 * time.Second)
	}
	if !vmStarted {
		return fmt.Errorf("VM '%s' did not start within 60 seconds", config.Name)
	}

	updateProgress(95, "虚拟机创建完成")

	// 写入vmlog
	logCmd := fmt.Sprintf("echo 'name: %s, cpu: %d, memory: %dMB, disk: %dGB, sshport: %d, ports: %d-%d, system: %s' >> %s/%s.log",
		config.Name, cpu, memoryMB, diskGB, sshPort, startPort, endPort, system, VMLogDir, config.Name)
	p.sshClient.Execute(logCmd)

	updateProgress(100, "QEMU虚拟机创建完成")
	return nil
}

// ensureScriptsAvailable 确保创建脚本可用
func (p *QEMUProvider) ensureScriptsAvailable(ctx context.Context) error {
	// 检查 oneqemu.sh 是否存在
	output, err := p.sshClient.Execute(fmt.Sprintf("test -f %s/oneqemu.sh && echo 'exists' || echo 'missing'", ScriptDir))
	if err != nil {
		return err
	}
	if strings.TrimSpace(output) == "exists" {
		return nil
	}

	// 下载脚本
	global.APP_LOG.Info("下载QEMU创建脚本", zap.String("repo", ScriptRepo))
	downloadCmd := fmt.Sprintf(
		"curl -L https://raw.githubusercontent.com/%s/main/scripts/oneqemu.sh -o %s/oneqemu.sh && chmod +x %s/oneqemu.sh",
		ScriptRepo, ScriptDir, ScriptDir,
	)
	if _, err := p.sshClient.Execute(downloadCmd); err != nil {
		// 尝试备用下载
		downloadCmd = fmt.Sprintf(
			"wget -q -O %s/oneqemu.sh https://raw.githubusercontent.com/%s/main/scripts/oneqemu.sh && chmod +x %s/oneqemu.sh",
			ScriptDir, ScriptRepo, ScriptDir,
		)
		if _, err := p.sshClient.Execute(downloadCmd); err != nil {
			return fmt.Errorf("failed to download oneqemu.sh: %w", err)
		}
	}
	return nil
}

// getVMIPAddress 获取虚拟机IP地址
func (p *QEMUProvider) getVMIPAddress(ctx context.Context, name string) string {
	// 尝试通过 virsh domifaddr 获取
	output, err := p.sshClient.Execute(fmt.Sprintf("virsh domifaddr '%s' 2>/dev/null | grep -oP '192\\.168\\.122\\.\\d+'", name))
	if err == nil {
		ip := strings.TrimSpace(output)
		if ip != "" {
			return ip
		}
	}

	// 尝试从 vmlog 获取
	output, err = p.sshClient.Execute(fmt.Sprintf("cat %s/%s.log 2>/dev/null | grep -oP '192\\.168\\.122\\.\\d+' | head -1", VMLogDir, name))
	if err == nil {
		ip := strings.TrimSpace(output)
		if ip != "" {
			return ip
		}
	}

	return ""
}

// mapVirshStatus 将virsh状态映射到统一状态
func mapVirshStatus(status string) string {
	status = strings.ToLower(strings.TrimSpace(status))
	switch {
	case strings.Contains(status, "running"):
		return "running"
	case strings.Contains(status, "shut off"), strings.Contains(status, "shutoff"):
		return "stopped"
	case strings.Contains(status, "paused"):
		return "paused"
	case strings.Contains(status, "crashed"):
		return "error"
	case strings.Contains(status, "in shutdown"):
		return "stopping"
	default:
		return "unknown"
	}
}

// parseKiBValue 解析 "1048576 KiB" 格式的值为KB数值
func parseKiBValue(value string) (int64, error) {
	parts := strings.Fields(value)
	if len(parts) == 0 {
		return 0, fmt.Errorf("empty value")
	}
	return strconv.ParseInt(parts[0], 10, 64)
}
