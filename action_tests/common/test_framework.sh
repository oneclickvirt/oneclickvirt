#!/bin/bash
# Test Framework Core - logging, assertions, reporting, wait functions
set -uo pipefail

RED='\033[0;31m'; GREEN='\033[0;32m'; YELLOW='\033[1;33m'
BLUE='\033[0;34m'; CYAN='\033[0;36m'; NC='\033[0m'

log_info()    { echo -e "${BLUE}[INFO]${NC} $(date '+%H:%M:%S') $*"; }
log_success() { echo -e "${GREEN}[PASS]${NC} $(date '+%H:%M:%S') $*"; }
log_error()   { echo -e "${RED}[FAIL]${NC} $(date '+%H:%M:%S') $*"; }
log_warning() { echo -e "${YELLOW}[WARN]${NC} $(date '+%H:%M:%S') $*"; }
log_section() { echo -e "\n${CYAN}========== $* ==========${NC}\n"; }
log_skip()    { echo -e "${YELLOW}[SKIP]${NC} $(date '+%H:%M:%S') $*"; }

# -- Counters --
TOTAL_TESTS=0; PASSED_TESTS=0; FAILED_TESTS=0; SKIPPED_TESTS=0
declare -A CHAIN_BROKEN
REPORT_FILE=""

# -- Global variables (shared across modules) --
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
TEST_USER2="test_user_ci_2"
TEST_USER2_PASS="TestUser2_123!@#"
USER_TOKEN2=""
PROVIDER_ID=""
ENV_TYPE="${ENV_TYPE:-docker}"
INSTANCE_TYPES="${INSTANCE_TYPES:-both}"
NODE_IP=""
NODE_PASSWORD=""
WORKER_IP=""
WORKER_PASSWORD=""
WORKER_ID=""

# -- JSON result collector for HTML report --
declare -a TEST_RESULTS_JSON=()

# -- API test function --
# Args: test_name method url expected_code [data] [group] [token_override]
test_api() {
    local name="$1" method="$2" url="$3" expected="$4"
    local data="${5:-}" group="${6:-default}" token="${7:-$ADMIN_TOKEN}"
    TOTAL_TESTS=$((TOTAL_TESTS + 1))
    if [[ -n "${CHAIN_BROKEN[$group]:-}" ]]; then
        SKIPPED_TESTS=$((SKIPPED_TESTS + 1))
        log_skip "${name} (chain broken: ${CHAIN_BROKEN[$group]})"
        report_add_skip "$name" "$method" "$url" "${CHAIN_BROKEN[$group]}"
        _add_result_json "$name" "$method" "$url" "SKIP" "" "" "${CHAIN_BROKEN[$group]}" "$group"
        return 1
    fi
    local args=(-s -w "\n%{http_code}" --max-time 60
        -H "Content-Type: application/json" -X "${method}")
    [[ -n "$token" ]] && args+=(-H "Authorization: Bearer ${token}")
    [[ -n "$data" ]] && args+=(-d "$data")
    local resp; resp=$(curl "${args[@]}" "${SERVER_URL}${url}" 2>&1) || true
    local code; code=$(echo "$resp" | tail -1)
    local body; body=$(echo "$resp" | sed '$d')
    # Support pipe-separated expected codes (e.g. "200|201|400")
    local match=false
    IFS='|' read -ra exp_codes <<< "$expected"
    for ec in "${exp_codes[@]}"; do
        [[ "$code" == "$ec" ]] && { match=true; break; }
    done
    if [[ "$match" == "false" ]]; then
        FAILED_TESTS=$((FAILED_TESTS + 1))
        log_error "${name} - expected HTTP ${expected}, got HTTP ${code}"
        report_add_fail "$name" "$method" "$url" "$data" "$expected" "$code" "$body"
        _add_result_json "$name" "$method" "$url" "FAIL" "$expected" "$code" "$body" "$group"
        return 1
    fi
    PASSED_TESTS=$((PASSED_TESTS + 1))
    log_success "${name}"
    report_add_pass "$name" "$method" "$url"
    _add_result_json "$name" "$method" "$url" "PASS" "$expected" "$code" "" "$group"
    echo "$body"
    return 0
}

