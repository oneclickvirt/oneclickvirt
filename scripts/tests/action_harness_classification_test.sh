#!/bin/bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"

# shellcheck source=../../action_tests/common/node_manager.sh
source "${ROOT_DIR}/action_tests/common/node_manager.sh"
# shellcheck source=../../action_tests/common/test_framework.sh
source "${ROOT_DIR}/action_tests/common/test_framework.sh"

fail() {
    echo "action harness classification test failed: $*" >&2
    exit 1
}

log_section() { :; }
log_info() { :; }
log_success() { :; }
log_warning() { :; }
log_error() { :; }

MOCK_EXEC_MODE="success"
MOCK_FAIL_MATCH=""
CAPTURED_COMMANDS=()

platform_exec_and_wait() {
    local _ip="$1" command="$2" _timeout="${3:-}"
    CAPTURED_COMMANDS+=("$command")
    [[ "$MOCK_EXEC_MODE" == "success" ]] && return 0
    [[ "$MOCK_EXEC_MODE" == "fail-all" ]] && return 1
    [[ -n "$MOCK_FAIL_MATCH" && "$command" == *"$MOCK_FAIL_MATCH"* ]] && return 1
    return 0
}

MOCK_SSH_REACHABLE=true
wait_for_ssh() {
    [[ "$MOCK_SSH_REACHABLE" == "true" ]]
}

assert_offline_empty_fixtures() {
    local env="$1" cli="$2" rc=0 commands
    MOCK_EXEC_MODE="success"
    MOCK_FAIL_MATCH=""
    CAPTURED_COMMANDS=()
    prepare_dirty_node worker-id 192.0.2.10 "$env" || rc=$?
    [[ "$rc" == "0" ]] || fail "${env} empty fixtures returned ${rc}"
    [[ "$DIRTY_NODE_CONTAINER_EXPECTED" == "true" && "$DIRTY_NODE_VM_EXPECTED" == "true" ]] ||
        fail "${env} did not mark both fixture types as expected"
    [[ "$DIRTY_NODE_CONTAINER_READY" == "true" && "$DIRTY_NODE_VM_READY" == "true" ]] ||
        fail "${env} did not mark both offline fixtures ready"
    commands=$(printf '%s\n' "${CAPTURED_COMMANDS[@]}")
    ! grep -Fq 'images:' <<< "$commands" || fail "${env} fixture still depends on a public image remote"
    grep -Fq "${cli} init pre-existing-1 --empty" <<< "$commands" || fail "${env} empty container command missing"
    grep -Fq "${cli} init pre-existing-vm --empty --vm" <<< "$commands" || fail "${env} empty VM command missing"
}

assert_offline_empty_fixtures lxd lxc
assert_offline_empty_fixtures incus incus

MOCK_EXEC_MODE="success"
CAPTURED_COMMANDS=()
prepare_dirty_node worker-id 192.0.2.10 lxd container || fail "LXD container-only fixture preparation failed"
[[ "$DIRTY_NODE_CONTAINER_EXPECTED" == "true" && "$DIRTY_NODE_VM_EXPECTED" == "false" ]] ||
    fail "LXD container-only run prepared the wrong fixture types"
! printf '%s\n' "${CAPTURED_COMMANDS[@]}" | grep -Fq 'pre-existing-vm' ||
    fail "LXD container-only run still prepared a VM fixture"

CAPTURED_COMMANDS=()
prepare_dirty_node worker-id 192.0.2.10 lxd vm || fail "LXD VM-only fixture preparation failed"
[[ "$DIRTY_NODE_CONTAINER_EXPECTED" == "false" && "$DIRTY_NODE_VM_EXPECTED" == "true" ]] ||
    fail "LXD VM-only run prepared the wrong fixture types"
! printf '%s\n' "${CAPTURED_COMMANDS[@]}" | grep -Fq 'pre-existing-1' ||
    fail "LXD VM-only run still prepared a container fixture"

assert_worker_budget() {
    local env="$1" types="$2" expected_cpu="$3" expected_memory="$4" expected_disk="$5" expected_kvm="$6"
    local actual_cpu actual_memory actual_disk actual_kvm
    configure_action_test_resources_for_env "$env"
    INSTANCE_TYPES="$types"
    read -r actual_cpu actual_memory actual_disk actual_kvm < <(worker_resource_requirements "$env" "$types")
    [[ "$actual_cpu" == "$expected_cpu" ]] || fail "${env}/${types} CPU budget ${actual_cpu}, expected ${expected_cpu}"
    [[ "$actual_memory" == "$expected_memory" ]] || fail "${env}/${types} memory budget ${actual_memory}, expected ${expected_memory}"
    [[ "$actual_disk" == "$expected_disk" ]] || fail "${env}/${types} disk budget ${actual_disk}, expected ${expected_disk}"
    [[ "$actual_kvm" == "$expected_kvm" ]] || fail "${env}/${types} KVM requirement ${actual_kvm}, expected ${expected_kvm}"
}

