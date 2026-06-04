package agent

import (
	"context"
	"fmt"
	"strings"
	"time"

	"oneclickvirt/global"
	providerModel "oneclickvirt/model/provider"
	"oneclickvirt/provider"

	"go.uber.org/zap"
	"gorm.io/gorm"
)

// isIPv6Capable reports whether the given network type includes IPv6.
func isIPv6Capable(networkType string) bool {
	return networkType == "nat_ipv4_ipv6" ||
		networkType == "dedicated_ipv4_ipv6" ||
		networkType == "ipv6_only"
}

// detectInstanceInterfaces detects the network interfaces for an instance.
// For IPv6-capable instances it returns two entries [V4-iface, V6-iface] so the
// Rust agent creates separate nft/ipt counters for each NIC and counts traffic on
// both.  For single-stack instances it returns one entry.
// vmidHint is the provider-side instance ID (Proxmox VMID string); pass "" to look it up via GetInstance.
func (s *MonitorService) detectInstanceInterfaces(
	providerInstance provider.Provider,
	instance *providerModel.Instance,
	vmidHint string,
) ([]string, error) {
	providerType := providerInstance.GetType()
	hasIPv6 := isIPv6Capable(instance.NetworkType)
	var interfaces []string

	switch providerType {
	case "docker", "podman", "containerd", "orbstack":
		// Docker-family: a single veth handles both IPv4 and IPv6 traffic.
		iface, err := s.detectVethInterface(providerInstance, instance.Name, providerType)
		if err != nil {
			return nil, err
		}
		interfaces = append(interfaces, iface)

	case "lxd", "incus":
		// V4 interface: host-side veth for eth0 (volatile.eth0.host_name)
		ifaceV4, err := s.detectLxdIncusInterface(providerInstance, instance.Name, providerType)
		if err != nil {
			return nil, err
		}
		interfaces = append(interfaces, ifaceV4)

		// V6 interface: host-side veth for eth1 (volatile.eth1.host_name).
		// Only relevant for IPv6-capable instances; only add if distinct from V4
		// (if no eth1 exists, GetVethInterfaceNameV6 falls back to eth0 → same value).
		if hasIPv6 {
			var ifaceV6 string
			if providerType == "lxd" {
				if lxdProv, ok := providerInstance.(interface {
					GetVethInterfaceNameV6(string) (string, error)
				}); ok {
					ifaceV6, _ = lxdProv.GetVethInterfaceNameV6(instance.Name)
				}
			} else {
				if incusProv, ok := providerInstance.(interface {
					GetVethInterfaceNameV6(context.Context, string) (string, error)
				}); ok {
					ctx6, cancel6 := context.WithTimeout(s.ctx, 15*time.Second)
					defer cancel6()
					ifaceV6, _ = incusProv.GetVethInterfaceNameV6(ctx6, instance.Name)
				}
			}
			if ifaceV6 != "" && ifaceV6 != ifaceV4 {
				interfaces = append(interfaces, ifaceV6)
			}
		}

	case "proxmox":
		// Resolve the actual Proxmox VMID (not the DB primary key).
		vmid := vmidHint
		if vmid == "" {
			pvmInst, err := providerInstance.GetInstance(s.ctx, instance.Name)
			if err != nil {
				return nil, fmt.Errorf("get proxmox vmid for %s: %w", instance.Name, err)
			}
			vmid = pvmInst.ID
		}
		// V4: first NIC (tap{vmid}i0 / veth{ctid}i0)
		ifaceV4, err := s.detectProxmoxInterface(providerInstance, instance.Name, vmid)
		if err != nil {
			return nil, err
		}
		interfaces = append(interfaces, ifaceV4)

		// V6: second NIC (tap{vmid}i1 / veth{ctid}i1) — only for IPv6-capable instances.
		if hasIPv6 {
			if ifaceV6, err := s.detectProxmoxInterfaceV6(providerInstance, instance.Name, vmid); err == nil && ifaceV6 != "" {
				interfaces = append(interfaces, ifaceV6)
			}
		}

	case "qemu":
		iface, err := s.detectQEMUInterface(providerInstance, instance.Name)
		if err != nil {
			return nil, err
		}
		interfaces = append(interfaces, iface)

	case "kubevirt":
		iface, err := s.detectKubeVirtInterface(providerInstance, instance.Name)
		if err != nil {
			return nil, err
		}
		interfaces = append(interfaces, iface)

	default:
		return nil, fmt.Errorf("unsupported provider type: %s", providerType)
	}

	return interfaces, nil
}

