package kubevirt

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"

	"oneclickvirt/provider"
	"oneclickvirt/utils"
)

// routedKubeVirtIPv6Plan describes one per-instance Multus attachment. A
// separate NetworkAttachmentDefinition keeps static IPAM isolated between
// instances and makes retries idempotent without mutating a shared CNI object.
type routedKubeVirtIPv6Plan struct {
	NetworkType string
	Container   bool
	Routed      *provider.RoutedIPv6Config
	NADName     string
	MAC         string
	NodeName    string
	PrimaryMAC  string
}

func kubeVirtNetworkType(config provider.InstanceConfig) string {
	if config.Metadata != nil {
		if value := strings.TrimSpace(config.Metadata["network_type"]); value != "" {
			return strings.ToLower(value)
		}
	}
	return "nat_ipv4"
}

func resolveKubeVirtIPv6Plan(config provider.InstanceConfig) (routedKubeVirtIPv6Plan, error) {
	plan := routedKubeVirtIPv6Plan{NetworkType: kubeVirtNetworkType(config), Container: config.InstanceType == "container", PrimaryMAC: kubeVirtRoutedMAC(config.Name + "-primary")}
	routed, present, err := provider.ResolveRoutedIPv6(config)
	if err != nil {
		return plan, err
	}
	requested := ""
	if config.Metadata != nil {
		requested = strings.TrimSpace(config.Metadata["static_ipv6"])
	}
	if present {
		if !utils.NetworkTypeHasIPv6(plan.NetworkType) {
			return plan, fmt.Errorf("已分配隧道路由IPv6，但网络类型 %q 未启用IPv6", plan.NetworkType)
		}
		plan.Routed = &routed
		plan.NADName = k8sResourceName(config.Name) + "-v6"
		if len(plan.NADName) > 63 {
			plan.NADName = strings.Trim(plan.NADName[:63], "-")
		}
		plan.MAC = kubeVirtRoutedMAC(routed.Address)
		return plan, nil
	}
	if requested != "" {
		return plan, fmt.Errorf("KubeVirt 静态IPv6必须提供 static_ipv6_cidr、网关和隧道桥；当前分配缺少路由元数据")
	}
	if utils.NetworkTypeHasIPv6(plan.NetworkType) {
		return plan, fmt.Errorf("KubeVirt 的IPv6网络必须绑定已启用的IPv6隧道路由地址池")
	}
	return plan, nil
}

func kubeVirtRoutedMAC(address string) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(address)))
	// Locally administered, unicast MAC. Using the address hash makes retries
	// produce the same interface identity without another remote round trip.
	return fmt.Sprintf("02:%s:%s:%s:%s:%s", hex.EncodeToString(sum[0:1]), hex.EncodeToString(sum[1:2]), hex.EncodeToString(sum[2:3]), hex.EncodeToString(sum[3:4]), hex.EncodeToString(sum[4:5]))
}

func (p *KubeVirtProvider) preflightKubeVirtIPv6(config provider.InstanceConfig) (routedKubeVirtIPv6Plan, error) {
	plan, err := resolveKubeVirtIPv6Plan(config)
	if err != nil {
		return plan, err
	}
	if plan.Routed == nil {
		return plan, nil
	}
	// The host bridge check, bridge CNI availability, CRD lookup, and local
	// Kubernetes-node identity are one remote command. The rendered workload is
	// pinned to that node because a tunnel bridge is node-local.
	command := plan.Routed.HostCheckCommand() + `
command -v kubectl >/dev/null 2>&1 || { echo 'kubectl is unavailable' >&2; exit 1; }
kubectl get crd network-attachment-definitions.k8s.cni.cncf.io >/dev/null 2>&1 || { echo 'Multus NetworkAttachmentDefinition CRD is unavailable' >&2; exit 1; }
kubectl api-resources --api-group=k8s.cni.cncf.io 2>/dev/null | grep -i networkattachmentdefinition >/dev/null || { echo 'Multus API is unavailable' >&2; exit 1; }
` + kubeVirtCNIPluginPreflightCommand() + `
node_name="$(hostname -s)"
kubectl get node "$node_name" >/dev/null 2>&1 || { echo "local hostname $node_name is not a Kubernetes node" >&2; exit 1; }
printf 'ONECLICKVIRT_K8S_NODE=%s\n' "$node_name"`
	output, err := p.sshClient.Execute(command)
	if err != nil {
		return plan, fmt.Errorf("KubeVirt隧道IPv6环境未就绪（需要Multus且每个节点存在 %s 网桥）: %s: %w", plan.Routed.Bridge, utils.TruncateString(strings.TrimSpace(output), 1600), err)
	}
	plan.NodeName = kubeVirtNodeNameFromPreflightOutput(output)
	if plan.NodeName == "" {
		return plan, fmt.Errorf("KubeVirt隧道IPv6环境未返回本机 Kubernetes 节点名")
	}
	return plan, nil
}

