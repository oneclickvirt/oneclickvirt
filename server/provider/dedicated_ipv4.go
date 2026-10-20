package provider

import (
	"context"
	"crypto/sha256"
	_ "embed"
	"fmt"
	"net"
	"regexp"
	"strings"

	"oneclickvirt/utils"
)

//go:embed dedicated_ipv4.sh
var dedicatedIPv4Helper string

const dedicatedIPv4HelperPath = "/usr/local/lib/oneclickvirt/dedicated-ipv4.sh"

var dedicatedIPv4Name = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.-]{0,127}$`)

func UsesDedicatedIPv4Forwarding(runtime string) bool {
	switch utils.NormalizeProviderType(runtime) {
	case "docker", "podman", "containerd", "incus", "lxd", "qemu":
		return true
	default:
		return false
	}
}

func dedicatedIPv4Owner(runtime, name string) string {
	sum := sha256.Sum256([]byte(runtime + ":" + name))
	return fmt.Sprintf("ocv4-%x", sum[:10])
}

// ConfigureDedicatedIPv4 completes both directions of the allocated endpoint.
// Runtime bridges keep their private guest addresses; only the assigned public
// /32 is forwarded, without changing the node's management address or gateway.
func ConfigureDedicatedIPv4(ctx context.Context, runtime Provider, config InstanceConfig) error {
	kind := utils.NormalizeProviderType(runtime.GetType())
	if !UsesDedicatedIPv4Forwarding(kind) {
		return nil
	}
	network := strings.ToLower(strings.TrimSpace(config.Metadata["network_type"]))
	if network != "dedicated_ipv4" && network != "dedicated_ipv4_ipv6" {
		return nil
	}
	address := strings.TrimSpace(config.Metadata["static_ipv4"])
	ip := net.ParseIP(address)
	if ip == nil || ip.To4() == nil || !ip.IsGlobalUnicast() || ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast() {
		return fmt.Errorf("独立IPv4缺少有效公网分配地址")
	}
	if !dedicatedIPv4Name.MatchString(config.Name) {
		return fmt.Errorf("独立IPv4实例名称无效")
	}
	owner := dedicatedIPv4Owner(kind, config.Name)
	args := utils.ShellSingleQuote(kind) + " " + utils.ShellSingleQuote(config.Name) + " " + utils.ShellSingleQuote(ip.String())
	unit := owner + ".service"
	unitBody := fmt.Sprintf(`[Unit]
Description=OneClickVirt dedicated IPv4 %s
After=network-online.target docker.service podman.service containerd.service incus.service snap.lxd.daemon.service libvirtd.service
Wants=network-online.target

[Service]
Type=simple
ExecStart=/bin/bash %s --watch %s
ExecStopPost=/bin/bash %s --remove %s
Restart=on-failure
RestartSec=5
TimeoutStopSec=45

[Install]
WantedBy=multi-user.target
`, owner, dedicatedIPv4HelperPath, args, dedicatedIPv4HelperPath, args)
	command := "set -eu\numask 077\ncommand -v python3 >/dev/null\ncommand -v iptables >/dev/null\ncommand -v flock >/dev/null\n" +
		"command -v systemctl >/dev/null || { echo 'dedicated IPv4 persistence requires systemd' >&2; exit 1; }\n" +
		"install -d -m 700 /usr/local/lib/oneclickvirt\n" +
		"temporary=$(mktemp /usr/local/lib/oneclickvirt/.dedicated-ipv4.XXXXXX)\n" +
		"cleanup() { status=$?; rm -f -- \"$temporary\"; if test \"$status\" -ne 0; then " +
		"systemctl disable --now " + unit + " >/dev/null 2>&1 || true; " +
		"/bin/bash " + dedicatedIPv4HelperPath + " --remove " + args + " || true; " +
		"rm -f /etc/systemd/system/" + unit + "; systemctl daemon-reload || true; fi; exit \"$status\"; }\n" +
		"trap cleanup EXIT\ntrap 'exit 1' HUP INT TERM\n" +
		"printf '%s' " + utils.ShellSingleQuote(dedicatedIPv4Helper) + " > \"$temporary\"\n" +
		"chmod 700 \"$temporary\"\nmv -f -- \"$temporary\" " + dedicatedIPv4HelperPath + "\n" +
		"/bin/bash " + dedicatedIPv4HelperPath + " --apply " + args + "\n" +
		"printf '%s' " + utils.ShellSingleQuote(unitBody) + " > /etc/systemd/system/" + unit + "\n" +
		"systemctl daemon-reload\nsystemctl enable " + unit + "\nsystemctl restart " + unit + "\n" +
		"/bin/bash " + dedicatedIPv4HelperPath + " --apply " + args + "\n"
	output, err := runtime.ExecuteSSHCommand(ctx, command)
	if err != nil {
		return fmt.Errorf("配置独立IPv4双向转发或持久恢复失败: output=%s: %w", utils.TruncateString(output, 1600), err)
	}
	return nil
}

func DedicatedIPv4Lifecycle(ctx context.Context, runtime Provider, name string, remove bool) error {
	kind := utils.NormalizeProviderType(runtime.GetType())
	if !UsesDedicatedIPv4Forwarding(kind) || !dedicatedIPv4Name.MatchString(name) {
		return nil
	}
	owner := dedicatedIPv4Owner(kind, name)
	unit := owner + ".service"
	command := "set -eu\nunit=/etc/systemd/system/" + unit + "\nif test -f \"$unit\"; then\n"
	if remove {
		command += "systemctl disable --now " + unit + "\nrm -f -- \"$unit\"\nsystemctl daemon-reload\n"
	} else {
		// ExecStart contains validated, non-secret runtime/name/address arguments.
		command += "line=$(sed -n 's|^ExecStart=/bin/bash " + dedicatedIPv4HelperPath + " --watch ||p' \"$unit\")\n" +
			"test -n \"$line\"\n/bin/bash -c " + utils.ShellSingleQuote("/bin/bash "+dedicatedIPv4HelperPath+" --apply ") + "\"$line\"\n"
	}
	command += "fi\n"
	if output, err := runtime.ExecuteSSHCommand(ctx, command); err != nil {
		return fmt.Errorf("恢复或回收独立IPv4失败: output=%s: %w", utils.TruncateString(output, 1200), err)
	}
	return nil
}
