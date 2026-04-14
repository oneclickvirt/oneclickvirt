#!/bin/bash
# Module 04: Invitation Code Management
# Dependencies: 01_init (ADMIN_TOKEN)

run_module_04() {
    report_add_section "04 - Invitation Codes"
    local group="invite_codes"

    # -- List --
    test_api "Invite code list" "GET" "/api/v1/admin/invite-codes?page=1&pageSize=10" "200" "" "$group"

    # -- Create custom code --
    local ic; ic=$(test_api "Create invite code" "POST" "/api/v1/admin/invite-codes" "200" \
        '{"code":"CITESTCODE","count":1,"maxUses":5}' "$group")
    local ic_id; ic_id=$(echo "$ic" | jq -r '.data.id // .data.ID // empty' 2>/dev/null)

    # -- Create duplicate code --
    test_api "Create duplicate code" "POST" "/api/v1/admin/invite-codes" "400|409" \
        '{"code":"CITESTCODE","count":1,"maxUses":5}' "$group"

    # -- Batch generate --
    test_api "Batch generate codes" "POST" "/api/v1/admin/invite-codes/generate" "200" \
        '{"count":5,"maxUses":3,"level":1}' "$group"

    # -- Generate with invalid count --
    test_api "Generate codes (invalid count)" "POST" "/api/v1/admin/invite-codes/generate" "400" \
        '{"count":0,"maxUses":3}' "$group"

    # -- Export --
    test_api "Export invite codes" "GET" "/api/v1/admin/invite-codes/export" "200" "" "$group"

    # -- Register with invite code (may return 403 if public registration is disabled in system defaults) --
    test_api_noauth "Register with invite code" "POST" "/api/v1/auth/register" "200|403" \
        '{"username":"invite_test_user","password":"InviteTest123!@#","email":"inv@ci.local","inviteCode":"CITESTCODE"}' "$group"

    # -- Register with invalid invite code --
    test_api_noauth "Register with invalid code" "POST" "/api/v1/auth/register" "400|403" \
        '{"username":"invite_fail_user","password":"InviteTest123!@#","email":"invf@ci.local","inviteCode":"NONEXISTENT_CODE"}' "$group"

    # -- Delete single --
    if [[ -n "$ic_id" ]]; then
        test_api "Delete invite code" "DELETE" "/api/v1/admin/invite-codes/${ic_id}" "200" "" "$group"
    fi

    # -- Delete nonexistent (GORM returns 200 for nonexistent) --
    test_api "Delete nonexistent code" "DELETE" "/api/v1/admin/invite-codes/99999" "200|404" "" "$group"

    # -- Batch delete remaining --
    local batch_ids; batch_ids=$(curl -s --max-time 30 -H "Authorization: Bearer ${ADMIN_TOKEN}" \
        "${SERVER_URL}/api/v1/admin/invite-codes?page=1&pageSize=50" 2>/dev/null | \
        jq -c '[.data.list[]?.id // .data.list[]?.ID] | map(select(. != null))' 2>/dev/null)
    if [[ -n "$batch_ids" && "$batch_ids" != "[]" && "$batch_ids" != "null" ]]; then
        test_api "Batch delete codes" "POST" "/api/v1/admin/invite-codes/batch-delete" "200" \
            "{\"ids\":${batch_ids}}" "$group"
    fi

    # -- Cleanup invite user --
    local inv_uid; inv_uid=$(curl -s --max-time 30 -H "Authorization: Bearer ${ADMIN_TOKEN}" \
        "${SERVER_URL}/api/v1/admin/users?page=1&pageSize=50" 2>/dev/null | \
        jq -r '[.data.list[]? | select(.username=="invite_test_user")][0].id // empty' 2>/dev/null)
    [[ -n "$inv_uid" ]] && curl -s --max-time 30 -H "Authorization: Bearer ${ADMIN_TOKEN}" \
        -X DELETE "${SERVER_URL}/api/v1/admin/users/${inv_uid}" 2>/dev/null || true

    # -- Negative: Generate with negative count --
    test_api "Generate codes (negative count)" "POST" "/api/v1/admin/invite-codes/generate" "400" \
        '{"count":-1,"maxUses":3}' "$group"

    # -- Negative: Generate with excessive count --
    test_api "Generate codes (excessive)" "POST" "/api/v1/admin/invite-codes/generate" "400" \
        '{"count":10000,"maxUses":3}' "$group"

    # -- Negative: Create with empty code (server auto-generates code when empty) --
    test_api "Create empty code" "POST" "/api/v1/admin/invite-codes" "200|400" \
        '{"code":"","count":1,"maxUses":5}' "$group"

    # -- Negative: Batch delete empty --
    test_api "Batch delete empty" "POST" "/api/v1/admin/invite-codes/batch-delete" "400" \
        '{"ids":[]}' "$group"

    # -- Negative: User cannot manage invite codes --
    if [[ -n "$USER_TOKEN" ]]; then
        test_api "User -> invite list (403)" "GET" "/api/v1/admin/invite-codes?page=1&pageSize=10" "401|403" "" "$group" "$USER_TOKEN"
        test_api "User -> create code (403)" "POST" "/api/v1/admin/invite-codes" "401|403" \
            '{"code":"USERCODE","count":1}' "$group" "$USER_TOKEN"
    fi
}
