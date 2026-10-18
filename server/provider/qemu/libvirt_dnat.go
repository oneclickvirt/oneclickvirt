package qemu

import (
	_ "embed"
	"fmt"
	"time"
)

//go:embed libvirt_dnat.sh
var libvirtDNATHook string

func (p *QEMUProvider) ensureLibvirtDNATForwarding() error {
	// libvirt has its own final reject rule. An accept in a separate nftables
	// base chain does not override it. Keep the exception limited to DNAT and
	// reapply it when the default network is restarted.
	script := fmt.Sprintf(`set -eu
hook=/etc/libvirt/hooks/network.d/50-oneclickvirt-qemu-dnat
mkdir -p "$(dirname "$hook")"
tmp=$(mktemp "${hook}.XXXXXX")
trap 'rm -f "$tmp"' EXIT
printf '%%s' %s > "$tmp"
chmod 0755 "$tmp"
mv -f "$tmp" "$hook"
bash "$hook" --apply
`, shellSingleQuote(libvirtDNATHook))
	if _, err := p.sshClient.ExecuteViaTempScript(script, nil, time.Minute); err != nil {
		return fmt.Errorf("configure libvirt DNAT forwarding: %w", err)
	}
	return nil
}