// detectVethInterface detects the veth interface for a Docker/Podman/Containerd/Orbstack container.
func (s *MonitorService) detectVethInterface(providerInstance provider.Provider, instanceName, providerType string) (string, error) {
	var runtimeCmd string
	switch providerType {
	case "docker", "orbstack":
		runtimeCmd = "docker"
	case "podman":
		runtimeCmd = "podman"
	case "containerd":
		runtimeCmd = "nerdctl"
	}

	detectCmd := fmt.Sprintf(`
CONTAINER_PID=$(%s inspect -f '{{.State.Pid}}' '%s' 2>/dev/null)
if [ -z "$CONTAINER_PID" ] || [ "$CONTAINER_PID" = "0" ]; then
    echo "ERROR: container not running"
    exit 1
fi
HOST_VETH_IFINDEX=$(nsenter -t $CONTAINER_PID -n ip link show eth0 2>/dev/null | head -n1 | sed -n 's/.*@if\([0-9]\+\).*/\1/p')
if [ -z "$HOST_VETH_IFINDEX" ]; then
    echo "ERROR: no veth ifindex"
    exit 1
fi
VETH_NAME=$(ip -o link show 2>/dev/null | awk -v idx="$HOST_VETH_IFINDEX" -F': ' '$1 == idx {print $2}' | cut -d'@' -f1)
if [ -n "$VETH_NAME" ]; then
    echo "$VETH_NAME"
    exit 0
fi
echo "ERROR: veth not found"
exit 1
`, runtimeCmd, instanceName)

	ctx, cancel := context.WithTimeout(s.ctx, 30*time.Second)
	defer cancel()

	output, err := providerInstance.ExecuteSSHCommand(ctx, detectCmd)
	if err != nil {
		return "", fmt.Errorf("detect veth for %s: %w (output: %s)", instanceName, err, output)
	}

	iface := strings.TrimSpace(output)
	if strings.HasPrefix(iface, "ERROR:") || iface == "" {
		return "", fmt.Errorf("detect veth for %s: %s", instanceName, iface)
	}
	return iface, nil
}

// detectLxdIncusInterface detects the veth interface for an LXD/Incus container.
func (s *MonitorService) detectLxdIncusInterface(providerInstance provider.Provider, instanceName, providerType string) (string, error) {
	// Try provider's GetVethInterfaceName first
	if providerType == "lxd" {
		if lxdProv, ok := providerInstance.(interface {
			GetVethInterfaceName(string) (string, error)
		}); ok {
			if name, err := lxdProv.GetVethInterfaceName(instanceName); err == nil && name != "" {
				return name, nil
			}
		}
	} else if providerType == "incus" {
		if incusProv, ok := providerInstance.(interface {
			GetVethInterfaceName(context.Context, string) (string, error)
		}); ok {
			ctx, cancel := context.WithTimeout(s.ctx, 10*time.Second)
			defer cancel()
			if name, err := incusProv.GetVethInterfaceName(ctx, instanceName); err == nil && name != "" {
				return name, nil
			}
		}
	}

	// Fallback: nsenter method
	cmd := "lxc"
	if providerType == "incus" {
		cmd = "incus"
	}

	detectCmd := fmt.Sprintf(`
CONTAINER_PID=$(%s info '%s' 2>/dev/null | grep -i 'PID:' | awk '{print $2}')
if [ -z "$CONTAINER_PID" ] || [ "$CONTAINER_PID" = "0" ]; then
    echo "ERROR: container not running"
    exit 1
fi
HOST_VETH_IFINDEX=$(nsenter -t $CONTAINER_PID -n ip link show eth0 2>/dev/null | head -n1 | sed -n 's/.*@if\([0-9]\+\).*/\1/p')
if [ -z "$HOST_VETH_IFINDEX" ]; then
    echo "ERROR: no veth ifindex"
    exit 1
fi
VETH_NAME=$(ip -o link show 2>/dev/null | awk -v idx="$HOST_VETH_IFINDEX" -F': ' '$1 == idx {print $2}' | cut -d'@' -f1)
if [ -n "$VETH_NAME" ]; then
    echo "$VETH_NAME"
    exit 0
fi
echo "ERROR: veth not found"
exit 1
`, cmd, instanceName)

	ctx, cancel := context.WithTimeout(s.ctx, 30*time.Second)
	defer cancel()

	output, err := providerInstance.ExecuteSSHCommand(ctx, detectCmd)
	if err != nil {
		return "", fmt.Errorf("detect veth for %s: %w", instanceName, err)
	}

	iface := strings.TrimSpace(output)
	if strings.HasPrefix(iface, "ERROR:") || iface == "" {
		return "", fmt.Errorf("detect veth for %s: %s", instanceName, iface)
	}
	return iface, nil
}

