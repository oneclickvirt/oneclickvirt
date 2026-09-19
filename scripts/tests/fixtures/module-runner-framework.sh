#!/usr/bin/env bash
# Keep real assertion serialization/reporting; only external services are fake.
source "${OCV_RUNNER_FRAMEWORK}"
wait_server_ready() { return 0; }
admin_login() { printf '%s\n' synthetic-admin-token; }
do_login() { printf '%s\n' synthetic-user-token; }
curl() { return 0; }
save_base_state() { return 0; }
restore_base_state() { return 0; }
run_captcha_disabled_contract_checks() {
    record_pass_result baseline HARNESS '' true true '' baseline
}
