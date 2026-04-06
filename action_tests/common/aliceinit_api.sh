#!/bin/bash
# AliceInit (Ephemera) API 封装库

ALICE_API_BASE="${ALICE_API_BASE:-https://app.alice.ws/cli/v1}"
ALICE_CLIENT_ID="${ALICE_CLIENT_ID:-}"
ALICE_CLIENT_SECRET="${ALICE_CLIENT_SECRET:-}"

alice_get_token() { echo "${ALICE_CLIENT_ID}:${ALICE_CLIENT_SECRET}"; }

alice_request() {
    local method="$1" endpoint="$2" data="${3:-}"
    local token; token=$(alice_get_token)
    local args=(-s -w "\n%{http_code}" --max-time 120
        -H "Authorization: Bearer ${token}"
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

# 等待实例就绪
alice_wait_instance_ready() {
    local id="$1" max="${2:-600}" interval="${3:-15}" elapsed=0
    log_info "等待实例 ${id} 就绪(最长${max}s)..."
    while [[ $elapsed -lt $max ]]; do
        local resp; resp=$(alice_get_instance "$id")
        local body; body=$(alice_parse_body "$resp")
        local st; st=$(echo "$body" | jq -r '.data.status // empty' 2>/dev/null)
        if [[ "$st" == "running" || "$st" == "active" ]]; then
            log_success "实例 ${id} 已就绪"
            echo "$body"; return 0
        fi
        sleep "$interval"; elapsed=$((elapsed + interval))
    done
    log_error "等待实例就绪超时"; return 1
}

# 执行命令并等待结果
alice_exec_and_wait() {
    local id="$1" command="$2" max="${3:-300}" interval="${4:-10}"
    local b64; b64=$(echo -n "$command" | base64)
    local resp; resp=$(alice_exec_command "$id" "$b64")
    local body; body=$(alice_parse_body "$resp")
    local uid; uid=$(echo "$body" | jq -r '.data.command_uid // empty' 2>/dev/null)
    [[ -z "$uid" ]] && { log_error "执行命令失败"; return 1; }
    log_info "命令已提交(UID:${uid})，等待结果..."
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
    log_error "等待命令执行超时"; return 1
}

# 创建并等待
alice_create_and_wait() {
    local pkg="$1" os="$2" hours="${3:-1}" boot="${4:-}" max="${5:-600}"
    local resp; resp=$(alice_create_instance "$pkg" "$os" "$hours" "$boot")
    local body; body=$(alice_parse_body "$resp")
    local code; code=$(echo "$body" | jq -r '.code // empty' 2>/dev/null)
    [[ "$code" != "200" ]] && { log_error "创建实例失败: $(echo "$body" | jq -r '.message // empty')"; echo "$body"; return 1; }
    local id; id=$(echo "$body" | jq -r '.data.id // .data.instance_id // empty' 2>/dev/null)
    [[ -z "$id" ]] && { log_error "无法获取实例ID"; return 1; }
    log_success "实例创建请求成功, ID: ${id}"
    alice_wait_instance_ready "$id" "$max"
}

alice_delete_and_confirm() {
    local id="$1"
    log_info "删除实例 ${id}..."
    alice_delete_instance "$id"
}
