package vmcli

import (
	"encoding/base64"
	"fmt"
	"path"
	"strings"
	"time"

	rootProvider "oneclickvirt/provider"
	"oneclickvirt/utils"
)

// preflightRoutedIPv6 performs the shared bridge validation once, followed by
// the small backend-specific capability check. No VM has been cloned or
// launched when this runs.
func (p *Provider) preflightRoutedIPv6(plan rootProvider.RoutedIPv6VMPlan) error {
	if plan.Routed == nil {
		return nil
	}
	exec := p.getExecutor()
	if exec == nil {
		return fmt.Errorf("%s provider not connected", p.spec.DisplayName)
	}
	command := plan.HostCheckCommand()
	switch p.spec.Type {
	case "virtualbox":
		command += "command -v VBoxManage >/dev/null 2>&1 || { echo 'VBoxManage is unavailable' >&2; exit 1; }\n"
	case "multipass":
		command += fmt.Sprintf(`command -v multipass >/dev/null 2>&1 || { echo 'multipass is unavailable' >&2; exit 1; }
multipass networks --format csv 2>/dev/null | tr -d '"' | awk -F, -v bridge=%s 'NR > 1 && ($1 == bridge || $2 == bridge) { found=1 } END { exit !found }' || { echo 'Multipass cannot attach the routed IPv6 bridge; update Multipass and expose the bridge as a Multipass network' >&2; exit 1; }
`, shellQuote(plan.Routed.Bridge))
	case "vagrant":
		command += "command -v vagrant >/dev/null 2>&1 || { echo 'vagrant is unavailable' >&2; exit 1; }\n"
	default:
		return fmt.Errorf("%s does not implement routed IPv6 preflight", p.spec.DisplayName)
	}
	output, err := exec.ExecuteWithTimeout(command, 45*time.Second)
	if err != nil {
		return fmt.Errorf("%s 隧道路由IPv6环境未就绪: %s: %w", p.spec.DisplayName, utils.TruncateString(strings.TrimSpace(output), 1600), err)
	}
	return nil
}

func (p *Provider) virtualBoxCreateScript(name, image, base string, cpu, memoryMB, diskGB int, config rootProvider.InstanceConfig, plan rootProvider.RoutedIPv6VMPlan) (string, time.Duration, error) {
	nic1 := fmt.Sprintf("VBoxManage modifyvm \"$name\" --cpus %d --memory %d --nic1 nat", cpu, memoryMB)
	if plan.IPv6Only {
		nic1 = fmt.Sprintf("VBoxManage modifyvm \"$name\" --cpus %d --memory %d --nic1 none", cpu, memoryMB)
	}
	routedSetup := ""
	if plan.Routed != nil {
		seedPath := path.Join(base, ".oneclickvirt-ipv6-seeds", rootProvider.RoutedIPv6VMSeedFileName(p.spec.Type, name))
		seedCommand, err := plan.NoCloudISOCommand(seedPath, name, config.Metadata["password"])
		if err != nil {
			return "", 0, err
		}
		mac := strings.ReplaceAll(plan.MAC, ":", "")
		routedSetup = fmt.Sprintf(`%s
VBoxManage modifyvm "$name" --nic2 bridged --bridgeadapter2 %s --nictype2 virtio --macaddress2 %s --cableconnected2 on
if ! VBoxManage storagectl "$name" --name oneclickvirt-ipv6-seed --add ide --controller PIIX4 2>/dev/null; then
  VBoxManage showvminfo "$name" --machinereadable | grep -F 'storagecontrollername' | grep -F 'oneclickvirt-ipv6-seed' >/dev/null || { echo 'failed to create dedicated cloud-init controller' >&2; exit 1; }
fi
VBoxManage storageattach "$name" --storagectl oneclickvirt-ipv6-seed --port 0 --device 0 --type dvddrive --medium %s
VBoxManage setextradata "$name" oneclickvirt.routed-ipv6-seed %s
vm_info="$(VBoxManage showvminfo "$name" --machinereadable)"
printf '%%s\n' "$vm_info" | grep -Fx 'nic2="bridged"' >/dev/null
printf '%%s\n' "$vm_info" | grep -Fx %s >/dev/null
`, seedCommand, shellQuote(plan.Routed.Bridge), shellQuote(mac), shellQuote(seedPath), shellQuote(seedPath), shellQuote("bridgeadapter2=\""+plan.Routed.Bridge+"\""))
	}

	return fmt.Sprintf(`set -eu
name=%s
image=%s
base=%s
VBoxManage showvminfo "$name" >/dev/null 2>&1 && { echo "VirtualBox instance already exists: $name" >&2; exit 1; }
created=0
cleanup() {
  if [ "$created" = 1 ]; then
    VBoxManage controlvm "$name" poweroff >/dev/null 2>&1 || true
    VBoxManage unregistervm "$name" --delete >/dev/null 2>&1 || true
  fi
}
trap cleanup ERR
mkdir -p "$base/$name"
created=1
if [ -n "$image" ] && VBoxManage showvminfo "$image" >/dev/null 2>&1; then
  VBoxManage clonevm "$image" --name "$name" --register
else
  VBoxManage createvm --name "$name" --ostype Linux_64 --basefolder "$base" --register
  VBoxManage createhd --filename "$base/$name/$name.vdi" --size %d
  VBoxManage storagectl "$name" --name SATA --add sata --controller IntelAhci
  VBoxManage storageattach "$name" --storagectl SATA --port 0 --device 0 --type hdd --medium "$base/$name/$name.vdi"
fi
%s
%s
VBoxManage startvm "$name" --type headless >/dev/null
trap - ERR
`, shellQuote(name), shellQuote(image), shellQuote(base), diskGB*1024, nic1, routedSetup), 20 * time.Minute, nil
}

