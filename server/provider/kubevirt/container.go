package kubevirt

import (
	"context"
	"encoding/json"
	"fmt"
	"path"
	"regexp"
	"strconv"
	"strings"
	"time"

	"oneclickvirt/global"
	"oneclickvirt/provider"
	"oneclickvirt/utils"

	"go.uber.org/zap"
)

type kubeVirtContainerPort struct {
	Name          string
	Protocol      string
	HostPort      int
	ContainerPort int
}

func (p *KubeVirtProvider) sshListK3sContainers(ctx context.Context) ([]provider.Instance, error) {
	if ctx != nil {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}
	}
	output, err := p.sshClient.Execute(fmt.Sprintf(
		"kubectl get deploy -n %s -l %s -o json 2>/dev/null",
		shellSingleQuote(Namespace), shellSingleQuote("oneclickvirt.io/type=container")))
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(output) == "" {
		return nil, nil
	}

	var result struct {
		Items []struct {
			Metadata struct {
				Name              string            `json:"name"`
				UID               string            `json:"uid"`
				CreationTimestamp time.Time         `json:"creationTimestamp"`
				Labels            map[string]string `json:"labels"`
			} `json:"metadata"`
			Spec struct {
				Replicas *int `json:"replicas"`
				Template struct {
					Spec struct {
						Containers []struct {
							Image     string `json:"image"`
							Resources struct {
								Limits struct {
									CPU    string `json:"cpu"`
									Memory string `json:"memory"`
								} `json:"limits"`
								Requests struct {
									CPU    string `json:"cpu"`
									Memory string `json:"memory"`
								} `json:"requests"`
							} `json:"resources"`
						} `json:"containers"`
					} `json:"spec"`
				} `json:"template"`
			} `json:"spec"`
			Status struct {
				ReadyReplicas int `json:"readyReplicas"`
				Replicas      int `json:"replicas"`
			} `json:"status"`
		} `json:"items"`
	}
	if err := json.Unmarshal([]byte(output), &result); err != nil {
		return nil, fmt.Errorf("failed to parse container deployment list: %w", err)
	}

	instances := make([]provider.Instance, 0, len(result.Items))
	for _, item := range result.Items {
		status := "stopped"
		desiredReplicas := 1
		if item.Spec.Replicas != nil {
			desiredReplicas = *item.Spec.Replicas
		}
		if desiredReplicas > 0 && item.Status.ReadyReplicas > 0 {
			status = "running"
		} else if desiredReplicas > 0 {
			status = "starting"
		}

		inst := provider.Instance{
			ID:       item.Metadata.UID,
			Name:     item.Metadata.Name,
			Type:     "container",
			Status:   status,
			Created:  item.Metadata.CreationTimestamp,
			Metadata: map[string]string{"backend": "k3s"},
		}
		if len(item.Spec.Template.Spec.Containers) > 0 {
			ctr := item.Spec.Template.Spec.Containers[0]
			inst.Image = ctr.Image
			if ctr.Resources.Limits.CPU != "" {
				inst.CPU = ctr.Resources.Limits.CPU
			} else {
				inst.CPU = ctr.Resources.Requests.CPU
			}
			if mem := ctr.Resources.Limits.Memory; mem != "" {
				inst.Memory = fmt.Sprintf("%d", parseMemoryString(mem))
			} else if mem := ctr.Resources.Requests.Memory; mem != "" {
				inst.Memory = fmt.Sprintf("%d", parseMemoryString(mem))
			}
		}
		instances = append(instances, inst)
	}
	return instances, nil
}