// detectProxmoxInterface detects the IPv4 network interface (i0) for a Proxmox instance.
// Proxmox names the first NIC tap{vmid}i0 (KVM VM) or veth{ctid}i0 (LXC container).
func (s *MonitorService) detectProxmoxInterface(providerInstance provider.Provider, instanceName, instanceID string) (string, error) {
	detectCmd := fmt.Sprintf(`
INSTANCE_ID='%s'
# LXC: veth<ctid>i0
if ip link show veth${INSTANCE_ID}i0 >/dev/null 2>&1; then
    echo "veth${INSTANCE_ID}i0"
    exit 0
fi
# KVM: tap<vmid>i0
if ip link show tap${INSTANCE_ID}i0 >/dev/null 2>&1; then
    echo "tap${INSTANCE_ID}i0"
    exit 0
fi
echo "ERROR: no i0 interface for instance $INSTANCE_ID"
exit 1
`, instanceID)

	ctx, cancel := context.WithTimeout(s.ctx, 30*time.Second)
	defer cancel()

	output, err := providerInstance.ExecuteSSHCommand(ctx, detectCmd)
	if err != nil {
		return "", fmt.Errorf("detect proxmox iface for %s: %w", instanceName, err)
	}

	iface := strings.TrimSpace(output)
	if strings.HasPrefix(iface, "ERROR:") || iface == "" {
		return "", fmt.Errorf("detect proxmox iface for %s: %s", instanceName, iface)
	}
	return iface, nil
}

// detectProxmoxInterfaceV6 detects the IPv6 network interface (i1) for a Proxmox instance.
// Proxmox names the second NIC tap{vmid}i1 (KVM VM) or veth{ctid}i1 (LXC container).
// Returns an error if no i1 interface is found; caller falls back to V4.
func (s *MonitorService) detectProxmoxInterfaceV6(providerInstance provider.Provider, instanceName, instanceID string) (string, error) {
	detectCmd := fmt.Sprintf(`
INSTANCE_ID='%s'
# LXC: veth<ctid>i1
if ip link show veth${INSTANCE_ID}i1 >/dev/null 2>&1; then
    echo "veth${INSTANCE_ID}i1"
    exit 0
fi
# KVM: tap<vmid>i1
if ip link show tap${INSTANCE_ID}i1 >/dev/null 2>&1; then
    echo "tap${INSTANCE_ID}i1"
    exit 0
fi
echo "ERROR: no i1 interface for instance $INSTANCE_ID"
exit 1
`, instanceID)

	ctx, cancel := context.WithTimeout(s.ctx, 30*time.Second)
	defer cancel()

	output, err := providerInstance.ExecuteSSHCommand(ctx, detectCmd)
	if err != nil {
		return "", fmt.Errorf("detect proxmox IPv6 iface for %s: %w", instanceName, err)
	}

	iface := strings.TrimSpace(output)
	if strings.HasPrefix(iface, "ERROR:") || iface == "" {
		return "", fmt.Errorf("detect proxmox IPv6 iface for %s: %s", instanceName, iface)
	}
	return iface, nil
}

// detectQEMUInterface detects the network interface for a QEMU/libvirt VM.
// Uses `virsh domiflist` to find the tap/vnet interface attached to the VM.
func (s *MonitorService) detectQEMUInterface(providerInstance provider.Provider, instanceName string) (string, error) {
	detectCmd := fmt.Sprintf(`
IFACE=$(virsh domiflist '%s' 2>/dev/null | awk 'NR>2 && $1 != "" {print $1}' | head -n1)
if [ -n "$IFACE" ]; then
    echo "$IFACE"
    exit 0
fi
echo "ERROR: no interface for VM %s"
exit 1
`, instanceName, instanceName)

	ctx, cancel := context.WithTimeout(s.ctx, 30*time.Second)
	defer cancel()

	output, err := providerInstance.ExecuteSSHCommand(ctx, detectCmd)
	if err != nil {
		return "", fmt.Errorf("detect qemu iface for %s: %w", instanceName, err)
	}

	iface := strings.TrimSpace(output)
	if strings.HasPrefix(iface, "ERROR:") || iface == "" {
		return "", fmt.Errorf("detect qemu iface for %s: %s", instanceName, iface)
	}
	return iface, nil
}

