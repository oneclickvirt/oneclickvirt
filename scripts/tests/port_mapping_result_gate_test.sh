#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
MODULE="$ROOT_DIR/action_tests/modules/13_port_mappings.sh"

bash -n "$MODULE"

# These are positive mapping assertions.  A 4xx response must be a failure,
# not an accepted result that silently prevents cleanup and follow-up checks.
if grep -Fq '"200|400"' "$MODULE"; then
    echo "port mapping module still accepts 400 for a positive mapping assertion" >&2
    exit 1
fi
grep -Fq 'local node_pm="" node_pm_request_ok=true node_pm_id=""' "$MODULE"
grep -Fq 'Create port mapping result' "$MODULE"
grep -Fq 'Create port mapping (node type) result' "$MODULE"
grep -Fq 'Delete node port mapping' "$MODULE"

# Controller mappings require a live Agent.  The module must gate positive
# controller assertions on the shared readiness helper and must not issue a
# duplicate-port assertion after the first mapping was skipped or failed.
grep -Fq 'ensure_action_test_agent_ready "$PROVIDER_ID" "$instance_group" "$ADMIN_TOKEN"' "$MODULE" ||
    { echo "controller mapping module has no Agent readiness gate" >&2; exit 1; }
grep -Fq '"$is_running" == "true" && "$agent_status" == "online"' "$ROOT_DIR/action_tests/common/test_framework.sh" ||
    { echo "Agent readiness accepts a standalone process without a live control connection" >&2; exit 1; }
grep -Fq 'pm_created=false' "$MODULE" ||
    { echo "controller mapping module has no creation state" >&2; exit 1; }
grep -Fq 'record_skip_result "Create duplicate port"' "$MODULE" ||
    { echo "duplicate port assertion is not skipped after setup failure" >&2; exit 1; }
grep -Fq '[[ "$manual_mapping_type" == "controller" ]] && duplicate_expected="400|409|infra"' "$MODULE" ||
    { echo "duplicate controller mapping does not classify a lost Agent connection as infrastructure SKIP" >&2; exit 1; }
[[ -x "$ROOT_DIR/scripts/tests/port_mapping_agent_readiness_test.sh" ]] ||
    { echo "Agent readiness fixture is missing or not executable" >&2; exit 1; }

FRAMEWORK="$ROOT_DIR/action_tests/common/test_framework.sh"
grep -Fq 'deadline=$((SECONDS + max_wait))' "$FRAMEWORK" ||
    { echo "Agent control status wait is not bounded by elapsed wall time" >&2; exit 1; }
! grep -Fq 'if [[ "$ACTION_TEST_AGENT_READY_PROVIDER" == "$provider_id" ]]; then' "$FRAMEWORK" ||
    { echo "stale Agent readiness cache can bypass the status probe" >&2; exit 1; }

echo "port mapping result gate tests passed"
