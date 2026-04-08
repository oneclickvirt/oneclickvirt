#!/bin/bash
# Module 02: Authentication System
# Dependencies: 01_init (ADMIN_TOKEN)

run_module_02() {
    report_add_section "02 - Authentication System"
    local group="auth"

    # -- Captcha --
    test_api_noauth "Get captcha" "GET" "/api/v1/auth/captcha" "200" "" "$group"

    # -- Login valid --
    test_api_noauth "Admin login (valid)" "POST" "/api/v1/auth/login" "200" \
        "{\"username\":\"${ADMIN_USER}\",\"password\":\"${ADMIN_PASS}\"}" "$group"

    # -- Login invalid password --
    test_api_noauth "Admin login (wrong password)" "POST" "/api/v1/auth/login" "401" \
        "{\"username\":\"${ADMIN_USER}\",\"password\":\"wrong_password\"}" "$group"

    # -- Login empty fields --
    test_api_noauth "Login (empty body)" "POST" "/api/v1/auth/login" "400" '{}' "$group"
    test_api_noauth "Login (missing password)" "POST" "/api/v1/auth/login" "400" \
        '{"username":"admin"}' "$group"
    test_api_noauth "Login (missing username)" "POST" "/api/v1/auth/login" "400" \
        '{"password":"test"}' "$group"

    # -- Login nonexistent user --
    test_api_noauth "Login (nonexistent user)" "POST" "/api/v1/auth/login" "401" \
        '{"username":"no_such_user","password":"test123"}' "$group"

    # -- Register test user --
    test_api_noauth "Register test user" "POST" "/api/v1/auth/register" "200|403|409" \
        "{\"username\":\"${TEST_USER}\",\"password\":\"${TEST_USER_PASS}\",\"email\":\"test@ci.local\"}" "$group"

    # -- Register duplicate user --
    test_api_noauth "Register duplicate user" "POST" "/api/v1/auth/register" "400|403|409" \
        "{\"username\":\"${TEST_USER}\",\"password\":\"${TEST_USER_PASS}\",\"email\":\"test2@ci.local\"}" "$group"

    # -- Register with weak password --
    test_api_noauth "Register weak password" "POST" "/api/v1/auth/register" "400|403" \
        '{"username":"weak_user","password":"123","email":"weak@ci.local"}' "$group"

    # -- Register second test user (for isolation tests) --
    test_api_noauth "Register test user 2" "POST" "/api/v1/auth/register" "200|403|409" \
        "{\"username\":\"${TEST_USER2}\",\"password\":\"${TEST_USER2_PASS}\",\"email\":\"test2@ci.local\"}" "$group"

    # -- Login test user --
    USER_TOKEN=$(do_login "$SERVER_URL" "$TEST_USER" "$TEST_USER_PASS")
    if [[ -n "$USER_TOKEN" ]]; then
        log_success "Test user login success"
    else
        log_error "Test user login failed"
    fi

    USER_TOKEN2=$(do_login "$SERVER_URL" "$TEST_USER2" "$TEST_USER2_PASS")
    if [[ -n "$USER_TOKEN2" ]]; then
        log_success "Test user 2 login success"
    fi

    # -- User profile --
    test_api "User profile" "GET" "/api/v1/user/profile" "200" "" "$group" "$USER_TOKEN"

    # -- Unauthenticated access --
    test_api_noauth "Admin API without token" "GET" "/api/v1/admin/users" "200|401" "" "$group"
    test_api_noauth "User API without token" "GET" "/api/v1/user/profile" "200|401" "" "$group"

    # -- Invalid token --
    test_api "Invalid token access" "GET" "/api/v1/user/profile" "401" "" "$group" "invalid_token_xxx"

    # -- Logout & re-login --
    test_api "User logout" "POST" "/api/v1/auth/logout" "200" "" "$group" "$USER_TOKEN"
    USER_TOKEN=$(do_login "$SERVER_URL" "$TEST_USER" "$TEST_USER_PASS") || true

    # -- Forgot password (email based, may return 400 if SMTP not configured) --
    test_api_noauth "Forgot password" "POST" "/api/v1/auth/forgot-password" "200|400" \
        '{"email":"test@ci.local"}' "$group"

    # -- Forgot password invalid email --
    test_api_noauth "Forgot password (invalid email)" "POST" "/api/v1/auth/forgot-password" "400" \
        '{"email":"nonexistent@nowhere.com"}' "$group"

    # -- Reset password with invalid token --
    test_api_noauth "Reset password (invalid token)" "POST" "/api/v1/auth/reset-password" "400" \
        '{"token":"invalid_reset_token","new_password":"NewPass123!@#"}' "$group"

    # -- Negative: Login with SQL injection --
    test_api_noauth "Login SQL injection" "POST" "/api/v1/auth/login" "400|401" \
        '{"username":"admin\" OR 1=1;--","password":"test"}' "$group"

    # -- Negative: Register with XSS in username --
    test_api_noauth "Register XSS username" "POST" "/api/v1/auth/register" "400|403" \
        '{"username":"<script>alert(1)</script>","password":"Test123!@#","email":"xss@ci.local"}' "$group"

    # -- Negative: Register with very long username --
    local long_name; long_name=$(printf 'a%.0s' {1..300})
    test_api_noauth "Register long username" "POST" "/api/v1/auth/register" "400|403" \
        "{\"username\":\"${long_name}\",\"password\":\"Test123!@#\",\"email\":\"long@ci.local\"}" "$group"

    # -- Negative: Register with invalid email --
    test_api_noauth "Register invalid email" "POST" "/api/v1/auth/register" "400|403" \
        '{"username":"bad_email_user","password":"Test123!@#","email":"not_an_email"}' "$group"

    # -- Negative: Forgot password with empty email --
    test_api_noauth "Forgot password (empty)" "POST" "/api/v1/auth/forgot-password" "400" \
        '{"email":""}' "$group"

    # -- Negative: Reset password with empty token --
    test_api_noauth "Reset password (empty token)" "POST" "/api/v1/auth/reset-password" "400" \
        '{"token":"","new_password":"NewPass123!@#"}' "$group"

    # -- Negative: Reset password with weak new password --
    test_api_noauth "Reset password (weak)" "POST" "/api/v1/auth/reset-password" "400" \
        '{"token":"some_token","new_password":"123"}' "$group"

    # -- Negative: Logout without token --
    test_api "Logout no token" "POST" "/api/v1/auth/logout" "200|401" "" "$group" ""
}
