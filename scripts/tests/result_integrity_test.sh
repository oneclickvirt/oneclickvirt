#!/usr/bin/env bash
set -Eeuo pipefail
ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
source "$ROOT_DIR/action_tests/common/result_integrity.sh"
test_dir=$(mktemp -d)
trap 'rm -r "$test_dir"' EXIT
results="$test_dir/results.jsonl"
printf '%s\n' '{"status":"PASS","group":"HARNESS"}' >"$results"
validate_test_run_results 0 "$results"
if validate_test_run_results 1 "$results";then echo 'Nonzero process status erased by PASS records' >&2;exit 1;fi
if module_recorded_results "$results" 1;then echo 'Baseline assertion masked empty module' >&2;exit 1;fi
printf '%s\n' '{"status":"SKIP","group":"FEATURE"}' >>"$results"
module_recorded_results "$results" 1
validate_test_run_results 0 "$results"
printf '%s\n' '{"status":"FAIL"}' >>"$results"
if validate_test_run_results 0 "$results";then echo 'FAIL assertion ignored' >&2;exit 1;fi
printf '%s\n' '{"status":"PASS"}' 'invalid JSON' >"$results"
if validate_test_run_results 0 "$results";then echo 'Malformed results accepted' >&2;exit 1;fi
: >"$results"
if validate_test_run_results 0 "$results";then echo 'Empty results accepted' >&2;exit 1;fi
if validate_test_run_results 0 "$test_dir/missing";then echo 'Missing results accepted' >&2;exit 1;fi
source "$ROOT_DIR/action_tests/common/test_framework.sh"
REPORT_FILE="$test_dir/report.md"
RESULTS_FILE="$results"
record_module_harness_failure "missing-entry" "Requested module has no entry function"
jq -e '.status == "FAIL" and .group == "HARNESS"' "$results" >/dev/null
echo 'Result integrity tests passed (9 scenarios; no skipped execution).'
