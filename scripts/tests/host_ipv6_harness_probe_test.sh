#!/usr/bin/env bash
set -euo pipefail

repo_root=$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)
tmp_dir=$(mktemp -d)
trap 'rm -rf "$tmp_dir"' EXIT
host_ipv6_before='2a01:4f8:c014:1a63::1'
host_ipv6_interface='eth0'
extract_probe_builder() {
    awk '
        $0 == "_m10_host_ipv6_probe_script() {" { inside=1 }
        inside && $0 == "run_module_10() {" { exit }
        inside { print }
    ' "$repo_root/action_tests/modules/10_instances.sh"
}
eval "$(extract_probe_builder)"
_m10_host_ipv6_probe_script "$host_ipv6_before" "$host_ipv6_interface" >"$tmp_dir/remote.sh"
bash -n "$tmp_dir/remote.sh"
probe_command=$(_m10_host_ipv6_probe_command "$host_ipv6_before" "$host_ipv6_interface")
[[ "$probe_command" != *$'\n'* ]] || { echo "remote probe command must be one line" >&2; exit 1; }
source_probe_command=$(_m10_host_ipv6_source_probe_command)
[[ "$source_probe_command" != *$'\n'* ]] || { echo "source probe command must be one line" >&2; exit 1; }

run_probe() {
    local scenario="$1"
    local prefix="${2:-120}"
    OCV_PROBE_SCENARIO="$scenario" OCV_PROBE_PREFIX="$prefix" bash -c '
ip() {
    case "$*" in
        "-j -6 addr show")
            if [ "$OCV_PROBE_SCENARIO" = localized ]; then
                printf "\033[31mUngültige IPv6-Adresse / adresse IPv6 invalide / IPv6 地址无效\033[0m\n"
            else
                printf "\033[32m%s\033[0m\n" "[{\"ifname\":\"eth0\",\"addr_info\":[{\"family\":\"inet6\",\"local\":\"2a01:4f8:c014:1a63::1\",\"prefixlen\":${OCV_PROBE_PREFIX}}]}]"
            fi
            ;;
        "-j -6 route show default")
            if [ "$OCV_PROBE_SCENARIO" = localized ]; then
                printf "\033[33mStandardroute / route par défaut / IPv6 默认路由\033[0m\n"
            else
                printf "\033[33m%s\033[0m\n" "[{\"dst\":\"default\",\"gateway\":\"fe80::1\"}]"
            fi
            ;;
    esac
}
curl() { printf "2a01:4f8:c014:1a63::1\n"; }
base64() {
    [[ "${1:-}" == "-d" ]] || return 1
    python3 -c "import base64,sys; sys.stdout.buffer.write(base64.b64decode(sys.stdin.buffer.read()))"
}
export -f ip curl base64
remote_command="timeout 90 bash -c $(printf "%q" "$1")"
bash -c "$remote_command"
' bash "$probe_command"
}

run_source_probe() {
    local scenario="$1"
    OCV_PROBE_SCENARIO="$scenario" bash -c '
ip() {
    case "$*" in
        "-j -6 addr show scope global")
            if [ "$OCV_PROBE_SCENARIO" = localized ]; then
                printf "\033[31mUngültige IPv6-Adresse / adresse IPv6 invalide / IPv6 地址无效\033[0m\n"
            elif [ "$OCV_PROBE_SCENARIO" = multiple ]; then
                printf "\033[32m%s\033[0m\n" "[{\"ifname\":\"eth1\",\"addr_info\":[{\"family\":\"inet6\",\"local\":\"2a01:4f8:c014:1a63::2\",\"scope\":\"global\"}]},{\"ifname\":\"eth0\",\"addr_info\":[{\"family\":\"inet6\",\"local\":\"2a01:4f8:c014:1a63::1\",\"scope\":\"global\"}]}]"
            else
                printf "\033[32m%s\033[0m\n" "[{\"ifname\":\"eth0\",\"addr_info\":[{\"family\":\"inet6\",\"local\":\"2a01:4f8:c014:1a63::1\",\"scope\":\"global\"}]}]"
            fi
            ;;
        "-j -6 route get 2606:4700:4700::1111")
            if [ "$OCV_PROBE_SCENARIO" = localized ]; then
                printf "\033[33mRoute IPv6 invalide / Ungültige IPv6-Route / IPv6 路由无效\033[0m\n"
            elif [ "$OCV_PROBE_SCENARIO" = wrong_source ]; then
                printf "\033[33m%s\033[0m\n" "[{\"dst\":\"2606:4700:4700::1111\",\"dev\":\"eth0\",\"src\":\"2a01:4f8:c014:1a63::99\"}]"
            else
                printf "\033[33m%s\033[0m\n" "[{\"dst\":\"2606:4700:4700::1111\",\"dev\":\"eth0\",\"src\":\"2a01:4f8:c014:1a63::1\"}]"
            fi
            ;;
    esac
}
base64() {
    [[ "${1:-}" == "-d" ]] || return 1
    python3 -c "import base64,sys; sys.stdout.buffer.write(base64.b64decode(sys.stdin.buffer.read()))"
}
export -f ip base64
remote_command="timeout 90 bash -c $(printf "%q" "$1")"
bash -c "$remote_command"
' bash "$source_probe_command"
}

for prefix in 32 48 56 64 80 96 120 128; do
    [ "$(run_probe colored "$prefix")" = '1|1|2a01:4f8:c014:1a63::1|2a01:4f8:c014:1a63::1' ]
done
[ "$(run_probe localized)" = '0|0|2a01:4f8:c014:1a63::1|2a01:4f8:c014:1a63::1' ]
[ "$(run_source_probe colored)" = '2a01:4f8:c014:1a63::1|eth0' ]
[ "$(run_source_probe multiple)" = '2a01:4f8:c014:1a63::1|eth0' ]
if run_source_probe wrong_source >/dev/null 2>&1; then
    echo "host source probe accepted an IPv6 source not assigned to its route device" >&2
    exit 1
fi
if run_source_probe localized >/dev/null 2>&1; then
    echo "host source probe accepted localized diagnostics as IPv6 JSON" >&2
    exit 1
fi
printf 'Host IPv6 harness probe handles color, variable prefixes, localized diagnostics, and route-selected sources\n'
