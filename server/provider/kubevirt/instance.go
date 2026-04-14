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
			Memory: item.Spec.Template.Spec.Domain.Resources.Requests.Memory,
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
	memoryGB, _ := strconv.Atoi(config.Memory)
	if memoryGB <= 0 {
		memoryGB = 1
	}
	diskGB, _ := strconv.Atoi(config.Disk)
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

	updateProgress(20, "创建 PVC 磁盘")

	// 创建 PVC
	pvcName := fmt.Sprintf("%s-disk", config.Name)
	pvcYAML := fmt.Sprintf(`apiVersion: v1
kind: PersistentVolumeClaim
metadata:
  name: %s
  namespace: %s
  labels:
    vm.kubevirt.io/name: "%s"
spec:
  accessModes:
    - ReadWriteOnce
  resources:
    requests:
      storage: %dGi`, pvcName, Namespace, config.Name, diskGB)

	pvcCmd := fmt.Sprintf("cat << 'PVCEOF' | kubectl apply -f - 2>&1\n%s\nPVCEOF", pvcYAML)
	output, err := p.sshClient.Execute(pvcCmd)
	if err != nil {
		global.APP_LOG.Error("PVC创建失败",
			zap.String("name", config.Name),
			zap.String("output", utils.TruncateString(output, 500)),
			zap.Error(err))
		return fmt.Errorf("failed to create PVC: %w", err)
	}

	updateProgress(35, "创建 VirtualMachine 资源")

	// 构建 VirtualMachine YAML
	vmYAML := fmt.Sprintf(`apiVersion: kubevirt.io/v1
kind: VirtualMachine
metadata:
  name: %s
  namespace: %s
spec:
  running: true
  template:
    metadata:
      labels:
        kubevirt.io/vm: "%s"
    spec:
      domain:
        cpu:
          cores: %d
        resources:
          requests:
            memory: "%dGi"
        devices:
          disks:
            - name: rootdisk
              disk:
                bus: virtio
            - name: cloudinitdisk
              disk:
                bus: virtio
          interfaces:
            - name: default
              masquerade: {}
      networks:
        - name: default
          pod: {}
      volumes:
        - name: rootdisk
          persistentVolumeClaim:
            claimName: %s
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
		config.Name, Namespace, config.Name,
		cpu, memoryGB,
		pvcName, config.Name, password)

	vmCmd := fmt.Sprintf("cat << 'VMEOF' | kubectl apply -f - 2>&1\n%s\nVMEOF", vmYAML)
	output, err = p.sshClient.Execute(vmCmd)
	if err != nil {
		global.APP_LOG.Error("VirtualMachine创建失败",
			zap.String("name", config.Name),
			zap.String("output", utils.TruncateString(output, 500)),
			zap.Error(err))
		// 清理 PVC
		p.sshClient.Execute(fmt.Sprintf("kubectl delete pvc '%s' -n %s 2>/dev/null || true", pvcName, Namespace))
		return fmt.Errorf("failed to create VM: %w", err)
	}

	updateProgress(50, "创建 SSH NodePort Service")

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

	updateProgress(60, "配置端口转发")

	// 配置防火墙端口转发（用于不通过 NodePort 的额外端口范围）
	if startPort > 0 && endPort > 0 && startPort <= endPort {
		fwMgr := firewall.NewManager(p.sshClient, NFTTableName, "")
		if _, err := fwMgr.DetectBackend(FWBackendFile); err == nil {
			fwMgr.InitTable()
			// KubeVirt 额外端口范围通过防火墙 DNAT 到 Pod IP
			// 此处仅初始化，实际端口映射在 VM 运行后通过 portmapping 层处理
			fwMgr.SaveRules()
		}
	}

	updateProgress(70, "等待虚拟机启动")

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
		utils.SanitizeShellArg(config.Name), sshPort, "***", cpu, memoryGB, diskGB, startPort, endPort, system)
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
