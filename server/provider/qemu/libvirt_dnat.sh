#!/usr/bin/env bash
# Managed by oneclickvirt: allow only traffic matching explicit DNAT mappings.
set -euo pipefail
if [[ "${1:-}" != --apply ]]; then
    [[ "${1:-}" == default ]] || exit 0
    case "${2:-}" in started|updated) ;; *) exit 0 ;; esac
fi
bridge=virbr0
if [[ -s /usr/local/bin/qemu_bridge ]]; then
    bridge=$(cat /usr/local/bin/qemu_bridge)
fi
[[ "$bridge" =~ ^[a-zA-Z0-9_.:-]+$ ]] || exit 1
exec 9>/run/lock/oneclickvirt-qemu-dnat.lock
flock -w 15 9 || exit 1
if command -v iptables >/dev/null 2>&1 && iptables -w 10 -S LIBVIRT_FWI >/dev/null 2>&1; then
    # libvirt may prepend its reject rules on DHCP updates. Reposition our
    # exact owned exception instead of accepting a stale rule after reject.
    while iptables -w 10 -C LIBVIRT_FWI -d 192.168.122.0/24 -o "$bridge" -m conntrack --ctstate DNAT -m comment --comment oneclickvirt-qemu-dnat -j ACCEPT 2>/dev/null; do
        iptables -w 10 -D LIBVIRT_FWI -d 192.168.122.0/24 -o "$bridge" -m conntrack --ctstate DNAT -m comment --comment oneclickvirt-qemu-dnat -j ACCEPT
    done
    iptables -w 10 -I LIBVIRT_FWI 1 -d 192.168.122.0/24 -o "$bridge" -m conntrack --ctstate DNAT -m comment --comment oneclickvirt-qemu-dnat -j ACCEPT
fi
if command -v nft >/dev/null 2>&1 && nft list chain ip libvirt_network guest_input >/dev/null 2>&1; then
    handles=$(nft -a list chain ip libvirt_network guest_input | awk '/comment "oneclickvirt-qemu-dnat"/ {for (i=1; i<NF; i++) if ($i=="handle") print $(i+1)}')
    while IFS= read -r handle; do
        [[ -n "$handle" ]] || continue
        [[ "$handle" =~ ^[0-9]+$ ]] || exit 1
        nft delete rule ip libvirt_network guest_input handle "$handle"
    done <<< "$handles"
    nft insert rule ip libvirt_network guest_input oifname "$bridge" ip daddr 192.168.122.0/24 ct status dnat accept comment '"oneclickvirt-qemu-dnat"'
fi
