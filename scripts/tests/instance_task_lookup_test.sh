#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
fixture=$(mktemp -d)
trap 'rm -rf "$fixture"' EXIT
# shellcheck disable=SC1091
source "$ROOT_DIR/action_tests/common/test_framework.sh"
export SERVER_URL="http://fixture" ADMIN_TOKEN="fixture-token"
fail() { echo "Instance task lookup failed: $*" >&2; exit 1; }
curl() {
    printf '%s\n' "$response"
    [[ "${transport_failure:-false}" != true ]]
}
expect_task() {
    local id="$1" type="$2" expected="$3" result
    result=$(get_latest_instance_task_response "$id" "$type") || fail "missing $type task for $id"
    [[ "$(jq -r '.data.id // .data.ID' <<< "$result")" == "$expected" ]] || fail "incorrect task for $id"
}
expect_missing() {
    if get_latest_instance_task_response "$1" "$2" > "$fixture/unexpected"; then
        fail "accepted an unrelated or unavailable task"
    fi
}

response='{"code":200,"data":{"list":[
    {"id":10,"instanceId":3,"taskType":"rebuild","status":"completed"},
    {"id":12,"instanceId":4,"taskType":"rebuild","status":"running","taskData":"{\"instanceId\":4,\"resetOldInstanceId\":3}"},
    {"id":99,"instanceId":8,"taskType":"rebuild","taskData":"{\"resetOldInstanceId\":7}"},
    {"id":100,"instanceId":3,"taskType":"delete"}
]}}'
expect_task 3 rebuild 12
echo 'PASS: original rebuild identity resolves to the newest migrated task'

response='{"code":200,"data":{"list":[
    {"ID":14,"instance_id":5,"task_type":"reset","task_data":{"reset_old_instance_id":4}}
]}}'
expect_task 4 reset 14
echo 'PASS: reset lookup accepts object metadata and legacy field names'

response='{"code":200,"data":{"list":[
    {"id":15,"instanceId":6,"taskType":"restart","taskData":"not-json"},
    {"id":16,"instanceId":8,"taskType":"rebuild","taskData":"not-json"}
]}}'
expect_task 6 restart 15
expect_missing 3 rebuild
echo 'PASS: malformed reset metadata cannot select an unrelated task'

response='{"code":500,"data":{"list":[{"id":17,"instanceId":3,"taskType":"rebuild"}]}}'
expect_missing 3 rebuild
response='not-json'
expect_missing 3 rebuild
response='{"code":200,"data":{"list":[]}}'
expect_missing 3 rebuild
echo 'PASS: API errors, malformed responses and missing tasks remain failures'

response='{"code":200,"data":{"list":[{"id":18,"instanceId":3,"taskType":"rebuild"}]}}'
transport_failure=true
expect_missing 3 rebuild
echo 'PASS: a transport failure cannot accept a partial response'

echo 'Instance task lookup tests passed: 5'