func (p *Provider) multipassCreateScript(name, image string, cpu, memoryMB, diskGB int, config rootProvider.InstanceConfig, plan rootProvider.RoutedIPv6VMPlan) (string, time.Duration, error) {
	cloudInitPath := ""
	cloudInitWrite := ""
	networkArgument := ""
	verify := ""
	if plan.Routed != nil {
		cloudInit, err := multipassRoutedCloudInit(plan, name, config.Metadata["password"])
		if err != nil {
			return "", 0, err
		}
		cloudInitPath = "$work/cloud-init.yaml"
		cloudInitWrite = fmt.Sprintf("printf '%%s' %s | base64 -d > \"$work/cloud-init.yaml\"", shellQuote(base64.StdEncoding.EncodeToString([]byte(cloudInit))))
		networkArgument = fmt.Sprintf(" --network %s", shellQuote("name="+plan.Routed.Bridge+",mode=manual,mac="+plan.MAC))
		guestCheck := fmt.Sprintf("ip -o -6 addr show dev oneclickvirt6 | awk '{print $4}' | grep -Fx %s >/dev/null && ip -6 route show default | grep -F %s >/dev/null", shellQuote(plan.Routed.AddressCIDR()), shellQuote(plan.Routed.Gateway))
		verify = fmt.Sprintf("multipass exec \"$name\" -- sh -ceu %s", shellQuote(guestCheck))
	}
	cloudInitArgument := ""
	if cloudInitPath != "" {
		cloudInitArgument = " --cloud-init " + cloudInitPath
	}

	return fmt.Sprintf(`set -eu
name=%s
image=%s
multipass info "$name" >/dev/null 2>&1 && { echo "multipass instance already exists" >&2; exit 1; }
work="$(mktemp -d /tmp/oneclickvirt-multipass.XXXXXX)"
created=0
cleanup() {
  rm -rf "$work"
  if [ "$created" = 1 ]; then
    multipass delete "$name" --purge >/dev/null 2>&1 || (multipass delete "$name" >/dev/null 2>&1 && multipass purge >/dev/null 2>&1) || true
  fi
}
trap cleanup ERR EXIT
%s
multipass launch "$image" --name "$name" --cpus %d --memory %dM --disk %dG%s%s
created=1
%s
created=0
rm -rf "$work"
trap - ERR EXIT
`, shellQuote(name), shellQuote(image), cloudInitWrite, cpu, memoryMB, diskGB, cloudInitArgument, networkArgument, verify), 30 * time.Minute, nil
}

func multipassRoutedCloudInit(plan rootProvider.RoutedIPv6VMPlan, name, password string) (string, error) {
	networkData, err := plan.NoCloudNetworkData()
	if err != nil {
		return "", err
	}
	return plan.NoCloudUserData(name, password) + "network:\n" + indentBlock(networkData, 2), nil
}

