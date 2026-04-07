#!/bin/bash
# Module 01: System Initialization & Health Checks
# Dependencies: none

run_module_01() {
    report_add_section "01 - System Initialization & Health"
    local group="init"

    # -- Health endpoints --
    test_api_noauth "Health check /health" "GET" "/health" "200" "" "$group"
    test_api_noauth "Health check /api/health" "GET" "/api/health" "200" "" "$group"
    test_api_noauth "Ping /api/ping" "GET" "/api/ping" "200" "" "$group"

    # -- Init status --
    local init_r; init_r=$(test_api_noauth "Init status check" "GET" "/api/v1/public/init/check" "200" "" "$group")
    local need_init; need_init=$(echo "$init_r" | jq -r '.data.needInit // "true"' 2>/dev/null)

    # -- Recommended DB type --
    test_api_noauth "Recommended DB type" "GET" "/api/v1/public/recommended-db-type" "200" "" "$group"

    # -- Test DB connection with invalid params --
    test_api_noauth "Test DB connection (invalid)" "POST" "/api/v1/public/test-db-connection" "400|7" \
        '{"db_type":"mysql","db_host":"invalid","db_port":9999,"db_name":"x","db_user":"x","db_password":"x"}' "$group"

    # -- System initialization (only if not already initialized by orchestrator) --
    if [[ "$need_init" == "true" ]]; then
        test_api_noauth "System init (SQLite)" "POST" "/api/v1/public/init" "200" \
            "{\"admin\":{\"username\":\"${ADMIN_USER}\",\"password\":\"${ADMIN_PASS}\",\"email\":\"${ADMIN_USER}@test.local\"},\"database\":{\"type\":\"sqlite\"}}" "$group"
        sleep 2
    else
        log_info "System already initialized, skipping init test"
    fi

    # -- Duplicate init should fail --
    test_api_noauth "Duplicate init (should fail)" "POST" "/api/v1/public/init" "400|7" \
        "{\"admin\":{\"username\":\"${ADMIN_USER}\",\"password\":\"${ADMIN_PASS}\",\"email\":\"${ADMIN_USER}@test.local\"},\"database\":{\"type\":\"sqlite\"}}" "$group"

    # -- Init with missing fields --
    test_api_noauth "Init missing username" "POST" "/api/v1/public/init" "400|7" \
        '{"admin":{"password":"test"},"database":{"type":"sqlite"}}' "$group"

    # -- Admin login --
    ADMIN_TOKEN=$(admin_login "$SERVER_URL" "$ADMIN_USER" "$ADMIN_PASS") || { chain_break "$group" "Admin login failed"; return 1; }

    # -- Public endpoints --
    test_api_noauth "Server version" "GET" "/api/v1/public/version" "200" "" "$group"
    test_api_noauth "Build info" "GET" "/api/v1/public/build-info" "200" "" "$group"
    test_api_noauth "Public announcements" "GET" "/api/v1/public/announcements" "200" "" "$group"
    test_api_noauth "Public stats" "GET" "/api/v1/public/stats" "200" "" "$group"
    test_api_noauth "Register config" "GET" "/api/v1/public/register-config" "200" "" "$group"
    test_api_noauth "System config" "GET" "/api/v1/public/system-config" "200" "" "$group"
    test_api_noauth "Available system images" "GET" "/api/v1/public/system-images/available" "200" "" "$group"

    # -- Swagger docs --
    test_api_noauth "Swagger docs" "GET" "/swagger/index.html" "200" "" "$group"
}
