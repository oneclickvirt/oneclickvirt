#!/usr/bin/env bash
# Controller-owned 1:1 IPv4 forwarding for managed Linux runtime bridges.
set -euo pipefail
export PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin:/snap/bin
export LC_ALL=C
mode=${1:?} runtime=${2:?} instance=${3:?} public=${4:?}
case "$mode" in --apply|--watch|--remove) ;; *) exit 2 ;; esac
case "$runtime" in docker|podman|containerd|incus|lxd|qemu) ;; *) exit 2 ;; esac
[[ "$instance" =~ ^[a-zA-Z0-9][a-zA-Z0-9_.-]{0,127}$ ]] || exit 2
python3 - "$public" <<'PY'
import ipaddress,sys
a=ipaddress.IPv4Address(sys.argv[1])
if not a.is_global: raise SystemExit('A dedicated public IPv4 is required')
PY
owner=$(printf '%s' "$runtime:$instance" | sha256sum)
owner=ocv4-${owner:0:20}
state_dir=/var/lib/oneclickvirt/dedicated-ipv4
mkdir -p "$state_dir" /run/lock
chmod 700 "$state_dir"
state=$state_dir/$owner.state
comment=oneclickvirt:$owner
nft_table=oneclickvirt_dedicated4_${owner#ocv4-}
ipt=(iptables)
if iptables --help 2>&1 | grep -q -- '--wait'; then ipt+=(--wait 5); fi

resolve_guest() {
    local raw uri domain_xml network_xml
    case "$runtime" in
        docker|podman) raw=$("$runtime" inspect "$instance") || return 1 ;;
        containerd) raw=$(nerdctl inspect "$instance") || return 1 ;;
        incus) raw=$(incus list "$instance" --format json) || return 1 ;;
        lxd) raw=$(lxc list "$instance" --format json) || return 1 ;;
        qemu)
            uri=qemu:///system
            if virsh -c lxc:/// dominfo "$instance" >/dev/null 2>&1; then uri=lxc:///; fi
            [[ $(virsh -c "$uri" domstate "$instance" 2>/dev/null) == running ]] || return 1
            raw=$(virsh -c "$uri" domifaddr "$instance" --source agent 2>/dev/null || true)
            if ! grep -q ipv4 <<<"$raw"; then raw=$(virsh -c "$uri" domifaddr "$instance" --source lease 2>/dev/null || true); fi
            if ! grep -q ipv4 <<<"$raw"; then
                # Static LXC guests have no DHCP lease. Resolve only the
                # reservation whose MAC belongs to this running domain.
                domain_xml=$(virsh -c "$uri" dumpxml "$instance") || return 1
                network_xml=$(virsh -c qemu:///system net-dumpxml default) || return 1
                raw=$(python3 - "$instance" "$domain_xml" "$network_xml" <<'PY'
import sys,xml.etree.ElementTree as E
name,domain_xml,network_xml=sys.argv[1:]
domain,network=E.fromstring(domain_xml),E.fromstring(network_xml)
if domain.findtext('name')!=name: raise SystemExit(1)
macs={m.get('address','').lower() for iface in domain.findall('./devices/interface')
      if iface.get('type')=='network' and (iface.find('source') is not None and iface.find('source').get('network')=='default')
      for m in iface.findall('mac')}
addresses={h.get('ip') for h in network.findall('./ip/dhcp/host')
           if h.get('name')==name and h.get('mac','').lower() in macs and h.get('ip')}
if len(addresses)!=1: raise SystemExit(1)
print(next(iter(addresses)))
PY
                ) || return 1
            fi
            ;;
    esac
    printf '%s' "$raw" | python3 -c '
import ipaddress,json,re,sys
runtime,name=sys.argv[1:]
raw=sys.stdin.read(); candidates=[]
if runtime in ["docker","podman","containerd"]:
 rows=json.loads(raw)
 if not rows or not rows[0].get("State",{}).get("Running"): raise SystemExit(1)
 networks=rows[0].get("NetworkSettings",{}).get("Networks",{})
 for key in sorted(networks,key=lambda n:("ipv4" not in n.lower(),n)):
  candidates.append(networks[key].get("IPAddress",""))
elif runtime in ["incus","lxd"]:
 rows=[r for r in json.loads(raw) if r.get("name")==name]
 if len(rows)!=1 or rows[0].get("status","").lower()!="running": raise SystemExit(1)
 for key,net in sorted((rows[0].get("state",{}).get("network") or {}).items()):
  if key=="lo": continue
  candidates.extend(a.get("address","") for a in net.get("addresses",[]) if a.get("family")=="inet")
else:
 candidates=re.findall(r"\b(?:\d{1,3}\.){3}\d{1,3}(?:/\d+)?",raw)
for candidate in candidates:
 try: a=ipaddress.ip_interface(candidate).ip
 except ValueError: continue
 if a.version==4 and a.is_private and not (a.is_loopback or a.is_link_local or a.is_unspecified):
  print(a);raise SystemExit(0)
raise SystemExit(1)
' "$runtime" "$instance"
}

