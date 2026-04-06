#!/bin/bash
# 测试公共工具库
# 提供日志、断言、报告生成等通用测试函数

set -euo pipefail

# ============================================================
# 颜色和日志
# ============================================================
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
CYAN='\033[0;36m'
NC='\033[0m'

log_info()    { echo -e "${BLUE}[INFO]${NC} $(date '+%H:%M:%S') $*"; }
log_success() { echo -e "${GREEN}[PASS]${NC} $(date '+%H:%M:%S') $*"; }
log_error()   { echo -e "${RED}[FAIL]${NC} $(date '+%H:%M:%S') $*"; }
log_warning() { echo -e "${YELLOW}[WARN]${NC} $(date '+%H:%M:%S') $*"; }
log_section() { echo -e "\n${CYAN}========== $* ==========${NC}\n"; }
log_skip()    { echo -e "${YELLOW}[SKIP]${NC} $(date '+%H:%M:%S') $*"; }

# ============================================================
# 测试计数器
# ============================================================
TOTAL_TESTS=0
PASSED_TESTS=0
FAILED_TESTS=0
SKIPPED_TESTS=0

# 测试链路中断标志（key=test_group_name, value=reason）
declare -A CHAIN_BROKEN

# 测试报告内容（追加写入）
REPORT_FILE=""
REPORT_CONTENT=""

# ============================================================
# 测试断言函数
# ============================================================

# 测试 API 并验证响应
# 参数: test_name, method, url, expected_http_code, data, group_name
# 可选环境变量: VALIDATE_BODY_FUNC (函数名，接收 body 参数进行额外验证)
test_api() {
    local test_name="$1"
    local method="$2"
    local url="$3"
    local expected_code="$4"
    local data="${5:-}"
    local group_name="${6:-default}"

    TOTAL_TESTS=$((TOTAL_TESTS + 1))

    # 检查是否因链路中断需要跳过
    if [[ -n "${CHAIN_BROKEN[$group_name]:-}" ]]; then
        SKIPPED_TESTS=$((SKIPPED_TESTS + 1))
        log_skip "${test_name} (前置依赖失败: ${CHAIN_BROKEN[$group_name]})"
        report_add_skip "$test_name" "$method" "$url" "前置依赖失败: ${CHAIN_BROKEN[$group_name]}"
        return 1
    fi

    local args=(-s -w "\n%{http_code}" --max-time 60)
    if [[ -n "$ADMIN_TOKEN" ]]; then
        args+=(-H "Authorization: Bearer ${ADMIN_TOKEN}")
    fi
    args+=(-H "Content-Type: application/json")
    args+=(-X "${method}")

    if [[ -n "$data" ]]; then
        args+=(-d "$data")
    fi

    local response
    response=$(curl "${args[@]}" "${SERVER_URL}${url}" 2>&1) || true
    local http_code
    http_code=$(echo "$response" | tail -1)
    local body
    body=$(echo "$response" | sed '$d')

    # 验证 HTTP 状态码
    if [[ "$http_code" != "$expected_code" ]]; then
        FAILED_TESTS=$((FAILED_TESTS + 1))
        log_error "${test_name} - 期望 HTTP ${expected_code}, 实际 HTTP ${http_code}"
        report_add_fail "$test_name" "$method" "$url" "$data" "$expected_code" "$http_code" "$body"
        return 1
    fi

    # 如果有额外的 body 验证函数
    if [[ -n "${VALIDATE_BODY_FUNC:-}" ]]; then
        local validate_result
        validate_result=$("$VALIDATE_BODY_FUNC" "$body" 2>&1)
        if [[ $? -ne 0 ]]; then
            FAILED_TESTS=$((FAILED_TESTS + 1))
            log_error "${test_name} - 响应体验证失败: ${validate_result}"
            report_add_fail "$test_name" "$method" "$url" "$data" "$expected_code" "$http_code" "验证失败: ${validate_result}\n响应: ${body}"
            return 1
        fi
    fi

    PASSED_TESTS=$((PASSED_TESTS + 1))
    log_success "${test_name}"
    report_add_pass "$test_name" "$method" "$url"
    echo "$body"
    return 0
}