// detectKubeVirtInterface detects the network interface for a KubeVirt VM.
// KubeVirt VMs use tap interfaces, detected via pod network namespace.
func (s *MonitorService) detectKubeVirtInterface(providerInstance provider.Provider, instanceName string) (string, error) {
	detectCmd := fmt.Sprintf(`
# Try to find the virt-launcher pod for this VM and its tap interface
POD=$(kubectl get pod -n kubevirt-vms -l kubevirt.io/vm=%s -o jsonpath='{.items[0].metadata.name}' 2>/dev/null)
if [ -z "$POD" ]; then
    echo "ERROR: no pod for VM %s"
    exit 1
fi
POD_PID=$(kubectl exec -n kubevirt-vms "$POD" -- cat /proc/1/status 2>/dev/null | grep ^NStgid | awk '{print $2}')
if [ -z "$POD_PID" ]; then
    # fallback: use crictl to get PID
    CID=$(kubectl get pod -n kubevirt-vms "$POD" -o jsonpath='{.status.containerStatuses[0].containerID}' 2>/dev/null | sed 's|.*://||')
    POD_PID=$(crictl inspect "$CID" 2>/dev/null | grep -m1 '"pid"' | grep -oE '[0-9]+')
fi
if [ -n "$POD_PID" ]; then
    HOST_VETH_IFINDEX=$(nsenter -t $POD_PID -n ip link show eth0 2>/dev/null | head -n1 | sed -n 's/.*@if\([0-9]\+\).*/\1/p')
    if [ -n "$HOST_VETH_IFINDEX" ]; then
        VETH_NAME=$(ip -o link show 2>/dev/null | awk -v idx="$HOST_VETH_IFINDEX" -F': ' '$1 == idx {print $2}' | cut -d'@' -f1)
        if [ -n "$VETH_NAME" ]; then
            echo "$VETH_NAME"
            exit 0
        fi
    fi
fi
echo "ERROR: no interface for KubeVirt VM %s"
exit 1
`, instanceName, instanceName, instanceName)

	ctx, cancel := context.WithTimeout(s.ctx, 30*time.Second)
	defer cancel()

	output, err := providerInstance.ExecuteSSHCommand(ctx, detectCmd)
	if err != nil {
		return "", fmt.Errorf("detect kubevirt iface for %s: %w", instanceName, err)
	}

	iface := strings.TrimSpace(output)
	if strings.HasPrefix(iface, "ERROR:") || iface == "" {
		return "", fmt.Errorf("detect kubevirt iface for %s: %s", instanceName, iface)
	}
	return iface, nil
}

func stringsEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// InstanceInterfaces holds the detected host-side network interface names for an instance.
// V4 is the veth backing eth0 (IPv4). V6 is the veth backing eth1 (IPv6);
// if the instance uses a single interface for both stacks, V6 equals V4.
type InstanceInterfaces struct {
	V4 string
	V6 string
}

