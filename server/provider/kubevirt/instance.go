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
	"oneclickvirt/provider/firewall"
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
		}
		// 将 K8s 内存字符串（如 "512Mi", "1Gi"）转换为 MB 整数字符串，与其他 Provider 保持一致
		rawMem := item.Spec.Template.Spec.Domain.Resources.Requests.Memory
		if memMB := parseMemoryString(rawMem); memMB > 0 {
			inst.Memory = fmt.Sprintf("%d", memMB)
		} else {
			inst.Memory = rawMem
		}

		instances = append(instances, inst)
	}

	return instances, nil
}

// sshCreateInstance 直接通过 kubectl 命令创建 KubeVirt 虚拟机（不依赖外部 shell 脚本）
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
	memoryMB := parseConfigMB(config.Memory)
	if memoryMB <= 0 {
		memoryMB = 512
	}
	diskGB := parseConfigGB(config.Disk)
	if diskGB <= 0 {
		diskGB = 10
	}

	password := "password"
	if pw, ok := config.Metadata["password"]; ok && pw != "" {
		password = pw
	}

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

	system := config.Image
	if system == "" {
		system = "ubuntu22"
	}

	updateProgress(10, "确保命名空间存在")

	// 确保命名空间和目录存在
	p.sshClient.Execute(fmt.Sprintf("kubectl create namespace %s 2>/dev/null || true", Namespace))
	p.sshClient.Execute(fmt.Sprintf("mkdir -p %s", VMLogDir))

	updateProgress(20, "解析镜像地址")

	// 直接使用 seed 数据库中的 ImageURL，对GitHub链接尝试CDN加速
	if config.ImageURL == "" {
		return fmt.Errorf("no image URL configured for system: %s", system)
	}
	resolvedURL := config.ImageURL
	if config.UseCDN && (strings.Contains(config.ImageURL, "github.com/") || strings.Contains(config.ImageURL, "raw.githubusercontent.com/")) {
		cdnURL := utils.GetCDNURL(p.sshClient, config.ImageURL, "KubeVirt")
		if cdnURL != "" {
			resolvedURL = cdnURL
		}
	}
	global.APP_LOG.Info("KubeVirt镜像地址已解析",
		zap.String("system", system),
		zap.String("url", utils.TruncateString(resolvedURL, 200)))

	updateProgress(25, "创建 CDI DataVolume")

	// 使用 CDI DataVolume 代替空 PVC，CDI 会自动从 HTTP URL 下载镜像到 PVC
	dvName := fmt.Sprintf("%s-dv", config.Name)
	dvYAML := fmt.Sprintf(`apiVersion: cdi.kubevirt.io/v1beta1
kind: DataVolume
metadata:
  name: %s
  namespace: %s
  labels:
    kubevirt.io/vm: "%s"
    app: kubevirt-vm
spec:
  source:
    http:
      url: "%s"
  storage:
    accessModes:
      - ReadWriteOnce
    resources:
      requests:
        storage: %dGi
    storageClassName: local-path`, dvName, Namespace, config.Name, resolvedURL, diskGB)

	// 先清理可能存在的同名 DataVolume
	p.sshClient.Execute(fmt.Sprintf("kubectl delete datavolume '%s' -n %s 2>/dev/null || true", dvName, Namespace))

	dvCmd := fmt.Sprintf("cat << 'DVEOF' | kubectl apply -f - 2>&1\n%s\nDVEOF", dvYAML)
	output, err := p.sshClient.Execute(dvCmd)
	if err != nil {
		global.APP_LOG.Error("DataVolume创建失败",
			zap.String("name", config.Name),
			zap.String("output", utils.TruncateString(output, 500)),
			zap.Error(err))
		return fmt.Errorf("failed to create DataVolume: %w", err)
	}

	updateProgress(30, "等待镜像导入完成")

	// 等待 DataVolume 导入完成（最长30分钟）
	var dvReady bool
	maxWait := 600 // 30分钟 / 3秒间隔
	for i := 0; i < maxWait; i++ {
		phaseOutput, phaseErr := p.sshClient.Execute(fmt.Sprintf(
			"kubectl get datavolume '%s' -n %s -o jsonpath='{.status.phase}' 2>/dev/null", dvName, Namespace))
		if phaseErr == nil {
			phase := strings.TrimSpace(phaseOutput)
			if phase == "Succeeded" {
				dvReady = true
				break
			}
			if phase == "Failed" {
				// 获取失败原因
				msgOutput, _ := p.sshClient.Execute(fmt.Sprintf(
					"kubectl get datavolume '%s' -n %s -o jsonpath='{.status.conditions[*].message}' 2>/dev/null", dvName, Namespace))
				p.sshClient.Execute(fmt.Sprintf("kubectl delete datavolume '%s' -n %s 2>/dev/null || true", dvName, Namespace))
				return fmt.Errorf("DataVolume import failed: %s", strings.TrimSpace(msgOutput))
			}
		}

		// 更新进度（30-50之间线性递增）
		if i%20 == 0 && i > 0 {
			pct := 30 + (i*20)/maxWait
			if pct > 50 {
				pct = 50
			}
			progressOutput, _ := p.sshClient.Execute(fmt.Sprintf(
				"kubectl get datavolume '%s' -n %s -o jsonpath='{.status.progress}' 2>/dev/null", dvName, Namespace))
			progress := strings.TrimSpace(progressOutput)
			if progress != "" {
				updateProgress(pct, fmt.Sprintf("镜像导入中 %s", progress))
			}
		}

		time.Sleep(3 * time.Second)
	}
	if !dvReady {
		p.sshClient.Execute(fmt.Sprintf("kubectl delete datavolume '%s' -n %s 2>/dev/null || true", dvName, Namespace))
		return fmt.Errorf("DataVolume import timed out for '%s' (30 minutes)", dvName)
	}

	updateProgress(55, "创建 VirtualMachine 资源")

	// 构建 VirtualMachine YAML（引用 DataVolume 而非 PVC）
	vmYAML := fmt.Sprintf(`apiVersion: kubevirt.io/v1
kind: VirtualMachine
metadata:
  name: %s
  namespace: %s
  labels:
    kubevirt.io/vm: "%s"
    app: kubevirt-vm
spec:
  running: true
  template:
    metadata:
      labels:
        kubevirt.io/vm: "%s"
        app: kubevirt-vm
    spec:
      domain:
        cpu:
          cores: %d
        resources:
          requests:
            memory: "%dMi"
        devices:
          disks:
            - name: datavolumedisk
              disk:
                bus: virtio
              bootOrder: 1
            - name: cloudinitdisk
              disk:
                bus: virtio
          interfaces:
            - name: default
              masquerade: {}
          rng: {}
      networks:
        - name: default
          pod: {}
      terminationGracePeriodSeconds: 30
      volumes:
        - name: datavolumedisk
          dataVolume:
            name: %s
        - name: cloudinitdisk
          cloudInitNoCloud:
            userData: |
              #cloud-config
              hostname: %s
              disable_root: false
              ssh_pwauth: true
              chpasswd:
                expire: false
                list:
                  - root:%s
              runcmd:
                - sed -i 's/^#*PermitRootLogin.*/PermitRootLogin yes/' /etc/ssh/sshd_config
                - sed -i 's/^#*PasswordAuthentication.*/PasswordAuthentication yes/' /etc/ssh/sshd_config
                - systemctl restart ssh 2>/dev/null || systemctl restart sshd 2>/dev/null || true`,
		config.Name, Namespace, config.Name, config.Name,
		cpu, memoryMB,
		dvName, config.Name, password)

	vmCmd := fmt.Sprintf("cat << 'VMEOF' | kubectl apply -f - 2>&1\n%s\nVMEOF", vmYAML)
	output, err = p.sshClient.Execute(vmCmd)
	if err != nil {
		global.APP_LOG.Error("VirtualMachine创建失败",
			zap.String("name", config.Name),
			zap.String("output", utils.TruncateString(output, 500)),
			zap.Error(err))
		// 清理 DataVolume
		p.sshClient.Execute(fmt.Sprintf("kubectl delete datavolume '%s' -n %s 2>/dev/null || true", dvName, Namespace))
		return fmt.Errorf("failed to create VM: %w", err)
	}

	updateProgress(70, "创建 SSH NodePort Service")

	// 创建 SSH Service (NodePort)
	if sshPort > 0 {
		sshSvcYAML := fmt.Sprintf(`apiVersion: v1
kind: Service
metadata:
  name: %s-ssh
  namespace: %s
spec:
  type: NodePort
  selector:
    kubevirt.io/vm: "%s"
  ports:
    - name: ssh
      protocol: TCP
      port: 22
      targetPort: 22
      nodePort: %d`, config.Name, Namespace, config.Name, sshPort)

		sshSvcCmd := fmt.Sprintf("cat << 'SVCEOF' | kubectl apply -f - 2>&1\n%s\nSVCEOF", sshSvcYAML)
		output, err = p.sshClient.Execute(sshSvcCmd)
		if err != nil {
			global.APP_LOG.Warn("SSH Service创建失败",
				zap.String("output", utils.TruncateString(output, 500)),
				zap.Error(err))
		}
	}

	updateProgress(80, "配置端口转发")

	// 配置防火墙端口转发（用于不通过 NodePort 的额外端口范围）
	if startPort > 0 && endPort > 0 && startPort <= endPort {
		fwMgr := firewall.NewManager(p.sshClient, NFTTableName, "")
		if _, err := fwMgr.DetectBackend(FWBackendFile); err == nil {
			if initErr := fwMgr.InitTable(); initErr != nil {
				global.APP_LOG.Warn("kubevirt: 防火墙初始化失败，端口映射可能不可用",
					zap.Error(initErr))
			}
			// KubeVirt 额外端口范围通过防火墙 DNAT 到 Pod IP
			// 此处仅初始化，实际端口映射在 VM 运行后通过 portmapping 层处理
			fwMgr.SaveRules()
		}
	}

	updateProgress(85, "等待虚拟机启动")

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

	// 写入vmlog（不写入密码）
	logCmd := fmt.Sprintf("echo '%s %d %s %d %d %d %d %d %s' >> /root/vmlog",
		utils.SanitizeShellArg(config.Name), sshPort, "***", cpu, memoryMB, diskGB, startPort, endPort, system)
	p.sshClient.Execute(logCmd)

	updateProgress(100, "KubeVirt虚拟机创建完成")
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

