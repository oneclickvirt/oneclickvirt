#!/usr/bin/env bash
set -euo pipefail
ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
fixture=$(mktemp -d)
trap 'rm -rf "$fixture"' EXIT
source "$ROOT_DIR/action_tests/common/module_selection.sh"
fail() { printf 'Module selection failed: %s\n' "$*" >&2; exit 1; }
mkdir -p "$fixture/modules"
touch "$fixture/modules/01_fixture.sh" "$fixture/modules/02_fixture.sh" "$fixture/modules/03_fixture.sh"
[[ "$(parse_test_modules all "$fixture/modules")" == '01 02 03' ]] || fail 'all'
[[ "$(parse_test_modules 1,02,01-03,3 "$fixture/modules")" == '01 02 03' ]] || fail 'overlapping selections execute more than once'
[[ "$(parse_test_modules 3,1 "$fixture/modules")" == '03 01' ]] || fail 'selection order'
for selection in '' 00 03-01 01, 01,,02 001 01-99999999999999 '../report' '1+2' '01;true' 'x[$(touch marker)]' 'all,01'; do
    if parse_test_modules "$selection" "$fixture/modules" > /dev/null 2>&1; then fail "accepted $selection"; fi
done
if validate_test_modules "$fixture/modules" 04 >/dev/null 2>&1; then fail 'missing module'; fi
# Invalid selections must fail before any worker provisioning or output files.
for selection in 00 03-01 99 '01,01;true'; do
    status=0
    REPORT_DIR="$fixture/reports" bash "$ROOT_DIR/action_tests/run_env_test.sh" docker "$selection" container > "$fixture/output" 2>&1 || status=$?
    [[ "$status" == 2 && ! -e "$fixture/reports" ]] || fail 'environment harness performed work before validating input'
done
printf 'Module selection tests passed (normalization, bounded input and pre-provision validation).\n'
