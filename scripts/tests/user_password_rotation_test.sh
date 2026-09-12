#!/bin/bash
set -euo pipefail

ROTATION_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
source "${ROTATION_ROOT}/action_tests/modules/18_user_features.sh"

fail() { echo "password rotation harness test failed: $*" >&2; exit 1; }
report_add_section() { :; }
chain_break() { BROKEN=true; }
record_fail_result() { EXPLICIT_FAILURE=true; }

# These API doubles run inside real Bash command substitutions, just as the
# integration harness does. Do not run the module itself in a subshell: fixture
# credentials must survive for subsequent modules and restore_base_state.
test_api() {
    local name="$1" method="$2" endpoint="$3" expected="$4" token="$7"
    case "$name" in
        "User reset password")
            [[ "$method" == PUT && "$endpoint" == /api/v1/user/reset-password && "$expected" == 200 && "$token" == old-jwt ]] || fail "reset contract"
            case "$SCENARIO" in
                reset_failed) return 1 ;;
                missing_password) printf '%s\n' '{"code":200,"data":{}}' ;;
                invalid_password) printf '%s\n' '{"code":200,"data":{"newPassword":123}}' ;;
                *) jq -cn --arg password "$ROTATED_PASSWORD" '{code:200,data:{newPassword:$password}}' ;;
            esac
            ;;
        "Old user token revoked after reset")
            [[ "$method" == GET && "$endpoint" == /api/v1/user/profile && "$expected" == 401 && "$token" == old-jwt ]] || fail "revocation contract"
            OLD_CHECKED=true
            [[ "$SCENARIO" != old_accepted ]]
            ;;
        "New user token works after reset")
            [[ "$expected" == 200 && "$token" == new-jwt ]] || fail "new token contract"
            NEW_CHECKED=true
            [[ "$SCENARIO" != new_rejected ]]
            ;;
        "Get available resources")
            [[ "$token" == new-jwt && "$TEST_USER_PASS" == "$ROTATED_PASSWORD" ]] || fail "downstream credentials stale"
            DOWNSTREAM_CHECKED=true
            ;;
        *) return 0 ;;
    esac
}

test_api_noauth() {
    [[ "$1" == "User login after password reset" && "$2" == POST && "$3" == /api/v1/auth/login && "$4" == 200 ]] || fail "login contract"
    jq -e --arg username "$TEST_USER" --arg password "$ROTATED_PASSWORD" \
        '.username == $username and .password == $password' <<< "$5" >/dev/null || fail "credentials not JSON escaped"
    [[ -z "$USER_TOKEN" ]] || fail "old token retained during login"
    case "$SCENARIO" in
        login_failed) return 1 ;;
        missing_token) printf '%s\n' '{"code":200,"data":{}}' ;;
        invalid_token) printf '%s\n' '{"code":200,"data":{"token":42}}' ;;
        *) printf '%s\n' '{"code":200,"data":{"token":"new-jwt"}}' ;;
    esac
}

TEST_USER='fixture"user'
ROTATED_PASSWORD=$'New!\\"Password\n123'
TEST_INSTANCE_ID=""
PROVIDER_ID=""
for SCENARIO in success reset_failed missing_password invalid_password login_failed missing_token invalid_token new_rejected old_accepted; do
    USER_TOKEN=old-jwt
    USER_TOKEN2=other-user-jwt
    TEST_USER_PASS=old-password
    BROKEN=false
    EXPLICIT_FAILURE=false
    OLD_CHECKED=false
    NEW_CHECKED=false
    DOWNSTREAM_CHECKED=false
    rc=0
    run_module_18 || rc=$?
    [[ "$USER_TOKEN2" == other-user-jwt ]] || fail "$SCENARIO changed another user's token"
    if [[ "$SCENARIO" == success ]]; then
        [[ "$rc" == 0 && "$USER_TOKEN" == new-jwt && "$TEST_USER_PASS" == "$ROTATED_PASSWORD" ]] || fail "success did not preserve credentials"
        [[ "$OLD_CHECKED" == true && "$NEW_CHECKED" == true && "$DOWNSTREAM_CHECKED" == true && "$BROKEN" == false ]] || fail "success skipped checks"
    else
        [[ "$rc" != 0 && "$DOWNSTREAM_CHECKED" == false ]] || fail "$SCENARIO continued with an invalid fixture"
        if [[ "$SCENARIO" == old_accepted ]]; then
            [[ "$USER_TOKEN" == new-jwt && "$NEW_CHECKED" == true ]] || fail "revocation failure prevented credential recovery"
        else
            [[ -z "$USER_TOKEN" && "$BROKEN" == true ]] || fail "$SCENARIO retained stale authentication"
        fi
        case "$SCENARIO" in
            missing_password|invalid_password|missing_token|invalid_token)
                [[ "$EXPLICIT_FAILURE" == true ]] || fail "$SCENARIO not reported as a failure" ;;
        esac
    fi
done
echo 'Password rotation harness tests passed (9 scenarios).'
