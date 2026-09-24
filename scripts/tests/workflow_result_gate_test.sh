#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
WORKFLOW="${ROOT_DIR}/.github/workflows/integration-tests.yml"
ARM_WORKFLOW="${ROOT_DIR}/.github/workflows/arm-controller-tests.yml"
AGENT_WORKFLOW="${ROOT_DIR}/.github/workflows/agent-regressions.yml"
DOCKER_WORKFLOW="${ROOT_DIR}/.github/workflows/build_docker.yml"
TEST_AGENT_WORKFLOW="${ROOT_DIR}/.github/workflows/build-test-agent.yml"

fail() {
    echo "workflow result gate test failed: $*" >&2
    exit 1
}

[[ -s "$WORKFLOW" ]] || fail "integration workflow is missing"
[[ -s "$ARM_WORKFLOW" ]] || fail "ARM controller workflow is missing"
[[ -s "$AGENT_WORKFLOW" ]] || fail "Agent regression workflow is missing"
[[ -s "$DOCKER_WORKFLOW" ]] || fail "Docker publish workflow is missing"
[[ -s "$TEST_AGENT_WORKFLOW" ]] || fail "Reusable test Agent workflow is missing"

# Callers deliberately isolate cancellation by environment/module. Reusing a
# workflow/ref-only group inside the called workflow cancels unrelated runs.
grep -Fq 'group: build-test-agent-${{ github.run_id }}-${{ github.run_attempt }}' "$TEST_AGENT_WORKFLOW" ||
    fail "reusable Agent build concurrency is not isolated per caller run/attempt"
! grep -Eq 'cancel-in-progress:[[:space:]]*true' "$TEST_AGENT_WORKFLOW" ||
    fail "reusable Agent build must leave cancellation to its callers"

# A registry login is part of the publish contract. If it is allowed to fail,
# the matrix can finish without any digest and the merge job can appear green
# while publishing nothing. Description updates may remain best-effort, but
# GHCR authentication must fail the build.
if awk '
    /name: Login to GitHub Container Registry/ { in_login=1; next }
    in_login && /name:/ { in_login=0 }
    in_login && /continue-on-error:[[:space:]]*true/ { found=1 }
    END { exit found ? 0 : 1 }
' "$DOCKER_WORKFLOW"; then
    fail "Docker workflow allows GHCR login failures to produce a green publish"
fi

# A skipped/aborted run has no authoritative TEST_EXIT and must fail the
# workflow.  Likewise, an infrastructure abort that produced no JSONL must be
# visible as incomplete rather than silently green.
grep -Fq '::error::Test step did not produce an exit code' "$WORKFLOW" ||
    fail "missing TEST_EXIT is not a hard failure"
grep -Fq '::error::Integration tests did not produce JSONL results' "$WORKFLOW" ||
    fail "missing JSONL output is not a hard failure"
grep -Fq 'Status | INCOMPLETE (' "$WORKFLOW" ||
    fail "incomplete runs are not labeled in the summary"
grep -Fq '::error::Integration tests produced an empty result set' "$WORKFLOW" ||
    fail "empty result sets are not a hard failure"
grep -Fq '::error::No module assertions were executed' "$WORKFLOW" ||
    fail "harness-only runs are still reported as successful integration tests"
! grep -Fq 'Module assertions were not executed in this run' "$WORKFLOW" ||
    fail "workflow still allows a harness-only run to pass"
grep -Fq 'pip3 install --quiet paramiko websocket-client' "$WORKFLOW" ||
    fail "live Agent/WebSSH dependency installation is incomplete"
grep -Fq 'python3 action_tests/static_audit.py --root . --output-dir ${{ env.REPORT_DIR }} --strict --min-route-coverage 82' "$WORKFLOW" ||
    fail "integration workflow does not run strict static audit"
grep -Fq 'REPORT_DIR: ${{ github.workspace }}/action_tests/reports/${{ github.run_id }}-${{ github.run_attempt }}-' "$WORKFLOW" ||
    fail "integration reports are not isolated from tracked historical reports"
grep -Fq 'current_results="${REPORT_DIR}/${{ matrix.environment }}-results.jsonl"' "$WORKFLOW" ||
    fail "integration gate does not select the current environment result file"
! grep -Fq 'result_files=(action_tests/reports/*.jsonl)' "$WORKFLOW" ||
    fail "integration gate still includes stale reports"
grep -Fq 'needs: [build-test-agent]' "$WORKFLOW" || fail "real Agent build is not required"
grep -Fq 'python3 scripts/validate_agent_assets.py server/assets/agent' "$WORKFLOW" ||
    fail "synthetic Agent artifacts are not rejected"
grep -Fq '::error::Action test static audit found blocking findings' "$WORKFLOW" ||
    fail "static audit findings are not surfaced as blocking errors"
grep -Fq 'exit "${audit_exit}"' "$WORKFLOW" ||
    fail "static audit exit code is not propagated"
! grep -Fq 'Status | SKIPPED (' "$WORKFLOW" ||
    fail "workflow still reports an empty/aborted run as SKIPPED"
grep -Fq "Unknown JSONL result status" "$WORKFLOW" ||
    fail "integration workflow does not reject unknown result statuses"
! grep -Fq '*) module_skipped=$((module_skipped + 1)); skipped=$((skipped + 1)) ;;' "$WORKFLOW" ||
    fail "integration workflow still converts unknown module statuses into SKIP"

# ARM controller validation must use the same fail-closed contract. Earlier
# versions treated a skipped harness, non-zero infrastructure exit, malformed
# status, or all-SKIP JSONL as a successful workflow.
grep -Fq 'ARM controller test step did not produce an exit code' "$ARM_WORKFLOW" ||
    fail "ARM workflow does not fail when the test step is skipped/aborted"
grep -Fq 'Status | INCOMPLETE (' "$ARM_WORKFLOW" ||
    fail "ARM workflow does not label infrastructure failures incomplete"
grep -Fq 'All ${skipped} ARM controller test assertions were skipped' "$ARM_WORKFLOW" ||
    fail "ARM workflow does not reject all-SKIP results"
grep -Fq 'result contains unknown status' "$ARM_WORKFLOW" ||
    fail "ARM workflow does not reject unknown result statuses"
! grep -Fq 'ARM controller tests skipped:' "$ARM_WORKFLOW" ||
    fail "ARM workflow still reports harness failures as successful skips"

# The original shared-WebSocket regression lives in the Go controller. A Rust
# Agent-only workflow does not exercise command timeout, WebSSH close, or old
# connection cleanup on that side, so both path triggers and a race job are
# mandatory.
grep -Fq "'server/service/agent/**'" "$AGENT_WORKFLOW" ||
    fail "Agent workflow does not trigger for Go controller Agent changes"
grep -Fq "'server/service/console/**'" "$AGENT_WORKFLOW" ||
    fail "Agent workflow does not trigger for WebSSH service changes"
grep -Fq "'server/api/v1/admin/*terminal*.go'" "$AGENT_WORKFLOW" ||
    fail "Agent workflow does not trigger for admin terminal handlers"
grep -Fq 'go test -count=1 -race ./service/agent ./service/console ./api/v1/admin' "$AGENT_WORKFLOW" ||
    fail "Agent workflow lacks Go controller lifecycle race tests"
grep -Fq 'go vet ./service/agent ./service/console ./api/v1/admin' "$AGENT_WORKFLOW" ||
    fail "Agent workflow lacks controller package vetting"

python3 "$ROOT_DIR/scripts/tests/workflow_result_gate_test.py"
echo "Workflow result gate tests passed"