func (p *KubeVirtProvider) sshCreateK3sContainer(ctx context.Context, config provider.InstanceConfig, updateProgress func(int, string)) error {
	name := k8sResourceName(config.Name)
	if name == "" {
		return fmt.Errorf("invalid container name: %s", config.Name)
	}
	updateProgress(5, "开始创建KubeVirt容器")

	cpu := strings.TrimSpace(config.CPU)
	if cpu == "" {
		cpu = "1"
	}
	memoryMB := parseConfigMB(config.Memory)
	if memoryMB <= 0 {
		memoryMB = 256
	}
	password := "password"
	if config.Metadata != nil {
		if pw := strings.TrimSpace(config.Metadata["password"]); pw != "" {
			password = pw
		}
	}

	updateProgress(10, "确保K3s命名空间存在")
	p.sshClient.Execute(fmt.Sprintf("kubectl create namespace %s 2>/dev/null || true", shellSingleQuote(Namespace)))
	p.sshClient.Execute(fmt.Sprintf("mkdir -p %s", shellSingleQuote(VMLogDir)))

	updateProgress(20, "准备容器镜像")
	imageRef, err := p.prepareK3sContainerImage(ctx, config, name, updateProgress)
	if err != nil {
		return err
	}

	ports := parseKubeVirtContainerPorts(config.Ports)
	containerPortsYAML := buildKubeVirtContainerPortsYAML(ports)
	resourcesYAML := buildKubeVirtContainerResourcesYAML(cpu, memoryMB)
	startupScript := buildKubeVirtContainerStartupScript(password)
	deploymentYAML := fmt.Sprintf(`apiVersion: apps/v1
kind: Deployment
metadata:
  name: %s
  namespace: %s
  labels:
    app: %s
    oneclickvirt.io/instance: %s
    oneclickvirt.io/type: container
spec:
  replicas: 1
  selector:
    matchLabels:
      app: %s
      oneclickvirt.io/instance: %s
  template:
    metadata:
      labels:
        app: %s
        oneclickvirt.io/instance: %s
        oneclickvirt.io/type: container
    spec:
      containers:
        - name: main
          image: %s
          imagePullPolicy: IfNotPresent
          command: ["/bin/sh", "-c"]
          args:
            - |
%s%s%s`, yamlDoubleQuote(name), yamlDoubleQuote(Namespace), yamlDoubleQuote(name), yamlDoubleQuote(name),
		yamlDoubleQuote(name), yamlDoubleQuote(name), yamlDoubleQuote(name), yamlDoubleQuote(name), yamlDoubleQuote(imageRef),
		indentBlock(startupScript, 14), containerPortsYAML, resourcesYAML)

	updateProgress(45, "创建K3s Deployment")
	p.sshClient.Execute(fmt.Sprintf("kubectl delete deploy %s -n %s --ignore-not-found=true 2>/dev/null", shellSingleQuote(name), shellSingleQuote(Namespace)))
	deployCmd := fmt.Sprintf("cat << 'DEPLOYEOF' | kubectl apply -f - 2>&1\n%s\nDEPLOYEOF", deploymentYAML)
	output, err := p.sshClient.Execute(deployCmd)
	if err != nil {
		global.APP_LOG.Error("KubeVirt容器Deployment创建失败", zap.String("name", config.Name), zap.String("output", utils.TruncateString(output, 500)), zap.Error(err))
		return fmt.Errorf("failed to create k3s deployment: %w (kubectl output: %s)", err, utils.TruncateString(strings.TrimSpace(output), 1200))
	}

	if len(ports) > 0 {
		updateProgress(60, "创建K3s NodePort Service")
		svcYAML := buildKubeVirtContainerServiceYAML(name, ports)
		p.sshClient.Execute(fmt.Sprintf("kubectl delete svc %s -n %s --ignore-not-found=true 2>/dev/null", shellSingleQuote(name+"-ports"), shellSingleQuote(Namespace)))
		svcCmd := fmt.Sprintf("cat << 'SVCEOF' | kubectl apply -f - 2>&1\n%s\nSVCEOF", svcYAML)
		output, err = p.sshClient.Execute(svcCmd)
		if err != nil {
			global.APP_LOG.Warn("KubeVirt容器Service创建失败", zap.String("name", config.Name), zap.String("output", utils.TruncateString(output, 500)), zap.Error(err))
			return fmt.Errorf("failed to create k3s service: %w (kubectl output: %s)", err, utils.TruncateString(strings.TrimSpace(output), 1200))
		}
	}

	updateProgress(80, "等待K3s容器就绪")
	rolloutCmd := fmt.Sprintf("kubectl rollout status deploy/%s -n %s --timeout=180s 2>&1", shellSingleQuote(name), shellSingleQuote(Namespace))
	output, err = p.sshClient.Execute(rolloutCmd)
	if err != nil {
		diagnostics := p.collectK3sContainerDiagnostics(name)
		_ = p.sshDeleteK3sContainer(context.Background(), name)
		return fmt.Errorf("container %s did not become ready: %w (output: %s; diagnostics: %s)", name, err, utils.TruncateString(strings.TrimSpace(output), 1200), utils.TruncateString(strings.TrimSpace(diagnostics), 8000))
	}

	logLine := fmt.Sprintf("%s %s %s", name, "***", imageRef)
	p.sshClient.Execute(fmt.Sprintf("printf '%%s\\n' %s >> /root/vmlog", shellSingleQuote(logLine)))
	updateProgress(100, "KubeVirt容器创建完成")
	return nil
}