// parseConfigMB 解析实例配置中的内存/磁盘字符串为MB数值
// 支持格式: "512m", "512M", "512MB", "1g", "1G", "1GB", "512"（纯数字视为MB）
func parseConfigMB(s string) int {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0
	}
	lower := strings.ToLower(s)
	switch {
	case strings.HasSuffix(lower, "mb"):
		n, _ := strconv.Atoi(strings.TrimSuffix(lower, "mb"))
		return n
	case strings.HasSuffix(lower, "m"):
		n, _ := strconv.Atoi(strings.TrimSuffix(lower, "m"))
		return n
	case strings.HasSuffix(lower, "gb"):
		n, _ := strconv.Atoi(strings.TrimSuffix(lower, "gb"))
		return n * 1024
	case strings.HasSuffix(lower, "g"):
		n, _ := strconv.Atoi(strings.TrimSuffix(lower, "g"))
		return n * 1024
	default:
		n, _ := strconv.Atoi(s)
		return n
	}
}

// parseConfigGB 解析实例配置中的磁盘字符串为GB数值（向上取整）
// 支持格式: "10240m", "10g", "10G", "10"（纯数字视为MB）
func parseConfigGB(s string) int {
	mb := parseConfigMB(s)
	if mb <= 0 {
		return 0
	}
	gb := (mb + 1023) / 1024
	if gb < 1 {
		gb = 1
	}
	return gb
}
