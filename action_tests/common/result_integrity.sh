#!/usr/bin/env bash

# Process failures and recorded assertions are independent evidence. An empty,
# incomplete or PASS-only report cannot erase an execution failure.
record_module_harness_failure() {
    record_fail_result "$1" "HARNESS" "" "module execution and recorded assertions" "incomplete" "$2" "HARNESS"
}

validate_test_run_results() {
    local process_status="$1" results_file="$2"
    [[ "$process_status" == 0 ]] || return 1
    [[ -s "$results_file" ]] || return 1
    if ! jq -e -s '
        length > 0 and
        all(.[]; type == "object" and (.status == "PASS" or .status == "SKIP"))
    ' "$results_file" >/dev/null 2>&1; then
        return 1
    fi
}

# Explicit SKIP assertions remain valid for unsupported features. A function
# returning without ANY record, however, did not execute a test module.
module_recorded_results() {
    local results_file="$1" before="$2" after
    [[ -f "$results_file" && "$before" =~ ^[0-9]+$ ]] || return 1
    after=$(wc -l < "$results_file")
    [[ "$after" -gt "$before" ]]
}
