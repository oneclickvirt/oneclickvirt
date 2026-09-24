#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
MODULE="$ROOT_DIR/action_tests/modules/26_instance_types.sh"

bash -n "$MODULE"

# Instance creation is a positive lifecycle assertion.  Only 2xx responses
# may enter task/ID handling; 4xx responses must remain recorded failures.
if grep -Fq '"200|201|400|409"' "$MODULE"; then
    echo "instance creation still accepts 4xx as success" >&2
    exit 1
fi
grep -Fq 'local ct_resp="" ct_request_ok=true' "$MODULE"
grep -Fq 'local vm_resp="" vm_request_ok=true' "$MODULE"
grep -Fq 'Create type-test container result' "$MODULE"
grep -Fq 'Create type-test VM result' "$MODULE"

# Exercise module 26 with the actual workflow disk value and test_api result
# classifier. Model the failed runner's remaining 10 GiB quota at the HTTP
# boundary; a 20 GiB request must still FAIL, not be accepted or infra-skipped.
fixture=$(mktemp -d)
trap 'rm -rf "$fixture"' EXIT
fail() { echo "instance creation result gate failed: $*" >&2; exit 1; }
unset ACTION_TEST_CONTAINER_DISK
source "$ROOT_DIR/action_tests/common/test_framework.sh"
[[ "$ACTION_TEST_CONTAINER_DISK" == 5 ]] || fail "native container default exceeds the shared runner budget"
workflow_disk=$(sed -n 's/^ *ACTION_TEST_CONTAINER_DISK: "\([0-9][0-9]*\)".*/\1/p' "$ROOT_DIR/.github/workflows/integration-tests.yml")
[[ "$workflow_disk" == 5 ]] || fail "workflow overrides the native container disk budget"
RESULTS_FILE="$fixture/results.jsonl"
SERVER_URL=http://fixture
ADMIN_TOKEN=fixture-token
USER_TOKEN=""
PROVIDER_ID=1
source "$MODULE"
log_info() { :; }
log_success() { :; }
log_warning() { :; }
log_error() { :; }
log_skip() { :; }
report_add_section() { :; }
report_add_pass() { :; }
report_add_skip() { :; }
report_add_fail() { :; }
log_failure_response() { :; }
capture_service_logs() { :; }
sleep() { :; }
ensure_provider_health_ready() { :; }
wait_provider_active_tasks_idle() { :; }
should_test_type() { [[ "$1" == container ]]; }
env_supports_container() { :; }
current_test_arch() { printf '%s\n' amd64; }
wait_instance_status() { printf '%s\n' '{"code":200,"data":{"status":"running"}}'; }
wait_instance_operation_settled() { :; }
curl() {
    local method=GET data="" http_output=false url="${!#}"
    while (( $# )); do
        case "$1" in
            -X) method="$2"; shift ;;
            -d) data="$2"; shift ;;
            -w) http_output=true; shift ;;
        esac
        shift
    done
    printf '%s %s\n' "$method" "$url" >> "$fixture/calls"
    local code=200 body='{"code":200,"data":{"id":41}}'
    if [[ "$method" == POST && "$url" == */admin/instances ]]; then
        printf '%s\n' "$data" >> "$fixture/creates"
        if [[ "$(jq -r '.disk' <<< "$data")" -gt 10 ]]; then
            code=409
            body='{"code":409,"data":null,"details":"创建任务失败: Provider资源不足: 磁盘资源不足：需要 20480 MB，可用 10240 MB"}'
        fi
    fi
    printf '%s\n' "$body"
    if [[ "$http_output" == true ]]; then printf '%s\n' "$code"; fi
}
run_case() {
    : > "$RESULTS_FILE"
    : > "$fixture/calls"
    : > "$fixture/creates"
    run_module_26 >/dev/null || true
}
for ENV_TYPE in docker podman containerd; do
    ACTION_TEST_CONTAINER_DISK="$workflow_disk"
    configure_action_test_resources_for_env "$ENV_TYPE"
    run_case
    jq -e -s 'length == 1 and .[0].disk == 5' "$fixture/creates" >/dev/null || fail "$ENV_TYPE did not create a 5 GiB guest"
    jq -e -s '[.[] | select(.name == "Create container instance")] | length == 1 and .[0].status == "PASS"' "$RESULTS_FILE" >/dev/null || fail "$ENV_TYPE creation did not pass"
    [[ "$(jq -s '[.[] | select(.status == "FAIL")] | length' "$RESULTS_FILE")" == 0 ]] || fail "$ENV_TYPE recorded an unexpected failure"
    grep -Fq 'DELETE http://fixture/api/v1/admin/instances/41' "$fixture/calls" || fail "$ENV_TYPE omitted cleanup"
done
ACTION_TEST_CONTAINER_DISK=7
configure_action_test_resources_for_env docker
run_case
jq -e -s 'length == 1 and .[0].disk == 7' "$fixture/creates" >/dev/null || fail "explicit disk override was ignored"
ACTION_TEST_CONTAINER_DISK=20
run_case
jq -e -s '[.[] | select(.name == "Create container instance")] | length == 1 and .[0].status == "FAIL" and .[0].actual == "409"' "$RESULTS_FILE" >/dev/null || fail "real disk quota rejection was masked"
! grep -Fq 'DELETE http://fixture/api/v1/admin/instances/41' "$fixture/calls" || fail "failed creation triggered unrelated cleanup"

echo "instance creation result gate tests passed"