func (p *KubeVirtProvider) prepareK3sContainerImage(ctx context.Context, config provider.InstanceConfig, name string, updateProgress func(int, string)) (string, error) {
	if ctx != nil {
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		default:
		}
	}
	if strings.TrimSpace(config.ImageURL) == "" {
		return normalizeK3sImageRef(config.Image), nil
	}
	resolvedURL := config.ImageURL
	if config.UseCDN && (strings.Contains(config.ImageURL, "github.com/") || strings.Contains(config.ImageURL, "raw.githubusercontent.com/")) {
		if cdnURL := utils.GetCDNURL(p.sshClient, config.ImageURL, "KubeVirt-K3s"); cdnURL != "" {
			resolvedURL = cdnURL
		}
	}
	targetRef := fmt.Sprintf("localhost/oneclickvirt/%s:latest", name)
	dest := path.Join("/var/lib/oneclickvirt/k3s-images", name+".tar.gz")
	script := fmt.Sprintf(`set -e
mkdir -p %s
if command -v curl >/dev/null 2>&1; then
  curl -fsSL --retry 3 -o %s %s
else
  wget -q -O %s %s
fi
if command -v nerdctl >/dev/null 2>&1; then
  nerdctl -n k8s.io load -i %s >/tmp/ocv_k3s_load.log 2>&1 || nerdctl load -i %s >/tmp/ocv_k3s_load.log 2>&1
  loaded=$(awk '/Loaded image:/ {print $3}' /tmp/ocv_k3s_load.log | tail -n1)
  if [ -n "$loaded" ]; then nerdctl -n k8s.io tag "$loaded" %s 2>/dev/null || nerdctl tag "$loaded" %s 2>/dev/null || true; fi
elif command -v k3s >/dev/null 2>&1; then
  k3s ctr -n k8s.io images import %s >/tmp/ocv_k3s_load.log 2>&1
  loaded=$(awk '/unpacking/ {print $2}' /tmp/ocv_k3s_load.log | tail -n1)
  if [ -n "$loaded" ]; then k3s ctr -n k8s.io images tag "$loaded" %s 2>/dev/null || true; fi
elif command -v ctr >/dev/null 2>&1; then
  ctr -n k8s.io images import %s >/tmp/ocv_k3s_load.log 2>&1
  loaded=$(awk '/unpacking/ {print $2}' /tmp/ocv_k3s_load.log | tail -n1)
  if [ -n "$loaded" ]; then ctr -n k8s.io images tag "$loaded" %s 2>/dev/null || true; fi
else
  echo "container image runtime not found" >&2
  exit 1
fi
printf '%%s' %s`, shellSingleQuote("/var/lib/oneclickvirt/k3s-images"), shellSingleQuote(dest), shellSingleQuote(resolvedURL),
		shellSingleQuote(dest), shellSingleQuote(resolvedURL), shellSingleQuote(dest), shellSingleQuote(dest), shellSingleQuote(targetRef), shellSingleQuote(targetRef),
		shellSingleQuote(dest), shellSingleQuote(targetRef), shellSingleQuote(dest), shellSingleQuote(targetRef), shellSingleQuote(targetRef))
	updateProgress(30, "下载并导入K3s容器镜像")
	output, err := p.sshClient.Execute(script)
	if err != nil {
		return "", fmt.Errorf("failed to import k3s container image: %w (output: %s)", err, utils.TruncateString(output, 300))
	}
	return strings.TrimSpace(output), nil
}

