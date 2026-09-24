#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
fixture=$(mktemp -d)
trap 'rm -rf "$fixture"' EXIT
fail() { echo "Agent readiness test failed: $*" >&2; exit 1; }
RESULTS_FILE="$fixture/results.jsonl"
ACTION_TEST_AGENT_STATUS_MAX_WAIT=5
export RESULTS_FILE ACTION_TEST_AGENT_STATUS_MAX_WAIT

# shellcheck source=../../action_tests/common/test_framework.sh
source "${ROOT_DIR}/action_tests/common/test_framework.sh"
SERVER_URL="http://fixture"
ADMIN_TOKEN="fixture-token"
PROVIDER_ID=1

# Keep the fixture focused on control flow and JSONL integrity.
log_info() { :; }
log_success() { :; }
log_warning() { :; }
log_error() { :; }
log_skip() { :; }
report_add_pass() { :; }
report_add_skip() { :; }
report_add_fail() { :; }
log_failure_response() { :; }
capture_service_logs() { :; }
# Advance the same wall-clock variable used by the bounded probe.
sleep() { SECONDS=$((SECONDS + ${1%%.*})); }

status_body='{"code":200,"data":{"is_running":false,"status":"offline"}}'
http_status=200
reconnect_after=0
: > "$fixture/status.calls"
curl() {
    local url="${!#}"
    printf '%s\n' "$url" >> "$fixture/calls"
    case "$url" in
        */monitoring/status)
            printf '%s\n' probe >> "$fixture/status.calls"
            local count
            count=$(wc -l < "$fixture/status.calls")
            if (( reconnect_after > 0 && count >= reconnect_after )); then
                printf '%s\n200\n' '{"code":200,"data":{"is_running":true,"status":"online"}}'
            else
                printf '%s\n%s\n' "$status_body" "$http_status"
            fi
            ;;
        *)
            if [[ "$*" == *'%{http_code}'* ]]; then
                printf '%s\n%s\n' "$fixture_api_body" "$fixture_api_status"
            else
                printf '%s\n' '{"code":200,"data":{"list":[]}}'
            fi
            ;;
    esac
}

assert_not_ready() {
    : > "$RESULTS_FILE"
    if ensure_action_test_agent_ready "$PROVIDER_ID" fixture "$ADMIN_TOKEN" 5 1; then
        fail "$1 was incorrectly reported ready"
    fi
    [[ -n "$ACTION_TEST_AGENT_READY_REASON" ]] || fail "$1 has no diagnostic"
    local failures
    failures=$(jq -s '[.[] | select(.status == "FAIL")] | length' "$RESULTS_FILE")
    [[ "$failures" == "$2" ]] || fail "$1 recorded $failures failure(s), want $2"
}

assert_not_ready "offline control connection" 0
status_body='{"code":200,"data":{"is_running":true}}'
assert_not_ready "running standalone SSH monitoring daemon" 0
grep -Fq '反向WebSocket' <<< "$ACTION_TEST_AGENT_READY_REASON" || fail "missing standalone explanation"
status_body='{"code":200,"data":{"is_running":true,"status":"offline"}}'
assert_not_ready "stale connected flag with offline heartbeat" 0
status_body='{"code":200,"data":{"is_running":false,"status":"online"}}'
assert_not_ready "online label without a connection" 0
status_body='{"code":200,"data":{"is_running":true,"status":"online"}}'
ensure_action_test_agent_ready "$PROVIDER_ID" fixture "$ADMIN_TOKEN" 5 1 || fail "online reverse Agent rejected"
# A previous ready result cannot bypass a fresh status probe after uninstall.
status_body='{"code":200,"data":{"is_running":false,"status":"offline"}}'
assert_not_ready "disconnected previously-ready Agent" 0
# A temporarily disconnected reverse Agent may reconnect within the budget.
# curl runs in command substitution, so persist the response sequence in a file.
: > "$fixture/status.calls"
: > "$RESULTS_FILE"
reconnect_after=2
ensure_action_test_agent_ready "$PROVIDER_ID" fixture "$ADMIN_TOKEN" 5 1 || fail "offline Agent was not allowed to reconnect"
[[ "$(wc -l < "$fixture/status.calls")" -ge 2 ]] || fail "reconnect fixture did not start offline"
[[ ! -s "$RESULTS_FILE" ]] || fail "successful reconnect recorded a failure or skip"
reconnect_after=0
status_body='{"code":400,"details":"invalid status request"}'
http_status=400
assert_not_ready "status API validation regression" 1
status_body='{"code":200,"data":{"is_running":true,"status":"online"}}'
http_status=500
assert_not_ready "HTTP error with misleading JSON code" 1
http_status=200
status_body='{}'
assert_not_ready "missing status envelope" 1
status_body='{"code":200,"data":{}}'
assert_not_ready "missing runtime status" 1
status_body='{"code":200,"data":{"is_running":"true","status":"online"}}'
assert_not_ready "string instead of boolean running flag" 1
status_body='{"code":200,"data":{"status":"online"}}'
assert_not_ready "online status without a running flag" 1
status_body='{"code":200,"data":{"is_running":true,"status":null}}'
assert_not_ready "null status is not a standalone daemon response" 1
status_body='{"code":200,"data":{"is_running":true,"status":""}}'
assert_not_ready "empty status is not a standalone daemon response" 1
status_body='{"code":200,"data":{"is_running":true,"status":123}}'
assert_not_ready "non-string runtime status" 1
status_body='{"code":200,"data":[]}'
assert_not_ready "non-object status payload" 1
status_body='<html>unexpected proxy response</html>'
assert_not_ready "non-JSON status response" 1
http_status=000
status_body=''
assert_not_ready "unreachable status endpoint" 0
! grep -Fq '/monitoring/agent' "$fixture/calls" || fail "control probe deployed a standalone monitoring daemon"