# 标记测试链路中断
chain_break() {
    local group_name="$1"
    local reason="$2"
    CHAIN_BROKEN[$group_name]="$reason"
    log_warning "测试链路中断 [${group_name}]: ${reason}"
}

# 带重试的 API 测试
test_api_retry() {
    local test_name="$1"
    local method="$2"
    local url="$3"
    local expected_code="$4"
    local data="${5:-}"
    local max_retries="${6:-3}"
    local retry_interval="${7:-5}"
    local group_name="${8:-default}"

    local attempt=0
    while [[ $attempt -lt $max_retries ]]; do
        attempt=$((attempt + 1))
        if [[ $attempt -gt 1 ]]; then
            log_info "重试 ${test_name} (${attempt}/${max_retries})..."
            sleep "$retry_interval"
        fi

        # 临时不计入统计
        local save_total=$TOTAL_TESTS
        local save_passed=$PASSED_TESTS
        local save_failed=$FAILED_TESTS

        local result
        result=$(test_api "$test_name" "$method" "$url" "$expected_code" "$data" "$group_name" 2>&1) && {
            echo "$result"
            return 0
        }

        # 最后一次重试才计入统计
        if [[ $attempt -lt $max_retries ]]; then
            TOTAL_TESTS=$save_total
            PASSED_TESTS=$save_passed
            FAILED_TESTS=$save_failed
        fi
    done

    return 1
}

# ============================================================
# 服务器等待函数
# ============================================================

# 等待主控服务器启动
wait_server_ready() {
    local server_url="$1"
    local max_wait="${2:-300}"
    local interval="${3:-10}"
    local elapsed=0

    log_info "等待主控服务器就绪: ${server_url} (最长 ${max_wait}s)..."
    while [[ $elapsed -lt $max_wait ]]; do
        local health_response
        health_response=$(curl -s -o /dev/null -w "%{http_code}" --max-time 10 "${server_url}/health" 2>/dev/null) || true
        if [[ "$health_response" == "200" ]]; then
            log_success "主控服务器已就绪"
            return 0
        fi
        sleep "$interval"
        elapsed=$((elapsed + interval))
        log_info "等待中... (${elapsed}/${max_wait}s, status: ${health_response:-timeout})"
    done

    log_error "等待主控服务器就绪超时 (${max_wait}s)"
    return 1
}

# 等待数据库就绪（通过 init/check 端点判断）
wait_db_ready() {
    local server_url="$1"
    local max_wait="${2:-120}"
    local interval="${3:-5}"
    local elapsed=0

    log_info "等待数据库初始化完成..."
    while [[ $elapsed -lt $max_wait ]]; do
        local response
        response=$(curl -s --max-time 10 "${server_url}/api/v1/public/init/check" 2>/dev/null) || true
        local initialized
        initialized=$(echo "$response" | jq -r '.data.initialized // false' 2>/dev/null)
        if [[ "$initialized" == "true" ]]; then
            log_success "数据库已初始化"
            return 0
        fi
        sleep "$interval"
        elapsed=$((elapsed + interval))
    done

    log_warning "数据库初始化检查超时，可能需要手动初始化"
    return 1
}

# 等待任务完成
wait_task_complete() {
    local server_url="$1"
    local task_id="$2"
    local token="$3"
    local max_wait="${4:-600}"
    local interval="${5:-10}"
    local elapsed=0

    log_info "等待任务 ${task_id} 完成（最长 ${max_wait}s）..."
    while [[ $elapsed -lt $max_wait ]]; do
        local response
        response=$(curl -s --max-time 10 \
            -H "Authorization: Bearer ${token}" \
            "${server_url}/api/v1/admin/tasks/${task_id}" 2>/dev/null) || true
        local status
        status=$(echo "$response" | jq -r '.data.status // empty' 2>/dev/null)

        case "$status" in
            completed)
                log_success "任务 ${task_id} 已完成"
                echo "$response"
                return 0
                ;;
            failed)
                local error_msg
                error_msg=$(echo "$response" | jq -r '.data.error_message // empty' 2>/dev/null)
                log_error "任务 ${task_id} 失败: ${error_msg}"
                echo "$response"
                return 1
                ;;
            cancelled|timeout)
                log_error "任务 ${task_id} 状态: ${status}"
                echo "$response"
                return 1
                ;;
        esac

        sleep "$interval"
        elapsed=$((elapsed + interval))
        local progress
        progress=$(echo "$response" | jq -r '.data.progress // 0' 2>/dev/null)
        log_info "任务状态: ${status:-pending}, 进度: ${progress}% (${elapsed}/${max_wait}s)"
    done

    log_error "等待任务完成超时 (${max_wait}s)"
    return 1
}

