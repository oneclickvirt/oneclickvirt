package vmware

import (
	"fmt"
	"path"
	"strings"
	"time"

	"oneclickvirt/provider"
	"oneclickvirt/utils"
)

const vmwareRoutedIPv6VMNet = "vmnet0"

// preflightRoutedIPv6 validates the VMware-specific leg of the tunnel bridge.
// VMware Workstation does not attach a VMX NIC directly to a Linux bridge; its
// bridged vmnet must be mapped to the managed bridge first.
func (p *VMwareProvider) preflightRoutedIPv6(plan provider.RoutedIPv6VMPlan) error {
	if plan.Routed == nil {
		return nil
	}
	exec := p.getExecutor()
	if exec == nil {
		return fmt.Errorf("VMware provider not connected")
	}
	command := plan.HostCheckCommand() + fmt.Sprintf(`command -v vmrun >/dev/null 2>&1 || { echo 'vmrun is unavailable' >&2; exit 1; }
test -c %s || { echo 'VMware vmnet0 device is unavailable' >&2; exit 1; }
test -r /etc/vmware/networking || { echo 'VMware network configuration is unavailable' >&2; exit 1; }
grep -F 'VNET_0_INTERFACE' /etc/vmware/networking | grep -F %s >/dev/null || { echo 'VMware vmnet0 is not bridged to the OneClickVirt routed IPv6 bridge; map VNET_0_INTERFACE to the bridge and restart vmware-networks before creating a dual-stack VM' >&2; exit 1; }
`, utils.ShellSingleQuote("/dev/"+vmwareRoutedIPv6VMNet), utils.ShellSingleQuote(plan.Routed.Bridge))
	output, err := exec.ExecuteWithTimeout(command, 45*time.Second)
	if err != nil {
		return fmt.Errorf("VMware隧道路由IPv6环境未就绪: %s: %w", utils.TruncateString(strings.TrimSpace(output), 1600), err)
	}
	return nil
}

// configureRoutedIPv6 writes the second custom vmnet NIC and a NoCloud seed
// atomically before the guest ever starts. A failure leaves no half-configured
// VMX state behind.
func (p *VMwareProvider) configureRoutedIPv6(exec utils.ShellExecutor, vmx string, config provider.InstanceConfig, plan provider.RoutedIPv6VMPlan) error {
	if plan.Routed == nil {
		return nil
	}
	seedPath := path.Join(p.libraryPath(), ".oneclickvirt-ipv6-seeds", provider.RoutedIPv6VMSeedFileName("vmware", config.Name))
	seedCommand, err := plan.NoCloudISOCommand(seedPath, config.Name, config.Metadata["password"])
	if err != nil {
		return err
	}
	if output, err := exec.ExecuteWithTimeout(seedCommand, 60*time.Second); err != nil {
		return fmt.Errorf("创建VMware IPv6 cloud-init种子失败: %s: %w", utils.TruncateString(strings.TrimSpace(output), 1200), err)
	}

	updates := []string{
		`ethernet1.present = "TRUE"`,
		`ethernet1.startConnected = "TRUE"`,
		`ethernet1.connectionType = "custom"`,
		`ethernet1.vnet = "` + vmwareRoutedIPv6VMNet + `"`,
		`ethernet1.virtualDev = "e1000e"`,
		`ethernet1.addressType = "static"`,
		`ethernet1.checkMACAddress = "FALSE"`,
		`ethernet1.address = "` + strings.ToLower(plan.MAC) + `"`,
		`ide1:1.present = "TRUE"`,
		`ide1:1.deviceType = "cdrom-image"`,
		`ide1:1.fileName = "` + seedPath + `"`,
	}
	if plan.IPv6Only {
		updates = append(updates, `ethernet0.present = "FALSE"`)
	}

	seedLine := `ide1:1.fileName = "` + seedPath + `"`
	var script strings.Builder
	fmt.Fprintf(&script, `set -eu
vmx=%s
tmp="${vmx}.oneclickvirt-ipv6.tmp"
next="${tmp}.next"
cp "$vmx" "$tmp"
if grep -q '^ide1:1.fileName = ' "$tmp" && ! grep -Fx %s "$tmp" >/dev/null; then
  echo 'VMware IDE slot ide1:1 is occupied; cannot safely attach routed IPv6 cloud-init seed' >&2
  exit 1
fi
upsert() {
  key=$1
  value=$2
  awk -v key="$key" -v value="$value" 'index($0, key " = ") == 1 { print value; found=1; next } { print } END { if (!found) print value }' "$tmp" > "$next"
  mv "$next" "$tmp"
}
`, shellQuote(vmx), shellQuote(seedLine))
	for _, line := range updates {
		key := strings.SplitN(line, " = ", 2)[0]
		fmt.Fprintf(&script, "upsert %s %s\n", shellQuote(key), shellQuote(line))
	}
	fmt.Fprintf(&script, `mv "$tmp" "$vmx"
grep -Fx %s "$vmx" >/dev/null
grep -Fx 'ethernet1.connectionType = "custom"' "$vmx" >/dev/null
grep -Fx 'ethernet1.vnet = "vmnet0"' "$vmx" >/dev/null
grep -Fx %s "$vmx" >/dev/null
`, shellQuote(seedLine), shellQuote(`ethernet1.address = "`+strings.ToLower(plan.MAC)+`"`))

	output, err := exec.ExecuteWithTimeout(script.String(), 45*time.Second)
	if err != nil {
		return fmt.Errorf("写入VMware隧道路由IPv6配置失败: %s: %w", utils.TruncateString(strings.TrimSpace(output), 1600), err)
	}
	return nil
}

func (p *VMwareProvider) cleanupRoutedIPv6VM(exec utils.ShellExecutor, vmx, instanceName string) {
	seedPath := path.Join(p.libraryPath(), ".oneclickvirt-ipv6-seeds", provider.RoutedIPv6VMSeedFileName("vmware", instanceName))
	_, _ = exec.ExecuteWithTimeout(fmt.Sprintf("vmrun deleteVM %s 2>/dev/null || rm -rf %s; rm -f -- %s", shellQuote(vmx), shellQuote(path.Dir(vmx)), shellQuote(seedPath)), 5*time.Minute)
}
