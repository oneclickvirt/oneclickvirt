#!/bin/bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
for module in "${ROOT}/action_tests/modules/09_providers.sh" "${ROOT}/action_tests/modules/30_provider_agent_mode.sh"; do
    grep -Fq '"200|infra"' "$module" || {
        echo "provider exec status contract is not fail-closed in ${module}" >&2
        exit 1
    }
    if grep -Fq '200|400|500|502' "$module"; then
        echo "provider exec status contract still accepts arbitrary failures in ${module}" >&2
        exit 1
    fi
done
echo 'Provider exec status contract classifies only matched infrastructure failures as SKIP.'