assert_worker_budget qemu both 4 8192 20 true
assert_worker_budget kubevirt both 4 8192 20 true
assert_worker_budget lxd both 4 8192 41 true
assert_worker_budget incus both 4 8192 41 true
assert_worker_budget lxd container 2 4096 21 false
assert_worker_budget lxd vm 2 4096 21 true
assert_worker_budget incus container 2 4096 21 false
assert_worker_budget incus vm 2 4096 21 true
assert_worker_budget proxmoxve both 4 8192 20 true
assert_worker_budget qemu container 2 4096 20 false
assert_worker_budget qemu vm 2 4096 20 true

for nested_env in lxd incus proxmoxve qemu kubevirt; do
    env_needs_worker_resource_check "$nested_env" || fail "${nested_env} bypasses worker resource validation"
done
if env_needs_worker_resource_check docker; then
    fail "Docker should not require nested-virtualization worker validation"
fi

lightnode_get_packages() {
    printf '%s\n200\n' '{"packages":[{"packageCode":"small","cpu":2,"memory":4},{"packageCode":"peak","cpu":4,"memory":8},{"packageCode":"large","cpu":8,"memory":16}]}'
}
LIGHTNODE_REGION="test-region"
LIGHTNODE_ZONE="test-zone"
ENV_TYPE="qemu"
INSTANCE_TYPES="both"
configure_action_test_resources_for_env "$ENV_TYPE"
LIGHTNODE_PACKAGE_CODE=""
LIGHTNODE_TARGET_CPU=2
LIGHTNODE_TARGET_MEMORY_MB=4096
LIGHTNODE_STRICT_RECOMMENDED_SPEC=true
[[ "$(_lightnode_get_default_package)" == "peak" ]] || fail "LightNode did not raise qemu/both to the 4C/8GB package"
LIGHTNODE_TARGET_CPU=8
LIGHTNODE_TARGET_MEMORY_MB=16384
[[ "$(_lightnode_get_default_package)" == "large" ]] || fail "LightNode downgraded an explicit larger worker target"
LIGHTNODE_TARGET_CPU=6
LIGHTNODE_TARGET_MEMORY_MB=12288
LIGHTNODE_STRICT_RECOMMENDED_SPEC=false
[[ "$(_lightnode_get_default_package)" == "large" ]] || fail "LightNode non-strict selection fell below a larger worker target"
LIGHTNODE_STRICT_RECOMMENDED_SPEC=true
LIGHTNODE_PACKAGE_CODE="small"
LIGHTNODE_TARGET_CPU=2
LIGHTNODE_TARGET_MEMORY_MB=4096
if _lightnode_get_default_package >/dev/null 2>&1; then
    fail "LightNode accepted an explicit package below the qemu/both peak budget"
fi
LIGHTNODE_PACKAGE_CODE="large"
[[ "$(_lightnode_get_default_package)" == "large" ]] || fail "LightNode rejected an explicit package above the peak budget"
LIGHTNODE_PACKAGE_CODE=""

RESOURCE_COMMAND_FILE=$(mktemp)
MOCK_RESOURCE_CHECK_RESULT=0
platform_ssh_exec() {
    local _ip="$1" command="$2" _timeout="${3:-}"
    printf '%s\n' "$command" > "$RESOURCE_COMMAND_FILE"
    printf '%s\n' 'WORKER_RESOURCE_CHECK mocked'
    return "$MOCK_RESOURCE_CHECK_RESULT"
}
platform_validate_worker_resources qemu 192.0.2.10 lightnode >/dev/null 2>&1 || fail "valid qemu/both worker check was rejected"
grep -Fq 'required>=4' "$RESOURCE_COMMAND_FILE" || fail "worker validation command does not enforce the 4 CPU peak budget"
grep -Fq 'required>=7680' "$RESOURCE_COMMAND_FILE" || fail "worker validation command does not allow only nominal-memory virtualization overhead"
grep -Fq '/dev/kvm missing' "$RESOURCE_COMMAND_FILE" || fail "worker validation command does not enforce nested virtualization"
MOCK_RESOURCE_CHECK_RESULT=1
resource_check_rc=0
platform_validate_worker_resources qemu 192.0.2.10 lightnode >/dev/null 2>&1 || resource_check_rc=$?
[[ "$resource_check_rc" == "75" ]] || fail "insufficient worker resources should return infrastructure status 75"
rm -f "$RESOURCE_COMMAND_FILE"

