#!/bin/bash
# Module 01: System Initialization & Health Checks
# Dependencies: none

run_module_01() {
    report_add_section "01 - System Initialization & Health"
    local group="init"

    # -- Health endpoints --
    test_api_noauth "Health check /health" "GET" "/health" "200" "" "$group"
    test_api_noauth "Health check /api/health" "GET" "/api/health" "200" "" "$group"
    test_api_noauth "Health check /api/v1/health" "GET" "/api/v1/health" "200" "" "$group"
    test_api_noauth "Ping /api/ping" "GET" "/api/ping" "200" "" "$group"

    # -- Init status --
    local init_r; init_r=$(test_api_noauth "Init status check" "GET" "/api/v1/public/init/check" "200" "" "$group")
    local need_init; need_init=$(echo "$init_r" | jq -r '.data.needInit // "true"' 2>/dev/null)

    # -- Recommended DB type --
    test_api_noauth "Recommended DB type" "GET" "/api/v1/public/recommended-db-type" "200" "" "$group"

    # -- Test DB connection with invalid params --
    test_api_noauth "Test DB connection (invalid)" "POST" "/api/v1/public/test-db-connection" "400" \
        '{"db_type":"mysql","db_host":"invalid","db_port":9999,"db_name":"x","db_user":"x","db_password":"x"}' "$group"

    # -- System initialization (only if not already initialized by orchestrator) --
    if [[ "$need_init" == "true" ]]; then
        test_api_noauth "System init (MySQL)" "POST" "/api/v1/public/init" "200|400" \
            "{\"admin\":{\"username\":\"${ADMIN_USER}\",\"password\":\"${ADMIN_PASS}\",\"email\":\"${ADMIN_USER}@test.local\"},\"database\":{\"type\":\"mysql\"}}" "$group"
        sleep 2
    else
        log_info "System already initialized, skipping init test"
    fi

    # -- Duplicate init should fail --
    test_api_noauth "Duplicate init (should fail)" "POST" "/api/v1/public/init" "400|409" \
        "{\"admin\":{\"username\":\"${ADMIN_USER}\",\"password\":\"${ADMIN_PASS}\",\"email\":\"${ADMIN_USER}@test.local\"},\"database\":{\"type\":\"mysql\"}}" "$group"

    # -- Init with missing fields --
    test_api_noauth "Init missing username" "POST" "/api/v1/public/init" "400|409" \
        '{"admin":{"password":"test"},"database":{"type":"mysql"}}' "$group"

    # -- Admin login --
    ADMIN_TOKEN=$(admin_login "$SERVER_URL" "$ADMIN_USER" "$ADMIN_PASS") || { chain_break "$group" "Admin login failed"; return 1; }

    # -- Public endpoints --
    test_api_noauth "Server version" "GET" "/api/v1/public/version" "200" "" "$group"
    test_api_noauth "Build info" "GET" "/api/v1/public/build-info" "200" "" "$group"
    test_api_noauth "Init progress" "GET" "/api/v1/public/init-progress" "200" "" "$group"
    test_api_noauth "Agent install script" "GET" "/api/v1/public/agent/install-agent.sh" "200" "" "$group"
    test_api_noauth "Missing agent release" "GET" "/api/v1/public/agent/releases/missing-release.tar.gz" "404|400" "" "$group"
    test_api_noauth "Public announcements" "GET" "/api/v1/public/announcements" "200" "" "$group"
    test_api_noauth "Public stats" "GET" "/api/v1/public/stats" "200" "" "$group"
    test_api_json_value_noauth "Register config captcha disabled by default" "GET" "/api/v1/public/register-config" "200" '.data.captchaEnabled' "false" "" "$group"
    test_api_noauth "System config" "GET" "/api/v1/public/system-config" "200" "" "$group"
    test_api_noauth "Available system images" "GET" "/api/v1/public/system-images/available" "200" "" "$group"

    # -- Swagger docs --
    test_api_noauth "Swagger docs" "GET" "/swagger/index.html" "200" "" "$group"
}
