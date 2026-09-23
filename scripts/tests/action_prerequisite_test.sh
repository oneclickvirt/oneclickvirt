#!/usr/bin/env bash
set -euo pipefail
ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
fixture=$(mktemp -d)
trap 'rm -rf "$fixture"' EXIT
fail() { echo "Action prerequisite test failed: $*" >&2; exit 1; }

REPORT_DIR="$fixture/reports" bash "$ROOT_DIR/action_tests/run_env_test.sh" --help >/dev/null
[[ ! -e "$fixture/reports" ]] || fail "--help created report state"
if REPORT_DIR="$fixture/reports" bash "$ROOT_DIR/action_tests/run_env_test.sh" invalid >/dev/null 2>&1; then
    fail "unknown environment was accepted"
fi
[[ ! -e "$fixture/reports" ]] || fail "invalid argument initialized runtime state"

source "$ROOT_DIR/action_tests/modules/19_speedtest.sh"
source "$ROOT_DIR/action_tests/modules/28_instance_ssh_speedtest.sh"
source "$ROOT_DIR/action_tests/modules/29_provider_images.sh"
report_add_section() { :; }
chain_break() { :; }
log_skip() { :; }
report_add_skip() { :; }
record_skip_result() { printf '%s\n' "$*" >> "$fixture/skips"; }
_record_result() { [[ "$4" == SKIP ]] || fail "expected SKIP"; printf '%s\n' "$*" >> "$fixture/skips"; }
PROVIDER_ID=""
TEST_INSTANCE_ID=""
TOTAL_TESTS=0
SKIPPED_TESTS=0
run_module_19
run_module_28
run_module_29
[[ "$(wc -l < "$fixture/skips" | tr -d ' ')" == 3 ]] || fail "missing prerequisites produced empty modules"
echo "Action prerequisite tests passed"