MOCK_EXEC_MODE="selective"
MOCK_FAIL_MATCH="pre-existing-vm"
CAPTURED_COMMANDS=()
partial_rc=0
prepare_dirty_node worker-id 192.0.2.10 lxd || partial_rc=$?
[[ "$partial_rc" == "1" ]] || fail "partial LXD fixture setup should return 1, got ${partial_rc}"
[[ "$DIRTY_NODE_CONTAINER_READY" == "true" && "$DIRTY_NODE_VM_READY" == "false" ]] ||
    fail "partial LXD fixture readiness was not preserved per type"

MOCK_EXEC_MODE="fail-all"
MOCK_FAIL_MATCH=""
CAPTURED_COMMANDS=()
missing_rc=0
prepare_dirty_node worker-id 192.0.2.10 lxd || missing_rc=$?
[[ "$missing_rc" == "75" ]] || fail "missing all dirty fixtures should return infrastructure status 75, got ${missing_rc}"

MOCK_EXEC_MODE="fail-all"
MOCK_SSH_REACHABLE=false
runtime_rc=0
verify_worker_runtime worker-id 192.0.2.10 docker || runtime_rc=$?
[[ "$runtime_rc" == "75" ]] || fail "unreachable worker runtime check should return 75, got ${runtime_rc}"

MOCK_SSH_REACHABLE=true
runtime_rc=0
verify_worker_runtime worker-id 192.0.2.10 docker || runtime_rc=$?
[[ "$runtime_rc" == "1" ]] || fail "reachable worker with a broken runtime should return 1, got ${runtime_rc}"

MOCK_EXEC_MODE="success"
runtime_rc=0
verify_worker_runtime worker-id 192.0.2.10 docker || runtime_rc=$?
[[ "$runtime_rc" == "0" ]] || fail "healthy worker runtime check should pass, got ${runtime_rc}"

vm_agent_timeout='Provider创建实例失败: 虚拟机Agent启动超时，无法继续配置: 等待实例可执行命令超时 (1800秒)'
ENV_TYPE=incus
is_infrastructure_failure_detail "$vm_agent_timeout" ||
    fail "Incus VM agent startup timeout should be classified as infrastructure"
ENV_TYPE=lxd
is_infrastructure_failure_detail "$vm_agent_timeout" ||
    fail "LXD VM agent startup timeout should be classified as infrastructure"
ENV_TYPE=docker
if is_infrastructure_failure_detail "$vm_agent_timeout"; then
    fail "Docker must not inherit the LXD/Incus VM timeout classification"
fi
ENV_TYPE=incus
if is_infrastructure_failure_detail '等待实例可执行命令超时 (30秒)'; then
    fail "generic or short instance timeout must remain a product failure"
fi
mark_vm_runtime_infrastructure_unavailable "$vm_agent_timeout"
if env_supports_vm; then
    fail "VM runtime circuit breaker did not disable subsequent VM tests"
fi
VM_RUNTIME_INFRA_UNAVAILABLE_REASON=""
ENV_TYPE=lxd
env_supports_vm || fail "clearing the VM runtime circuit breaker did not restore the provider capability"

DISCOVERY_MODULE="${ROOT_DIR}/action_tests/modules/23_discovery.sh"
! grep -Fq 'any(.data.discoveredInstances' "$DISCOVERY_MODULE" ||
    fail "discovery module still accepts an arbitrary container or VM"
grep -Fq 'Discover exact pre-existing container' "$DISCOVERY_MODULE" ||
    fail "exact container fixture assertion missing"
grep -Fq 'Discover exact pre-existing VM' "$DISCOVERY_MODULE" ||
    fail "exact VM fixture assertion missing"
grep -Fq -- '--arg container_name' "$DISCOVERY_MODULE" ||
    fail "fixture-specific import selection missing"

RUN_ENV_TEST="${ROOT_DIR}/action_tests/run_env_test.sh"
grep -Fq 'install_rc == 75' "$RUN_ENV_TEST" ||
    fail "the environment orchestrator does not preserve transient installer status 75"
grep -Fq 'runtime_rc == 75' "$RUN_ENV_TEST" ||
    fail "the environment orchestrator does not preserve transient runtime status 75"
grep -Fq 'dirty_node_rc == 75' "$RUN_ENV_TEST" ||
    fail "the environment orchestrator does not classify missing fixtures as infrastructure"
