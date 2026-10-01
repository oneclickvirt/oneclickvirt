#!/usr/bin/env bash
set -euo pipefail

repo_root=$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)
module="$repo_root/action_tests/modules/29_provider_images.sh"
eval "$(sed -n '/^_m29_delete_or_preserve_instance() {/,/^}/p' "$module")"

delete_calls=0
skip_calls=0
last_skip_name=""
delete_instance_safe() {
    delete_calls=$((delete_calls + 1))
    [[ "$1" == "42" && "$2" == "token" && "$3" == "300" ]]
}
_m29_record_skip() {
    skip_calls=$((skip_calls + 1))
    last_skip_name="$1"
    [[ "$2" == "DELETE" && "$3" == "/api/v1/admin/instances/42" ]]
}
log_info() { :; }

unset ACTION_TEST_PRESERVE_INSTANCES || true
if _m29_delete_or_preserve_instance 42 token 300 "Image[1]: Debian" provider_images; then
    status=0
else
    status=$?
fi
[[ "$status" -eq 2 && "$delete_calls" -eq 0 && "$skip_calls" -eq 1 ]] || {
    printf 'Default image-test cleanup did not preserve the instance\n' >&2
    exit 1
}
[[ "$last_skip_name" == "Preserve Image[1]: Debian" ]]

status=0
ACTION_TEST_PRESERVE_INSTANCES=0 \
    _m29_delete_or_preserve_instance 42 token 300 "Image[1]: Debian" provider_images || status=$?
[[ "${status:-0}" -eq 2 && "$delete_calls" -eq 0 && "$skip_calls" -eq 2 ]] || {
    printf 'Unknown preservation setting was not handled conservatively\n' >&2
    exit 1
}

ACTION_TEST_PRESERVE_INSTANCES=false \
    _m29_delete_or_preserve_instance 42 token 300 "Image[1]: Debian" provider_images
[[ "$delete_calls" -eq 1 && "$skip_calls" -eq 2 ]] || {
    printf 'Explicit image-test cleanup did not call the deletion API exactly once\n' >&2
    exit 1
}

printf 'Provider image test preservation tests passed\n'