# Retry wrapper
test_api_retry() {
    local name="$1" method="$2" url="$3" expected="$4" data="${5:-}" retries="${6:-3}" interval="${7:-5}" group="${8:-default}" token="${9:-$ADMIN_TOKEN}"
    local i=0
    while [[ $i -lt $retries ]]; do
        i=$((i + 1))
        [[ $i -gt 1 ]] && { log_info "Retry ${name} (${i}/${retries})..."; sleep "$interval"; }
        local st=$TOTAL_TESTS sp=$PASSED_TESTS sf=$FAILED_TESTS ss=$SKIPPED_TESTS
        local result; result=$(test_api "$name" "$method" "$url" "$expected" "$data" "$group" "$token" 2>&1) && { echo "$result"; return 0; }
        [[ $i -lt $retries ]] && { TOTAL_TESTS=$st; PASSED_TESTS=$sp; FAILED_TESTS=$sf; SKIPPED_TESTS=$ss; }
    done
    return 1
}

# Without auth token
test_api_noauth() {
    local name="$1" method="$2" url="$3" expected="$4" data="${5:-}" group="${6:-default}"
    test_api "$name" "$method" "$url" "$expected" "$data" "$group" ""
}

chain_break() { CHAIN_BROKEN[$1]="$2"; log_warning "Chain broken [${1}]: ${2}"; }

# -- Utility: should we test this instance type? --
should_test_type() {
    local itype="$1"
    case "$INSTANCE_TYPES" in
        both) return 0 ;;
        container) [[ "$itype" == "container" ]] && return 0 || return 1 ;;
        vm) [[ "$itype" == "vm" ]] && return 0 || return 1 ;;
    esac
    return 0
}

# -- Environment capabilities --
env_supports_container() {
    case "$ENV_TYPE" in
        docker|lxd|incus|podman|containerd|proxmoxve) return 0 ;;
        *) return 1 ;;
    esac
}

env_supports_vm() {
    case "$ENV_TYPE" in
        lxd|incus|proxmoxve) return 0 ;;
        *) return 1 ;;
    esac
}

# -- Wait functions --
wait_server_ready() {
    local url="$1" max="${2:-300}" interval="${3:-10}" elapsed=0
    log_info "Waiting for server: ${url}"
    while [[ $elapsed -lt $max ]]; do
        local r; r=$(curl -s -o /dev/null -w "%{http_code}" --max-time 10 "${url}/health" 2>/dev/null) || true
        [[ "$r" == "200" ]] && { log_success "Server is ready"; return 0; }
        sleep "$interval"; elapsed=$((elapsed + interval))
    done
    log_error "Server readiness timeout (${max}s)"; return 1
}

wait_db_ready() {
    local url="$1" max="${2:-120}" interval="${3:-5}" elapsed=0
    while [[ $elapsed -lt $max ]]; do
        local r; r=$(curl -s --max-time 10 "${url}/api/v1/public/init/check" 2>/dev/null) || true
        local init; init=$(echo "$r" | jq -r '.data.initialized // false' 2>/dev/null)
        [[ "$init" == "true" ]] && { log_success "Database ready"; return 0; }
        sleep "$interval"; elapsed=$((elapsed + interval))
    done
    return 1
}

wait_task_complete() {
    local url="$1" task_id="$2" token="$3" max="${4:-600}" interval="${5:-10}" elapsed=0
    log_info "Waiting for task ${task_id} (max ${max}s)..."
    while [[ $elapsed -lt $max ]]; do
        local r; r=$(curl -s --max-time 10 -H "Authorization: Bearer ${token}" \
            "${url}/api/v1/admin/tasks/${task_id}" 2>/dev/null) || true
        local st; st=$(echo "$r" | jq -r '.data.status // empty' 2>/dev/null)
        case "$st" in
            completed) log_success "Task ${task_id} completed"; echo "$r"; return 0 ;;
            failed|cancelled|timeout) log_error "Task ${task_id}: ${st}"; echo "$r"; return 1 ;;
        esac
        sleep "$interval"; elapsed=$((elapsed + interval))
    done
    log_error "Task timeout"; return 1
}

# -- Auth helpers --
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
    [[ -n "$token" ]] && { log_success "Login success: ${user}"; echo "$token"; return 0; }
    log_error "Login failed: ${user}"; return 1
}

add_provider() {
    local url="$1" token="$2" name="$3" ptype="$4" ip="$5" port="${6:-22}" user="${7:-root}" pass="$8"
    curl -s --max-time 60 -H "Authorization: Bearer ${token}" -H "Content-Type: application/json" \
        -X POST -d "{\"name\":\"${name}\",\"type\":\"${ptype}\",\"ssh_host\":\"${ip}\",\"ssh_port\":${port},\"ssh_user\":\"${user}\",\"ssh_password\":\"${pass}\"}" \
        "${url}/api/v1/admin/providers" 2>/dev/null
}