# ============================================================
# 认证函数
# ============================================================

# 系统初始化
init_system() {
    local server_url="$1"
    local admin_user="${2:-admin}"
    local admin_pass="${3:-Admin123!@#}"
    local db_type="${4:-sqlite}"

    log_info "执行系统初始化..."
    local init_data
    if [[ "$db_type" == "sqlite" ]]; then
        init_data="{\"admin_username\":\"${admin_user}\",\"admin_password\":\"${admin_pass}\",\"db_type\":\"sqlite\"}"
    else
        init_data="{\"admin_username\":\"${admin_user}\",\"admin_password\":\"${admin_pass}\",\"db_type\":\"mysql\",\"db_host\":\"127.0.0.1\",\"db_port\":3306,\"db_name\":\"oneclickvirt\",\"db_user\":\"root\",\"db_password\":\"\"}"
    fi

    local response
    response=$(curl -s -w "\n%{http_code}" --max-time 30 \
        -H "Content-Type: application/json" \
        -X POST \
        -d "$init_data" \
        "${server_url}/api/v1/public/init" 2>/dev/null)
    local body
    body=$(echo "$response" | sed '$d')
    local http_code
    http_code=$(echo "$response" | tail -1)

    if [[ "$http_code" == "200" ]]; then
        log_success "系统初始化成功"
    else
        log_warning "系统初始化返回 HTTP ${http_code}: $(echo "$body" | jq -r '.message // empty' 2>/dev/null)"
    fi
    echo "$body"
}

# 管理员登录
admin_login() {
    local server_url="$1"
    local admin_user="${2:-admin}"
    local admin_pass="${3:-Admin123!@#}"

    log_info "管理员登录..."
    local response
    response=$(curl -s --max-time 30 \
        -H "Content-Type: application/json" \
        -X POST \
        -d "{\"username\":\"${admin_user}\",\"password\":\"${admin_pass}\"}" \
        "${server_url}/api/v1/auth/login" 2>/dev/null)

    local token
    token=$(echo "$response" | jq -r '.data.token // empty' 2>/dev/null)
    if [[ -n "$token" ]]; then
        log_success "管理员登录成功"
        echo "$token"
        return 0
    else
        log_error "管理员登录失败: $(echo "$response" | jq -r '.message // empty' 2>/dev/null)"
        return 1
    fi
}

# 用户注册
register_user() {
    local server_url="$1"
    local username="$2"
    local password="$3"
    local email="${4:-}"

    local data="{\"username\":\"${username}\",\"password\":\"${password}\"}"
    if [[ -n "$email" ]]; then
        data="{\"username\":\"${username}\",\"password\":\"${password}\",\"email\":\"${email}\"}"
    fi

    log_info "注册用户: ${username}..."
    local response
    response=$(curl -s --max-time 30 \
        -H "Content-Type: application/json" \
        -X POST \
        -d "$data" \
        "${server_url}/api/v1/auth/register" 2>/dev/null)

    echo "$response"
}

# 用户登录
user_login() {
    local server_url="$1"
    local username="$2"
    local password="$3"

    local response
    response=$(curl -s --max-time 30 \
        -H "Content-Type: application/json" \
        -X POST \
        -d "{\"username\":\"${username}\",\"password\":\"${password}\"}" \
        "${server_url}/api/v1/auth/login" 2>/dev/null)

    local token
    token=$(echo "$response" | jq -r '.data.token // empty' 2>/dev/null)
    if [[ -n "$token" ]]; then
        echo "$token"
        return 0
    fi
    return 1
}

# ============================================================
# 报告生成函数
# ============================================================

