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

echo "port mapping result gate tests passed"