# Execute the actual module, not just a static grep: while standalone/offline,
# no positive controller mapping or dependent duplicate POST may reach the API.
source "$ROOT_DIR/action_tests/modules/13_port_mappings.sh"
report_add_section() { :; }
require_test_instance() { TEST_INSTANCE_ID=2; }
# Keep the actual test_api classifier and JSONL writer. Only the HTTP boundary
# is intercepted, so unrelated 500s cannot accidentally become accepted SKIPs.
eval "$(declare -f test_api | sed '1s/test_api/fixture_real_test_api/')"
mapping_create_code=200
duplicate_code=409
failure_body='{"code":500,"details":"unexpected database error"}'
test_api() {
    printf '%s|%s|%s|%s\n' "$1" "$2" "$3" "$4" >> "$fixture/assertions"
    local fixture_api_status="${4%%|*}" fixture_api_body
    case "$1" in
        'Create port mapping') fixture_api_status=$mapping_create_code ;;
        'Create duplicate port') fixture_api_status=$duplicate_code ;;
    esac
    fixture_api_body="{\"code\":${fixture_api_status},\"data\":{\"id\":3}}"
    [[ "$fixture_api_status" == 500 ]] && fixture_api_body="$failure_body"
    fixture_real_test_api "$@"
}
ENV_TYPE=docker
USER_TOKEN=""
http_status=200
run_case() {
    : > "$RESULTS_FILE"
    : > "$fixture/assertions"
    run_module_13 >/dev/null || true
}
assert_result() {
    jq -e -s --arg name "$1" --arg status "$2" '[.[] | select(.name == $name)] | length == 1 and .[0].status == $status' "$RESULTS_FILE" >/dev/null || fail "$1 was not recorded as $2"
}
for status_body in '{"code":200,"data":{"is_running":true}}' '{"code":200,"data":{"is_running":false,"status":"offline"}}'; do
    run_case
    [[ "$(jq -s '[.[] | select(.status == "SKIP")] | length' "$RESULTS_FILE")" == 4 ]] || fail "controller-dependent assertions were not all skipped"
    ! grep -Eq '^(Create port mapping\||Create port mapping \(controller type\)|no_port_mapping allows controller mapping|Create duplicate port)' "$fixture/assertions" || fail "offline controller mapping reached the API"
    assert_result 'Create port mapping (node type)' PASS
done
status_body='{"code":200,"data":{"is_running":true,"status":"online"}}'
run_case
assert_result 'Create port mapping' PASS
assert_result 'Create duplicate port' PASS
mapping_create_code=500
run_case
assert_result 'Create port mapping' FAIL
assert_result 'Create duplicate port' SKIP
! grep -Fq 'Create duplicate port|' "$fixture/assertions" || fail "duplicate POST ran after failed initial creation"
failure_body='{"code":500,"details":"启动控制端端口转发失败: provider 1 的 Agent 当前离线或未连接"}'
run_case
assert_result 'Create port mapping' SKIP
assert_result 'Create duplicate port' SKIP
mapping_create_code=200
duplicate_code=500
run_case
assert_result 'Create duplicate port' SKIP
failure_body='{"code":500,"details":"unexpected database error"}'
run_case
assert_result 'Create duplicate port' FAIL
# Node-side duplicates do not depend on the reverse Agent; do not broaden their
# 400/409 assertion to hide a server-side error behind controller prerequisites.
ENV_TYPE=lxd
failure_body='{"code":500,"details":"provider 1 的 Agent 当前离线或未连接"}'
run_case
assert_result 'Create duplicate port' FAIL

echo "port mapping Agent readiness tests passed"