// K3s stores executable plugin symlinks outside the conventional CNI paths.
// Test executability directly so both real binaries and valid symlinks work,
// and check static IPAM before importing an image or creating a workload.
func kubeVirtCNIPluginPreflightCommand(directories ...string) string {
	if len(directories) == 0 {
		directories = []string{"/opt/cni/bin", "/usr/lib/cni", "/usr/libexec/cni", "/var/lib/rancher/k3s/data/cni", "/var/lib/rancher/k3s/data/current/bin"}
	}
	quoted := make([]string, len(directories))
	for index, directory := range directories {
		quoted[index] = utils.ShellSingleQuote(directory)
	}
	return `for _ocv_cni_plugin in bridge static; do
  _ocv_cni_found=0
  for _ocv_cni_directory in ` + strings.Join(quoted, " ") + `; do
    if [ -x "$_ocv_cni_directory/$_ocv_cni_plugin" ] && [ ! -d "$_ocv_cni_directory/$_ocv_cni_plugin" ]; then
      _ocv_cni_found=1
      break
    fi
  done
  [ "$_ocv_cni_found" -eq 1 ] || { printf '%s CNI plugin is unavailable\n' "$_ocv_cni_plugin" >&2; exit 1; }
done
`
}

func kubeVirtNodeNameFromPreflightOutput(output string) string {
	const marker = "ONECLICKVIRT_K8S_NODE="
	for _, line := range strings.Split(output, "\n") {
		clean := strings.TrimSpace(line)
		if strings.HasPrefix(clean, marker) {
			return strings.TrimSpace(strings.TrimPrefix(clean, marker))
		}
	}
	return ""
}

func kubeVirtRoutedNADYAML(plan routedKubeVirtIPv6Plan) (string, error) {
	if plan.Routed == nil || plan.NADName == "" {
		return "", fmt.Errorf("KubeVirt隧道IPv6计划为空")
	}
	config := map[string]interface{}{
		"cniVersion": "0.3.1",
		"name":       plan.NADName,
		// A bridge CNI creates a veth peer on the host bridge. macvlan cannot
		// reach a gateway that lives on its parent, which is exactly how the
		// managed tunnel prefix is routed.
		"type":      "bridge",
		"bridge":    plan.Routed.Bridge,
		"isGateway": false,
		"ipMasq":    false,
		"ipam": map[string]interface{}{
			"type": "static",
			"addresses": []map[string]string{{
				"address": plan.Routed.AddressCIDR(),
				"gateway": plan.Routed.Gateway,
			}},
		},
	}
	// VM routing belongs in cloud-init networkData. Adding a Pod default here
	// can merge with the primary CNI default into an IPv6 ECMP route, whose
	// LinkIndex is zero and cannot be read by KubeVirt's nmstate adapter.
	// Containers use this attachment directly. Give its default a distinct,
	// preferred metric so it cannot merge with the primary CNI's 1024 default,
	// while keeping the primary IPv4 default intact.
	if plan.Container {
		config["ipam"].(map[string]interface{})["routes"] = []map[string]interface{}{{
			"dst": "::/0", "gw": plan.Routed.Gateway, "priority": 512,
		}}
	}
	encoded, err := json.Marshal(config)
	if err != nil {
		return "", fmt.Errorf("序列化KubeVirt隧道IPv6 CNI配置失败: %w", err)
	}
	return fmt.Sprintf(`apiVersion: k8s.cni.cncf.io/v1
kind: NetworkAttachmentDefinition
metadata:
  name: %s
  namespace: %s
  labels:
    oneclickvirt.io/managed: "true"
    oneclickvirt.io/ipv6-tunnel: "%d"
spec:
  config: %s
`, yamlDoubleQuote(plan.NADName), yamlDoubleQuote(Namespace), plan.Routed.TunnelID, yamlDoubleQuote(string(encoded))), nil
}

