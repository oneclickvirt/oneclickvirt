#!/bin/bash
# 测试框架核心 - 日志、断言、报告、等待函数
set -uo pipefail

RED='\033[0;31m'; GREEN='\033[0;32m'; YELLOW='\033[1;33m'
BLUE='\033[0;34m'; CYAN='\033[0;36m'; NC='\033[0m'

log_info()    { echo -e "${BLUE}[INFO]${NC} $(date '+%H:%M:%S') $*"; }
log_success() { echo -e "${GREEN}[PASS]${NC} $(date '+%H:%M:%S') $*"; }
log_error()   { echo -e "${RED}[FAIL]${NC} $(date '+%H:%M:%S') $*"; }
log_warning() { echo -e "${YELLOW}[WARN]${NC} $(date '+%H:%M:%S') $*"; }
log_section() { echo -e "\n${CYAN}========== $* ==========${NC}\n"; }
log_skip()    { echo -e "${YELLOW}[SKIP]${NC} $(date '+%H:%M:%S') $*"; }

# ── 计数器 ──
TOTAL_TESTS=0; PASSED_TESTS=0; FAILED_TESTS=0; SKIPPED_TESTS=0
declare -A CHAIN_BROKEN
REPORT_FILE=""

# ── 全局变量（模块间共享） ──
SERVER_URL=""
ADMIN_TOKEN=""
ADMIN_USER="${ADMIN_USER:-admin}"
ADMIN_PASS="${ADMIN_PASS:-Admin123!@#}"
NORMAL_ADMIN_TOKEN=""
NORMAL_ADMIN_USER="test_admin"
NORMAL_ADMIN_PASS="TestAdmin123!@#"
USER_TOKEN=""
TEST_USER="test_user_ci"
TEST_USER_PASS="TestUser123!@#"
PROVIDER_ID=""
ENV_TYPE="${ENV_TYPE:-docker}"
NODE_IP=""
NODE_PASSWORD=""

# ── API 测试函数 ──
# 参数: test_name method url expected_code [data] [group] [token_override]
test_api() {
    local name="$1" method="$2" url="$3" expected="$4"
    local data="${5:-}" group="${6:-default}" token="${7:-$ADMIN_TOKEN}"
    TOTAL_TESTS=$((TOTAL_TESTS + 1))
    if [[ -n "${CHAIN_BROKEN[$group]:-}" ]]; then
        SKIPPED_TESTS=$((SKIPPED_TESTS + 1))
        log_skip "${name} (链路中断: ${CHAIN_BROKEN[$group]})"
        report_add_skip "$name" "$method" "$url" "${CHAIN_BROKEN[$group]}"
        return 1
    fi
    local args=(-s -w "\n%{http_code}" --max-time 60
        -H "Content-Type: application/json" -X "${method}")
    [[ -n "$token" ]] && args+=(-H "Authorization: Bearer ${token}")
    [[ -n "$data" ]] && args+=(-d "$data")
    local resp; resp=$(curl "${args[@]}" "${SERVER_URL}${url}" 2>&1) || true
    local code; code=$(echo "$resp" | tail -1)
    local body; body=$(echo "$resp" | sed '$d')
    if [[ "$code" != "$expected" ]]; then
        FAILED_TESTS=$((FAILED_TESTS + 1))
        log_error "${name} - 期望 HTTP ${expected}, 实际 HTTP ${code}"
        report_add_fail "$name" "$method" "$url" "$data" "$expected" "$code" "$body"
        return 1
    fi
    PASSED_TESTS=$((PASSED_TESTS + 1))
    log_success "${name}"
    report_add_pass "$name" "$method" "$url"
    echo "$body"
    return 0
}

# 带重试
test_api_retry() {
    local name="$1" method="$2" url="$3" expected="$4" data="${5:-}" retries="${6:-3}" interval="${7:-5}" group="${8:-default}" token="${9:-$ADMIN_TOKEN}"
    local i=0
    while [[ $i -lt $retries ]]; do
        i=$((i + 1))
        [[ $i -gt 1 ]] && { log_info "重试 ${name} (${i}/${retries})..."; sleep "$interval"; }
        local st=$TOTAL_TESTS sp=$PASSED_TESTS sf=$FAILED_TESTS
        local result; result=$(test_api "$name" "$method" "$url" "$expected" "$data" "$group" "$token" 2>&1) && { echo "$result"; return 0; }
        [[ $i -lt $retries ]] && { TOTAL_TESTS=$st; PASSED_TESTS=$sp; FAILED_TESTS=$sf; }
    done
    return 1
}