# -- JSON result helper for HTML report --
_add_result_json() {
    local name="$1" method="$2" url="$3" status="$4" expected="$5" actual="$6" detail="$7" group="$8"
    local safe_detail; safe_detail=$(echo "$detail" | head -c 2000 | sed 's/"/\\"/g' | tr '\n' ' ')
    TEST_RESULTS_JSON+=("{\"name\":\"${name}\",\"method\":\"${method}\",\"url\":\"${url}\",\"status\":\"${status}\",\"expected\":\"${expected}\",\"actual\":\"${actual}\",\"detail\":\"${safe_detail}\",\"group\":\"${group}\"}")
}

# -- Markdown report --
report_init() {
    REPORT_FILE="$1"
    local env="$2" ts; ts=$(date -u '+%Y-%m-%d %H:%M:%S UTC')
    cat > "$REPORT_FILE" << EOF
# ${env} Integration Test Report

Test Time: ${ts}
Environment: ${env}
Instance Types: ${INSTANCE_TYPES}

## Summary

| Metric | Value |
|--------|-------|
| Total | _PENDING_ |
| Passed | _PENDING_ |
| Failed | _PENDING_ |
| Skipped | _PENDING_ |
| Pass Rate | _PENDING_ |

## Test Details

EOF
}

report_add_section() {
    [[ -z "$REPORT_FILE" ]] && return
    echo -e "\n### $1\n\n| Status | Test | Method | Route | Note |\n|--------|------|--------|-------|------|" >> "$REPORT_FILE"
}

report_add_pass() {
    [[ -z "$REPORT_FILE" ]] && return
    echo "| PASS | $1 | $2 | \`$3\` | - |" >> "$REPORT_FILE"
}