func normalizeK3sImageRef(image string) string {
	image = strings.TrimSpace(image)
	if image == "" {
		return "alpine:latest"
	}
	if strings.Contains(image, "/") || strings.Contains(image, ":") {
		return image
	}
	if strings.HasPrefix(image, "spiritlhl-") {
		return "spiritlhl/" + strings.TrimPrefix(image, "spiritlhl-") + ":latest"
	}
	return image + ":latest"
}

func parseKubeVirtContainerPorts(ports []string) []kubeVirtContainerPort {
	seen := map[string]bool{}
	parsed := make([]kubeVirtContainerPort, 0, len(ports))
	for _, raw := range ports {
		raw = strings.TrimSpace(raw)
		if raw == "" {
			continue
		}
		proto := "TCP"
		if before, after, ok := strings.Cut(raw, "/"); ok {
			raw = before
			proto = strings.ToUpper(strings.TrimSpace(after))
		}
		if proto != "TCP" && proto != "UDP" {
			proto = "TCP"
		}
		parts := strings.Split(raw, ":")
		if len(parts) < 2 {
			continue
		}
		hostStr := parts[len(parts)-2]
		guestStr := parts[len(parts)-1]
		hostPort, hostErr := strconv.Atoi(hostStr)
		guestPort, guestErr := strconv.Atoi(guestStr)
		if hostErr != nil || guestErr != nil || hostPort <= 0 || guestPort <= 0 {
			continue
		}
		key := fmt.Sprintf("%d/%d/%s", hostPort, guestPort, proto)
		if seen[key] {
			continue
		}
		seen[key] = true
		name := fmt.Sprintf("p%d-%s", guestPort, strings.ToLower(proto))
		parsed = append(parsed, kubeVirtContainerPort{Name: name, Protocol: proto, HostPort: hostPort, ContainerPort: guestPort})
	}
	return parsed
}

func buildKubeVirtContainerPortsYAML(ports []kubeVirtContainerPort) string {
	if len(ports) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("\n          ports:")
	seenNames := map[string]int{}
	for _, p := range ports {
		name := p.Name
		if seenNames[name] > 0 {
			name = fmt.Sprintf("%s-%d", name, seenNames[p.Name]+1)
		}
		seenNames[p.Name]++
		b.WriteString(fmt.Sprintf("\n            - name: %s\n              containerPort: %d\n              protocol: %s", yamlDoubleQuote(name), p.ContainerPort, p.Protocol))
	}
	return b.String()
}

func buildKubeVirtContainerResourcesYAML(cpu string, memoryMB int) string {
	memory := fmt.Sprintf("%dMi", memoryMB)
	return fmt.Sprintf(`
          resources:
            requests:
              cpu: %s
              memory: %s
            limits:
              cpu: %s
              memory: %s`, yamlDoubleQuote(cpu), yamlDoubleQuote(memory), yamlDoubleQuote(cpu), yamlDoubleQuote(memory))
}

