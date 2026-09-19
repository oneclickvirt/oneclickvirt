#!/usr/bin/env bash
# Read-only checks. A responding daemon alone does not mean instances can boot.
verify_lxc_runtime() {
    local runtime="$1" profile roots pool nics nic network state nic_list profile_filter network_filter
    command -v "$runtime" >/dev/null || return 1
    command -v jq >/dev/null || return 1
    "$runtime" info >/dev/null || return 1
    profile=$("$runtime" query /1.0/profiles/default) || return 1
    profile_filter='
        if length != 1 or (.[0] | type) != "object"
        then error("expected one profile object") else .[0] end |
        if has("metadata") then
            if .type == "sync" and (.metadata | type) == "object"
               and ((has("status_code") | not) or .status_code == 200)
            then .metadata else error("invalid profile envelope") end
        elif .type == "error" or .type == "async" or .type == "sync"
        then error("invalid profile response") else . end |
        if (.devices | type) == "object"
           and all(.devices[]; type == "object" and all(.[]; type == "string"))
        then . else error("invalid profile devices") end
    '
    profile=$(jq -cs "$profile_filter" <<< "$profile") || return 1
    roots=$(jq -er '[.devices[]? | select(.type == "disk" and .path == "/")] | length' <<< "$profile") || return 1
    [ "$roots" -eq 1 ] || { echo "default profile must have one root disk" >&2; return 1; }
    pool=$(jq -er '.devices[] | select(.type == "disk" and .path == "/") | .pool | select(length > 0)' <<< "$profile") || return 1
    "$runtime" storage show "$pool" >/dev/null || return 1
    nics=$(jq -er '[.devices[]? | select(.type == "nic")] | length' <<< "$profile") || return 1
    [ "$nics" -gt 0 ] || { echo "default profile has no NIC" >&2; return 1; }
    nic_list=$(jq -c '.devices[] | select(.type == "nic")' <<< "$profile") || return 1
    while IFS= read -r nic; do
        network=$(jq -r '.network // empty' <<< "$nic") || return 1
        if [ -n "$network" ]; then
            # `none` is a valid explicit profile choice. It deliberately
            # disables networking and therefore has no /1.0/networks/none
            # resource to query.
            [ "$network" = "none" ] && continue
            state=$("$runtime" query "/1.0/networks/$network") || return 1
            network_filter='
                if length != 1 or (.[0] | type) != "object"
                then error("expected one network object") else .[0] end |
                if has("metadata") then
                    if .type == "sync" and (.metadata | type) == "object"
                       and ((has("status_code") | not) or .status_code == 200)
                    then .metadata else error("invalid network envelope") end
                elif .type == "error" or .type == "async" or .type == "sync"
                then error("invalid network response") else . end |
                if (.type | type) == "string" then . else error("invalid network type") end |
                if .type == "bridge" and .managed == true then
                    if (.config | type) == "object" and all(.config[]; type == "string")
                    then . else error("invalid managed bridge configuration") end
                else . end
            '
            state=$(jq -cs "$network_filter" <<< "$state") || return 1
            jq -e 'type == "object" and (.type | type == "string")' <<< "$state" >/dev/null || return 1
            if jq -e '.type == "bridge" and .managed == true' <<< "$state" >/dev/null; then
                if ! jq -e '.config["ipv4.address"] != null and .config["ipv4.address"] != "" and .config["ipv4.address"] != "none" and .config["ipv4.dhcp"] != "false"' <<< "$state" >/dev/null; then
                    echo "managed bridge $network has no IPv4/DHCP for instance tests" >&2
                    return 1
                fi
                ip link show dev "$network" >/dev/null || return 1
            fi
        else
            network=$(jq -r '.parent // empty' <<< "$nic") || return 1
            [ -z "$network" ] || ip link show dev "$network" >/dev/null || return 1
        fi
    done <<< "$nic_list"
}