grep -Fq 'configure_action_test_resources_for_env "$ENV_TYPE"' "$RUN_ENV_TEST" ||
    fail "the environment orchestrator does not apply provider-specific instance sizing"
NODE_MANAGER="${ROOT_DIR}/action_tests/common/node_manager.sh"
grep -Fq 'platform_validate_worker_resources "$env" "$ip" "${ACTIVE_PLATFORM:-}"' "$NODE_MANAGER" ||
    fail "runtime verification does not recheck the worker peak resource budget"
NETWORK_MODE_TEST="${ROOT_DIR}/action_tests/run_network_mode_test.sh"
grep -Fq 'configure_action_test_resources_for_env "$ENV_TYPE"' "$NETWORK_MODE_TEST" ||
    fail "the network-mode worker path does not apply provider-specific instance sizing"

INTEGRATION_WORKFLOW="${ROOT_DIR}/.github/workflows/integration-tests.yml"
grep -Fq 'bash scripts/tests/action_harness_classification_test.sh' "$INTEGRATION_WORKFLOW" ||
    fail "the harness regression test is not enforced by the integration workflow"

RESULTS_FILE=$(mktemp)
init_results_file "$RESULTS_FILE"
captured_result=$(record_fail_result "captured failure" "GET" "/captured" "200" "500" "body" "subshell" 2>/dev/null)
[[ -z "$captured_result" ]] || fail "record_fail_result unexpectedly wrote response data"
[[ "$(jq -r 'select(.name == "captured failure" and .status == "FAIL") | 1' "$RESULTS_FILE")" == "1" ]] ||
    fail "a failure recorded inside command substitution was lost from JSONL"
rm -f "$RESULTS_FILE"

RESULTS_FILE=$(mktemp)
init_results_file "$RESULTS_FILE"
record_pass_result "captured pass" "PREFLIGHT" "commands" "available" "available" "pass is persisted" "HARNESS"
[[ "$(jq -r 'select(.name == "captured pass" and .status == "PASS" and .group == "HARNESS") | 1' "$RESULTS_FILE")" == "1" ]] ||
    fail "record_pass_result did not persist a HARNESS PASS to JSONL"
rm -f "$RESULTS_FILE"

RUN_MODULE="${ROOT_DIR}/action_tests/run_module.sh"
if grep -Fq 'for _result_json in "${TEST_RESULTS_JSON[@]}"' "$RUN_MODULE"; then
    fail "run_module still overwrites authoritative JSONL from its parent-shell array"
fi
grep -Fq 'RESULTS_FILE_SHARED' "$RUN_MODULE" || fail "run_module does not preserve shared harness JSONL"
grep -Fq 'export RESULTS_FILE_SHARED=true' "$RUN_ENV_TEST" || fail "run_env_test does not mark its JSONL as shared"

# PVE network reload resilience regression.  The mock deliberately drops one
# status query, then lets SSH recover; the mutating launcher must remain
# single-shot and the detached status must drive completion.
MOCK_PVE_QUERY_COUNT=0
MOCK_PVE_LAUNCH_COUNT=0
MOCK_PVE_STATUS_MODE="recover"
MOCK_PVE_POSTCONDITION=""
MOCK_LAST_POSTCONDITION_COMMAND=""
CAPTURED_COMMANDS=()
MOCK_PVE_QUERY_FILE=$(mktemp)
MOCK_PVE_LAUNCH_FILE=$(mktemp)
MOCK_PVE_POSTCONDITION_FILE=$(mktemp)
printf '0\n' > "$MOCK_PVE_QUERY_FILE"
printf '0\n' > "$MOCK_PVE_LAUNCH_FILE"
platform_exec_once() {
    local _ip="$1" command="$2" _timeout="${3:-}"
    CAPTURED_COMMANDS+=("$command")
    if [[ "$command" == *"mkdir -p /tmp/oneclickvirt-pve-jobs"* ]]; then
        local launch_count; launch_count=$(cat "$MOCK_PVE_LAUNCH_FILE")
        launch_count=$((launch_count + 1)); printf '%s\n' "$launch_count" > "$MOCK_PVE_LAUNCH_FILE"
        printf '%s\n' STARTED
        return 0
    fi
    if [[ "$command" == *"if [ -s "* && "$command" == *".status"* ]]; then
        local query_count; query_count=$(cat "$MOCK_PVE_QUERY_FILE")
        query_count=$((query_count + 1)); printf '%s\n' "$query_count" > "$MOCK_PVE_QUERY_FILE"
        case "$MOCK_PVE_STATUS_MODE:$query_count" in
            recover:1) return 1 ;;
            recover:2) printf '%s\n' RUNNING; return 0 ;;
            recover:*) printf '%s\n' 0; return 0 ;;
            running:*) printf '%s\n' RUNNING; return 0 ;;
            failed:*) printf '%s\n' 1; return 0 ;;
        esac
    fi
    if [[ "$command" == *"test -s /usr/local/bin/build_backend_pve.txt"* || "$command" == *"test -r /etc/network/interfaces"* ]]; then
        printf '%s' "$command" > "$MOCK_PVE_POSTCONDITION_FILE"
        [[ "$MOCK_PVE_POSTCONDITION" == "backend" && "$command" == *"build_backend_pve.txt"* ]] || [[ "$MOCK_PVE_POSTCONDITION" == "nat" && "$command" == *"pve_nat_subnet"* ]]
        return
    fi
    return 0
}