func buildKubeVirtContainerStartupScript(password string) string {
	password = strings.ReplaceAll(strings.ReplaceAll(password, "\r", ""), "\n", "")
	return fmt.Sprintf(`set +e
printf 'root:%s\n' | chpasswd 2>/dev/null || true
sed -i 's/^#*PermitRootLogin.*/PermitRootLogin yes/' /etc/ssh/sshd_config 2>/dev/null || true
sed -i 's/^#*PasswordAuthentication.*/PasswordAuthentication yes/' /etc/ssh/sshd_config 2>/dev/null || true
mkdir -p /run/sshd /var/run/sshd 2>/dev/null || true
if command -v sshd >/dev/null 2>&1; then
  /usr/sbin/sshd -D || /usr/sbin/sshd || true
elif command -v dropbear >/dev/null 2>&1; then
  dropbear -F -E || dropbear || true
fi
tail -f /dev/null`, strings.ReplaceAll(password, "'", "'\\''"))
}

func buildKubeVirtContainerServiceYAML(name string, ports []kubeVirtContainerPort) string {
	var b strings.Builder
	b.WriteString(fmt.Sprintf(`apiVersion: v1
kind: Service
metadata:
  name: %s
  namespace: %s
  labels:
    app: %s
    oneclickvirt.io/instance: %s
spec:
  type: NodePort
  selector:
    app: %s
    oneclickvirt.io/instance: %s
  ports:`, yamlDoubleQuote(name+"-ports"), yamlDoubleQuote(Namespace), yamlDoubleQuote(name), yamlDoubleQuote(name), yamlDoubleQuote(name), yamlDoubleQuote(name)))
	seenNames := map[string]int{}
	for _, p := range ports {
		portName := p.Name
		if seenNames[portName] > 0 {
			portName = fmt.Sprintf("%s-%d", portName, seenNames[p.Name]+1)
		}
		seenNames[p.Name]++
		b.WriteString(fmt.Sprintf(`
    - name: %s
      protocol: %s
      port: %d
      targetPort: %d
      nodePort: %d`, yamlDoubleQuote(portName), p.Protocol, p.ContainerPort, p.ContainerPort, p.HostPort))
	}
	return b.String()
}

func (p *KubeVirtProvider) collectK3sContainerDiagnostics(name string) string {
	commands := []struct {
		label string
		cmd   string
	}{
		{"deployment yaml", fmt.Sprintf("kubectl get deploy %s -n %s -o yaml 2>&1", shellSingleQuote(name), shellSingleQuote(Namespace))},
		{"deployment describe", fmt.Sprintf("kubectl describe deploy %s -n %s 2>&1", shellSingleQuote(name), shellSingleQuote(Namespace))},
		{"pods", fmt.Sprintf("kubectl get pods -n %s -l %s -o wide 2>&1", shellSingleQuote(Namespace), shellSingleQuote("app="+name))},
		{"pod describe", fmt.Sprintf("kubectl describe pods -n %s -l %s 2>&1", shellSingleQuote(Namespace), shellSingleQuote("app="+name))},
		{"pod logs", fmt.Sprintf("kubectl logs -n %s -l %s --all-containers --tail=120 2>&1", shellSingleQuote(Namespace), shellSingleQuote("app="+name))},
		{"service yaml", fmt.Sprintf("kubectl get svc %s -n %s -o yaml 2>&1", shellSingleQuote(name+"-ports"), shellSingleQuote(Namespace))},
		{"namespace events", fmt.Sprintf("kubectl get events -n %s --sort-by=.lastTimestamp 2>&1 | tail -120", shellSingleQuote(Namespace))},
	}
	var parts []string
	for _, command := range commands {
		output, err := p.sshClient.Execute(command.cmd)
		if trimmed := strings.TrimSpace(output); trimmed != "" {
			parts = append(parts, fmt.Sprintf("[%s]\n%s", command.label, trimmed))
		}
		if err != nil {
			parts = append(parts, fmt.Sprintf("[%s error]\n%v", command.label, err))
		}
	}
	return strings.Join(parts, "\n\n")
}

func indentBlock(s string, spaces int) string {
	prefix := strings.Repeat(" ", spaces)
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	for i, line := range lines {
		lines[i] = prefix + line
	}
	return strings.Join(lines, "\n")
}

