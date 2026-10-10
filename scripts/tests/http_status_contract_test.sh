#!/usr/bin/env bash
set -Eeuo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
TEST_DIR="$(mktemp -d)"
trap 'rm -rf "$TEST_DIR"' EXIT

source "$ROOT_DIR/action_tests/common/test_framework.sh"
SERVER_URL="http://mock.local"
ADMIN_TOKEN="test-token"
REPORT_FILE="$TEST_DIR/report.md"
RESULTS_FILE="$TEST_DIR/results.jsonl"
report_init "$REPORT_FILE" "HTTP status contract"
init_results_file "$RESULTS_FILE"

MOCK_CODE=200
MOCK_BODY='{"code":200,"data":{"ok":true}}'
curl() {
    printf '%s\n%s\n' "$MOCK_BODY" "$MOCK_CODE"
}

test_api "successful response" GET /ok "200|infra" "" contract >/dev/null
[[ "$(jq -s -r '.[0].status' "$RESULTS_FILE")" == PASS ]] || {
    echo "2xx response was not recorded as PASS" >&2
    exit 1
}

MOCK_CODE=502
MOCK_BODY='{"code":502,"message":"SSH connection timeout"}'
test_api "matched infrastructure response" GET /infra "200|infra" "" contract >/dev/null
[[ "$(jq -r '.[1].status' <(jq -s . "$RESULTS_FILE"))" == SKIP ]] || {
    echo "matched infrastructure response was not recorded as SKIP" >&2
    exit 1
}

MOCK_CODE=400
MOCK_BODY='{"code":400,"message":"invalid port range"}'
if test_api "validation response" GET /invalid "200|infra" "" contract >/dev/null; then
    echo "ordinary validation response was accepted" >&2
    exit 1
fi
[[ "$(jq -r '.[2].status' <(jq -s . "$RESULTS_FILE"))" == FAIL ]] || {
    echo "ordinary validation response was not recorded as FAIL" >&2
    exit 1
}

for code in 401 403 404; do
    for side in http api; do
        MOCK_CODE=502
        MOCK_BODY='{"code":502,"message":"SSH connection timeout"}'
        if [[ "$side" == http ]]; then
            MOCK_CODE="$code"
        else
            MOCK_BODY=$(jq -cn --argjson code "$code" '{code:$code,message:"SSH connection timeout"}')
        fi
        if test_api "protected $side $code" GET /protected "200|infra" "" contract >/dev/null; then
            echo "$side $code was hidden as infrastructure" >&2
            exit 1
        fi
        [[ "$(jq -s -r '.[-1].status' "$RESULTS_FILE")" == FAIL ]]
    done
done

MOCK_CODE=502
MOCK_BODY='SSH connection timeout'
test_api "plain infrastructure response" GET /plain-infra "200|infra" "" contract >/dev/null || {
    echo 'plain infrastructure diagnostic was not classified as SKIP' >&2
    exit 1
}
for MOCK_BODY in 'SSH connection timeout: {' '{"message":"SSH connection timeout"}' \
    $'{"code":502,"message":"SSH connection timeout"}\n{"code":502}'; do
    if test_api "malformed infrastructure body" GET /invalid "200|infra" "" contract >/dev/null; then
        echo 'malformed API response became a successful infrastructure skip' >&2
        exit 1
    fi
done

echo "HTTP status contract tests passed (PASS/SKIP/FAIL and protected errors)."