export PVE_REMOTE_POLL_INTERVAL=1
export PVE_REMOTE_SSH_RECOVERY_WAIT=2
MOCK_SSH_REACHABLE=true
wait_for_ssh() { [[ "$MOCK_SSH_REACHABLE" == "true" ]]; }
pve_run_remote_job 192.0.2.10 'echo mutating-pve-command' 'network-reload-regression' 5 || fail "detached PVE job did not complete after temporary SSH loss"
MOCK_PVE_LAUNCH_COUNT=$(cat "$MOCK_PVE_LAUNCH_FILE")
MOCK_PVE_QUERY_COUNT=$(cat "$MOCK_PVE_QUERY_FILE")
[[ "$MOCK_PVE_LAUNCH_COUNT" == "1" ]] || fail "detached PVE job launcher was invoked ${MOCK_PVE_LAUNCH_COUNT} times"
[[ "$MOCK_PVE_QUERY_COUNT" -ge 3 ]] || fail "detached PVE job status was not polled through recovery"

# build_backend.sh exits 1 when its marker already exists; its durable marker
# and PVE runtime postcondition should make this an accepted completion.
MOCK_PVE_STATUS_MODE="failed"
printf '0\n' > "$MOCK_PVE_QUERY_FILE"
printf '0\n' > "$MOCK_PVE_LAUNCH_FILE"
MOCK_PVE_POSTCONDITION="backend"
pve_run_job_or_accept_postcondition 192.0.2.10 'echo backend' 'backend-marker-regression' backend 5 || fail "backend marker postcondition was not accepted"

# A job that is still RUNNING after the primary and grace windows must remain
# untouched; a durable postcondition cannot authorize the next mutating phase
# while the previous one may still be changing network state.
MOCK_PVE_STATUS_MODE="running"
printf '0\n' > "$MOCK_PVE_QUERY_FILE"
printf '0\n' > "$MOCK_PVE_LAUNCH_FILE"
export PVE_REMOTE_RUNNING_GRACE_WAIT=1
MOCK_PVE_POSTCONDITION="backend"
running_rc=0
pve_run_job_or_accept_postcondition 192.0.2.10 'echo still-running' 'running-job-regression' backend 1 || running_rc=$?
[[ "$running_rc" == "75" ]] || fail "still-running PVE job was not classified as infrastructure"
[[ "$PVE_LAST_JOB_STATE" == "running" ]] || fail "still-running PVE job state was not preserved"
unset PVE_REMOTE_RUNNING_GRACE_WAIT

MOCK_PVE_POSTCONDITION="nat"
pve_check_postcondition 192.0.2.10 nat || fail "NAT postcondition was rejected by the mock"
MOCK_LAST_POSTCONDITION_COMMAND=$(cat "$MOCK_PVE_POSTCONDITION_FILE")
grep -Fq 'pve_nat_subnet' <<<"$MOCK_LAST_POSTCONDITION_COMMAND" || fail "NAT postcondition did not inspect persisted subnet state"
grep -Fq 'masquerade' <<<"$MOCK_LAST_POSTCONDITION_COMMAND" || fail "NAT postcondition did not inspect masquerade rules"

rm -f "$MOCK_PVE_QUERY_FILE" "$MOCK_PVE_LAUNCH_FILE" "$MOCK_PVE_POSTCONDITION_FILE"

grep -Fq 'record_pass_result "Platform resolution"' "$RUN_ENV_TEST" || fail "preflight PASS results are not recorded"
grep -Fq '"$group" == "HARNESS"' "$INTEGRATION_WORKFLOW" || fail "workflow does not separate HARNESS results from module assertions"
grep -Fq 'Module tests executed' "$INTEGRATION_WORKFLOW" || fail "workflow does not state whether module assertions ran"

echo "action harness classification tests passed"
