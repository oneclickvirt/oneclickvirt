#!/bin/bash
# Module 27: Advanced Configuration & Tasks
# Dependencies: 01_init (ADMIN_TOKEN), 09_providers (PROVIDER_ID)

run_module_27() {
    report_add_section "27 - Config & Tasks Advanced"
    local group="config_advanced"

    if [[ -z "$ADMIN_TOKEN" ]]; then
        chain_break "$group" "No admin token"
        return 1
    fi

    # ---- Unified config via /api/v1/config ----
    local config_resp; config_resp=$(test_api "Get unified config (alt)" "GET" "/api/v1/config" "200" \
        "" "$group" "$ADMIN_TOKEN")
    test_api "Update unified config (alt)" "PUT" "/api/v1/config" "200" \
        '{"site_name":"Test Site Updated"}' "$group" "$ADMIN_TOKEN"

    # ---- Admin config ----
    test_api "Get admin config" "GET" "/api/v1/admin/config" "200" "" "$group" "$ADMIN_TOKEN"

    # ---- Configuration tasks ----
    test_api "List config tasks" "GET" "/api/v1/admin/configuration-tasks" "200" "" "$group" "$ADMIN_TOKEN"
    test_api "Get nonexistent task" "GET" "/api/v1/admin/configuration-tasks/99999" "404" "" "$group" "$ADMIN_TOKEN"

    # ---- Auto-configure provider (creates a task) ----
    if [[ -n "$PROVIDER_ID" ]]; then
        local auto_resp; auto_resp=$(test_api "Auto-configure provider" "POST" \
            "/api/v1/admin/providers/auto-configure" "200|201|400" \
            '{"providerId":'"$PROVIDER_ID"'}' "$group" "$ADMIN_TOKEN")
        local cfg_task; cfg_task=$(echo "$auto_resp" | grep -o '"task_id":[0-9]*\|"task_id":"[^"]*"' | head -1 | grep -o '[0-9]*$\|[^"]*"$' | tr -d '"')

        if [[ -n "$cfg_task" ]]; then
            test_api "Get config task detail" "GET" "/api/v1/admin/configuration-tasks/${cfg_task}" "200" \
                "" "$group" "$ADMIN_TOKEN"
            test_api "Cancel config task" "POST" "/api/v1/admin/configuration-tasks/${cfg_task}/cancel" "200|400" \
                "" "$group" "$ADMIN_TOKEN"
        fi

        # ---- Export provider configs ----
        test_api "Export provider configs" "POST" "/api/v1/admin/providers/export-configs" "200" \
            '{"provider_ids":['"$PROVIDER_ID"']}' "$group" "$ADMIN_TOKEN"
        test_api "Export empty provider list" "POST" "/api/v1/admin/providers/export-configs" "200" \
            '{"provider_ids":[]}' "$group" "$ADMIN_TOKEN"

        # ---- Hardware report ----
        test_api "Save hardware report" "POST" "/api/v1/admin/providers/${PROVIDER_ID}/hardware-report" "200|400|409|500" \
            '{"pasteUrl":"https://paste.spiritlhl.net/#/show/ENn4E.txt"}' "$group" "$ADMIN_TOKEN"
        test_api "Save hardware report (invalid URL)" "POST" "/api/v1/admin/providers/${PROVIDER_ID}/hardware-report" "400" \
            '{"pasteUrl":"https://example.com/badreport.txt"}' "$group" "$ADMIN_TOKEN"
        test_api "Get hardware report (admin)" "GET" "/api/v1/admin/providers/${PROVIDER_ID}/hardware-report" "200|404" \
            "" "$group" "$ADMIN_TOKEN"
        test_api "Get hardware report (public)" "GET" "/api/v1/public/providers/${PROVIDER_ID}/hardware-report" "200|404" \
            "" "$group" ""
        test_api "Delete hardware report" "DELETE" "/api/v1/admin/providers/${PROVIDER_ID}/hardware-report" "200" \
            "" "$group" "$ADMIN_TOKEN"
        test_api "Get deleted hw report" "GET" "/api/v1/admin/providers/${PROVIDER_ID}/hardware-report" "404|200" \
            "" "$group" "$ADMIN_TOKEN"
    fi

    # ---- Task management ----
    test_api "List all admin tasks" "GET" "/api/v1/admin/tasks?page=1&pageSize=10" "200" "" "$group" "$ADMIN_TOKEN"
    test_api "Task statistics" "GET" "/api/v1/admin/tasks/stats" "200" "" "$group" "$ADMIN_TOKEN"
    test_api "Task overall stats" "GET" "/api/v1/admin/tasks/overall-stats" "200" "" "$group" "$ADMIN_TOKEN"

    # ---- Force stop nonexistent task ----
    test_api "Force stop bad task" "POST" "/api/v1/admin/tasks/force-stop" "400|404" \
        '{"task_id":"nonexistent"}' "$group" "$ADMIN_TOKEN"

    # ---- Admin group info ----
    test_api "Get group info" "GET" "/api/v1/admin/group-info" "200" "" "$group" "$ADMIN_TOKEN"
    test_api "Update group info" "PUT" "/api/v1/admin/group-info" "200" \
        '{"name":"Test Group","description":"Updated via test"}' "$group" "$ADMIN_TOKEN"

    # ---- User quota (nonexistent user) ----
    test_api "User quota (nonexistent)" "GET" "/api/v1/admin/quota/users/99999" "200|400|404" \
        "" "$group" "$ADMIN_TOKEN"

    # ---- Dashboard stats ----
    test_api "Dashboard stats" "GET" "/api/v1/dashboard/stats" "200" "" "$group" "$ADMIN_TOKEN"

    # ---- Register config (public) ----
    test_api_json_value_noauth "Register config captcha disabled by default" "GET" "/api/v1/public/register-config" "200" '.data.captchaEnabled' "false" "" "$group"
    test_api "System config (public)" "GET" "/api/v1/public/system-config" "200" "" "$group" ""

    # ---- Version and build info ----
    test_api "Version info" "GET" "/api/v1/public/version" "200" "" "$group" ""
    test_api "Build info" "GET" "/api/v1/public/build-info" "200" "" "$group" ""

    # ---- Performance history ----
    test_api "Performance history" "GET" "/api/v1/admin/performance/history" "200" "" "$group" "$ADMIN_TOKEN"

    # ---- Provider traffic history ----
    if [[ -n "$PROVIDER_ID" ]]; then
        test_api "Provider traffic history" "GET" "/api/v1/admin/providers/${PROVIDER_ID}/traffic/history" "200" \
            "" "$group" "$ADMIN_TOKEN"
    fi

    # ---- Negative: Export with nonexistent providers ----
    test_api "Export nonexistent providers" "POST" "/api/v1/admin/providers/export-configs" "200|400" \
        '{"provider_ids":[99999]}' "$group" "$ADMIN_TOKEN"

    # ---- Negative: Hardware report for nonexistent provider ----
    test_api "Hardware report nonexistent" "POST" "/api/v1/admin/providers/99999/hardware-report" "400|404" \
        '{"pasteUrl":"https://paste.spiritlhl.net/#/show/test.txt"}' "$group" "$ADMIN_TOKEN"

    # ---- Negative: Get tasks with invalid pagination ----
    test_api "Tasks (page=0)" "GET" "/api/v1/admin/tasks?page=0&pageSize=10" "200|400" "" "$group" "$ADMIN_TOKEN"
    test_api "Tasks (huge pageSize)" "GET" "/api/v1/admin/tasks?page=1&pageSize=99999" "200|400" "" "$group" "$ADMIN_TOKEN"

    # ---- Negative: Group info update with empty body ----
    test_api "Group info (empty body)" "PUT" "/api/v1/admin/group-info" "200|400" \
        '{}' "$group" "$ADMIN_TOKEN"

    # ---- Negative: User cannot access admin tasks ----
    if [[ -n "$USER_TOKEN" ]]; then
        test_api "User -> admin tasks (403)" "GET" "/api/v1/admin/tasks?page=1&pageSize=10" "401|403" "" "$group" "$USER_TOKEN"
        test_api "User -> force stop (403)" "POST" "/api/v1/admin/tasks/force-stop" "401|403" \
            '{"task_id":"test"}' "$group" "$USER_TOKEN"
        test_api "User -> export configs (403)" "POST" "/api/v1/admin/providers/export-configs" "401|403" \
            '{"provider_ids":[1]}' "$group" "$USER_TOKEN"
    fi

    # ---- Negative: Performance history with nonexistent provider ----
    test_api "Traffic history nonexistent" "GET" "/api/v1/admin/providers/99999/traffic/history" "200|400|404" \
        "" "$group" "$ADMIN_TOKEN"
}