report_add_fail() {
    local name="$1" method="$2" url="$3" data="$4" expect="$5" actual="$6" body="$7"
    [[ -z "$REPORT_FILE" ]] && return
    echo "| FAIL | ${name} | ${method} | \`${url}\` | expected ${expect}, got ${actual} |" >> "$REPORT_FILE"
    {
        echo ""; echo "<details>"; echo "<summary>${name} - Details</summary>"; echo ""
        echo "**Request**: \`${method} ${url}\`"
        [[ -n "$data" ]] && { echo ""; echo '```json'; echo "$data" | jq '.' 2>/dev/null || echo "$data"; echo '```'; }
        echo ""; echo "**Expected**: ${expect} / **Actual**: ${actual}"; echo ""
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
    local rate=0
    [[ $TOTAL_TESTS -gt 0 ]] && rate=$(( PASSED_TESTS * 100 / TOTAL_TESTS ))
    sed -i.bak "s/| Total | _PENDING_ |/| Total | ${TOTAL_TESTS} |/" "$REPORT_FILE"
    sed -i.bak "s/| Passed | _PENDING_ |/| Passed | ${PASSED_TESTS} |/" "$REPORT_FILE"
    sed -i.bak "s/| Failed | _PENDING_ |/| Failed | ${FAILED_TESTS} |/" "$REPORT_FILE"
    sed -i.bak "s/| Skipped | _PENDING_ |/| Skipped | ${SKIPPED_TESTS} |/" "$REPORT_FILE"
    sed -i.bak "s/| Pass Rate | _PENDING_ |/| Pass Rate | ${rate}% |/" "$REPORT_FILE"
    rm -f "${REPORT_FILE}.bak"
    echo -e "\n---\n\nCompleted: Total=${TOTAL_TESTS} Passed=${PASSED_TESTS} Failed=${FAILED_TESTS} Skipped=${SKIPPED_TESTS} Rate=${rate}%" >> "$REPORT_FILE"
    log_section "Results: Total=${TOTAL_TESTS} Passed=${PASSED_TESTS} Failed=${FAILED_TESTS} Skipped=${SKIPPED_TESTS} Rate=${rate}%"
}

# -- HTML report generation --
generate_html_report() {
    local output_file="$1" env_name="$2"
    local ts; ts=$(date -u '+%Y-%m-%d %H:%M:%S UTC')
    local rate=0
    [[ $TOTAL_TESTS -gt 0 ]] && rate=$(( PASSED_TESTS * 100 / TOTAL_TESTS ))

    cat > "$output_file" << 'HTMLHEAD'
<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="UTF-8">
<meta name="viewport" content="width=device-width, initial-scale=1.0">
<title>Integration Test Report</title>
<style>
:root{--pass:#22c55e;--fail:#ef4444;--skip:#eab308;--bg:#0f172a;--card:#1e293b;--text:#e2e8f0;--border:#334155}
*{margin:0;padding:0;box-sizing:border-box}
body{font-family:-apple-system,BlinkMacSystemFont,'Segoe UI',Roboto,sans-serif;background:var(--bg);color:var(--text);padding:2rem}
.container{max-width:1200px;margin:0 auto}
h1{font-size:1.8rem;margin-bottom:0.5rem}
.meta{color:#94a3b8;margin-bottom:2rem}
.summary{display:grid;grid-template-columns:repeat(auto-fit,minmax(150px,1fr));gap:1rem;margin-bottom:2rem}
.stat{background:var(--card);border-radius:8px;padding:1.2rem;text-align:center;border:1px solid var(--border)}
.stat .value{font-size:2rem;font-weight:700}
.stat .label{color:#94a3b8;font-size:0.85rem;margin-top:0.3rem}
.stat.pass .value{color:var(--pass)} .stat.fail .value{color:var(--fail)}
.stat.skip .value{color:var(--skip)} .stat.rate .value{color:#60a5fa}
.section{background:var(--card);border-radius:8px;margin-bottom:1.5rem;border:1px solid var(--border);overflow:hidden}
.section-header{padding:1rem 1.2rem;background:#283548;font-weight:600;cursor:pointer;display:flex;justify-content:space-between;align-items:center}
.section-header:hover{background:#334155}
table{width:100%;border-collapse:collapse}
th{background:#283548;padding:0.6rem 1rem;text-align:left;font-size:0.8rem;text-transform:uppercase;color:#94a3b8}
td{padding:0.5rem 1rem;border-top:1px solid var(--border);font-size:0.85rem}
tr:hover{background:#283548}
.badge{display:inline-block;padding:2px 8px;border-radius:4px;font-size:0.75rem;font-weight:600}
.badge.pass{background:rgba(34,197,94,0.15);color:var(--pass)}
.badge.fail{background:rgba(239,68,68,0.15);color:var(--fail)}
.badge.skip{background:rgba(234,179,8,0.15);color:var(--skip)}
.detail{padding:0.8rem 1rem;background:#0f172a;font-family:monospace;font-size:0.78rem;white-space:pre-wrap;word-break:break-all;max-height:200px;overflow:auto;display:none;border-top:1px solid var(--border)}
.toggle-detail{cursor:pointer;color:#60a5fa;font-size:0.78rem}
.toggle-detail:hover{text-decoration:underline}
.filter-bar{margin-bottom:1.5rem;display:flex;gap:0.5rem;flex-wrap:wrap}
.filter-btn{background:var(--card);border:1px solid var(--border);color:var(--text);padding:0.4rem 1rem;border-radius:6px;cursor:pointer;font-size:0.85rem}
.filter-btn:hover,.filter-btn.active{background:#334155;border-color:#60a5fa}
</style>
</head>
<body>
<div class="container">
HTMLHEAD

    {
        echo "<h1>Integration Test Report - ${env_name}</h1>"
        echo "<p class=\"meta\">Time: ${ts} | Instance Types: ${INSTANCE_TYPES}</p>"
        echo "<div class=\"summary\">"
        echo "<div class=\"stat\"><div class=\"value\">${TOTAL_TESTS}</div><div class=\"label\">Total</div></div>"
        echo "<div class=\"stat pass\"><div class=\"value\">${PASSED_TESTS}</div><div class=\"label\">Passed</div></div>"
        echo "<div class=\"stat fail\"><div class=\"value\">${FAILED_TESTS}</div><div class=\"label\">Failed</div></div>"
        echo "<div class=\"stat skip\"><div class=\"value\">${SKIPPED_TESTS}</div><div class=\"label\">Skipped</div></div>"
        echo "<div class=\"stat rate\"><div class=\"value\">${rate}%</div><div class=\"label\">Pass Rate</div></div>"
        echo "</div>"
        echo "<div class=\"filter-bar\">"
        echo "<button class=\"filter-btn active\" onclick=\"filterTests('all')\">All</button>"
        echo "<button class=\"filter-btn\" onclick=\"filterTests('PASS')\">Passed</button>"
        echo "<button class=\"filter-btn\" onclick=\"filterTests('FAIL')\">Failed</button>"
        echo "<button class=\"filter-btn\" onclick=\"filterTests('SKIP')\">Skipped</button>"
        echo "</div>"

        local current_group=""
        local idx=0
        for result in "${TEST_RESULTS_JSON[@]}"; do
            local grp; grp=$(echo "$result" | jq -r '.group' 2>/dev/null)
            if [[ "$grp" != "$current_group" ]]; then
                [[ -n "$current_group" ]] && echo "</table></div></div>"
                current_group="$grp"
                echo "<div class=\"section\" data-group=\"${grp}\">"
                echo "<div class=\"section-header\" onclick=\"this.nextElementSibling.style.display=this.nextElementSibling.style.display==='none'?'block':'none'\">"
                echo "<span>${grp}</span><span>&#9660;</span></div>"
                echo "<div class=\"section-body\">"
                echo "<table><tr><th>Status</th><th>Test</th><th>Method</th><th>Endpoint</th><th>Detail</th></tr>"
            fi
            local st; st=$(echo "$result" | jq -r '.status' 2>/dev/null)
            local nm; nm=$(echo "$result" | jq -r '.name' 2>/dev/null)
            local mt; mt=$(echo "$result" | jq -r '.method' 2>/dev/null)
            local ur; ur=$(echo "$result" | jq -r '.url' 2>/dev/null)
            local dt; dt=$(echo "$result" | jq -r '.detail' 2>/dev/null)
            local st_class; st_class=$(echo "$st" | tr '[:upper:]' '[:lower:]')
            echo "<tr class=\"test-row\" data-status=\"${st}\">"
            echo "<td><span class=\"badge ${st_class}\">${st}</span></td>"
            echo "<td>${nm}</td><td>${mt}</td><td>${ur}</td>"
            if [[ -n "$dt" && "$dt" != "null" && "$dt" != "" ]]; then
                echo "<td><span class=\"toggle-detail\" onclick=\"var d=document.getElementById('d${idx}');d.style.display=d.style.display==='none'?'block':'none'\">show</span><div class=\"detail\" id=\"d${idx}\">${dt}</div></td>"
            else
                echo "<td>-</td>"
            fi
            echo "</tr>"
            idx=$((idx + 1))
        done
        [[ -n "$current_group" ]] && echo "</table></div></div>"

        cat << 'HTMLFOOT'
</div>
<script>
function filterTests(status){
  document.querySelectorAll('.filter-btn').forEach(b=>b.classList.remove('active'));
  event.target.classList.add('active');
  document.querySelectorAll('.test-row').forEach(r=>{
    r.style.display=(status==='all'||r.dataset.status===status)?'':'none';
  });
}
</script>
</body>
</html>
HTMLFOOT
    } >> "$output_file"
    log_info "HTML report generated: ${output_file}"
}

# -- HTML index page generator (for Pages) --
generate_index_html() {
    local output_file="$1" report_dir="$2"
    local ts; ts=$(date -u '+%Y-%m-%d %H:%M:%S UTC')
    cat > "$output_file" << INDEXHTML
<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="UTF-8">
<meta name="viewport" content="width=device-width, initial-scale=1.0">
<title>OneClickVirt Integration Test Reports</title>
<style>
:root{--bg:#0f172a;--card:#1e293b;--text:#e2e8f0;--border:#334155}
*{margin:0;padding:0;box-sizing:border-box}
body{font-family:-apple-system,BlinkMacSystemFont,'Segoe UI',Roboto,sans-serif;background:var(--bg);color:var(--text);padding:2rem}
.container{max-width:900px;margin:0 auto}
h1{font-size:2rem;margin-bottom:0.5rem}
.meta{color:#94a3b8;margin-bottom:2rem}
.card{background:var(--card);border:1px solid var(--border);border-radius:8px;padding:1.2rem;margin-bottom:1rem;display:flex;justify-content:space-between;align-items:center}
.card:hover{border-color:#60a5fa}
a{color:#60a5fa;text-decoration:none}
a:hover{text-decoration:underline}
.env-name{font-weight:600;font-size:1.1rem}
</style>
</head>
<body>
<div class="container">
<h1>OneClickVirt Integration Test Reports</h1>
<p class="meta">Generated: ${ts}</p>
INDEXHTML

    for html_file in "${report_dir}"/*.html; do
        [[ ! -f "$html_file" ]] && continue
        local fname; fname=$(basename "$html_file")
        [[ "$fname" == "index.html" ]] && continue
        local env_name; env_name=$(echo "$fname" | sed 's/-report\.html//' | sed 's/_/ /g')
        echo "<div class=\"card\"><span class=\"env-name\">${env_name}</span><a href=\"${fname}\">View Report</a></div>" >> "$output_file"
    done

    echo "</div></body></html>" >> "$output_file"
}