remove_rule() {
    local table=$1 chain=$2
    shift 2
    while "${ipt[@]}" -t "$table" -C "$chain" "$@" 2>/dev/null; do
        "${ipt[@]}" -t "$table" -D "$chain" "$@" || return 1
    done
}

rules() {
    local operation=$1 private=$2
    "$operation" nat PREROUTING -d "$public/32" -m comment --comment "$comment" -j DNAT --to-destination "$private" || return 1
    "$operation" nat OUTPUT -d "$public/32" -m comment --comment "$comment" -j DNAT --to-destination "$private" || return 1
    "$operation" nat POSTROUTING -s "$private/32" -m comment --comment "$comment" -j SNAT --to-source "$public" || return 1
    "$operation" nat POSTROUTING -s "$public/32" -d "$private/32" -m comment --comment "$comment" -j MASQUERADE || return 1
    "$operation" filter FORWARD -d "$private/32" -m comment --comment "$comment" -j ACCEPT || return 1
    "$operation" filter FORWARD -s "$private/32" -m comment --comment "$comment" -j ACCEPT || return 1
}

# Incus/LXD can have native nftables masquerading chains at priority 100.
# Their earlier registration wins over a subsequently inserted iptables SNAT.
# Use a separate, instance-owned NAT table before those chains when available.
ensure_native_nat() {
    local private=$1 primary=$2
    command -v nft >/dev/null 2>&1 || return 0
    local replace=""
    if nft list table ip "$nft_table" >/dev/null 2>&1; then
        replace="delete table ip $nft_table"
    fi
    nft -f - <<EOF
$replace
table ip $nft_table {
 chain prerouting {
  type nat hook prerouting priority -101; policy accept;
  ip daddr $public dnat to $private
 }
 chain output {
  type nat hook output priority -101; policy accept;
  ip daddr $public dnat to $private
 }
 chain postrouting {
  type nat hook postrouting priority 99; policy accept;
  ip saddr $public ip daddr $private snat to $primary
  ip saddr $private snat to $public
 }
}
EOF
}

remove_native_nat() {
    command -v nft >/dev/null 2>&1 || return 0
    if nft list table ip "$nft_table" >/dev/null 2>&1; then
        nft delete table ip "$nft_table" || return 1
    fi
}

ensure_rule() {
    local table=$1 chain=$2
    shift 2
    if ! "${ipt[@]}" -t "$table" -C "$chain" "$@" 2>/dev/null; then
        "${ipt[@]}" -t "$table" -I "$chain" 1 "$@"
    fi
}

valid_iface() { [[ "$1" =~ ^[a-zA-Z0-9][a-zA-Z0-9_.:-]{0,14}$ && "$1" != lo ]]; }

clean_locked() {
    local final=${1:-false} old_public old_private old_iface owned
    if [[ -s "$state" ]]; then
        read -r old_public old_private old_iface owned <"$state"
        [[ "$old_public" == "$public" ]] || return 1
        valid_iface "$old_iface" || return 1
        remove_native_nat || return 1
        if [[ "$owned" == 1 ]]; then
            if ip -o -4 address show dev "$old_iface" | awk -v address="$public" '{for(i=1;i<NF;i++) if($i=="inet") {split($(i+1),a,"/");if(a[1]==address)found=1}} END {exit !found}'; then
                ip -4 address del "$public/32" dev "$old_iface" || return 1
            fi
        fi
        rules remove_rule "$old_private" || return 1
        rm -f -- "$state"
    fi
    if [[ "$final" == true ]]; then
        remove_rule filter INPUT -d "$public/32" -m conntrack ! --ctstate ESTABLISHED,RELATED -m comment --comment "$comment" -j DROP || return 1
        remove_rule filter INPUT -d "$public/32" -m comment --comment "$comment" -j DROP || return 1
    fi
}