func k8sResourceName(name string) string {
	name = strings.ToLower(strings.TrimSpace(name))
	re := regexp.MustCompile(`[^a-z0-9-]+`)
	name = re.ReplaceAllString(name, "-")
	name = strings.Trim(name, "-")
	if len(name) > 50 {
		name = strings.Trim(name[:50], "-")
	}
	return name
}

func (p *KubeVirtProvider) sshK3sContainerExists(id string) (bool, error) {
	name := k8sResourceName(id)
	if name == "" {
		return false, nil
	}
	output, err := p.sshClient.Execute(fmt.Sprintf(
		"kubectl get deploy %s -n %s -o name 2>/dev/null",
		shellSingleQuote(name), shellSingleQuote(Namespace)))
	if err != nil {
		return false, err
	}
	return strings.TrimSpace(output) != "", nil
}

func (p *KubeVirtProvider) sshScaleK3sContainer(ctx context.Context, id string, replicas int) error {
	name := k8sResourceName(id)
	if name == "" {
		return fmt.Errorf("invalid container name: %s", id)
	}
	output, err := p.sshClient.Execute(fmt.Sprintf(
		"kubectl scale deploy/%s -n %s --replicas=%d 2>&1",
		shellSingleQuote(name), shellSingleQuote(Namespace), replicas))
	if err != nil {
		return fmt.Errorf("failed to scale KubeVirt container %s: %w (output: %s)", id, err, utils.TruncateString(output, 300))
	}
	if replicas <= 0 {
		return nil
	}
	for i := 0; i < 60; i++ {
		ready, err := p.sshClient.Execute(fmt.Sprintf(
			"kubectl get deploy %s -n %s -o jsonpath='{.status.readyReplicas}' 2>/dev/null",
			shellSingleQuote(name), shellSingleQuote(Namespace)))
		if err == nil && strings.TrimSpace(ready) != "" && strings.TrimSpace(ready) != "0" {
			return nil
		}
		if err := sleepWithContext(ctx, 3*time.Second); err != nil {
			return fmt.Errorf("waiting for KubeVirt container '%s' to start cancelled: %w", id, err)
		}
	}
	return fmt.Errorf("KubeVirt container '%s' did not become ready within timeout; diagnostics: %s", id, utils.TruncateString(strings.TrimSpace(p.collectK3sContainerDiagnostics(name)), 8000))
}

func (p *KubeVirtProvider) sshDeleteK3sContainer(ctx context.Context, id string) error {
	name := k8sResourceName(id)
	if name == "" {
		return fmt.Errorf("invalid container name: %s", id)
	}
	global.APP_LOG.Info("开始删除KubeVirt容器", zap.String("id", utils.TruncateString(id, 32)))
	p.sshClient.Execute(fmt.Sprintf("kubectl delete deploy %s -n %s --ignore-not-found=true 2>/dev/null", shellSingleQuote(name), shellSingleQuote(Namespace)))
	p.sshClient.Execute(fmt.Sprintf("kubectl delete svc %s -n %s --ignore-not-found=true 2>/dev/null", shellSingleQuote(name+"-ports"), shellSingleQuote(Namespace)))
	p.sshClient.Execute(fmt.Sprintf("grep -Fv %s /root/vmlog > /root/vmlog.tmp 2>/dev/null && mv /root/vmlog.tmp /root/vmlog || true", shellSingleQuote(name+" ")))
	if err := sleepWithContext(ctx, 2*time.Second); err != nil {
		return fmt.Errorf("waiting after deleting KubeVirt container cancelled: %w", err)
	}
	output, err := p.sshClient.Execute(fmt.Sprintf("kubectl get deploy %s -n %s 2>&1", shellSingleQuote(name), shellSingleQuote(Namespace)))
	if err != nil || strings.Contains(output, "NotFound") || strings.Contains(output, "not found") {
		global.APP_LOG.Info("KubeVirt容器删除成功", zap.String("id", utils.TruncateString(id, 32)))
		return nil
	}
	return fmt.Errorf("KubeVirt container %s still exists after deletion", id)
}