report_init() {
    REPORT_FILE="$1"
    local env_name="$2"
    local timestamp
    timestamp=$(date '+%Y-%m-%d %H:%M:%S')

    cat > "$REPORT_FILE" << EOF
# ${env_name} 测试报告

测试时间: ${timestamp}

## 测试概要

| 指标 | 数值 |
|------|------|
| 总计 | 待更新 |
| 通过 | 待更新 |
| 失败 | 待更新 |
| 跳过 | 待更新 |
| 通过率 | 待更新 |

## 测试详情

EOF
}

report_add_section() {
    local section_name="$1"
    echo "" >> "$REPORT_FILE"
    echo "### ${section_name}" >> "$REPORT_FILE"
    echo "" >> "$REPORT_FILE"
    echo "| 状态 | 测试名 | 方法 | 路由 | 说明 |" >> "$REPORT_FILE"
    echo "|------|--------|------|------|------|" >> "$REPORT_FILE"
}

report_add_pass() {
    local test_name="$1"
    local method="$2"
    local url="$3"
    echo "| PASS | ${test_name} | ${method} | \`${url}\` | - |" >> "$REPORT_FILE"
}

report_add_fail() {
    local test_name="$1"
    local method="$2"
    local url="$3"
    local request_data="$4"
    local expected_code="$5"
    local actual_code="$6"
    local response_body="$7"

    echo "| FAIL | ${test_name} | ${method} | \`${url}\` | 期望 HTTP ${expected_code}, 实际 HTTP ${actual_code} |" >> "$REPORT_FILE"

    # 追加详细错误信息
    echo "" >> "$REPORT_FILE"
    echo "<details>" >> "$REPORT_FILE"
    echo "<summary>${test_name} - 详细错误</summary>" >> "$REPORT_FILE"
    echo "" >> "$REPORT_FILE"
    echo "**请求**: \`${method} ${url}\`" >> "$REPORT_FILE"
    if [[ -n "$request_data" ]]; then
        echo "" >> "$REPORT_FILE"
        echo "**请求数据**:" >> "$REPORT_FILE"
        echo '```json' >> "$REPORT_FILE"
        echo "$request_data" | jq '.' 2>/dev/null || echo "$request_data" >> "$REPORT_FILE"
        echo '```' >> "$REPORT_FILE"
    fi
    echo "" >> "$REPORT_FILE"
    echo "**期望状态码**: ${expected_code}" >> "$REPORT_FILE"
    echo "" >> "$REPORT_FILE"
    echo "**实际状态码**: ${actual_code}" >> "$REPORT_FILE"
    echo "" >> "$REPORT_FILE"
    echo "**实际响应**:" >> "$REPORT_FILE"
    echo '```json' >> "$REPORT_FILE"
    echo "$response_body" | jq '.' 2>/dev/null || echo "$response_body" >> "$REPORT_FILE"
    echo '```' >> "$REPORT_FILE"
    echo "" >> "$REPORT_FILE"
    echo "</details>" >> "$REPORT_FILE"
    echo "" >> "$REPORT_FILE"
}

report_add_skip() {
    local test_name="$1"
    local method="$2"
    local url="$3"
    local reason="$4"
    echo "| SKIP | ${test_name} | ${method} | \`${url}\` | ${reason} |" >> "$REPORT_FILE"
}

report_finalize() {
    local total=$TOTAL_TESTS
    local passed=$PASSED_TESTS
    local failed=$FAILED_TESTS
    local skipped=$SKIPPED_TESTS
    local rate=0
    if [[ $total -gt 0 ]]; then
        rate=$(awk "BEGIN {printf \"%.1f\", ($passed / $total) * 100}")
    fi

    # 更新报告头部的统计信息
    if command -v sed &>/dev/null; then
        sed -i.bak "s/| 总计 | 待更新 |/| 总计 | ${total} |/" "$REPORT_FILE"
        sed -i.bak "s/| 通过 | 待更新 |/| 通过 | ${passed} |/" "$REPORT_FILE"
        sed -i.bak "s/| 失败 | 待更新 |/| 失败 | ${failed} |/" "$REPORT_FILE"
        sed -i.bak "s/| 跳过 | 待更新 |/| 跳过 | ${skipped} |/" "$REPORT_FILE"
        sed -i.bak "s/| 通过率 | 待更新 |/| 通过率 | ${rate}% |/" "$REPORT_FILE"
        rm -f "${REPORT_FILE}.bak"
    fi

    echo "" >> "$REPORT_FILE"
    echo "---" >> "$REPORT_FILE"
    echo "" >> "$REPORT_FILE"
    echo "测试完成时间: $(date '+%Y-%m-%d %H:%M:%S')" >> "$REPORT_FILE"

    log_section "测试结果汇总"
    echo -e "总计: ${total}"
    echo -e "通过: ${GREEN}${passed}${NC}"
    echo -e "失败: ${RED}${failed}${NC}"
    echo -e "跳过: ${YELLOW}${skipped}${NC}"
    echo -e "通过率: ${rate}%"
}

