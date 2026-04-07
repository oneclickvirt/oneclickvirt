#!/bin/bash
# Module 25: Error Handling & Boundary Testing
# Dependencies: 01_init (ADMIN_TOKEN), 02_auth (USER_TOKEN)

run_module_25() {
    report_add_section "25 - Error Handling"
    local group="error_handling"

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
        '{"username":"<script>alert(1)</script>","password":"Test123!@#"}' "$group" ""
    test_api "XSS in announcement" "POST" "/api/v1/admin/announcements" "200|400" \
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

    # ---- Zero IDs (GORM may return 200 or 500 for id=0) ----
    test_api "Zero user ID" "DELETE" "/api/v1/admin/users/0" "200|400|404" "" "$group" "$ADMIN_TOKEN"
    test_api "Zero instance ID" "DELETE" "/api/v1/admin/instances/0" "200|400|404|500" "" "$group" "$ADMIN_TOKEN"

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
}
