#!/bin/bash
# Module 25: Error Handling & Boundary Testing
# Dependencies: 01_init (ADMIN_TOKEN), 02_auth (USER_TOKEN)

run_module_25() {
    report_add_section "25 - Error Handling"
    local group="error_handling"
    local xss_username="<i>${RANDOM}</i>"

    if [[ -z "$ADMIN_TOKEN" ]]; then
        chain_break "$group" "No admin token"
        return 1
    fi

    # ---- SQL injection attempts ----
    test_api "SQL injection login" "POST" "/api/v1/auth/login" "400|401" \
        '{"username":"admin'\'' OR 1=1 --","password":"test"}' "$group" ""
    test_api "SQL injection register" "POST" "/api/v1/auth/register" "400|403" \
        '{"username":"test; DROP TABLE users;--","password":"Test123!@#"}' "$group" ""
    test_api "SQL injection provider name" "GET" "/api/v1/admin/providers/check-name?name=test%27%20OR%201%3D1%20--" "200|400" \
        "" "$group" "$ADMIN_TOKEN"

    # ---- XSS attempts (may return 403 if registration disabled) ----
    test_api "XSS in username" "POST" "/api/v1/auth/register" "400|403" \
        "{\"username\":\"${xss_username}\",\"password\":\"Test123!@#\"}" "$group" ""
    test_api "XSS in announcement" "POST" "/api/v1/admin/announcements" "400" \
        '{"title":"<img onerror=alert(1) src=x>","content":"test","type":"notice","status":"active"}' \
        "$group" "$ADMIN_TOKEN"

    # ---- Oversized payloads (may return 403 if registration disabled) ----
    local big_str; big_str=$(python3 -c "print('A'*100000)" 2>/dev/null || printf '%0.sA' {1..10000})
    test_api "Oversized username" "POST" "/api/v1/auth/register" "400|403|413" \
        '{"username":"'"$big_str"'","password":"Test123!@#"}' "$group" ""

    # ---- Invalid JSON ----
    # Use raw curl for malformed JSON
    local resp_code
    resp_code=$(curl -s -o /dev/null -w "%{http_code}" -X POST "${SERVER_URL}/api/v1/auth/login" \
        -H "Content-Type: application/json" -d 'NOT_JSON{{{')
    TOTAL_TESTS=$((TOTAL_TESTS + 1))
    if [[ "$resp_code" == "400" || "$resp_code" == "422" ]]; then
        PASSED_TESTS=$((PASSED_TESTS + 1))
        log_success "Invalid JSON rejected ($resp_code)"
        _add_result_json "Invalid JSON body" "POST" "/api/v1/auth/login" "PASS" "400|422" "$resp_code" "" "$group"
    else
        FAILED_TESTS=$((FAILED_TESTS + 1))
        log_error "Invalid JSON not rejected (got $resp_code)"
        _add_result_json "Invalid JSON body" "POST" "/api/v1/auth/login" "FAIL" "400|422" "$resp_code" "" "$group"
    fi

    # ---- Negative IDs ----
    test_api "Negative user ID" "GET" "/api/v1/admin/users/-1" "400|404" "" "$group" "$ADMIN_TOKEN"
    test_api "Negative instance ID" "GET" "/api/v1/admin/instances/-1" "400|404" "" "$group" "$ADMIN_TOKEN"
    test_api "Negative provider ID" "GET" "/api/v1/admin/providers/-1" "400|404" "" "$group" "$ADMIN_TOKEN"

    # ---- Zero IDs (GORM may return 200 or 400 for id=0) ----
    test_api "Zero user ID" "DELETE" "/api/v1/admin/users/0" "200|400|404" "" "$group" "$ADMIN_TOKEN"
    test_api "Zero instance ID" "DELETE" "/api/v1/admin/instances/0" "200|400|404" "" "$group" "$ADMIN_TOKEN"

    # ---- Non-numeric IDs ----
    test_api "String user ID" "GET" "/api/v1/admin/users/abc" "400|404" "" "$group" "$ADMIN_TOKEN"
    test_api "String instance ID" "GET" "/api/v1/admin/instances/abc" "400|404" "" "$group" "$ADMIN_TOKEN"

    # ---- Pagination boundaries ----
    test_api "Page 0" "GET" "/api/v1/admin/users?page=0&pageSize=10" "200|400" "" "$group" "$ADMIN_TOKEN"
    test_api "Negative page" "GET" "/api/v1/admin/users?page=-1&pageSize=10" "200|400" "" "$group" "$ADMIN_TOKEN"
    test_api "Huge pageSize" "GET" "/api/v1/admin/users?page=1&pageSize=999999" "200|400" "" "$group" "$ADMIN_TOKEN"
    test_api "Page very large" "GET" "/api/v1/admin/users?page=999999&pageSize=10" "200" "" "$group" "$ADMIN_TOKEN"

    # ---- Empty required fields ----
    test_api "Empty login" "POST" "/api/v1/auth/login" "400" '{}' "$group" ""
    test_api "Null password" "POST" "/api/v1/auth/login" "400" '{"username":"admin","password":null}' "$group" ""
    test_api "Empty provider" "POST" "/api/v1/admin/providers" "400" '{}' "$group" "$ADMIN_TOKEN"
    test_api "Empty instance" "POST" "/api/v1/admin/instances" "400" '{}' "$group" "$ADMIN_TOKEN"
    test_api "Empty block rule" "POST" "/api/v1/admin/block-rules" "400" '{}' "$group" "$ADMIN_TOKEN"

    # ---- Method not allowed ----
    test_api "PATCH on login" "PATCH" "/api/v1/auth/login" "404|405" '{}' "$group" ""
    test_api "DELETE on login" "DELETE" "/api/v1/auth/login" "404|405" '' "$group" ""

    # ---- Nonexistent routes ----
    test_api "Nonexistent route" "GET" "/api/v1/nonexistent/route" "404" "" "$group" "$ADMIN_TOKEN"
    test_api "Nonexistent admin route" "GET" "/api/v1/admin/nonexistent" "404" "" "$group" "$ADMIN_TOKEN"

    # ---- Existence and permission checks for interactive/file endpoints (no real browser/WebSocket) ----
    test_api "Admin SSH websocket without upgrade" "GET" "/api/v1/admin/instances/99999/ssh" "400|404|426|500" "" "$group" "$ADMIN_TOKEN"
    test_api "Admin instance SFTP list missing" "GET" "/api/v1/admin/instances/99999/sftp/list?path=/" "400|404|500" "" "$group" "$ADMIN_TOKEN"
    test_api "Admin instance SFTP download missing" "GET" "/api/v1/admin/instances/99999/sftp/download?path=/tmp/missing" "400|404|500" "" "$group" "$ADMIN_TOKEN"
    test_api "Admin instance SFTP upload missing" "POST" "/api/v1/admin/instances/99999/sftp/upload" "400|404|500" '{}' "$group" "$ADMIN_TOKEN"
    test_api "Admin instance SFTP upload status missing" "GET" "/api/v1/admin/instances/99999/sftp/upload/status?uploadId=missing" "400|404|500" "" "$group" "$ADMIN_TOKEN"
    test_api "Admin instance SFTP upload abort missing" "POST" "/api/v1/admin/instances/99999/sftp/upload/abort" "400|404|500" '{"uploadId":"missing"}' "$group" "$ADMIN_TOKEN"

    test_api "Admin provider terminal without upgrade" "GET" "/api/v1/admin/providers/99999/terminal" "400|404|426|500" "" "$group" "$ADMIN_TOKEN"
    test_api "Admin provider SFTP list missing" "GET" "/api/v1/admin/providers/99999/sftp/list?path=/" "400|404|500" "" "$group" "$ADMIN_TOKEN"
    test_api "Admin provider SFTP download missing" "GET" "/api/v1/admin/providers/99999/sftp/download?path=/tmp/missing" "400|404|500" "" "$group" "$ADMIN_TOKEN"
    test_api "Admin provider SFTP upload missing" "POST" "/api/v1/admin/providers/99999/sftp/upload" "400|404|500" '{}' "$group" "$ADMIN_TOKEN"
    test_api "Admin provider SFTP upload status missing" "GET" "/api/v1/admin/providers/99999/sftp/upload/status?uploadId=missing" "400|404|500" "" "$group" "$ADMIN_TOKEN"
    test_api "Admin provider SFTP upload abort missing" "POST" "/api/v1/admin/providers/99999/sftp/upload/abort" "400|404|500" '{"uploadId":"missing"}' "$group" "$ADMIN_TOKEN"
    test_api "Admin provider FM list missing" "GET" "/api/v1/admin/providers/99999/fm/list?path=/" "400|404|500" "" "$group" "$ADMIN_TOKEN"
    test_api "Admin provider FM download missing" "GET" "/api/v1/admin/providers/99999/fm/download?path=/tmp/missing" "400|404|500" "" "$group" "$ADMIN_TOKEN"
    test_api "Admin provider FM upload missing" "POST" "/api/v1/admin/providers/99999/fm/upload" "400|404|500" '{}' "$group" "$ADMIN_TOKEN"
    test_api "Admin provider FM delete missing" "DELETE" "/api/v1/admin/providers/99999/fm/file?path=/tmp/missing" "400|404|500" "" "$group" "$ADMIN_TOKEN"
    test_api "Admin provider FM mkdir missing" "POST" "/api/v1/admin/providers/99999/fm/mkdir" "400|404|500" '{"path":"/tmp/missing"}' "$group" "$ADMIN_TOKEN"

    test_api "Admin API token list" "GET" "/api/v1/admin/api-tokens?page=1&pageSize=10" "200" "" "$group" "$ADMIN_TOKEN"
    test_api "Admin API token delete missing" "DELETE" "/api/v1/admin/api-tokens/99999" "200|404" "" "$group" "$ADMIN_TOKEN"
    test_api "Admin API token batch delete empty" "POST" "/api/v1/admin/api-tokens/batch-delete" "400" '{"ids":[]}' "$group" "$ADMIN_TOKEN"

    test_api "Provider API connect empty" "POST" "/api/v1/providers/connect" "400|404|500" '{}' "$group" "$ADMIN_TOKEN"
    test_api "Provider API start missing instance" "POST" "/api/v1/providers/99999/instances/missing/start" "400|404|500" "" "$group" "$ADMIN_TOKEN"
    test_api "Provider API stop missing instance" "POST" "/api/v1/providers/99999/instances/missing/stop" "400|404|500" "" "$group" "$ADMIN_TOKEN"
    test_api "Provider API pull image missing provider" "POST" "/api/v1/providers/99999/images/pull" "400|404|500" '{"image":"missing"}' "$group" "$ADMIN_TOKEN"
    test_api "Provider API delete image missing provider" "DELETE" "/api/v1/providers/99999/images/missing" "400|404|500" "" "$group" "$ADMIN_TOKEN"

    test_api "Agent websocket without upgrade" "GET" "/api/v1/ws/agent" "400|401|404|426" "" "$group" ""
    test_api "Legacy agent websocket without upgrade" "GET" "/api/ws/agent" "400|401|404|426" "" "$group" ""

    if [[ -n "${USER_TOKEN:-}" ]]; then
        test_api "User SSH websocket missing" "GET" "/api/v1/user/instances/99999/ssh" "400|403|404|426|500" "" "$group" "$USER_TOKEN"
        test_api "User exec websocket missing" "GET" "/api/v1/user/instances/99999/exec" "400|403|404|426|500" "" "$group" "$USER_TOKEN"
        test_api "User SFTP list missing" "GET" "/api/v1/user/instances/99999/sftp/list?path=/" "400|403|404|500" "" "$group" "$USER_TOKEN"
        test_api "User SFTP download missing" "GET" "/api/v1/user/instances/99999/sftp/download?path=/tmp/missing" "400|403|404|500" "" "$group" "$USER_TOKEN"
        test_api "User SFTP upload missing" "POST" "/api/v1/user/instances/99999/sftp/upload" "400|403|404|500" '{}' "$group" "$USER_TOKEN"
        test_api "User SFTP upload status missing" "GET" "/api/v1/user/instances/99999/sftp/upload/status?uploadId=missing" "400|403|404|500" "" "$group" "$USER_TOKEN"
        test_api "User SFTP upload abort missing" "POST" "/api/v1/user/instances/99999/sftp/upload/abort" "400|403|404|500" '{"uploadId":"missing"}' "$group" "$USER_TOKEN"
        test_api "User provider GPUs missing" "GET" "/api/v1/user/providers/99999/gpus" "400|403|404|500" "" "$group" "$USER_TOKEN"
        test_api "User cancel task missing" "POST" "/api/v1/user/tasks/99999/cancel" "400|403|404|500" "" "$group" "$USER_TOKEN"
        test_api "User API token list" "GET" "/api/v1/user/api-tokens" "200" "" "$group" "$USER_TOKEN"
        test_api "User API token create boundary" "POST" "/api/v1/user/api-tokens" "200|400" '{"name":"ci-boundary-token","expiresInDays":1}' "$group" "$USER_TOKEN"
        test_api "User API token delete missing" "DELETE" "/api/v1/user/api-tokens/99999" "200|404" "" "$group" "$USER_TOKEN"
    fi

    # ---- Content-Type enforcement ----
    resp_code=$(curl -s -o /dev/null -w "%{http_code}" -X POST "${SERVER_URL}/api/v1/auth/login" \
        -H "Content-Type: text/plain" -d '{"username":"admin","password":"test"}')
    TOTAL_TESTS=$((TOTAL_TESTS + 1))
    if [[ "$resp_code" == "400" || "$resp_code" == "415" || "$resp_code" == "401" ]]; then
        PASSED_TESTS=$((PASSED_TESTS + 1))
        log_success "Wrong content-type handled ($resp_code)"
        _add_result_json "Wrong Content-Type" "POST" "/api/v1/auth/login" "PASS" "400|415|401" "$resp_code" "" "$group"
    else
        FAILED_TESTS=$((FAILED_TESTS + 1))
        log_warning "Wrong content-type returned $resp_code"
        _add_result_json "Wrong Content-Type" "POST" "/api/v1/auth/login" "FAIL" "400|415|401" "$resp_code" "" "$group"
    fi

    # ---- Concurrent duplicate requests ----
    test_api "Rapid duplicate create" "POST" "/api/v1/admin/announcements" "200|201|400|409" \
        '{"title":"Concurrent Test","content":"test","type":"notice","status":"draft"}' "$group" "$ADMIN_TOKEN"

    # ---- Unicode in fields (may return 403 if registration disabled) ----
    test_api "Unicode username" "POST" "/api/v1/auth/register" "200|400|403" \
        '{"username":"用户测试","password":"Test123!@#"}' "$group" ""
    test_api "Emoji in announcement" "POST" "/api/v1/admin/announcements" "200|400" \
        '{"title":"🎉 Test","content":"emoji test","type":"notice","status":"draft"}' "$group" "$ADMIN_TOKEN"

    # ---- Path traversal ----
    test_api "Path traversal" "GET" "/api/v1/admin/../../etc/passwd" "400|404" "" "$group" "$ADMIN_TOKEN"

    # ==============================
    # Instance Share Link Edge Cases
    # ==============================
    log_info "Testing instance share link edge cases..."
    local share_err_group="share_errors"

    # -- Invalid/malformed share token --
    test_api_noauth "Share link (malformed token)" "GET" \
        "/api/v1/public/instance-shares/!@#\$%^&*()" "401|404" "" "$share_err_group"

    # -- Very long share token --
    local long_token; long_token=$(python3 -c "print('A'*1000)" 2>/dev/null || printf '%0.sA' {1..500})
    test_api_noauth "Share link (long token)" "GET" \
        "/api/v1/public/instance-shares/${long_token}" "401|404" "" "$share_err_group"

    # -- Share link with path traversal in token --
    test_api_noauth "Share link (path traversal token)" "GET" \
        "/api/v1/public/instance-shares/../../../etc/passwd" "401|404" "" "$share_err_group"

    # -- Share link with SQL injection in token --
    test_api_noauth "Share link (SQL injection token)" "GET" \
        "/api/v1/public/instance-shares/'%20OR%201=1%20--" "400|401|404" "" "$share_err_group"

    # -- Share link action without body --
    test_api_noauth "Share link action (no body)" "POST" \
        "/api/v1/public/instance-shares/fake_token/action" "400|401|404" "" "$share_err_group"

    # -- Share link reset password without body (should fail) --
    test_api_noauth "Share link reset password (no body)" "PUT" \
        "/api/v1/public/instance-shares/fake_token/reset-password" "400|401|404" "" "$share_err_group"

    # -- Share link images with invalid token --
    test_api_noauth "Share link images (invalid)" "GET" \
        "/api/v1/public/instance-shares/invalid_token/images/filtered" "401|404" "" "$share_err_group"

    # -- Share link ports with invalid token --
    test_api_noauth "Share link ports (invalid)" "GET" \
        "/api/v1/public/instance-shares/invalid_token/ports" "401|404" "" "$share_err_group"

    # -- Share link monitoring with invalid token --
    test_api_noauth "Share link monitoring (invalid)" "GET" \
        "/api/v1/public/instance-shares/invalid_token/monitoring" "401|404" "" "$share_err_group"

    # -- Share link traffic detail with invalid token --
    test_api_noauth "Share link traffic (invalid)" "GET" \
        "/api/v1/public/instance-shares/invalid_token/traffic/detail" "401|404" "" "$share_err_group"

    # -- Share link WebSocket endpoints with invalid token --
    test_api_noauth "Share link SSH ws (invalid)" "GET" \
        "/api/v1/public/instance-shares/invalid_token/ssh" "400|401|404|426" "" "$share_err_group"
    test_api_noauth "Share link exec ws (invalid)" "GET" \
        "/api/v1/public/instance-shares/invalid_token/exec" "400|401|404|426" "" "$share_err_group"

    # -- Share link SFTP endpoints with invalid token --
    test_api_noauth "Share link SFTP list (invalid)" "GET" \
        "/api/v1/public/instance-shares/invalid_token/sftp/list?path=/" "400|401|404" "" "$share_err_group"
    test_api_noauth "Share link SFTP download (invalid)" "GET" \
        "/api/v1/public/instance-shares/invalid_token/sftp/download?path=/tmp/missing" "400|401|404" "" "$share_err_group"
    test_api_noauth "Share link SFTP upload (invalid)" "POST" \
        "/api/v1/public/instance-shares/invalid_token/sftp/upload" "400|401|404" '{}' "$share_err_group"
    test_api_noauth "Share link SFTP upload status (invalid)" "GET" \
        "/api/v1/public/instance-shares/invalid_token/sftp/upload/status?uploadId=missing" "400|401|404" "" "$share_err_group"
    test_api_noauth "Share link SFTP upload abort (invalid)" "POST" \
        "/api/v1/public/instance-shares/invalid_token/sftp/upload/abort" "400|401|404" '{"uploadId":"missing"}' "$share_err_group"

    # -- Negative: User tries to create share link for nonexistent instance --
    if [[ -n "${USER_TOKEN:-}" ]]; then
        test_api "User share link nonexistent instance" "POST" \
            "/api/v1/user/instances/99999/share-links" "400|403|404" \
            '{"expiresInMinutes":30}' "$share_err_group" "$USER_TOKEN"
    fi

    # -- Negative: Admin tries to create share link for nonexistent instance --
    test_api "Admin share link nonexistent instance" "POST" \
        "/api/v1/admin/instances/99999/share-links" "400|404" \
        '{"expiresInMinutes":30}' "$share_err_group" "$ADMIN_TOKEN"
}
