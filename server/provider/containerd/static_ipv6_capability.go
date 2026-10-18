package containerd

import (
	"context"
	"fmt"
	"oneclickvirt/utils"
)

// Upgrade only a bridge whose name, plugin and recorded subnet prove ownership.
// Older installers omitted the CNI ips capability, silently ignoring --ip6.
func (c *ContainerdProvider) ensureStaticIPv6Capability(ctx context.Context) error {
	command := "python3 - /etc/cni/net.d/11-containerd-ipv6.conflist /usr/local/bin/containerd_ipv6_subnet <<'OCV_STATIC_IPS'\n" + containerdStaticIPv6CapabilityProgram + "\nOCV_STATIC_IPS"
	output, err := utils.ExecuteShellCommandContext(ctx, c.sshClient, command)
	if err != nil {
		return fmt.Errorf("启用托管 CNI 固定 IPv6 能力失败: %w; output: %s", err, utils.TruncateString(output, 1000))
	}
	return nil
}

const containerdStaticIPv6CapabilityProgram = `import fcntl
import ipaddress
import json
import os
from pathlib import Path
import stat
import sys
import tempfile

path, state = map(Path, sys.argv[1:])
if path.is_symlink():
    sys.exit('Refusing to change a symlinked CNI configuration')
with path.with_suffix(path.suffix + '.ocv.lock').open('a') as lock:
    fcntl.flock(lock, fcntl.LOCK_EX)
    config = json.loads(path.read_text())
    subnet = ipaddress.IPv6Network(state.read_text().strip(), strict=True)
    bridges = [p for p in config.get('plugins', []) if p.get('type') == 'bridge' and p.get('bridge') == 'ctn-br1' and p.get('ipam', {}).get('type') == 'host-local']
    if config.get('name') != 'containerd-ipv6' or len(bridges) != 1:
        sys.exit('CNI configuration is not the managed containerd IPv6 bridge')
    bridge = bridges[0]
    ranges = bridge['ipam'].get('ranges', [])
    if not any(ipaddress.ip_network(r['subnet']) == subnet for group in ranges for r in group):
        sys.exit('CNI subnet differs from recorded containerd IPv6 subnet')
    capabilities = bridge.setdefault('capabilities', {})
    if capabilities.get('ips') is not True:
        capabilities['ips'] = True
        mode = stat.S_IMODE(path.stat().st_mode)
        fd, temp = tempfile.mkstemp(prefix=path.name + '.', dir=path.parent)
        try:
            with os.fdopen(fd, 'w') as f:
                os.fchmod(f.fileno(), mode)
                json.dump(config, f, indent=2)
                f.write('\n')
                f.flush()
                os.fsync(f.fileno())
            os.replace(temp, path)
        finally:
            if os.path.exists(temp):
                os.unlink(temp)`