// detectBothInterfaces detects both the IPv4 and IPv6 host-side network interfaces
// for an instance. It never returns an error for missing V6; in that case V6 = V4.
func (s *MonitorService) detectBothInterfaces(
	providerInstance provider.Provider,
	instance *providerModel.Instance,
	vmidHint string,
) (*InstanceInterfaces, error) {
	providerType := providerInstance.GetType()
	result := &InstanceInterfaces{}

	// 仅在网络类型包含IPv6时才尝试检测V6独立接口（eth1 veth）
	hasIPv6 := isIPv6Capable(instance.NetworkType)

	switch providerType {
	case "lxd":
		if lxdProv, ok := providerInstance.(interface {
			GetVethInterfaceName(string) (string, error)
			GetVethInterfaceNameV6(string) (string, error)
		}); ok {
			if v4, err := lxdProv.GetVethInterfaceName(instance.Name); err == nil && v4 != "" {
				result.V4 = v4
			}
			if hasIPv6 {
				if v6, err := lxdProv.GetVethInterfaceNameV6(instance.Name); err == nil && v6 != "" {
					result.V6 = v6
				}
			}
		}
		if result.V4 == "" {
			iface, err := s.detectLxdIncusInterface(providerInstance, instance.Name, providerType)
			if err != nil {
				return nil, err
			}
			result.V4 = iface
		}
		if result.V6 == "" {
			result.V6 = result.V4
		}

	case "incus":
		if incusProv, ok := providerInstance.(interface {
			GetVethInterfaceName(context.Context, string) (string, error)
			GetVethInterfaceNameV6(context.Context, string) (string, error)
		}); ok {
			ctx4, cancel4 := context.WithTimeout(s.ctx, 15*time.Second)
			defer cancel4()
			if v4, err := incusProv.GetVethInterfaceName(ctx4, instance.Name); err == nil && v4 != "" {
				result.V4 = v4
			}
			if hasIPv6 {
				ctx6, cancel6 := context.WithTimeout(s.ctx, 15*time.Second)
				defer cancel6()
				if v6, err := incusProv.GetVethInterfaceNameV6(ctx6, instance.Name); err == nil && v6 != "" {
					result.V6 = v6
				}
			}
		}
		if result.V4 == "" {
			iface, err := s.detectLxdIncusInterface(providerInstance, instance.Name, providerType)
			if err != nil {
				return nil, err
			}
			result.V4 = iface
		}
		if result.V6 == "" {
			result.V6 = result.V4
		}

	case "docker", "podman", "containerd", "orbstack":
		iface, err := s.detectVethInterface(providerInstance, instance.Name, providerType)
		if err != nil {
			return nil, err
		}
		result.V4 = iface
		result.V6 = iface // same veth handles both stacks

	case "proxmox":
		vmid := vmidHint
		if vmid == "" {
			pvmInst, err := providerInstance.GetInstance(s.ctx, instance.Name)
			if err != nil {
				return nil, fmt.Errorf("get proxmox vmid for %s: %w", instance.Name, err)
			}
			vmid = pvmInst.ID
		}
		// V4: always the first NIC (i0)
		iface, err := s.detectProxmoxInterface(providerInstance, instance.Name, vmid)
		if err != nil {
			return nil, err
		}
		result.V4 = iface
		// V6: the second NIC (i1) only for IPv6-capable instances
		if hasIPv6 {
			if v6iface, err := s.detectProxmoxInterfaceV6(providerInstance, instance.Name, vmid); err == nil && v6iface != "" {
				result.V6 = v6iface
			}
		}
		if result.V6 == "" {
			result.V6 = result.V4 // single-stack: both use the same interface
		}

	default:
		ifaces, err := s.detectInstanceInterfaces(providerInstance, instance, vmidHint)
		if err != nil {
			return nil, err
		}
		if len(ifaces) > 0 {
			result.V4 = ifaces[0]
			result.V6 = ifaces[0]
		}
		if len(ifaces) > 1 {
			result.V6 = ifaces[1]
		}
	}

	return result, nil
}

// DetectAndSaveInstanceInterfaces detects the host-side network interfaces for the given
// instance via SSH and persists pmacct_interface_v4 / pmacct_interface_v6 to the database.
// This function is safe to call regardless of whether agent monitoring is enabled.
func DetectAndSaveInstanceInterfaces(
	ctx context.Context,
	db *gorm.DB,
	providerInstance provider.Provider,
	instance *providerModel.Instance,
	vmidHint string,
) error {
	svc := NewMonitorService(ctx, db)
	ifaces, err := svc.detectBothInterfaces(providerInstance, instance, vmidHint)
	if err != nil {
		return fmt.Errorf("detect interfaces for %s: %w", instance.Name, err)
	}

	updates := map[string]interface{}{}
	if ifaces.V4 != "" && instance.PmacctInterfaceV4 != ifaces.V4 {
		updates["pmacct_interface_v4"] = ifaces.V4
	}
	// Only save V6 if the network type supports IPv6.
	// For no_port_mapping and other IPv4-only network types, V6 should not be saved.
	if ifaces.V6 != "" && isIPv6Capable(instance.NetworkType) && instance.PmacctInterfaceV6 != ifaces.V6 {
		updates["pmacct_interface_v6"] = ifaces.V6
	}

	if len(updates) == 0 {
		return nil
	}

	if err := db.WithContext(ctx).Model(instance).Updates(updates).Error; err != nil {
		return fmt.Errorf("save interfaces for %s: %w", instance.Name, err)
	}

	if global.APP_LOG != nil {
		global.APP_LOG.Debug("updated instance network interfaces",
			zap.Uint("instance_id", instance.ID),
			zap.String("v4", ifaces.V4),
			zap.String("v6", ifaces.V6))
	}
	return nil
}
