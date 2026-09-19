package utils

import (
	"fmt"
	"strings"
	"time"
)

// IPv6EgressOptions controls the host IPv6 egress preflight.  The preflight
// deliberately does not invent a gateway: a missing default route is repaired
// only through mechanisms which can obtain the provider's actual RA/DHCPv6
// configuration.  A guessed gateway can black-hole an otherwise working node.
type IPv6EgressOptions struct {
	// PersistRA writes the interface's accept_ra=2 setting only after the
	// external probe succeeds. Provider creation paths enable it explicitly;
	// callers that only need a read-only probe can leave it false.
	PersistRA bool
	// ProbeURL is the endpoint which returns the observed public IPv6 address.
	// Empty uses https://ipv6.ip.sb.
	ProbeURL string
}

// IPv6HostEgressCommand returns a self-contained POSIX shell probe.  It is
// intentionally suitable for both SSH and Agent executors and emits no
// credentials or unrelated command output.
func IPv6HostEgressCommand(options IPv6EgressOptions) string {
	probeURL := strings.TrimSpace(options.ProbeURL)
	if probeURL == "" {
		probeURL = "https://ipv6.ip.sb"
	}
	persist := "0"
	if options.PersistRA {
		persist = "1"
	}
	// The URL is fixed by callers in production and is shell-quoted here so
	// tests and future callers cannot turn a probe option into shell syntax.
	return fmt.Sprintf(`set -eu
PROBE_URL=%s
PERSIST_RA=%s

route_ready() {
    ip -6 route show default 2>/dev/null | grep -Eq '(^|[[:space:]])default([[:space:]]|$)'
}

iface=""
repair_changed=0
old_iface_ra=""
old_all_ra=""
restore_runtime() {
    [ "$repair_changed" = 1 ] || return 0
    if [ -n "$iface" ] && [ -n "$old_iface_ra" ] && [ -w "/proc/sys/net/ipv6/conf/$iface/accept_ra" ]; then
        printf '%%s' "$old_iface_ra" > "/proc/sys/net/ipv6/conf/$iface/accept_ra" || true
    fi
    if [ -n "$old_all_ra" ] && [ -w /proc/sys/net/ipv6/conf/all/accept_ra ]; then
        printf '%%s' "$old_all_ra" > /proc/sys/net/ipv6/conf/all/accept_ra || true
    fi
}
trap 'status=$?; if [ "$status" -ne 0 ]; then restore_runtime; fi; exit "$status"' EXIT
if ! route_ready; then
    # Pick a real, globally-scoped local address.  Do not use an address
    # returned by an external service or a tunnel allocation as the host NIC.
    iface="$(ip -o -6 addr show scope global 2>/dev/null | awk '$0 !~ / tentative/ {split($4,a,"/"); if (a[1] !~ /^fe80:/) {print $2; exit}}')"
    case "$iface" in
        "") echo "ipv6 egress repair: no global IPv6 interface" >&2; exit 41 ;;
        *[!A-Za-z0-9_.:-]*) echo "ipv6 egress repair: invalid interface name" >&2; exit 42 ;;
    esac

    # accept_ra=2 allows Router Advertisements even when forwarding is on,
    # which is required by many Incus/LXD hosts.  These writes are temporary;
    # persistence happens only after the public probe below succeeds.
    if [ -r "/proc/sys/net/ipv6/conf/$iface/accept_ra" ]; then
        old_iface_ra="$(cat "/proc/sys/net/ipv6/conf/$iface/accept_ra")"
    fi
    if [ -r /proc/sys/net/ipv6/conf/all/accept_ra ]; then
        old_all_ra="$(cat /proc/sys/net/ipv6/conf/all/accept_ra)"
    fi
    if [ -w "/proc/sys/net/ipv6/conf/$iface/accept_ra" ]; then
        printf '2' > "/proc/sys/net/ipv6/conf/$iface/accept_ra" || true
        repair_changed=1
    fi
    if [ -w /proc/sys/net/ipv6/conf/all/accept_ra ]; then
        printf '2' > /proc/sys/net/ipv6/conf/all/accept_ra || true
        repair_changed=1
    fi
    if command -v networkctl >/dev/null 2>&1; then
        networkctl reconfigure "$iface" >/dev/null 2>&1 || true
    fi
    if command -v rdisc6 >/dev/null 2>&1 && command -v timeout >/dev/null 2>&1; then
        timeout 10 rdisc6 -1 "$iface" >/dev/null 2>&1 || true
    fi
    if command -v dhclient >/dev/null 2>&1 && command -v timeout >/dev/null 2>&1; then
        timeout 20 dhclient -6 -1 "$iface" >/dev/null 2>&1 || true
    fi
    # Network-manager/systemd-networkd may reset the runtime knob while
    # reconfiguring. Re-apply it before waiting for the advertised route.
    if [ -w "/proc/sys/net/ipv6/conf/$iface/accept_ra" ]; then
        printf '2' > "/proc/sys/net/ipv6/conf/$iface/accept_ra" || true
    fi

    attempts=0
    while ! route_ready && [ "$attempts" -lt 8 ]; do
        attempts=$((attempts + 1))
        sleep 1
    done
fi

if ! route_ready; then
    echo "ipv6 egress repair: no usable IPv6 default route" >&2
    exit 43
fi

# A route entry alone is insufficient: verify the actual public source via an
# external service, bypassing HTTP proxies which could mask a broken host.
egress="$(curl --noproxy '*' -6 -fsS --connect-timeout 10 --max-time 20 "$PROBE_URL")" || {
    echo "ipv6 egress probe failed" >&2
    exit 44
}
case "$egress" in
    ""|*[!0-9A-Fa-f:.-]*) echo "ipv6 egress probe returned invalid address" >&2; exit 45 ;;
esac
case "$egress" in
    *:*) : ;;
    *) echo "ipv6 egress probe returned a non-IPv6 address" >&2; exit 46 ;;
esac

if [ "$PERSIST_RA" = 1 ] && [ -n "$iface" ] && [ "$(id -u)" = 0 ]; then
    conf=/etc/sysctl.d/99-oneclickvirt-ipv6-egress.conf
    temporary="$conf.tmp.$$"
    umask 022
    printf 'net.ipv6.conf.%%s.accept_ra=2\n' "$iface" > "$temporary"
    mv -f "$temporary" "$conf"
fi
repair_changed=0
printf 'ipv6-egress=%%s\n' "$egress"
`, ShellSingleQuote(probeURL), persist)
}

