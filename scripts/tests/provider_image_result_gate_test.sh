#!/usr/bin/env bash
set -euo pipefail
ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
fixture=$(mktemp -d)
trap 'rm -rf "$fixture"' EXIT
source "$ROOT_DIR/action_tests/common/test_framework.sh"
source "$ROOT_DIR/action_tests/modules/29_provider_images.sh"
RESULTS_FILE="$fixture/results.jsonl" REPORT_FILE="$fixture/report.md"
SERVER_URL=http://fixture.invalid ADMIN_TOKEN=fixture PROVIDER_ID=1
ENV_TYPE=incus INSTANCE_TYPES=both TEST_IMAGES=debian
ACTION_TEST_PRESERVE_INSTANCES=true
log_info() { :; }
log_warning() { :; }
log_error() { :; }
log_skip() { :; }
log_success() { :; }
log_section() { :; }
sleep() { :; }
ensure_provider_health_ready() { :; }
wait_provider_active_tasks_idle() { :; }
fail() { printf 'Provider image result gate failed: %s\n' "$*" >&2; exit 1; }
reset_fixture() {
    : > "$RESULTS_FILE"; : > "$fixture/calls"; : > "$fixture/creates"
    rm -f "$fixture"/deleted-*
    failed=0 skipped=0 group=provider_images
    MOCK_HTTP=200 MOCK_API=200 MOCK_TRANSPORT=0 MOCK_MODE=normal MOCK_DELETE=404
}
curl() {
    local url="${!#}" method=GET data='' with_code=false body code=200 status=0
    while (( $# )); do
        case "$1" in
            -X) method="$2"; shift ;;
            -d) data="$2"; shift ;;
            -w) with_code=true; shift ;;
        esac
        shift
    done
    printf '%s %s\n' "$method" "$url" >> "$fixture/calls"
    case "$method:$url" in
        POST:*/admin/instances)
            jq -e . <<< "$data" >/dev/null || fail 'image name generated invalid request JSON'
            printf '%s\n' "$data" >> "$fixture/creates"
            code="$MOCK_HTTP" status="$MOCK_TRANSPORT"
            local instance_id
            instance_id=$((40 + $(wc -l < "$fixture/creates")))
            body=$(jq -cn --argjson code "$MOCK_API" --argjson id "$instance_id" '{code:$code,data:{id:$id}}')
            case "$MOCK_MODE" in
                invalid) body='{' ;;
                validation) body='{"code":400,"message":"invalid image field"}' ;;
                infra) body=$(jq -cn --argjson code "$MOCK_API" '{code:$code,message:"Provider资源不足: 磁盘资源不足"}') ;;
                missing) body='{"code":200,"data":{}}' ;;
                task_failed|task_camel) body=$(jq -cn --argjson id "$((instance_id + 30))" '{code:200,data:{taskId:$id}}') ;;
            esac
            ;;
        GET:*/providers/1/images)
            body='{"code":200,"data":[{"name":"debian-12-\"quoted\"","instanceType":"container","architecture":"amd64","osType":"debian"},{"name":"debian-12-\"quoted\"","instanceType":"vm","architecture":"amd64","osType":"debian"}]}' ;;
        GET:*/admin/providers/1) body='{"code":200,"data":{"architecture":"amd64"}}' ;;
        GET:*/admin/instances/4[12])
            if [[ "$with_code" == true && -f "$fixture/deleted-${url##*/}" ]]; then
                code="$MOCK_DELETE"
                body=$(jq -cn --argjson code "$code" '{code:$code}')
                [[ "$MOCK_DELETE" != 000 ]] || status=28
            else
                body='{"code":200,"data":{"status":"running","image":"debian-12"}}'
            fi
            ;;
        *) fail "unexpected API request $method $url" ;;
    esac
    printf '%s\n' "$body"
    [[ "$with_code" != true ]] || printf '%s\n' "$code"
    return "$status"
}
wait_task_complete_nonfatal() {
    if [[ "$MOCK_MODE" == task_failed ]]; then
        printf '%s\n' '{"code":200,"data":{"status":"failed","errorMessage":"invalid image configuration"}}'
        return 1
    fi
    jq -cn --argjson id "$(($2 - 30))" '{code:200,data:{status:"completed",instanceId:$id}}'
}
wait_instance_status_nonfatal() { printf '%s\n' '{"code":200,"data":{"status":"running"}}'; }
delete_instance_safe() { touch "$fixture/deleted-$1"; }

