#!/bin/bash
# AliceInit (Ephemera) API wrapper library

ALICE_API_BASE="${ALICE_API_BASE:-}"
ALICEINIT_TOKEN="${ALICEINIT_TOKEN:-}"

alice_request() {
    local method="$1" endpoint="$2" data="${3:-}"
    local args=(-s -w "\n%{http_code}" --max-time 120
        -H "Authorization: Bearer ${ALICEINIT_TOKEN}"
        -H "Content-Type: application/json"
        -X "${method}")
    [[ -n "$data" ]] && args+=(-d "$data")
    curl "${args[@]}" "${ALICE_API_BASE}${endpoint}"
}

alice_parse_body() { echo "$1" | sed '$d'; }
alice_parse_code() { echo "$1" | tail -1; }

alice_get_profile()    { alice_request "GET" "/account/profile"; }
alice_get_packages()   { alice_request "GET" "/evo/packages"; }
alice_get_os_list()    { alice_request "GET" "/evo/os"; }
alice_list_instances()  { alice_request "GET" "/evo/instances"; }
alice_get_instance()    { alice_request "GET" "/evo/instances/$1"; }
alice_delete_instance() { alice_request "DELETE" "/evo/instances/$1"; }

alice_create_instance() {
    local pkg_id="$1" os_id="$2" hours="${3:-1}" boot_b64="${4:-}"
    local data="{\"package_id\":${pkg_id},\"os_id\":${os_id},\"time\":${hours}"
    [[ -n "$boot_b64" ]] && data="${data},\"boot_script\":\"${boot_b64}\""
    data="${data}}"
    alice_request "POST" "/evo/instances" "$data"
}

alice_instance_power() {
    alice_request "POST" "/evo/instances/$1/power" "{\"action\":\"$2\"}"
}

alice_exec_command() {
    alice_request "POST" "/evo/instances/$1/exec" "{\"command\":\"$2\"}"
}

alice_get_exec_result() {
    alice_request "GET" "/evo/instances/$1/exec/$2"
}

alice_wait_instance_ready() {
    local id="$1" max="${2:-600}" interval="${3:-15}" elapsed=0
    log_info "Waiting for instance ${id} (max ${max}s)..."
    while [[ $elapsed -lt $max ]]; do
        local resp; resp=$(alice_get_instance "$id")
        local body; body=$(alice_parse_body "$resp")
        local st; st=$(echo "$body" | jq -r '.data.status // empty' 2>/dev/null)
        if [[ "$st" == "running" || "$st" == "active" ]]; then
            log_success "Instance ${id} ready"
            echo "$body"; return 0
        fi
        sleep "$interval"; elapsed=$((elapsed + interval))
    done
    log_error "Instance readiness timeout"; return 1
}

alice_exec_and_wait() {
    local id="$1" command="$2" max="${3:-300}" interval="${4:-10}"
    local b64; b64=$(echo -n "$command" | base64)
    local resp; resp=$(alice_exec_command "$id" "$b64")
    local body; body=$(alice_parse_body "$resp")
    local uid; uid=$(echo "$body" | jq -r '.data.command_uid // empty' 2>/dev/null)
    [[ -z "$uid" ]] && { log_error "Command submission failed"; return 1; }
    log_info "Command submitted (UID:${uid}), waiting..."
    local elapsed=0
    while [[ $elapsed -lt $max ]]; do
        sleep "$interval"; elapsed=$((elapsed + interval))
        local rr; rr=$(alice_get_exec_result "$id" "$uid")
        local rb; rb=$(alice_parse_body "$rr")
        local st; st=$(echo "$rb" | jq -r '.data.status // empty' 2>/dev/null)
        if [[ "$st" == "fetched" || "$st" == "completed" ]]; then
            echo "$rb"; return 0
        fi
    done
    log_error "Command execution timeout"; return 1
}

alice_create_and_wait() {
    local pkg="$1" os="$2" hours="${3:-1}" boot="${4:-}" max="${5:-600}"
    local resp; resp=$(alice_create_instance "$pkg" "$os" "$hours" "$boot")
    local body; body=$(alice_parse_body "$resp")
    local code; code=$(echo "$body" | jq -r '.code // empty' 2>/dev/null)
    [[ "$code" != "200" ]] && { log_error "Create instance failed: $(echo "$body" | jq -r '.message // empty')"; echo "$body"; return 1; }
    local id; id=$(echo "$body" | jq -r '.data.id // .data.instance_id // empty' 2>/dev/null)
    [[ -z "$id" ]] && { log_error "Cannot get instance ID"; return 1; }
    log_success "Instance creation requested, ID: ${id}"
    alice_wait_instance_ready "$id" "$max"
}

alice_delete_and_confirm() {
    local id="$1"
    log_info "Deleting instance ${id}..."
    alice_delete_instance "$id"
}
