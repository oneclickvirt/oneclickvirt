#!/usr/bin/env bash
set -euo pipefail
repo_root=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../.." && pwd)
# shellcheck source=../../action_tests/common/node_manager.sh
source "$repo_root/action_tests/common/node_manager.sh"
fail() { printf 'FAIL: %s\n' "$*" >&2; exit 1; }
log_section() { :; }
log_info() { :; }
log_warning() { :; }
log_error() { :; }
ensure_worker_swap() { :; }
wait_for_apt_lock() { :; }
ensure_worker_dns() { :; }
stabilize_worker_network_for_env() { :; }
platform_validate_worker_resources() { :; }
mock_ssh_ready=true mock_install_status=37 mock_installs=0 mock_first_only=false
mock_commands=()
wait_for_ssh() { $mock_ssh_ready; }
build_env_install_command() {
    printf '%s\n' mocked-installer
}
platform_exec_and_wait() {
    mock_commands+=("$2")
    if [[ "$2" == mocked-installer ]]; then
        mock_installs=$((mock_installs + 1))
        if $mock_first_only && [ "$mock_installs" -gt 1 ]; then return 0; fi
        return "$mock_install_status"
    fi
}
run_kubevirt_installer_with_retry() { platform_exec_and_wait "$1" "$2"; }
mock_qemu_runtime_ready=false
verify_worker_runtime() {
    [[ "$3" == qemu ]] && $mock_qemu_runtime_ready
}
for runtime in incus lxd docker podman containerd qemu kubevirt; do
    rc=0 mock_installs=0
    install_env worker 192.0.2.1 "$runtime" || rc=$?
    [[ "$rc" == 37 ]] || fail "$runtime installer failure was swallowed: $rc"
done
mock_qemu_runtime_ready=true mock_installs=0
install_env worker 192.0.2.1 qemu || fail 'ready QEMU runtime should be accepted after upstream already-active error'
[[ "$mock_installs" == 1 ]] || fail 'QEMU installer was rerun after already-active error'
mock_qemu_runtime_ready=false
mock_first_only=true mock_installs=0
mock_commands=()
install_env worker 192.0.2.1 incus || fail 'post-reboot retry must still allow recovery'
[[ "$mock_installs" == 2 ]] || fail 'post-reboot installer was not run'
! printf '%s\n' "${mock_commands[@]}" | grep -Fxq reboot || fail 'failed installer pass must not trigger an unconditional reboot'
mock_ssh_ready=false mock_installs=0
rc=0
install_env worker 192.0.2.1 incus || rc=$?
[[ "$rc" == 75 && "$mock_installs" == 1 ]] || fail 'failed SSH recovery must stop before reinstall'

mock_profile='{"devices":{"root":{"type":"disk","path":"/","pool":"local"},"eth0":{"type":"nic","network":"incusbr0"}}}'
mock_network='{"type":"bridge","managed":true,"config":{"ipv4.address":"10.23.0.1/24","ipv4.dhcp":"true"}}'
mock_bridge_ready=true
mock_api_envelope=true
emit_api() {
    if $mock_api_envelope; then
        jq -cn --argjson metadata "$1" '{type:"sync",status:"Success",status_code:200,metadata:$metadata}'
    else
        printf '%s\n' "$1"
    fi
}
mock_cli() {
    case "$*" in
        info|'storage show local') return 0 ;;
        'query /1.0/profiles/default') emit_api "$mock_profile" ;;
        'query /1.0/networks/incusbr0') $mock_bridge_ready && emit_api "$mock_network" ;;
        *) fail "Unexpected runtime command: $*" ;;
    esac
}
ip() { $mock_bridge_ready; }
verify_lxc_runtime mock_cli || fail 'complete existing environment should be accepted'
mock_bridge_ready=false
if verify_lxc_runtime mock_cli; then fail 'responding daemon with missing bridge must fail'; fi
mock_bridge_ready=true
mock_profile='{"devices":{}}'
if verify_lxc_runtime mock_cli; then fail 'empty profile must fail'; fi
mock_profile='{"devices":{"root":{"type":"disk","path":"/","pool":"local"},"eth0":{"type":"nic","network":"incusbr0"}}}'
mock_api_envelope=false
verify_lxc_runtime mock_cli || fail 'direct API metadata must remain supported'

# Readiness must fail on malformed/empty responses, not pass because jq 1.6
# returned zero without producing a value. Retain the original scenarios.
for resource in profile network; do
    for failure in empty whitespace null scalar multiple malformed envelope missing_schema wrong_schema wrong_field command_failure; do
        (
            set +o pipefail
            if [[ "$resource" == profile ]]; then payload=$mock_profile; else payload=$mock_network; fi
            case "$failure" in
                empty) payload='' ;;
                whitespace) payload=$' \n\t' ;;
                null) payload=null ;;
                scalar) payload=true ;;
                multiple) payload="$payload $payload" ;;
                malformed) payload='{' ;;
                envelope) payload=$(jq -cn --argjson metadata "$payload" '{type:"error",metadata:$metadata}') ;;
                missing_schema) payload=$(jq 'del(.devices, .config)' <<<"$payload") ;;
                wrong_schema)
                    if [[ "$resource" == profile ]]; then payload='{"devices":[]}';
                    else payload=$(jq '.config=[]' <<<"$payload"); fi ;;
                wrong_field)
                    if [[ "$resource" == profile ]]; then payload='{"devices":{"root":false}}';
                    else payload=$(jq '.config["ipv4.dhcp"]=false' <<<"$payload"); fi ;;
                command_failure) emit_api() { printf '%s\n' "$1"; return 42; } ;;
            esac
            if [[ "$resource" == profile ]]; then mock_profile=$payload; else mock_network=$payload; fi
            if verify_lxc_runtime mock_cli; then fail "$failure $resource readiness unexpectedly passed"; fi
        )
    done
done
printf 'Runtime readiness API boundaries passed (22 additional scenarios, no skipped tests)\n'

local_install="$repo_root/scripts/local_install.sh"
local_source=$(<"$local_install")
grep -Fq 'libvirt-daemon-driver-lxc' <<<"$local_source" ||
    fail 'local installer must install the libvirt LXC driver when LXC support is enabled'
grep -Fq 'verify_runtime()' <<<"$local_source" ||
    fail 'local installer must verify libvirt runtimes after starting services'
if grep -Fq 'run systemctl enable --now "$svc.service" || true' <<<"$local_source" ||
   grep -Fq 'run rc-service "$svc" start || true' <<<"$local_source"; then
    fail 'local installer must not swallow service start failures'
fi
printf 'Non-PVE installer failure propagation and runtime readiness tests passed (13 scenarios)\n'
