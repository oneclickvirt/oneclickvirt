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
grep -Fq 'ACTION_TEST_QEMU_CONTAINER_CPU' "$RUN_ENV_TEST" ||
    fail "QEMU container sizing does not leave capacity for the VM matrix"

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

RUN_MODULE="${ROOT_DIR}/action_tests/run_module.sh"
if grep -Fq 'for _result_json in "${TEST_RESULTS_JSON[@]}"' "$RUN_MODULE"; then
    fail "run_module still overwrites authoritative JSONL from its parent-shell array"
fi

echo "action harness classification tests passed"