# 不带Token
test_api_noauth() {
    local name="$1" method="$2" url="$3" expected="$4" data="${5:-}" group="${6:-default}"
    test_api "$name" "$method" "$url" "$expected" "$data" "$group" ""
}

chain_break() { CHAIN_BROKEN[$1]="$2"; log_warning "链路中断 [${1}]: ${2}"; }

# ── 等待函数 ──
wait_server_ready() {
    local url="$1" max="${2:-300}" interval="${3:-10}" elapsed=0
    log_info "等待服务器就绪: ${url}"
    while [[ $elapsed -lt $max ]]; do
        local r; r=$(curl -s -o /dev/null -w "%{http_code}" --max-time 10 "${url}/health" 2>/dev/null) || true
        [[ "$r" == "200" ]] && { log_success "服务器已就绪"; return 0; }
        sleep "$interval"; elapsed=$((elapsed + interval))
    done
    log_error "服务器就绪超时(${max}s)"; return 1
}

wait_db_ready() {
    local url="$1" max="${2:-120}" interval="${3:-5}" elapsed=0
    while [[ $elapsed -lt $max ]]; do
        local r; r=$(curl -s --max-time 10 "${url}/api/v1/public/init/check" 2>/dev/null) || true
        local init; init=$(echo "$r" | jq -r '.data.initialized // false' 2>/dev/null)
        [[ "$init" == "true" ]] && { log_success "数据库已就绪"; return 0; }
        sleep "$interval"; elapsed=$((elapsed + interval))
    done
    return 1
}

wait_task_complete() {
    local url="$1" task_id="$2" token="$3" max="${4:-600}" interval="${5:-10}" elapsed=0
    log_info "等待任务 ${task_id} (最长${max}s)..."
    while [[ $elapsed -lt $max ]]; do
        local r; r=$(curl -s --max-time 10 -H "Authorization: Bearer ${token}" \
            "${url}/api/v1/admin/tasks/${task_id}" 2>/dev/null) || true
        local st; st=$(echo "$r" | jq -r '.data.status // empty' 2>/dev/null)
        case "$st" in
            completed) log_success "任务 ${task_id} 完成"; echo "$r"; return 0 ;;
            failed|cancelled|timeout) log_error "任务 ${task_id}: ${st}"; echo "$r"; return 1 ;;
        esac
        sleep "$interval"; elapsed=$((elapsed + interval))
    done
    log_error "任务超时"; return 1
}

# ── 认证 ──
init_system() {
    local url="$1" user="$2" pass="$3" db="${4:-mysql}"
    local data
    if [[ "$db" == "sqlite" ]]; then
        data="{\"admin_username\":\"${user}\",\"admin_password\":\"${pass}\",\"db_type\":\"sqlite\"}"
    else
        data="{\"admin_username\":\"${user}\",\"admin_password\":\"${pass}\",\"db_type\":\"mysql\",\"db_host\":\"127.0.0.1\",\"db_port\":3306,\"db_name\":\"oneclickvirt\",\"db_user\":\"root\",\"db_password\":\"\"}"
    fi
    curl -s --max-time 30 -H "Content-Type: application/json" -X POST -d "$data" "${url}/api/v1/public/init" 2>/dev/null
}

do_login() {
    local url="$1" user="$2" pass="$3"
    local r; r=$(curl -s --max-time 30 -H "Content-Type: application/json" -X POST \
        -d "{\"username\":\"${user}\",\"password\":\"${pass}\"}" "${url}/api/v1/auth/login" 2>/dev/null)
    echo "$r" | jq -r '.data.token // empty' 2>/dev/null
}

admin_login() {
    local url="$1" user="${2:-admin}" pass="${3:-Admin123!@#}"
    local token; token=$(do_login "$url" "$user" "$pass")
    [[ -n "$token" ]] && { log_success "登录成功: ${user}"; echo "$token"; return 0; }
    log_error "登录失败: ${user}"; return 1
}

# 添加 Provider
add_provider() {
    local url="$1" token="$2" name="$3" ptype="$4" ip="$5" port="${6:-22}" user="${7:-root}" pass="$8"
    curl -s --max-time 60 -H "Authorization: Bearer ${token}" -H "Content-Type: application/json" \
        -X POST -d "{\"name\":\"${name}\",\"type\":\"${ptype}\",\"ssh_host\":\"${ip}\",\"ssh_port\":${port},\"ssh_user\":\"${user}\",\"ssh_password\":\"${pass}\"}" \
        "${url}/api/v1/admin/providers" 2>/dev/null
}

