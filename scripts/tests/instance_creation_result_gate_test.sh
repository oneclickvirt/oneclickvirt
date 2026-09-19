#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
MODULE="$ROOT_DIR/action_tests/modules/26_instance_types.sh"

bash -n "$MODULE"

# Instance creation is a positive lifecycle assertion.  Only 2xx responses
# may enter task/ID handling; 4xx responses must remain recorded failures.
if grep -Fq '"200|201|400|409"' "$MODULE"; then
    echo "instance creation still accepts 4xx as success" >&2
    exit 1
fi
grep -Fq 'local ct_resp="" ct_request_ok=true' "$MODULE"
grep -Fq 'local vm_resp="" vm_request_ok=true' "$MODULE"
grep -Fq 'Create type-test container result' "$MODULE"
grep -Fq 'Create type-test VM result' "$MODULE"

echo "instance creation result gate tests passed"