func (p *Provider) vagrantCreateScript(name, image, base string, cpu, memoryMB int, config rootProvider.InstanceConfig, plan rootProvider.RoutedIPv6VMPlan) (string, time.Duration, error) {
	routedBlock := ""
	verify := ""
	if plan.Routed != nil {
		guestScript := vagrantRoutedIPv6GuestScript(plan)
		encoded := base64.StdEncoding.EncodeToString([]byte(guestScript))
		bridge := escapeVagrantString(plan.Routed.Bridge)
		routedBlock = fmt.Sprintf(`
  config.vm.network "public_network",
    bridge: "%s",
    dev: "%s",
    mode: "bridge",
    type: "bridge",
    auto_config: false
  config.vm.provision "shell", privileged: true, inline: "printf '%%s' '%s' | base64 -d | /bin/sh"
`, bridge, bridge, encoded)
		guestCheck := fmt.Sprintf("ip -o -6 addr show dev oneclickvirt6 | awk '{print $4}' | grep -Fx %s >/dev/null && ip -6 route show default | grep -F %s >/dev/null", shellQuote(plan.Routed.AddressCIDR()), shellQuote(plan.Routed.Gateway))
		verify = fmt.Sprintf("vagrant ssh -c %s", shellQuote(guestCheck))
	}

	return fmt.Sprintf(`set -eu
name=%s
box=%s
base=%s
dir="$base/$name"
test -e "$dir/Vagrantfile" && { echo "Vagrant instance already exists: $name" >&2; exit 1; }
mkdir -p "$dir"
created=0
cleanup() {
  if [ "$created" = 1 ]; then
    (cd "$dir" && vagrant destroy -f >/dev/null 2>&1) || true
  fi
}
trap cleanup ERR
cat > "$dir/Vagrantfile" <<'VAGRANTFILE'
Vagrant.configure("2") do |config|
  config.vm.box = "%s"
  config.vm.hostname = "%s"
  config.vm.provider "virtualbox" do |vb|
    vb.cpus = %d
    vb.memory = %d
  end
  config.vm.provider "libvirt" do |lv|
    lv.cpus = %d
    lv.memory = %d
  end%s
end
VAGRANTFILE
cd "$dir"
created=1
vagrant up --provider=libvirt || vagrant up --provider=virtualbox || vagrant up
%s
trap - ERR
`, shellQuote(name), shellQuote(image), shellQuote(base), escapeVagrantString(image), escapeVagrantString(name), cpu, memoryMB, cpu, memoryMB, routedBlock, verify), 40 * time.Minute, nil
}

func vagrantRoutedIPv6GuestScript(plan rootProvider.RoutedIPv6VMPlan) string {
	return fmt.Sprintf(`#!/bin/sh
set -eu
command -v systemctl >/dev/null 2>&1 || { echo 'systemd is required to persist routed IPv6 in Vagrant guests' >&2; exit 1; }
install -d -m 0755 /usr/local/sbin /etc/systemd/system
cat > /usr/local/sbin/oneclickvirt-routed-ipv6 <<'SCRIPT'
#!/bin/sh
set -eu
address=%s
gateway=%s
primary="$(ip -4 route show default 2>/dev/null | awk 'NR == 1 { for (i = 1; i <= NF; i++) if ($i == "dev") { print $(i + 1); exit } }')"
iface=""
for entry in /sys/class/net/*; do
  candidate="${entry##*/}"
  case "$candidate" in lo|docker*|veth*|br-*|virbr*) continue ;; esac
  [ "$candidate" = "$primary" ] && continue
  iface="$candidate"
  break
done
[ -n "$iface" ] || { echo 'routed IPv6 NIC was not attached by Vagrant provider' >&2; exit 1; }
if [ "$iface" != oneclickvirt6 ]; then
  ip link set dev "$iface" name oneclickvirt6
  iface=oneclickvirt6
fi
ip link set dev "$iface" up
ip -6 addr replace "$address" dev "$iface"
ip -6 route replace "$gateway/128" dev "$iface"
ip -6 route replace default via "$gateway" dev "$iface" onlink 2>/dev/null || ip -6 route replace default via "$gateway" dev "$iface"
ip -o -6 addr show dev "$iface" | awk '{print $4}' | grep -Fx "$address" >/dev/null
ip -6 route show default | grep -F "$gateway" >/dev/null
SCRIPT
chmod 0755 /usr/local/sbin/oneclickvirt-routed-ipv6
cat > /etc/systemd/system/oneclickvirt-routed-ipv6.service <<'UNIT'
[Unit]
Description=OneClickVirt routed IPv6
After=network-online.target
Wants=network-online.target

[Service]
Type=oneshot
ExecStart=/usr/local/sbin/oneclickvirt-routed-ipv6
RemainAfterExit=yes

[Install]
WantedBy=multi-user.target
UNIT
systemctl daemon-reload
/usr/local/sbin/oneclickvirt-routed-ipv6
systemctl enable oneclickvirt-routed-ipv6.service >/dev/null
`, shellQuote(plan.Routed.AddressCIDR()), shellQuote(plan.Routed.Gateway))
}

func indentBlock(value string, spaces int) string {
	prefix := strings.Repeat(" ", spaces)
	lines := strings.Split(strings.TrimSuffix(value, "\n"), "\n")
	for index, line := range lines {
		lines[index] = prefix + line
	}
	return strings.Join(lines, "\n") + "\n"
}
