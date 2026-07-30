#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"

go_contract=$(sed -n 's/^const APIContractVersion = "\([^"]*\)"$/\1/p' "$ROOT_DIR/server/constant/version.go")
installer_contract=$(sed -n 's/^EXPECTED_API_CONTRACT="\([^"]*\)"$/\1/p' "$ROOT_DIR/scripts/install.sh")
web_contract=$(sed -n "s/^export const expectedApiContract = '\([^']*\)'$/\1/p" "$ROOT_DIR/web/src/utils/apiCompatibilityCore.js")

test -n "$go_contract"
test "$installer_contract" = "$go_contract"
test "$web_contract" = "$go_contract"

echo "API contract sync tests passed: $go_contract"
