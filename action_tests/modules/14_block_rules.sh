#!/bin/bash
# Module 14: Firewall & Block Rules
# Dependencies: 09_providers (PROVIDER_ID)

run_module_14() {
    report_add_section "14 - Block Rules"
    local group="block_rules"

    if [[ -z "$PROVIDER_ID" ]]; then
        chain_break "$group" "No provider"
        return 1
    fi

    # -- List --
    test_api "Block rule list" "GET" "/api/v1/admin/block-rules?page=1&pageSize=10" "200" "" "$group"

    # -- Create IP-based rule --
    local br1; br1=$(test_api "Create IP block rule" "POST" "/api/v1/admin/block-rules" "200" \
        '{"name":"CI IP Block","category":"ip","strings":["192.168.100.0/24"],"enabled":true}' "$group")
    local br1_id; br1_id=$(echo "$br1" | jq -r '.data.id // .data.ID // empty' 2>/dev/null)

    # -- Create port-based rule --
    local br2; br2=$(test_api "Create port block rule" "POST" "/api/v1/admin/block-rules" "200" \
        '{"name":"CI Port Block","category":"port","strings":["8080"],"enabled":true}' "$group")
    local br2_id; br2_id=$(echo "$br2" | jq -r '.data.id // .data.ID // empty' 2>/dev/null)

    # -- Create with missing fields --
    test_api "Create rule (no name)" "POST" "/api/v1/admin/block-rules" "400" \
        '{"type":"ip","source":"10.0.0.0/8"}' "$group"

    # -- Get rule detail --
    if [[ -n "$br1_id" ]]; then
        test_api "Get rule detail" "GET" "/api/v1/admin/block-rules/${br1_id}" "200" "" "$group"
    fi

    # -- Edit rule --
    if [[ -n "$br1_id" ]]; then
        test_api "Edit block rule" "PUT" "/api/v1/admin/block-rules/${br1_id}" "200" \
            '{"name":"CI IP Block Updated"}' "$group"
    fi

    # -- Three-tier application --
    # Global apply
    if [[ -n "$br1_id" ]]; then
        test_api "Apply rule globally" "POST" "/api/v1/admin/block-rules/apply" "200" \
            "{\"rule_ids\":[${br1_id}],\"scope\":\"global\"}" "$group"
    fi

    # Provider apply
    if [[ -n "$br2_id" ]]; then
        test_api "Apply rule to provider" "POST" "/api/v1/admin/block-rules/apply" "200" \
            "{\"rule_ids\":[${br2_id}],\"scope\":\"provider\",\"target_ids\":[${PROVIDER_ID}]}" "$group"
    fi

    # -- Applications list --
    test_api "List applications" "GET" "/api/v1/admin/block-rules/applications?page=1&pageSize=10" "200" "" "$group"

    # -- Provider block status --
    test_api "Provider block status" "GET" "/api/v1/admin/providers/${PROVIDER_ID}/block-status" "200" "" "$group"

    # -- Agent-enabled providers --
    test_api "Agent-enabled providers" "GET" "/api/v1/admin/block-rules/agent-providers" "200" "" "$group"

    # -- Remove application --
    local app_ids; app_ids=$(curl -s --max-time 30 -H "Authorization: Bearer ${ADMIN_TOKEN}" \
        "${SERVER_URL}/api/v1/admin/block-rules/applications?page=1&pageSize=50" 2>/dev/null | \
        jq -c '[.data.list[]?.id // .data.list[]?.ID] | map(select(. != null))' 2>/dev/null)
    if [[ -n "$app_ids" && "$app_ids" != "[]" && "$app_ids" != "null" ]]; then
        test_api "Remove rule application" "POST" "/api/v1/admin/block-rules/remove" "200" \
            "{\"application_ids\":${app_ids}}" "$group"
    fi

    # -- Delete rules --
    if [[ -n "$br1_id" ]]; then
        test_api "Delete IP rule" "DELETE" "/api/v1/admin/block-rules/${br1_id}" "200" "" "$group"
    fi
    if [[ -n "$br2_id" ]]; then
        test_api "Delete port rule" "DELETE" "/api/v1/admin/block-rules/${br2_id}" "200" "" "$group"
    fi

    # -- Delete nonexistent rule --
    test_api "Delete nonexistent rule" "DELETE" "/api/v1/admin/block-rules/99999" "404|500" "" "$group"
}
