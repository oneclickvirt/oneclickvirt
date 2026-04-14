#!/bin/bash
# Module 06: Announcement Management
# Dependencies: 01_init (ADMIN_TOKEN)

run_module_06() {
    report_add_section "06 - Announcements"
    local group="announcements"

    # -- List --
    test_api "Announcement list" "GET" "/api/v1/admin/announcements?page=1&pageSize=10" "200" "" "$group"

    # -- Create --
    local a1; a1=$(test_api "Create announcement (info)" "POST" "/api/v1/admin/announcements" "200" \
        '{"title":"CI Test Info","content":"Integration test announcement","type":"homepage","priority":5,"isSticky":false}' "$group")
    local aid1; aid1=$(echo "$a1" | jq -r '.data.id // .data.ID // empty' 2>/dev/null)

    local a2; a2=$(test_api "Create announcement (warning)" "POST" "/api/v1/admin/announcements" "200" \
        '{"title":"CI Test Warning","content":"Warning announcement","type":"topbar","priority":8,"isSticky":false}' "$group")
    local aid2; aid2=$(echo "$a2" | jq -r '.data.id // .data.ID // empty' 2>/dev/null)

    test_api "Create announcement (inactive)" "POST" "/api/v1/admin/announcements" "200" \
        '{"title":"CI Inactive","content":"Inactive announcement","type":"homepage","priority":1,"isSticky":false}' "$group"

    # -- Create with missing title --
    test_api "Create announcement (no title)" "POST" "/api/v1/admin/announcements" "400" \
        '{"content":"no title"}' "$group"

    # -- Edit --
    if [[ -n "$aid1" ]]; then
        test_api "Edit announcement" "PUT" "/api/v1/admin/announcements/${aid1}" "200" \
            '{"title":"CI Test Updated","content":"Updated content"}' "$group"
    fi

    # -- Batch status update --
    if [[ -n "$aid1" && -n "$aid2" ]]; then
        test_api "Batch deactivate" "PUT" "/api/v1/admin/announcements/batch-status" "200" \
            "{\"ids\":[${aid1},${aid2}],\"status\":\"inactive\"}" "$group"
        test_api "Batch activate" "PUT" "/api/v1/admin/announcements/batch-status" "200" \
            "{\"ids\":[${aid1},${aid2}],\"status\":\"active\"}" "$group"
    fi

    # -- Public access --
    test_api_noauth "Public announcements" "GET" "/api/v1/public/announcements" "200" "" "$group"

    # -- Delete single --
    if [[ -n "$aid1" ]]; then
        test_api "Delete announcement" "DELETE" "/api/v1/admin/announcements/${aid1}" "200" "" "$group"
    fi

    # -- Delete nonexistent (GORM returns 200 for nonexistent) --
    test_api "Delete nonexistent" "DELETE" "/api/v1/admin/announcements/99999" "200|404" "" "$group"

    # -- Batch delete --
    local all_aids; all_aids=$(curl -s --max-time 30 -H "Authorization: Bearer ${ADMIN_TOKEN}" \
        "${SERVER_URL}/api/v1/admin/announcements?page=1&pageSize=50" 2>/dev/null | \
        jq -c '[.data.list[]?.id // .data.list[]?.ID] | map(select(. != null))' 2>/dev/null)
    if [[ -n "$all_aids" && "$all_aids" != "[]" && "$all_aids" != "null" ]]; then
        test_api "Batch delete announcements" "POST" "/api/v1/admin/announcements/batch-delete" "200" \
            "{\"ids\":${all_aids}}" "$group"
    fi

    # -- Negative: Edit nonexistent announcement --
    test_api "Edit nonexistent announcement" "PUT" "/api/v1/admin/announcements/99999" "400|404" \
        '{"title":"Ghost"}' "$group"

    # -- Negative: Create with empty content --
    test_api "Create (empty content)" "POST" "/api/v1/admin/announcements" "400" \
        '{"title":"EmptyContent","content":""}' "$group"

    # -- Negative: Batch status with empty ids --
    test_api "Batch status (empty ids)" "PUT" "/api/v1/admin/announcements/batch-status" "400" \
        '{"ids":[],"status":"active"}' "$group"

    # -- Negative: Batch delete empty --
    test_api "Batch delete (empty ids)" "POST" "/api/v1/admin/announcements/batch-delete" "400" \
        '{"ids":[]}' "$group"

    # -- Negative: User cannot manage announcements --
    if [[ -n "$USER_TOKEN" ]]; then
        test_api "User -> create announcement (403)" "POST" "/api/v1/admin/announcements" "401|403" \
            '{"title":"Hack","content":"test"}' "$group" "$USER_TOKEN"
    fi
}