func kubeVirtRoutedNetworkAnnotation(plan routedKubeVirtIPv6Plan) string {
	if plan.Routed == nil || plan.NADName == "" {
		return ""
	}
	return fmt.Sprintf(`[{"name":"%s","interface":"oneclickvirt6"}]`, plan.NADName)
}

func kubeVirtVMRoutedInterfaceYAML(plan routedKubeVirtIPv6Plan) string {
	if plan.Routed == nil {
		return ""
	}
	return fmt.Sprintf(`- name: tunnelv6
  bridge: {}
  macAddress: %s`, yamlDoubleQuote(plan.MAC))
}

func kubeVirtVMRoutedNetworkYAML(plan routedKubeVirtIPv6Plan) string {
	if plan.Routed == nil {
		return ""
	}
	return fmt.Sprintf(`- name: tunnelv6
  multus:
    networkName: %s`, yamlDoubleQuote(Namespace+"/"+plan.NADName))
}

func kubeVirtVMRoutedNetworkData(plan routedKubeVirtIPv6Plan) string {
	if plan.Routed == nil {
		return ""
	}
	primary := ""
	if plan.NetworkType != "ipv6_only" {
		primary = fmt.Sprintf(`  - type: physical
    name: eth0
    mac_address: %s
    subnets:
      - type: dhcp4
`, yamlDoubleQuote(plan.PrimaryMAC))
	}
	return fmt.Sprintf(`version: 1
config:
%s  - type: physical
    name: tunnelv6
    mac_address: %s
    subnets:
      - type: static6
        address: %s
        gateway: %s
  - type: nameserver
    address:
      - "2606:4700:4700::1111"
      - "2001:4860:4860::8888"
`, primary, yamlDoubleQuote(plan.MAC), yamlDoubleQuote(plan.Routed.AddressCIDR()), yamlDoubleQuote(plan.Routed.Gateway))
}