write_state() {
    printf '%s %s %s %s\n' "$public" "$1" "$2" "$3" >"$state.new" || return 1
    chmod 600 "$state.new" || return 1
    mv -f -- "$state.new" "$state"
}

apply_locked() {
    local private=$1 route iface primary alias_owned=0 old_public old_private old_iface owned
    # Check ownership before binding an alias or changing any packet path.
    for other in "$state_dir"/*.state; do
        [[ -f "$other" && "$other" != "$state" ]] || continue
        read -r old_public _ <"$other"
        [[ "$old_public" != "$public" ]] || { echo 'Dedicated IPv4 is owned by another instance' >&2; return 1; }
    done
    route=$(ip -j -4 route get 1.1.1.1 2>/dev/null || ip -4 route get 1.1.1.1)
    read -r iface primary < <(printf '%s' "$route" | python3 -c '
import json,sys
s=sys.stdin.read()
try:
 r=json.loads(s)[0]; print(r["dev"],r.get("prefsrc",r.get("src","")))
except (ValueError,KeyError,IndexError,TypeError):
 f=s.split();print(f[f.index("dev")+1],f[f.index("src")+1])
')
    valid_iface "$iface" && [[ -n "$primary" && "$primary" != "$public" ]] || { echo 'Refusing to forward the node management IPv4' >&2; return 1; }
    [[ "$private" != "$public" ]] || return 1
    if [[ -s "$state" ]]; then
        read -r old_public old_private old_iface owned <"$state"
        [[ "$old_public" == "$public" ]] || return 1
        if [[ "$old_private" != "$private" || "$old_iface" != "$iface" ]]; then
            clean_locked false || return 1
        else
            alias_owned=$owned
        fi
    fi
    ensure_rule filter INPUT -d "$public/32" -m conntrack ! --ctstate ESTABLISHED,RELATED -m comment --comment "$comment" -j DROP || return 1
    # Upgrade the original unconditional guard without blocking replies to a
    # node connection whose local source is this same public alias.
    remove_rule filter INPUT -d "$public/32" -m comment --comment "$comment" -j DROP || return 1
    # Write ownership first so a partial failure can undo only these rules.
    write_state "$private" "$iface" "$alias_owned" || return 1
    if ! ensure_native_nat "$private" "$primary"; then clean_locked true; return 1; fi
    if ! rules ensure_rule "$private"; then clean_locked true; return 1; fi
    sysctl -w net.ipv4.ip_forward=1 >/dev/null || { clean_locked true; return 1; }
    if ! ip -o -4 address show dev "$iface" | awk -v address="$public" '{for(i=1;i<NF;i++) if($i=="inet") {split($(i+1),a,"/");if(a[1]==address)found=1}} END {exit !found}'; then
        ip -4 address add "$public/32" dev "$iface" || { clean_locked true; return 1; }
        if ! write_state "$private" "$iface" 1; then
            ip -4 address del "$public/32" dev "$iface" || true
            clean_locked true
            return 1
        fi
    fi
}

once() {
    local private
    private=$(resolve_guest) || return 1
    (
        flock -x 9 || exit 1
        apply_locked "$private"
    ) 9>/run/lock/oneclickvirt-dedicated-ipv4.lock
}

case "$mode" in
    --apply)
        ready=false
        for ((attempt=0; attempt<45; attempt++)); do
            if private=$(resolve_guest); then ready=true; break; fi
            sleep 2
        done
        [[ "$ready" == true ]] || exit 1
        (flock -x 9 || exit 1; apply_locked "$private") 9>/run/lock/oneclickvirt-dedicated-ipv4.lock
        ;;
    --remove)
        (flock -x 9 || exit 1; clean_locked true) 9>/run/lock/oneclickvirt-dedicated-ipv4.lock
        ;;
    --watch)
        while :; do
            if ! once; then
                # Keep the INPUT guard while a stopped guest has no address.
                (flock -x 9 || exit 1; clean_locked false) 9>/run/lock/oneclickvirt-dedicated-ipv4.lock || exit 1
            fi
            sleep 5
        done
        ;;
esac
