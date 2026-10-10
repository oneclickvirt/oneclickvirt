#!/usr/bin/env bash
set -euo pipefail
ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
fixture=$(mktemp -d)
trap 'rm -rf "$fixture"' EXIT
source "$ROOT_DIR/action_tests/common/test_framework.sh"
SERVER_URL=http://fixture.invalid ADMIN_TOKEN=fixture RESULTS_FILE="$fixture/results.jsonl"
REPORT_FILE="$fixture/report.md"
sleep() { :; }
capture_service_logs() { :; }
curl() {
    printf 'call\n' >> "$fixture/calls"
    local count
    count=$(wc -l < "$fixture/calls")
    if [[ "$count" -eq 1 || "$always_fail" == true ]]; then
        printf '%s\n200\n' '{"code":200,"data":{"accepted":true}}'
        return 28 # A response was received, but curl did not finish reading it.
    fi
    printf '%s\n200\n' '{"code":200,"data":{"accepted":true}}'
}
reset_fixture() { : > "$fixture/calls"; : > "$RESULTS_FILE"; TEST_RESULTS_JSON=(); TOTAL_TESTS=0; PASSED_TESTS=0; FAILED_TESTS=0; SKIPPED_TESTS=0; }
fail() { printf 'Retry safety failed: %s\n' "$*" >&2; exit 1; }
always_fail=false
for method in POST PUT DELETE PATCH; do
    reset_fixture
    if test_api_retry mutation "$method" /mutation 200 '{}' 3 0 safety > /dev/null 2>&1; then fail 'incomplete mutation response passed'; fi
    [[ "$(wc -l < "$fixture/calls")" -eq 1 ]] || fail 'mutation retried'
    jq -e -s 'length == 1 and .[0].status == "FAIL"' "$RESULTS_FILE" >/dev/null
done
reset_fixture
test_api_retry read GET /read 200 '' 3 0 safety >/dev/null 2>&1
[[ "$(wc -l < "$fixture/calls")" -eq 2 ]] || fail 'safe read did not retry'
jq -e -s 'length == 1 and .[0].status == "PASS"' "$RESULTS_FILE" >/dev/null
reset_fixture
record_pass_result baseline HARNESS '' true true '' HARNESS >/dev/null 2>&1
test_api_retry read_with_history GET /read 200 '' 3 0 safety >/dev/null 2>&1
jq -e -s 'length == 2 and .[0].name == "baseline" and all(.[]; .status == "PASS")' "$RESULTS_FILE" >/dev/null
reset_fixture
always_fail=true
if test_api_retry exhausted GET /read 200 '' 99 0 safety >/dev/null 2>&1; then fail 'exhausted read passed'; fi
[[ "$(wc -l < "$fixture/calls")" -eq 3 ]] || fail 'read retry was unbounded'
jq -e -s 'length == 1 and .[0].status == "FAIL"' "$RESULTS_FILE" >/dev/null
reset_fixture
if test_api_json_value truncated GET /read 200 .data.accepted true '' safety > /dev/null 2>&1; then fail 'JSON assertion ignored transport failure'; fi
jq -e -s 'length == 1 and .[0].status == "FAIL"' "$RESULTS_FILE" >/dev/null
printf 'API retry safety tests passed (single mutation, bounded reads, transport failures).\n'
