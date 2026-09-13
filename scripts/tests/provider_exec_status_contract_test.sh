#!/bin/bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
for module in "${ROOT}/action_tests/modules/09_providers.sh" "${ROOT}/action_tests/modules/30_provider_agent_mode.sh"; do
    grep -Fq '200|400|500|502' "$module" || {
        echo "provider exec status contract missing 502 in ${module}" >&2
        exit 1
    }
done
echo 'Provider exec status contract accepts upstream 502 failures.'
