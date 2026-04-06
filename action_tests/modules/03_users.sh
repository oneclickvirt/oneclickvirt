#!/bin/bash
# Module 03: User Management (Super Admin)
# Dependencies: 01_init (ADMIN_TOKEN)

run_module_03() {
    report_add_section "03 - User Management"
    local group="users"

    # -- List users --
    test_api "User list" "GET" "/api/v1/admin/users?page=1&pageSize=10" "200" "" "$group"

    # -- Create user --
    local cu; cu=$(test_api "Create user" "POST" "/api/v1/admin/users" "200" \
        '{"username":"admin_created_user","password":"AdminCreated123!@#","email":"ac@ci.local","level":1}' "$group")
    local created_uid; created_uid=$(echo "$cu" | jq -r '.data.id // .data.ID // empty' 2>/dev/null)

    # -- Create duplicate username --
    test_api "Create duplicate user" "POST" "/api/v1/admin/users" "400" \
        '{"username":"admin_created_user","password":"Test123!@#","email":"ac2@ci.local"}' "$group"

    # -- Create with missing fields --
    test_api "Create user (no username)" "POST" "/api/v1/admin/users" "400" \
        '{"password":"Test123!@#"}' "$group"

    # -- Create multiple for batch tests --
    test_api "Create batch user 1" "POST" "/api/v1/admin/users" "200" \
        '{"username":"batch_user_1","password":"Batch123!@#","email":"b1@ci.local","level":1}' "$group"
    test_api "Create batch user 2" "POST" "/api/v1/admin/users" "200" \
        '{"username":"batch_user_2","password":"Batch123!@#","email":"b2@ci.local","level":1}' "$group"

    # -- Edit user --
    if [[ -n "$created_uid" ]]; then
        test_api "Edit user" "PUT" "/api/v1/admin/users/${created_uid}" "200" \
            '{"email":"updated@ci.local"}' "$group"

        # -- Change level --
        test_api "Update user level" "PUT" "/api/v1/admin/users/${created_uid}/level" "200" \
            '{"level":2}' "$group"

        # -- Change level to invalid --
        test_api "Update user level (invalid)" "PUT" "/api/v1/admin/users/${created_uid}/level" "400" \
            '{"level":-1}' "$group"

        # -- Reset password --
        test_api "Reset user password" "PUT" "/api/v1/admin/users/${created_uid}/reset-password" "200" \
            '{"password":"NewPassword123!@#"}' "$group"

        # -- Disable user --
        test_api "Disable user" "PUT" "/api/v1/admin/users/${created_uid}/status" "200" \
            '{"status":"disabled"}' "$group"

        # -- Verify disabled user cannot login --
        test_api_noauth "Disabled user login" "POST" "/api/v1/auth/login" "401" \
            '{"username":"admin_created_user","password":"NewPassword123!@#"}' "$group"

        # -- Enable user --
        test_api "Enable user" "PUT" "/api/v1/admin/users/${created_uid}/status" "200" \
            '{"status":"active"}' "$group"
    fi

    # -- Batch level update --
    local uid_list; uid_list=$(curl -s --max-time 30 -H "Authorization: Bearer ${ADMIN_TOKEN}" \
        "${SERVER_URL}/api/v1/admin/users?page=1&pageSize=50" 2>/dev/null | \
        jq -r '[.data.list[]? | select(.username | test("batch_user")) | .id // .ID] | join(",")' 2>/dev/null)
    if [[ -n "$uid_list" ]]; then
        test_api "Batch update level" "PUT" "/api/v1/admin/users/batch-level" "200" \
            "{\"user_ids\":[${uid_list}],\"level\":2}" "$group"
        test_api "Batch update status (disable)" "PUT" "/api/v1/admin/users/batch-status" "200" \
            "{\"user_ids\":[${uid_list}],\"status\":\"disabled\"}" "$group"
        test_api "Batch update status (enable)" "PUT" "/api/v1/admin/users/batch-status" "200" \
            "{\"user_ids\":[${uid_list}],\"status\":\"active\"}" "$group"
    fi

    # -- Create normal admin user --
    test_api "Create normal admin" "POST" "/api/v1/admin/users" "200" \
        "{\"username\":\"${NORMAL_ADMIN_USER}\",\"password\":\"${NORMAL_ADMIN_PASS}\",\"email\":\"nadmin@ci.local\",\"user_type\":\"normal_admin\"}" "$group"
    NORMAL_ADMIN_TOKEN=$(do_login "$SERVER_URL" "$NORMAL_ADMIN_USER" "$NORMAL_ADMIN_PASS") || true

    # -- User expiry --
    if [[ -n "$created_uid" ]]; then
        local exp_date; exp_date=$(date -u -d "+30 days" '+%Y-%m-%dT%H:%M:%SZ' 2>/dev/null || date -u -v+30d '+%Y-%m-%dT%H:%M:%SZ')
        test_api "Set user expiry" "POST" "/api/v1/admin/users/set-expiry" "200" \
            "{\"user_id\":${created_uid},\"expires_at\":\"${exp_date}\"}" "$group"
    fi

    # -- Admin login as user --
    local test_uid; test_uid=$(curl -s --max-time 30 -H "Authorization: Bearer ${ADMIN_TOKEN}" \
        "${SERVER_URL}/api/v1/admin/users?page=1&pageSize=50" 2>/dev/null | \
        jq -r "[.data.list[]? | select(.username==\"${TEST_USER}\")][0].id // empty" 2>/dev/null)
    if [[ -n "$test_uid" ]]; then
        test_api "Login as user" "POST" "/api/v1/admin/users/${test_uid}/login-as" "200" "" "$group"
    fi

    # -- Quota query --
    if [[ -n "$test_uid" ]]; then
        test_api "User quota info" "GET" "/api/v1/admin/quota/users/${test_uid}" "200" "" "$group"
    fi

    # -- Instance type permissions --
    test_api "Get instance type permissions" "GET" "/api/v1/admin/instance-type-permissions" "200" "" "$group"
    test_api "Update instance type permissions" "PUT" "/api/v1/admin/instance-type-permissions" "200" \
        '{"container":true,"vm":true}' "$group"

    # -- Delete user --
    if [[ -n "$created_uid" ]]; then
        test_api "Delete user" "DELETE" "/api/v1/admin/users/${created_uid}" "200" "" "$group"
    fi

    # -- Delete nonexistent user --
    test_api "Delete nonexistent user" "DELETE" "/api/v1/admin/users/99999" "404" "" "$group"

    # -- Batch delete --
    if [[ -n "$uid_list" ]]; then
        test_api "Batch delete users" "POST" "/api/v1/admin/users/batch-delete" "200" \
            "{\"user_ids\":[${uid_list}]}" "$group"
    fi
}
