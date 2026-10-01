#!/usr/bin/env bash
set -euo pipefail

repo_root=$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)
source "$repo_root/action_tests/common/node_manager.sh"

ensure_worker_dns() { :; }
log_info() { :; }
log_warning() { :; }
platform_exec_and_wait() { captured_command=$2; }

captured_command=
stabilize_worker_network_for_env 192.0.2.1 lxd test-worker
[[ -n "$captured_command" ]] || { echo 'LXD readiness command missing' >&2; exit 1; }

test_dir=$(mktemp -d)
trap 'rm -rf "$test_dir"' EXIT
export TEST_SYSTEMCTL_LOG="$test_dir/systemctl.log"

ready_stub='timeout() { shift; "$@"; }
lxc() { return 0; }
systemctl() { printf "%s\n" "$*" >> "$TEST_SYSTEMCTL_LOG"; }
snap() { echo "unexpected snap restart" >&2; return 1; }
sleep() { :; }
'
bash -c "$ready_stub$captured_command"
[[ ! -e "$TEST_SYSTEMCTL_LOG" ]] || { echo 'Ready LXD daemon was restarted' >&2; exit 1; }

recovery_stub='timeout() { shift; "$@"; }
probe_count=0
lxc() { probe_count=$((probe_count + 1)); [[ "$probe_count" -gt 1 ]]; }
systemctl() { printf "%s\n" "$*" >> "$TEST_SYSTEMCTL_LOG"; }
snap() { echo "unexpected snap restart" >&2; return 1; }
sleep() { :; }
'
bash -c "$recovery_stub$captured_command"
[[ $(wc -l < "$TEST_SYSTEMCTL_LOG") -eq 1 ]] || { echo 'LXD recovery restarted daemon more than once' >&2; exit 1; }
grep -Fxq 'restart --no-block snap.lxd.daemon' "$TEST_SYSTEMCTL_LOG" || {
    echo 'LXD recovery did not use a nonblocking restart' >&2
    exit 1
}
echo 'LXD daemon DNS refresh avoids duplicate blocking restarts'