# ── 报告 ──
report_init() {
    REPORT_FILE="$1"
    local env="$2" ts; ts=$(date '+%Y-%m-%d %H:%M:%S')
    cat > "$REPORT_FILE" << EOF
# ${env} 测试报告

测试时间: ${ts}

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
    [[ -z "$REPORT_FILE" ]] && return
    echo -e "\n### $1\n\n| 状态 | 测试名 | 方法 | 路由 | 说明 |\n|------|--------|------|------|------|" >> "$REPORT_FILE"
}

report_add_pass() {
    [[ -z "$REPORT_FILE" ]] && return
    echo "| PASS | $1 | $2 | \`$3\` | - |" >> "$REPORT_FILE"
}

report_add_fail() {
    local name="$1" method="$2" url="$3" data="$4" expect="$5" actual="$6" body="$7"
    [[ -z "$REPORT_FILE" ]] && return
    echo "| FAIL | ${name} | ${method} | \`${url}\` | 期望${expect},实际${actual} |" >> "$REPORT_FILE"
    {
        echo ""; echo "<details>"; echo "<summary>${name} - 详情</summary>"; echo ""
        echo "**请求**: \`${method} ${url}\`"
        [[ -n "$data" ]] && { echo ""; echo '```json'; echo "$data" | jq '.' 2>/dev/null || echo "$data"; echo '```'; }
        echo ""; echo "**期望**: ${expect} / **实际**: ${actual}"; echo ""
        echo '```json'; echo "$body" | jq '.' 2>/dev/null || echo "$body"; echo '```'
        echo ""; echo "</details>"; echo ""
    } >> "$REPORT_FILE"
}

report_add_skip() {
    [[ -z "$REPORT_FILE" ]] && return
    echo "| SKIP | $1 | $2 | \`$3\` | $4 |" >> "$REPORT_FILE"
}

report_finalize() {
    [[ -z "$REPORT_FILE" ]] && return
    local rate="0%"
    [[ $TOTAL_TESTS -gt 0 ]] && rate="$(( PASSED_TESTS * 100 / TOTAL_TESTS ))%"
    # Update summary table
    if command -v sed &> /dev/null; then
        sed -i.bak "s/| 总计 | 待更新 |/| 总计 | ${TOTAL_TESTS} |/" "$REPORT_FILE"
        sed -i.bak "s/| 通过 | 待更新 |/| 通过 | ${PASSED_TESTS} |/" "$REPORT_FILE"
        sed -i.bak "s/| 失败 | 待更新 |/| 失败 | ${FAILED_TESTS} |/" "$REPORT_FILE"
        sed -i.bak "s/| 跳过 | 待更新 |/| 跳过 | ${SKIPPED_TESTS} |/" "$REPORT_FILE"
        sed -i.bak "s/| 通过率 | 待更新 |/| 通过率 | ${rate} |/" "$REPORT_FILE"
        rm -f "${REPORT_FILE}.bak"
    fi
    echo ""
    echo "报告已生成: ${REPORT_FILE}"
}

report_add_skip() {
    [[ -z "$REPORT_FILE" ]] && return
    echo "| SKIP | $1 | $2 | \`$3\` | $4 |" >> "$REPORT_FILE"
}

report_finalize() {
    [[ -z "$REPORT_FILE" ]] && return
    local rate=0
    [[ $TOTAL_TESTS -gt 0 ]] && rate=$(( PASSED_TESTS * 100 / TOTAL_TESTS ))
    sed -i.bak "s/总计 | 待更新/总计 | ${TOTAL_TESTS}/" "$REPORT_FILE"
    sed -i.bak "s/通过 | 待更新/通过 | ${PASSED_TESTS}/" "$REPORT_FILE"
    sed -i.bak "s/失败 | 待更新/失败 | ${FAILED_TESTS}/" "$REPORT_FILE"
    sed -i.bak "s/跳过 | 待更新/跳过 | ${SKIPPED_TESTS}/" "$REPORT_FILE"
    sed -i.bak "s/通过率 | 待更新/通过率 | ${rate}%/" "$REPORT_FILE"
    rm -f "${REPORT_FILE}.bak"
    echo -e "\n---\n\n测试完成: 总${TOTAL_TESTS} 通过${PASSED_TESTS} 失败${FAILED_TESTS} 跳过${SKIPPED_TESTS} 通过率${rate}%" >> "$REPORT_FILE"
    log_section "测试结果: 总${TOTAL_TESTS} 通过${PASSED_TESTS} 失败${FAILED_TESTS} 跳过${SKIPPED_TESTS} 通过率${rate}%"
}