// kubeVirtVMYAML renders a complete VM manifest with optional routed IPv6.
// Keeping optional blocks outside a loose raw-string interpolation avoids
// tabs/blank keys in YAML when the instance uses an existing native network.
func kubeVirtVMYAML(name string, cpu, memoryMB int, dvName, password string, plan routedKubeVirtIPv6Plan) string {
	if plan.PrimaryMAC == "" {
		plan.PrimaryMAC = kubeVirtRoutedMAC(name + "-primary")
	}
	annotations := ""
	if annotation := kubeVirtRoutedNetworkAnnotation(plan); annotation != "" {
		annotations = fmt.Sprintf("\n      annotations:\n        k8s.v1.cni.cncf.io/networks: %s", yamlDoubleQuote(annotation))
	}
	primaryInterface := fmt.Sprintf("            - name: default\n              macAddress: %s\n              masquerade: {}\n", yamlDoubleQuote(plan.PrimaryMAC))
	primaryNetwork := "        - name: default\n          pod: {}\n"
	if plan.NetworkType == "ipv6_only" {
		primaryInterface = ""
		primaryNetwork = ""
	}
	routedInterface := ""
	if block := kubeVirtVMRoutedInterfaceYAML(plan); block != "" {
		routedInterface = indentBlock(block, 12) + "\n"
	}
	routedNetwork := ""
	if block := kubeVirtVMRoutedNetworkYAML(plan); block != "" {
		routedNetwork = indentBlock(block, 8) + "\n"
	}
	networkData := ""
	if block := kubeVirtVMRoutedNetworkData(plan); block != "" {
		networkData = "\n            networkData: |\n" + indentBlock(block, 14)
	}
	nodeSelector := ""
	if plan.Routed != nil && plan.NodeName != "" {
		nodeSelector = fmt.Sprintf("      nodeSelector:\n        kubernetes.io/hostname: %s\n", yamlDoubleQuote(plan.NodeName))
	}
	return fmt.Sprintf(`apiVersion: kubevirt.io/v1
kind: VirtualMachine
metadata:
  name: %s
  namespace: %s
  labels:
    kubevirt.io/vm: %s
    app: kubevirt-vm
spec:
  runStrategy: Always
  template:
    metadata:
      labels:
        kubevirt.io/vm: %s
        app: kubevirt-vm%s
    spec:
%s      domain:
        cpu:
          cores: %d
        resources:
          requests:
            memory: %s
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
%s%s          rng: {}
      networks:
%s%s      terminationGracePeriodSeconds: 30
      volumes:
        - name: datavolumedisk
          dataVolume:
            name: %s
        - name: cloudinitdisk
          cloudInitNoCloud:
            userData: |
%s%s`,
		yamlDoubleQuote(name), yamlDoubleQuote(Namespace), yamlDoubleQuote(name), yamlDoubleQuote(name), annotations,
		nodeSelector, cpu, yamlDoubleQuote(fmt.Sprintf("%dMi", memoryMB)), primaryInterface, routedInterface,
		primaryNetwork, routedNetwork, yamlDoubleQuote(dvName), indentBlock(kubeVirtVMCloudInitUserData(name, password), 14), networkData)
}

func (p *KubeVirtProvider) deleteRoutedKubeVirtNAD(plan routedKubeVirtIPv6Plan) error {
	if plan.NADName == "" {
		return nil
	}
	return p.deleteRoutedKubeVirtNADName(plan.NADName)
}

func (p *KubeVirtProvider) deleteRoutedKubeVirtNADByInstance(ctx context.Context, id string) error {
	name := k8sResourceName(id)
	if name == "" {
		return nil
	}
	return p.deleteRoutedKubeVirtNADNameContext(ctx, name+"-v6")
}

func (p *KubeVirtProvider) deleteRoutedKubeVirtNADName(name string) error {
	return p.deleteRoutedKubeVirtNADNameContext(context.Background(), name)
}

func (p *KubeVirtProvider) deleteRoutedKubeVirtNADNameContext(ctx context.Context, name string) error {
	// A normal IPv4-only installation does not require Multus. kubectl's
	// --ignore-not-found ignores a missing object, but not a missing resource
	// type. Check the CRD itself before trying to delete an optional NAD.
	output, err := utils.ExecuteShellCommandContext(ctx, p.sshClient, "kubectl get crd network-attachment-definitions.k8s.cni.cncf.io -o name --ignore-not-found=true 2>&1")
	if err != nil {
		return fmt.Errorf("检查KubeVirt隧道网络CRD失败: %w (output: %s)", err, utils.TruncateString(strings.TrimSpace(output), 1000))
	}
	if strings.TrimSpace(output) == "" {
		return nil
	}
	output, err = utils.ExecuteShellCommandContext(ctx, p.sshClient, fmt.Sprintf("kubectl delete network-attachment-definition %s -n %s --ignore-not-found=true 2>&1", shellSingleQuote(name), shellSingleQuote(Namespace)))
	if err != nil && !kubeVirtNotFound(output, err) {
		return fmt.Errorf("删除KubeVirt隧道网络 %s 失败: %w (output: %s)", name, err, utils.TruncateString(strings.TrimSpace(output), 1000))
	}
	return nil
}