// EnsureIPv6HostEgress validates and, when safe, repairs native host IPv6
// egress.  It fails closed when no route or external probe can be confirmed.
func EnsureIPv6HostEgress(executor ShellExecutor, options IPv6EgressOptions) error {
	if executor == nil {
		return fmt.Errorf("IPv6出口预检执行器不可用")
	}
	output, err := executor.ExecuteWithTimeout(IPv6HostEgressCommand(options), 45*time.Second)
	if err != nil {
		return fmt.Errorf("IPv6默认路由或公网出口不可用: output=%s: %w", SanitizeUserInput(strings.TrimSpace(output)), err)
	}
	var observed string
	for _, line := range strings.Split(strings.ReplaceAll(output, "\r\n", "\n"), "\n") {
		if !strings.HasPrefix(line, "ipv6-egress=") {
			continue
		}
		value := strings.TrimSpace(strings.TrimPrefix(line, "ipv6-egress="))
		if observed != "" && observed != value {
			return fmt.Errorf("IPv6公网出口探针返回了冲突地址: output=%s", SanitizeUserInput(strings.TrimSpace(output)))
		}
		observed = value
	}
	if observed == "" {
		return fmt.Errorf("IPv6公网出口探针未返回有效结果: output=%s", SanitizeUserInput(strings.TrimSpace(output)))
	}
	normalized, parseErr := NormalizeIPv6Address(observed)
	if parseErr != nil || !IsPublicIPv6(normalized) {
		return fmt.Errorf("IPv6公网出口探针返回了非公网IPv6地址: output=%s", SanitizeUserInput(strings.TrimSpace(output)))
	}
	return nil
}
