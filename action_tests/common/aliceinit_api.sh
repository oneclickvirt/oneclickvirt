#!/bin/bash
# AliceInit (Ephemera) API 封装库
# 提供 AliceInit 云平台 API 的 Shell 函数封装

# ============================================================
# 配置
# ============================================================
ALICE_API_BASE="${ALICE_API_BASE:-https://app.alice.ws/cli/v1}"
ALICE_CLIENT_ID="${ALICE_CLIENT_ID:-}"
ALICE_CLIENT_SECRET="${ALICE_CLIENT_SECRET:-}"

# 生成 Bearer Token
alice_get_token() {
    echo "${ALICE_CLIENT_ID}:${ALICE_CLIENT_SECRET}"
}

# ============================================================
# 通用请求函数
# ============================================================
alice_request() {
    local method="$1"
    local endpoint="$2"
    local data="$3"
    local token
    token=$(alice_get_token)

    local args=(-s -w "\n%{http_code}" --max-time 120)
    args+=(-H "Authorization: Bearer ${token}")
    args+=(-H "Content-Type: application/json")
    args+=(-X "${method}")

    if [[ -n "$data" ]]; then
        args+=(-d "$data")
    fi

    local response
    response=$(curl "${args[@]}" "${ALICE_API_BASE}${endpoint}")
    echo "$response"
}

# 解析响应体和状态码
alice_parse_response() {
    local response="$1"
    local http_code
    http_code=$(echo "$response" | tail -1)
    local body
    body=$(echo "$response" | sed '$d')
    echo "${body}"
    return 0
}

alice_parse_http_code() {
    local response="$1"
    echo "$response" | tail -1
}

# ============================================================
# 账户 API
# ============================================================
alice_get_profile() {
    alice_request "GET" "/account/profile"
}

alice_get_ssh_keys() {
    alice_request "GET" "/account/ssh-keys"
}

# ============================================================
# EVO 实例 API
# ============================================================
alice_get_permissions() {
    alice_request "GET" "/evo/permissions"
}

alice_list_instances() {
    alice_request "GET" "/evo/instances"
}

alice_get_instance() {
    local instance_id="$1"
    alice_request "GET" "/evo/instances/${instance_id}"
}

alice_create_instance() {
    local package_id="$1"
    local os_id="$2"
    local time_hours="${3:-1}"
    local boot_script_b64="${4:-}"

    local data="{\"package_id\": ${package_id}, \"os_id\": ${os_id}, \"time\": ${time_hours}"
    if [[ -n "$boot_script_b64" ]]; then
        data="${data}, \"boot_script\": \"${boot_script_b64}\""
    fi
    data="${data}}"

    alice_request "POST" "/evo/instances" "$data"
}

alice_delete_instance() {
    local instance_id="$1"
    alice_request "DELETE" "/evo/instances/${instance_id}"
}

alice_instance_power() {
    local instance_id="$1"
    local action="$2"  # boot, shutdown, restart, poweroff
    alice_request "POST" "/evo/instances/${instance_id}/power" "{\"action\": \"${action}\"}"
}

alice_rebuild_instance() {
    local instance_id="$1"
    local os_id="$2"
    local boot_script_b64="${3:-}"
    local ssh_key_id="${4:-}"

    local data="{\"os_id\": ${os_id}"
    if [[ -n "$boot_script_b64" ]]; then
        data="${data}, \"boot_script\": \"${boot_script_b64}\""
    fi
    if [[ -n "$ssh_key_id" ]]; then
        data="${data}, \"ssh_key_id\": ${ssh_key_id}"
    fi
    data="${data}}"

    alice_request "POST" "/evo/instances/${instance_id}/rebuild" "$data"
}

alice_renew_instance() {
    local instance_id="$1"
    local time_hours="$2"
    alice_request "POST" "/evo/instances/${instance_id}/renewals" "{\"time\": ${time_hours}}"
}

alice_exec_command() {
    local instance_id="$1"
    local command_b64="$2"
    alice_request "POST" "/evo/instances/${instance_id}/exec" "{\"command\": \"${command_b64}\"}"
}

alice_get_exec_result() {
    local instance_id="$1"
    local command_uid="$2"
    alice_request "GET" "/evo/instances/${instance_id}/exec/${command_uid}"
}

alice_get_packages() {
    alice_request "GET" "/evo/packages"
}

alice_get_os_list() {
    alice_request "GET" "/evo/os"
}

# ============================================================
# 高级封装函数
# ============================================================

# 等待实例就绪（状态为 running）
# 参数: instance_id, max_wait_seconds(默认600), check_interval(默认15)
alice_wait_instance_ready() {
    local instance_id="$1"
    local max_wait="${2:-600}"
    local interval="${3:-15}"
    local elapsed=0

    log_info "等待实例 ${instance_id} 就绪（最长等待 ${max_wait} 秒）..."
    while [[ $elapsed -lt $max_wait ]]; do
        local response
        response=$(alice_get_instance "$instance_id")
        local body
        body=$(alice_parse_response "$response")
        local status
        status=$(echo "$body" | jq -r '.data.status // empty' 2>/dev/null)

        if [[ "$status" == "running" || "$status" == "active" ]]; then
            log_success "实例 ${instance_id} 已就绪 (status: ${status})"
            echo "$body"
            return 0
        fi

        log_info "实例状态: ${status:-unknown}，继续等待... (${elapsed}/${max_wait}s)"
        sleep "$interval"
        elapsed=$((elapsed + interval))
    done

    log_error "等待实例就绪超时 (${max_wait}s)"
    return 1
}

