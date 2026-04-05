package kubevirt

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"oneclickvirt/global"
	"oneclickvirt/provider"
	"oneclickvirt/utils"

	"go.uber.org/zap"
)

func (p *KubeVirtProvider) ListInstances(ctx context.Context) ([]provider.Instance, error) {
	if !p.connected {
		return nil, fmt.Errorf("not connected")
	}
	return p.sshListInstances(ctx)
}

func (p *KubeVirtProvider) CreateInstance(ctx context.Context, config provider.InstanceConfig) error {
	if !p.connected {
		return fmt.Errorf("not connected")
	}
	return p.sshCreateInstance(ctx, config, nil)
}

func (p *KubeVirtProvider) CreateInstanceWithProgress(ctx context.Context, config provider.InstanceConfig, progressCallback provider.ProgressCallback) error {
	if !p.connected {
		return fmt.Errorf("not connected")
	}
	return p.sshCreateInstance(ctx, config, progressCallback)
}

func (p *KubeVirtProvider) GetInstance(ctx context.Context, id string) (*provider.Instance, error) {
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

// vmStatusJSON kubectl get vmi 的JSON输出结构
type vmStatusJSON struct {
	Items []struct {
		Metadata struct {
			Name string `json:"name"`
			UID  string `json:"uid"`
		} `json:"metadata"`
		Status struct {
			Phase string `json:"phase"`
		} `json:"status"`
	} `json:"items"`
}

// sshListInstances 通过kubectl列出所有KubeVirt虚拟机
func (p *KubeVirtProvider) sshListInstances(ctx context.Context) ([]provider.Instance, error) {
	// 获取所有VM (VirtualMachine 资源)
	output, err := p.sshClient.Execute(fmt.Sprintf(
		"kubectl get vm -n %s -o json 2>/dev/null", Namespace))
	if err != nil {
		return nil, fmt.Errorf("failed to list VMs: %w", err)
	}

	var result struct {
		Items []struct {
			Metadata struct {
				Name string `json:"name"`
				UID  string `json:"uid"`
			} `json:"metadata"`
			Spec struct {
				Running  *bool `json:"running"`
				Template struct {
					Spec struct {
						Domain struct {
							CPU struct {
								Cores int `json:"cores"`
							} `json:"cpu"`
							Resources struct {
								Requests struct {
									Memory string `json:"memory"`
								} `json:"requests"`
							} `json:"resources"`
						} `json:"domain"`
					} `json:"spec"`
				} `json:"template"`
			} `json:"spec"`
			Status struct {
				Ready           bool   `json:"ready"`
				PrintableStatus string `json:"printableStatus"`
			} `json:"status"`
		} `json:"items"`
	}

	if err := json.Unmarshal([]byte(output), &result); err != nil {
		return nil, fmt.Errorf("failed to parse VM list: %w", err)
	}

	var instances []provider.Instance
	for _, item := range result.Items {
		status := mapKubeVirtStatus(item.Status.PrintableStatus)

		inst := provider.Instance{
			ID:     item.Metadata.UID,
			Name:   item.Metadata.Name,
			Type:   "vm",
			Status: status,
			CPU:    fmt.Sprintf("%d", item.Spec.Template.Spec.Domain.CPU.Cores),
			Memory: item.Spec.Template.Spec.Domain.Resources.Requests.Memory,
		}

		instances = append(instances, inst)
	}

	return instances, nil
}

// sshCreateInstance 创建KubeVirt虚拟机
func (p *KubeVirtProvider) sshCreateInstance(ctx context.Context, config provider.InstanceConfig, progressCallback provider.ProgressCallback) error {
	updateProgress := func(pct int, msg string) {
		if progressCallback != nil {
			progressCallback(pct, msg)
		}
		global.APP_LOG.Debug(msg, zap.String("instance", config.Name), zap.Int("progress", pct))
	}

	updateProgress(5, "开始创建KubeVirt虚拟机")

	// 解析资源配置
	cpu, _ := strconv.Atoi(config.CPU)
	if cpu <= 0 {
		cpu = 1
	}
	memoryGB, _ := strconv.Atoi(config.Memory)
	if memoryGB <= 0 {
		memoryGB = 1
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
		system = "ubuntu22"
	}

	updateProgress(10, "检查并准备镜像")

	// 确保脚本可用
	if err := p.ensureScriptsAvailable(ctx); err != nil {
		return fmt.Errorf("failed to ensure scripts: %w", err)
	}

	updateProgress(20, "准备创建脚本")

	// 确保命名空间存在
	p.sshClient.Execute(fmt.Sprintf("kubectl create namespace %s 2>/dev/null || true", Namespace))
	p.sshClient.Execute(fmt.Sprintf("mkdir -p %s", VMLogDir))

	updateProgress(30, "执行虚拟机创建命令")

	// 使用 onevm.sh 脚本创建VM
	// 用法: ./onevm.sh <name> <cpu> <memory_gb> <disk_gb> <password> <sshport> <startport> <endport> [system]
	createCmd := fmt.Sprintf("cd %s && bash onevm.sh '%s' %d %d %d '%s' %d %d %d '%s' 2>&1",
		ScriptDir,
		config.Name,
		cpu,
		memoryGB,
		diskGB,
		password,
		sshPort,
		startPort,
		endPort,
		system,
	)

	output, err := p.sshClient.Execute(createCmd)
	if err != nil {
		global.APP_LOG.Error("KubeVirt虚拟机创建失败",
			zap.String("name", config.Name),
			zap.String("output", utils.TruncateString(output, 1000)),
			zap.Error(err))
		return fmt.Errorf("failed to create VM: %w", err)
	}

	updateProgress(80, "等待虚拟机启动")

	// 等待VM进入Running状态
	var vmStarted bool
	for i := 0; i < 60; i++ {
		statusOutput, err := p.sshClient.Execute(fmt.Sprintf(
			"kubectl get vmi '%s' -n %s -o jsonpath='{.status.phase}' 2>/dev/null", config.Name, Namespace))
		if err == nil && strings.TrimSpace(statusOutput) == "Running" {
			vmStarted = true
			break
		}
		time.Sleep(3 * time.Second)
	}
	if !vmStarted {
		return fmt.Errorf("VM '%s' did not reach Running state within 180 seconds", config.Name)
	}

	updateProgress(95, "虚拟机创建完成")

	// 写入vmlog
	logCmd := fmt.Sprintf("echo 'name: %s, cpu: %d, memory: %dGB, disk: %dGB, sshport: %d, ports: %d-%d, system: %s' >> %s/%s.log",
		config.Name, cpu, memoryGB, diskGB, sshPort, startPort, endPort, system, VMLogDir, config.Name)
	p.sshClient.Execute(logCmd)

	updateProgress(100, "KubeVirt虚拟机创建完成")
	return nil
}

// ensureScriptsAvailable 确保创建脚本可用
func (p *KubeVirtProvider) ensureScriptsAvailable(ctx context.Context) error {
	output, err := p.sshClient.Execute(fmt.Sprintf("test -f %s/onevm.sh && echo 'exists' || echo 'missing'", ScriptDir))
	if err != nil {
		return err
	}
	if strings.TrimSpace(output) == "exists" {
		return nil
	}

	// 下载脚本
	global.APP_LOG.Info("下载KubeVirt创建脚本", zap.String("repo", ScriptRepo))
	downloadCmd := fmt.Sprintf(
		"curl -L https://raw.githubusercontent.com/%s/main/scripts/onevm.sh -o %s/onevm.sh && chmod +x %s/onevm.sh",
		ScriptRepo, ScriptDir, ScriptDir,
	)
	if _, err := p.sshClient.Execute(downloadCmd); err != nil {
		downloadCmd = fmt.Sprintf(
			"wget -q -O %s/onevm.sh https://raw.githubusercontent.com/%s/main/scripts/onevm.sh && chmod +x %s/onevm.sh",
			ScriptDir, ScriptRepo, ScriptDir,
		)
		if _, err := p.sshClient.Execute(downloadCmd); err != nil {
			return fmt.Errorf("failed to download onevm.sh: %w", err)
		}
	}
	return nil
}

// mapKubeVirtStatus 映射KubeVirt状态
func mapKubeVirtStatus(status string) string {
	status = strings.ToLower(strings.TrimSpace(status))
	switch {
	case strings.Contains(status, "running"):
		return "running"
	case strings.Contains(status, "stopped"):
		return "stopped"
	case strings.Contains(status, "starting"):
		return "starting"
	case strings.Contains(status, "provisioning"):
		return "creating"
	case strings.Contains(status, "terminating"):
		return "stopping"
	case strings.Contains(status, "error"), strings.Contains(status, "crash"):
		return "error"
	case strings.Contains(status, "paused"):
		return "paused"
	default:
		return "unknown"
	}
}
