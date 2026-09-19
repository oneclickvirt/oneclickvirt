#!/usr/bin/env bash
set -Eeuo pipefail
ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
test_dir=$(mktemp -d)
trap 'rm -r "$test_dir"' EXIT
mkdir -p "$test_dir/common" "$test_dir/modules"
cp "$ROOT_DIR/action_tests/run_module.sh" "$test_dir/run_module.sh"
cp "$ROOT_DIR/action_tests/common/result_integrity.sh" "$test_dir/common/"
cp "$ROOT_DIR/scripts/tests/fixtures/module-runner-framework.sh" "$test_dir/common/test_framework.sh"
: > "$test_dir/common/node_manager.sh"
export OCV_RUNNER_FRAMEWORK="$ROOT_DIR/action_tests/common/test_framework.sh"
export GENERATE_MODULE_REPORT=false TEST_INSTANCE_ID='' RESULTS_FILE_SHARED=false

run_case() {
    local name="$1" module_source="$2" expected="$3" failure_name="${4:-}" selection="${5:-01}" rc=0
    local report_dir="$test_dir/$name"
    mkdir -p "$report_dir"
    # Empty source is a genuinely missing module, not a feature skip.
    if [[ -n "$module_source" ]]; then
        printf '%s\n' "$module_source" > "$test_dir/modules/01_fixture.sh"
    fi
    REPORT_DIR="$report_dir" RESULTS_FILE="$report_dir/results.jsonl" \
        bash "$test_dir/run_module.sh" "$selection" http://127.0.0.1:1 >"$report_dir/output.log" 2>&1 || rc=$?
    if [[ "$rc" != "$expected" ]]; then
        cat "$report_dir/output.log" >&2
        echo "$name: expected exit $expected, got $rc" >&2
        exit 1
    fi
    if [[ -n "$failure_name" ]]; then
        jq -e -s --arg name "$failure_name" \
            'any(.[]; .name == $name and .status == "FAIL" and .group == "HARNESS")' \
            "$report_dir/results.jsonl" >/dev/null
    fi
    if [[ "$expected" == 0 ]]; then
        jq -e -s 'length >= 2 and all(.[]; .status == "PASS" or .status == "SKIP")' \
            "$report_dir/results.jsonl" >/dev/null
    fi
    echo "PASS module runner: $name"
}

run_case missing '' 1 module-01-missing
run_case no_entry 'unrelated_function() { :; }' 1 module-01-entry
run_case source_failure 'return 42' 1 module-01-load
run_case syntax_failure 'run_module_01() {' 1 module-01-load
run_case empty 'run_module_01() { return 0; }' 1 module-01-empty
run_case passed_then_error 'run_module_01() { record_pass_result assertion TEST ""; return 42; }' 1 module-01-exit
run_case pass 'run_module_01() { record_pass_result assertion TEST ""; }' 0
run_case explicit_skip 'run_module_01() { record_skip_result unsupported TEST "" "unsupported feature" FEATURE; }' 0
run_case assertion_failure 'run_module_01() { record_fail_result assertion TEST "" true false "synthetic failure" FEATURE; return 0; }' 1
run_case next_module_missing 'run_module_01() { record_pass_result assertion TEST ""; }' 1 module-02-missing 01,02
echo 'Real module runner integrity tests passed (10 scenarios; no external API calls).'