# 在实例上执行命令并等待结果
# 参数: instance_id, command_string, max_wait_seconds(默认300)
alice_exec_and_wait() {
    local instance_id="$1"
    local command="$2"
    local max_wait="${3:-300}"
    local interval="${4:-10}"

    local command_b64
    command_b64=$(echo -n "$command" | base64)

    log_info "在实例 ${instance_id} 上执行命令..."
    local exec_response
    exec_response=$(alice_exec_command "$instance_id" "$command_b64")
    local exec_body
    exec_body=$(alice_parse_response "$exec_response")
    local command_uid
    command_uid=$(echo "$exec_body" | jq -r '.data.command_uid // empty' 2>/dev/null)

    if [[ -z "$command_uid" ]]; then
        log_error "执行命令失败: $(echo "$exec_body" | jq -r '.message // empty' 2>/dev/null)"
        echo "$exec_body"
        return 1
    fi

    log_info "命令已提交, UID: ${command_uid}，等待执行结果..."
    local elapsed=0
    while [[ $elapsed -lt $max_wait ]]; do
        sleep "$interval"
        elapsed=$((elapsed + interval))

        local result_response
        result_response=$(alice_get_exec_result "$instance_id" "$command_uid")
        local result_body
        result_body=$(alice_parse_response "$result_response")
        local status
        status=$(echo "$result_body" | jq -r '.data.status // empty' 2>/dev/null)

        if [[ "$status" == "fetched" || "$status" == "completed" ]]; then
            local result
            result=$(echo "$result_body" | jq -r '.data.result // empty' 2>/dev/null)
            if [[ "$result" == "success" ]]; then
                log_success "命令执行成功"
            else
                log_warning "命令执行完成，结果: ${result}"
            fi
            echo "$result_body"
            return 0
        fi

        log_info "命令状态: ${status:-pending}，继续等待... (${elapsed}/${max_wait}s)"
    done

    log_error "等待命令执行结果超时 (${max_wait}s)"
    return 1
}

# 创建实例并等待就绪
# 参数: package_id, os_id, time_hours, boot_script_b64, max_wait_seconds
alice_create_and_wait() {
    local package_id="$1"
    local os_id="$2"
    local time_hours="${3:-1}"
    local boot_script_b64="${4:-}"
    local max_wait="${5:-600}"

    log_info "创建实例 (package: ${package_id}, os: ${os_id}, time: ${time_hours}h)..."
    local response
    response=$(alice_create_instance "$package_id" "$os_id" "$time_hours" "$boot_script_b64")
    local body
    body=$(alice_parse_response "$response")
    local http_code
    http_code=$(alice_parse_http_code "$response")
    local code
    code=$(echo "$body" | jq -r '.code // empty' 2>/dev/null)

    if [[ "$code" != "200" ]]; then
        log_error "创建实例失败 (code: ${code}): $(echo "$body" | jq -r '.message // empty' 2>/dev/null)"
        echo "$body"
        return 1
    fi

    local instance_id
    instance_id=$(echo "$body" | jq -r '.data.id // .data.instance_id // empty' 2>/dev/null)
    if [[ -z "$instance_id" ]]; then
        log_error "无法获取实例 ID"
        echo "$body"
        return 1
    fi

    log_success "实例创建请求成功, ID: ${instance_id}"
    alice_wait_instance_ready "$instance_id" "$max_wait"
}

# 删除实例并确认
alice_delete_and_confirm() {
    local instance_id="$1"
    local max_wait="${2:-120}"

    log_info "删除实例 ${instance_id}..."
    local response
    response=$(alice_delete_instance "$instance_id")
    local body
    body=$(alice_parse_response "$response")
    local code
    code=$(echo "$body" | jq -r '.code // empty' 2>/dev/null)

    if [[ "$code" != "200" ]]; then
        log_warning "删除实例返回非200: $(echo "$body" | jq -r '.message // empty' 2>/dev/null)"
    fi

    # 等待实例真正删除
    local elapsed=0
    while [[ $elapsed -lt $max_wait ]]; do
        sleep 10
        elapsed=$((elapsed + 10))
        local check
        check=$(alice_get_instance "$instance_id")
        local check_body
        check_body=$(alice_parse_response "$check")
        local check_code
        check_code=$(echo "$check_body" | jq -r '.code // empty' 2>/dev/null)
        if [[ "$check_code" == "404" ]] || [[ "$check_code" != "200" ]]; then
            log_success "实例 ${instance_id} 已删除"
            return 0
        fi
        local status
        status=$(echo "$check_body" | jq -r '.data.status // empty' 2>/dev/null)
        if [[ "$status" == "deleted" || "$status" == "terminated" ]]; then
            log_success "实例 ${instance_id} 已删除"
            return 0
        fi
        log_info "等待实例删除... (${elapsed}/${max_wait}s)"
    done

    log_warning "等待实例删除超时，可能仍在删除中"
    return 0
}
