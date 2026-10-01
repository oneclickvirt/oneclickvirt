#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
# shellcheck source=../../action_tests/common/platforms/hetzner_api.sh
source "${ROOT_DIR}/action_tests/common/platforms/hetzner_api.sh"

calls=$(mktemp)
trap 'rm -f "$calls"' EXIT
log_info() { :; }
log_success() { :; }
log_warning() { :; }
log_error() { printf '%s\n' "$*" >&2; }
fail() { printf 'Hetzner password rebuild test failed: %s\n' "$*" >&2; exit 1; }

hetzner_request() {
    local method="$1" endpoint="$2"
    printf '%s %s\n' "$method" "$endpoint" >> "$calls"
    case "$method $endpoint" in
        'POST /servers/42/actions/rebuild') printf '%s\n201\n' '{"action":{"id":11},"root_password":"expired-password"}' ;;
        'GET /actions/11') printf '%s\n200\n' '{"action":{"status":"success"}}' ;;
        'POST /servers/42/actions/reset_password') printf '%s\n201\n' '{"action":{"id":12},"root_password":"usable-password"}' ;;
        'GET /actions/12') printf '%s\n200\n' '{"action":{"status":"success"}}' ;;
        'GET /servers/42') printf '%s\n200\n' '{"server":{"public_net":{"ipv4":{"ip":"192.0.2.42"}}}}' ;;
        *) fail "unexpected API call: $method $endpoint" ;;
    esac
}

PLATFORM_SSH_KEY_FILE=""
result=$(hetzner_platform_reinstall_instance 42 debian) || fail 'rebuild failed'
[[ $(jq -r '.password' <<< "$result") == 'usable-password' ]] || fail 'rebuild returned the expired password'
[[ $(jq -r '.ipv4' <<< "$result") == '192.0.2.42' ]] || fail 'rebuild returned the wrong IPv4 address'
expected=$'POST /servers/42/actions/rebuild\nGET /actions/11\nPOST /servers/42/actions/reset_password\nGET /actions/12\nGET /servers/42'
[[ $(cat "$calls") == "$expected" ]] || fail 'rebuild and password reset were not sequenced correctly'

hetzner_platform_list_instances() { printf '%s\n' '[{"instance_id":"42"}]'; }
if hetzner_platform_create_instance docker 8 >/dev/null 2>&1; then
    fail 'a second Hetzner server was created despite an existing worker'
fi
[[ $(cat "$calls") == "$expected" ]] || fail 'second-server guard issued a create request'

sleep() { :; }
printf '0\n' > "$calls"
hetzner_request() {
    local method="$1" endpoint="$2"
    case "$method $endpoint" in
        'POST /servers/42/actions/reset_password')
            local reset_count
            reset_count=$(cat "$calls")
            reset_count=$((reset_count + 1))
            printf '%s\n' "$reset_count" > "$calls"
            if [[ "$reset_count" -eq 1 ]]; then
                printf '%s\n201\n' '{"action":{"id":21},"root_password":"expired-attempt"}'
            else
                printf '%s\n201\n' '{"action":{"id":22},"root_password":"ready-attempt"}'
            fi
            ;;
        'GET /actions/21') printf '%s\n200\n' '{"action":{"status":"error","error":{"code":"guest_agent_unavailable"}}}' ;;
        'GET /actions/22') printf '%s\n200\n' '{"action":{"status":"success"}}' ;;
        *) fail "unexpected retry API call: $method $endpoint" ;;
    esac
}
password=$(_hetzner_reset_root_password 42) || fail 'guest-agent readiness retry failed'
[[ "$password" == 'ready-attempt' ]] || fail 'retry returned the failed password'

printf 'Hetzner password rebuild test passed\n'