# ============================================================
# 节点管理辅助函数
# ============================================================

# 添加 Provider 节点到主控
add_provider() {
    local server_url="$1"
    local token="$2"
    local provider_name="$3"
    local provider_type="$4"
    local ssh_host="$5"
    local ssh_port="${6:-22}"
    local ssh_user="${7:-root}"
    local ssh_password="$8"

    log_info "添加 Provider 节点: ${provider_name} (${provider_type})..."
    local data="{
        \"name\": \"${provider_name}\",
        \"type\": \"${provider_type}\",
        \"ssh_host\": \"${ssh_host}\",
        \"ssh_port\": ${ssh_port},
        \"ssh_user\": \"${ssh_user}\",
        \"ssh_password\": \"${ssh_password}\",
        \"execution_rule\": \"ssh_only\"
    }"

    local response
    response=$(curl -s --max-time 60 \
        -H "Authorization: Bearer ${token}" \
        -H "Content-Type: application/json" \
        -X POST \
        -d "$data" \
        "${server_url}/api/v1/admin/providers" 2>/dev/null)
    echo "$response"
}

# 删除 Provider 节点
delete_provider() {
    local server_url="$1"
    local token="$2"
    local provider_id="$3"

    log_info "删除 Provider 节点: ${provider_id}..."
    curl -s --max-time 30 \
        -H "Authorization: Bearer ${token}" \
        -X DELETE \
        "${server_url}/api/v1/admin/providers/${provider_id}" 2>/dev/null
}

# ============================================================
# 监控验证函数
# ============================================================

# 循环检查监控数据
verify_monitoring_data() {
    local server_url="$1"
    local token="$2"
    local instance_id="$3"
    local max_wait="${4:-120}"
    local interval="${5:-10}"
    local elapsed=0

    log_info "验证实例 ${instance_id} 监控数据（每 ${interval}s 检查一次，最多 ${max_wait}s）..."
    while [[ $elapsed -lt $max_wait ]]; do
        local response
        response=$(curl -s --max-time 10 \
            -H "Authorization: Bearer ${token}" \
            "${server_url}/api/v1/user/instances/${instance_id}/monitoring/resources" 2>/dev/null)
        local data_count
        data_count=$(echo "$response" | jq '.data | length // 0' 2>/dev/null)

        if [[ "$data_count" -gt 0 ]]; then
            log_success "监控数据已有 ${data_count} 条记录"
            echo "$response"
            return 0
        fi

        sleep "$interval"
        elapsed=$((elapsed + interval))
        log_info "暂无监控数据，继续等待... (${elapsed}/${max_wait}s)"
    done

    log_warning "未能在 ${max_wait}s 内获取到监控数据"
    return 1
}

# ============================================================
# 实例功能测试辅助函数
# ============================================================

# 远程连接测试（通过 SSH 端口检测）
test_instance_ssh_reachable() {
    local host="$1"
    local port="${2:-22}"
    local max_wait="${3:-60}"
    local interval="${4:-5}"
    local elapsed=0

    log_info "测试实例 SSH 可达性 ${host}:${port}..."
    while [[ $elapsed -lt $max_wait ]]; do
        if nc -z -w 3 "$host" "$port" 2>/dev/null; then
            log_success "实例 SSH 端口 ${host}:${port} 可达"
            return 0
        fi
        sleep "$interval"
        elapsed=$((elapsed + interval))
    done

    log_error "实例 SSH 端口不可达 ${host}:${port}"
    return 1
}