for http in 200 201; do
    reset_fixture; MOCK_HTTP="$http"
    _m29_create_instance_nonfatal create '{"image":"debian"}'
    jq -e -s 'length == 1 and .[0].status == "PASS"' "$RESULTS_FILE" >/dev/null
done
for mode in validation invalid; do
    reset_fixture; MOCK_MODE="$mode"; [[ "$mode" != validation ]] || MOCK_HTTP=400
    if _m29_create_instance_nonfatal create '{}' > /dev/null; then fail "$mode creation passed"; fi
    jq -e -s 'length == 1 and .[0].status == "FAIL"' "$RESULTS_FILE" >/dev/null
done
reset_fixture; MOCK_MODE=infra MOCK_HTTP=409 MOCK_API=409
if _m29_create_instance_nonfatal create '{}' > /dev/null; then fail 'capacity rejection passed'; fi
jq -e -s 'length == 1 and .[0].status == "SKIP"' "$RESULTS_FILE" >/dev/null
for code in 401 403 404; do
    for side in http api; do
        reset_fixture; MOCK_MODE=infra MOCK_HTTP=500 MOCK_API=409
        if [[ "$side" == http ]]; then MOCK_HTTP="$code"; else MOCK_API="$code"; fi
        if _m29_create_instance_nonfatal create '{}' >/dev/null; then fail "$side $code passed"; fi
        jq -e -s 'length == 1 and .[0].status == "FAIL"' "$RESULTS_FILE" >/dev/null || fail "$side $code was hidden as an infrastructure skip"
    done
done
reset_fixture; MOCK_TRANSPORT=28
if _m29_create_instance_nonfatal create '{}' >/dev/null; then fail 'truncated response passed'; fi
jq -e -s 'length == 1 and .[0].status == "FAIL"' "$RESULTS_FILE" >/dev/null

for mode in missing task_failed; do
    reset_fixture; MOCK_MODE="$mode"
    status=0; run_module_29 >/dev/null || status=$?
    [[ "$status" != 0 ]] || fail "$mode module returned success"
    jq -e -s 'any(.[]; .status == "FAIL")' "$RESULTS_FILE" >/dev/null
    ! grep -q 'instances?page' "$fixture/calls" || fail 'claimed an instance by image instead of its identity'
done
reset_fixture; MOCK_MODE=task_camel
run_module_29 >/dev/null
[[ "$(wc -l < "$fixture/creates")" -eq 2 ]] || fail 'same name hid one instance type'
jq -e -s 'all(.[]; .status != "FAIL")' "$RESULTS_FILE" >/dev/null
[[ ! -f "$fixture/deleted-41" ]] || fail 'preserved instance was deleted'

for deletion in 404 000 500 200; do
    reset_fixture; MOCK_DELETE="$deletion"; ACTION_TEST_PRESERVE_INSTANCES=false
    status=0; run_module_29 >/dev/null || status=$?
    if [[ "$deletion" == 404 ]]; then
        [[ "$status" == 0 ]] || fail 'confirmed deletion failed'
        jq -e -s 'any(.[]; (.name | startswith("Verify deleted")) and .status == "PASS")' "$RESULTS_FILE" >/dev/null
    else
        [[ "$status" != 0 ]] || fail "unconfirmed deletion $deletion passed"
        jq -e -s 'any(.[]; (.name | startswith("Verify deleted")) and .status == "FAIL")' "$RESULTS_FILE" >/dev/null
    fi
done
printf 'Provider image result gate passed (HTTP/task/identity/type/deletion boundaries).\n'
