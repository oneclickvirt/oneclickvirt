#!/usr/bin/env bash
set -euo pipefail

root_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
# shellcheck source=../../action_tests/common/test_framework.sh
source "${root_dir}/action_tests/common/test_framework.sh"

log_info() { :; }
log_success() { :; }
log_warning() { :; }
log_error() { printf '%s\n' "$*" >&2; }
platform_init() {
    [[ "$1" == "hetzner" ]]
    PLATFORM_SSH_PASSWORD=""
}
wait_for_ssh() {
    [[ "$1" == "192.0.2.5" && "$PLATFORM_SSH_PASSWORD" == "worker-secret" ]]
}

WORKER_IP=192.0.2.5
WORKER_PLATFORM=hetzner
WORKER_PASSWORD=worker-secret
PLATFORM_SSH_PASSWORD=""
ensure_worker_ssh_reachable 1 'password inheritance' || {
    printf 'worker SSH auth test failed: password was not restored in module process\n' >&2
    exit 1
}

printf 'Worker SSH auth test passed\n'
