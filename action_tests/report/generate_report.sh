#!/bin/bash
# HTML Test Report Generator
# Reads JSON Lines results file and generates a comprehensive HTML report
# Features: Bilingual (zh-CN/en-US), Light/Dark theme, version info, last 3 reports history, search/filter
# Usage: bash generate_report.sh <results_file> <output_html> [env_name] [service_log_file] [server_version] [agent_version]
set -uo pipefail

RESULTS_FILE="${1:-}"
OUTPUT_HTML="${2:-}"
ENV_NAME="${3:-unknown}"
SERVICE_LOG_FILE="${4:-}"
SERVER_VERSION="${5:-unknown}"
AGENT_VERSION="${6:-unknown}"

if [[ -z "$RESULTS_FILE" || -z "$OUTPUT_HTML" ]]; then
    echo "Usage: $0 <results.jsonl> <output.html> [env_name] [service_log_file]"
    exit 1
fi

if [[ ! -f "$RESULTS_FILE" ]]; then
    echo "Error: Results file not found: $RESULTS_FILE"
    exit 1
fi

# ── History management: keep only latest 3 reports ──
OUTPUT_DIR=$(dirname "$OUTPUT_HTML")
if [[ -d "$OUTPUT_DIR" ]]; then
    # Find old reports matching the pattern and keep only the 3 newest (including current)
    old_reports=$(find "$OUTPUT_DIR" -maxdepth 1 -name "*-report.html" -type f 2>/dev/null | sort -r | tail -n +3)
    for old in $old_reports; do
        rm -f "$old" 2>/dev/null || true
    done
    old_jsonl=$(find "$OUTPUT_DIR" -maxdepth 1 -name "*-results.jsonl" -type f 2>/dev/null | sort -r | tail -n +3)
    for old in $old_jsonl; do
        rm -f "$old" 2>/dev/null || true
    done
fi

# Count results
TOTAL=0; PASSED=0; FAILED=0; SKIPPED=0
while IFS= read -r line; do
    [[ -z "$line" ]] && continue
    status=$(echo "$line" | jq -r '.status // empty' 2>/dev/null)
    case "$status" in
        PASS) PASSED=$((PASSED + 1)); TOTAL=$((TOTAL + 1)) ;;
        FAIL) FAILED=$((FAILED + 1)); TOTAL=$((TOTAL + 1)) ;;
        SKIP) SKIPPED=$((SKIPPED + 1)); TOTAL=$((TOTAL + 1)) ;;
    esac
done < "$RESULTS_FILE"

RATE=0
[[ $TOTAL -gt 0 ]] && RATE=$(( PASSED * 100 / TOTAL ))

TS=$(date -u '+%Y-%m-%d %H:%M:%S UTC')

html_escape() {
    printf '%s' "$1" | sed 's/&/\&amp;/g; s/</\&lt;/g; s/>/\&gt;/g; s/"/\&quot;/g'
}

GIT_SHA_RAW="${GITHUB_SHA:-}"
if [[ -z "$GIT_SHA_RAW" ]]; then
    GIT_SHA_RAW=$(git rev-parse --short HEAD 2>/dev/null || true)
fi
if [[ ${#GIT_SHA_RAW} -gt 12 ]]; then
    GIT_SHA_RAW="${GIT_SHA_RAW:0:12}"
fi
GIT_REF_RAW="${GITHUB_REF_NAME:-}"
if [[ -z "$GIT_REF_RAW" ]]; then
    GIT_REF_RAW=$(git rev-parse --abbrev-ref HEAD 2>/dev/null || true)
fi
RUN_ID_RAW="${GITHUB_RUN_ID:-local}"
RUN_ATTEMPT_RAW="${GITHUB_RUN_ATTEMPT:-1}"
WORKFLOW_RAW="${GITHUB_WORKFLOW:-local}"
GIT_SHA=$(html_escape "${GIT_SHA_RAW:-unknown}")
GIT_REF=$(html_escape "${GIT_REF_RAW:-unknown}")
RUN_ID=$(html_escape "$RUN_ID_RAW")
RUN_ATTEMPT=$(html_escape "$RUN_ATTEMPT_RAW")
WORKFLOW_NAME=$(html_escape "$WORKFLOW_RAW")

# Read service logs if available
SERVICE_LOGS=""
if [[ -n "$SERVICE_LOG_FILE" && -f "$SERVICE_LOG_FILE" ]]; then
    SERVICE_LOGS=$(cat "$SERVICE_LOG_FILE" | sed 's/</\&lt;/g; s/>/\&gt;/g; s/"/\&quot;/g')
fi

# ── Collect history from sibling report files for comparison ──
HISTORY_JSON="[]"
if [[ -d "$OUTPUT_DIR" ]]; then
    history_entries=()
    for hf in $(find "$OUTPUT_DIR" -maxdepth 1 -name "*-results.jsonl" -type f 2>/dev/null | sort -r | head -3); do
        [[ "$hf" == "$RESULTS_FILE" ]] && continue
        h_total=0; h_pass=0; h_fail=0; h_skip=0
        while IFS= read -r hl; do
            [[ -z "$hl" ]] && continue
            hs=$(echo "$hl" | jq -r '.status // empty' 2>/dev/null)
            case "$hs" in
                PASS) h_pass=$((h_pass+1)); h_total=$((h_total+1)) ;;
                FAIL) h_fail=$((h_fail+1)); h_total=$((h_total+1)) ;;
                SKIP) h_skip=$((h_skip+1)); h_total=$((h_total+1)) ;;
            esac
        done < "$hf"
        h_rate=0
        [[ $h_total -gt 0 ]] && h_rate=$((h_pass * 100 / h_total))
        h_name=$(basename "$hf" | sed 's/-results.jsonl//')
        history_entries+=("{\"name\":\"${h_name}\",\"total\":${h_total},\"pass\":${h_pass},\"fail\":${h_fail},\"skip\":${h_skip},\"rate\":${h_rate}}")
    done
    if [[ ${#history_entries[@]} -gt 0 ]]; then
        HISTORY_JSON="[$(IFS=,; echo "${history_entries[*]}")]"
    fi
fi

# Generate HTML
cat > "$OUTPUT_HTML" << 'HTMLHEAD'
<!DOCTYPE html>
<html lang="zh-CN" data-lang="zh">
<head>
<meta charset="UTF-8">
<meta name="viewport" content="width=device-width, initial-scale=1.0">
<title>OneClickVirt 集成测试报告 / Integration Test Report</title>
<style>
/* ── Theme Variables ── */
:root {
  --pass:#22c55e;--fail:#ef4444;--skip:#eab308;--accent:#60a5fa;
  --bg:#f8fafc;--card:#ffffff;--text:#1e293b;--border:#e2e8f0;--hover:#f1f5f9;
  --text-muted:#64748b;--code-bg:#f1f5f9;--input-bg:#ffffff;
  --shadow:0 4px 24px rgba(0,0,0,0.08);
}
[data-theme="dark"] {
  --bg:#0f172a;--card:#1e293b;--text:#e2e8f0;--border:#334155;--hover:#283548;
  --text-muted:#94a3b8;--code-bg:#0f172a;--input-bg:#0f172a;
  --shadow:0 4px 24px rgba(0,0,0,0.3);
}
*{margin:0;padding:0;box-sizing:border-box}
body{font-family:-apple-system,BlinkMacSystemFont,'Segoe UI',Roboto,'Noto Sans SC',sans-serif;background:var(--bg);color:var(--text);line-height:1.6;transition:background 0.3s,color 0.3s}
.container{max-width:1400px;margin:0 auto;padding:1.5rem}
header{margin-bottom:2rem;display:flex;justify-content:space-between;align-items:flex-start;flex-wrap:wrap;gap:1rem}
.header-left{flex:1}
h1{font-size:1.8rem;margin-bottom:0.3rem;background:linear-gradient(135deg,var(--accent),#a78bfa);-webkit-background-clip:text;-webkit-text-fill-color:transparent;background-clip:text}
.meta{color:var(--text-muted);font-size:0.9rem}
.theme-toggle{background:var(--card);border:1px solid var(--border);color:var(--text);padding:0.5rem 1rem;border-radius:8px;cursor:pointer;font-size:0.9rem;display:flex;align-items:center;gap:0.4rem;transition:all 0.2s;box-shadow:var(--shadow)}
.theme-toggle:hover{border-color:var(--accent);transform:translateY(-1px)}
.lang-toggle{background:var(--card);border:1px solid var(--border);color:var(--text);padding:0.5rem 1rem;border-radius:8px;cursor:pointer;font-size:0.9rem;display:flex;align-items:center;gap:0.4rem;transition:all 0.2s;box-shadow:var(--shadow)}
.lang-toggle:hover{border-color:var(--accent);transform:translateY(-1px)}
.header-btns{display:flex;gap:0.5rem;align-items:flex-start}
.version-info{display:inline-flex;gap:0.8rem;margin-top:0.3rem;font-size:0.8rem;color:var(--text-muted);font-family:monospace}
.version-info span{background:var(--hover);padding:2px 8px;border-radius:4px;border:1px solid var(--border)}
[data-lang="en"] .zh{display:none}
[data-lang="zh"] .en{display:none}
.dashboard{display:grid;grid-template-columns:repeat(auto-fit,minmax(150px,1fr));gap:1rem;margin-bottom:2rem}
.stat-card{background:var(--card);border-radius:12px;padding:1.2rem;text-align:center;border:1px solid var(--border);transition:all 0.2s;box-shadow:var(--shadow)}
.stat-card:hover{border-color:var(--accent);transform:translateY(-2px)}
.stat-card .value{font-size:2.2rem;font-weight:700}
.stat-card .label{color:var(--text-muted);font-size:0.8rem;margin-top:0.2rem;text-transform:uppercase;letter-spacing:0.05em}
.stat-card.pass .value{color:var(--pass)}
.stat-card.fail .value{color:var(--fail)}
.stat-card.skip .value{color:var(--skip)}
.stat-card.rate .value{color:var(--accent)}
.progress-bar{width:100%;height:8px;background:var(--border);border-radius:4px;margin-top:1.5rem;overflow:hidden}
.progress-bar .fill{height:100%;border-radius:4px;transition:width 0.8s ease}
.toolbar{display:flex;gap:0.8rem;flex-wrap:wrap;align-items:center;margin-bottom:1.5rem;background:var(--card);padding:1rem;border-radius:12px;border:1px solid var(--border);box-shadow:var(--shadow)}
.search-box{flex:1;min-width:200px;background:var(--input-bg);border:1px solid var(--border);color:var(--text);padding:0.5rem 1rem;border-radius:8px;font-size:0.9rem;outline:none;transition:border-color 0.2s}
.search-box:focus{border-color:var(--accent);box-shadow:0 0 0 3px rgba(96,165,250,0.15)}
.search-box::placeholder{color:var(--text-muted)}
.filter-btn{background:var(--input-bg);border:1px solid var(--border);color:var(--text);padding:0.4rem 1rem;border-radius:8px;cursor:pointer;font-size:0.85rem;transition:all 0.2s}
.filter-btn:hover,.filter-btn.active{background:var(--hover);border-color:var(--accent);color:var(--accent)}
.filter-btn .count{margin-left:0.3rem;opacity:0.7;font-size:0.75rem}
.group-select{background:var(--input-bg);border:1px solid var(--border);color:var(--text);padding:0.4rem 0.8rem;border-radius:8px;font-size:0.85rem;outline:none;cursor:pointer}
.section{background:var(--card);border-radius:12px;margin-bottom:1rem;border:1px solid var(--border);overflow:hidden;box-shadow:var(--shadow);transition:border-color 0.2s}
.section:hover{border-color:rgba(96,165,250,0.3)}
.section-header{padding:0.8rem 1.2rem;background:var(--hover);font-weight:600;cursor:pointer;display:flex;justify-content:space-between;align-items:center;user-select:none;font-size:0.95rem;transition:background 0.2s}
.section-header:hover{background:var(--border)}
.section-header .badge-group{display:flex;gap:0.4rem}
.section-header .mini-badge{font-size:0.7rem;padding:2px 8px;border-radius:4px;font-weight:600}
.section-header .mini-badge.pass{background:rgba(34,197,94,0.15);color:var(--pass)}
.section-header .mini-badge.fail{background:rgba(239,68,68,0.15);color:var(--fail)}
.section-header .mini-badge.skip{background:rgba(234,179,8,0.15);color:var(--skip)}
.section-body{display:none}
.section-body.open{display:block}
.chevron{transition:transform 0.3s;font-size:0.8rem}
.chevron.open{transform:rotate(180deg)}
table{width:100%;border-collapse:collapse}
th{background:var(--hover);padding:0.5rem 1rem;text-align:left;font-size:0.75rem;text-transform:uppercase;color:var(--text-muted);letter-spacing:0.05em;position:sticky;top:0}
td{padding:0.45rem 1rem;border-top:1px solid var(--border);font-size:0.85rem;vertical-align:top}
tr.test-row:hover{background:var(--hover)}
tr.test-row.hidden{display:none}
.badge{display:inline-block;padding:2px 10px;border-radius:6px;font-size:0.75rem;font-weight:600;min-width:42px;text-align:center}
.badge.pass{background:rgba(34,197,94,0.15);color:var(--pass)}
.badge.fail{background:rgba(239,68,68,0.15);color:var(--fail)}
.badge.skip{background:rgba(234,179,8,0.15);color:var(--skip)}
.detail-toggle{cursor:pointer;color:var(--accent);font-size:0.78rem;text-decoration:none}
.detail-toggle:hover{text-decoration:underline}
.detail-panel{padding:0.8rem 1rem;background:var(--code-bg);font-family:'SF Mono',Consolas,'Liberation Mono',Menlo,monospace;font-size:0.78rem;white-space:pre-wrap;word-break:break-all;overflow:visible;border-top:1px solid var(--border);display:none;line-height:1.5}
.detail-panel.open{display:block}
.detail-panel .err-line{color:var(--fail);font-weight:600}
.detail-panel .warn-line{color:var(--skip)}
.timestamp{color:var(--text-muted);font-size:0.75rem;font-family:monospace}
.log-section{background:var(--card);border-radius:12px;margin-top:2rem;border:1px solid var(--border);overflow:hidden;box-shadow:var(--shadow)}
.log-header{padding:0.8rem 1.2rem;background:var(--hover);font-weight:600;cursor:pointer;display:flex;justify-content:space-between;align-items:center}
.log-body{display:none;padding:1rem;font-family:monospace;font-size:0.78rem;white-space:pre-wrap;overflow:visible;background:var(--code-bg);line-height:1.5}
.log-body.open{display:block}
.copy-btn{background:var(--input-bg);border:1px solid var(--border);color:var(--accent);padding:0.3rem 0.8rem;border-radius:6px;cursor:pointer;font-size:0.75rem;transition:all 0.2s}
.copy-btn:hover{background:var(--hover);transform:translateY(-1px)}
/* ── History comparison ── */
.history-section{background:var(--card);border-radius:12px;margin-bottom:2rem;padding:1.2rem;border:1px solid var(--border);box-shadow:var(--shadow)}
.history-section h3{font-size:0.95rem;margin-bottom:0.8rem;color:var(--text-muted)}
.history-grid{display:grid;grid-template-columns:repeat(auto-fit,minmax(200px,1fr));gap:1rem}
.history-card{background:var(--hover);border-radius:8px;padding:1rem;border:1px solid var(--border)}
.history-card .h-name{font-weight:600;font-size:0.85rem;margin-bottom:0.5rem;color:var(--text)}
.history-card .h-stats{display:flex;gap:0.6rem;font-size:0.8rem;flex-wrap:wrap}
.history-card .h-stats span{padding:2px 6px;border-radius:4px}
/* ── Footer ── */
footer{margin-top:3rem;text-align:center;color:var(--text-muted);font-size:0.8rem;padding:1.5rem;border-top:1px solid var(--border)}
footer a{color:var(--accent);text-decoration:none}
footer a:hover{text-decoration:underline}
kbd{background:var(--hover);border:1px solid var(--border);padding:1px 5px;border-radius:3px;font-size:0.75rem;font-family:monospace}
@media(max-width:768px){.container{padding:1rem}.dashboard{grid-template-columns:repeat(2,1fr)}.toolbar{flex-direction:column}.search-box{min-width:100%}header{flex-direction:column}}
</style>
</head>
<body>
<div class="container">
HTMLHEAD

# Write header with dynamic values
cat >> "$OUTPUT_HTML" << HEADER
<header>
<div class="header-left">
<h1><span class="zh">OneClickVirt 集成测试报告</span><span class="en">OneClickVirt Integration Test Report</span></h1>
<div class="version-info"><span>Server ${SERVER_VERSION}</span><span>Agent ${AGENT_VERSION}</span><span>Ref ${GIT_REF}</span><span>SHA ${GIT_SHA}</span><span>Run ${RUN_ID}.${RUN_ATTEMPT}</span></div>
<p class="meta"><span class="zh">环境: <strong>${ENV_NAME}</strong> | 工作流: ${WORKFLOW_NAME} | 生成时间: ${TS} | 快捷键: <kbd>/</kbd> 搜索 <kbd>1-4</kbd> 筛选 <kbd>T</kbd> 主题 <kbd>L</kbd> 语言</span><span class="en">Env: <strong>${ENV_NAME}</strong> | Workflow: ${WORKFLOW_NAME} | Generated: ${TS} | Keys: <kbd>/</kbd> search <kbd>1-4</kbd> filter <kbd>T</kbd> theme <kbd>L</kbd> lang</span></p>
</div>
<div class="header-btns">
<button class="lang-toggle" onclick="toggleLang()" title="切换中英文 / Toggle Language">
<span id="langLabel">EN</span>
</button>
<button class="theme-toggle" onclick="toggleTheme()" title="切换亮色/暗色主题">
<span id="themeIcon">☀️</span> <span id="themeLabel"><span class="zh">亮色</span><span class="en">Light</span></span>
</button>
</div>
</header>

<div class="dashboard">
<div class="stat-card"><div class="value">${TOTAL}</div><div class="label"><span class="zh">总计</span><span class="en">Total</span></div></div>
<div class="stat-card pass"><div class="value">${PASSED}</div><div class="label"><span class="zh">通过</span><span class="en">Pass</span></div></div>
<div class="stat-card fail"><div class="value">${FAILED}</div><div class="label"><span class="zh">失败</span><span class="en">Fail</span></div></div>
<div class="stat-card skip"><div class="value">${SKIPPED}</div><div class="label"><span class="zh">跳过</span><span class="en">Skip</span></div></div>
<div class="stat-card rate"><div class="value">${RATE}%</div><div class="label"><span class="zh">通过率</span><span class="en">Rate</span></div></div>
</div>
<div class="progress-bar"><div class="fill" style="width:${RATE}%;background:linear-gradient(90deg,var(--pass),var(--accent))"></div></div>
HEADER

# Write history comparison if available
if [[ "$HISTORY_JSON" != "[]" ]]; then
    cat >> "$OUTPUT_HTML" << 'HISTHEAD'
<div class="history-section">
<h3><span class="zh">历史对比 (最近报告)</span><span class="en">History Comparison (Recent Reports)</span></h3>
<div class="history-grid" id="historyGrid"></div>
</div>
HISTHEAD
fi

# Write toolbar
cat >> "$OUTPUT_HTML" << TOOLBAR
<div class="toolbar">
<input type="text" class="search-box" id="searchBox" placeholder="" />
<select class="group-select" id="groupFilter" onchange="applyFilters()"><option value="all" class="i18n-all-modules"></option></select>
<button class="filter-btn active" data-filter="all" onclick="setStatusFilter('all')"><span class="zh">全部</span><span class="en">All</span> <span class="count">${TOTAL}</span></button>
<button class="filter-btn" data-filter="PASS" onclick="setStatusFilter('PASS')"><span class="zh">通过</span><span class="en">Pass</span> <span class="count">${PASSED}</span></button>
<button class="filter-btn" data-filter="FAIL" onclick="setStatusFilter('FAIL')"><span class="zh">失败</span><span class="en">Fail</span> <span class="count">${FAILED}</span></button>
<button class="filter-btn" data-filter="SKIP" onclick="setStatusFilter('SKIP')"><span class="zh">跳过</span><span class="en">Skip</span> <span class="count">${SKIPPED}</span></button>
<button class="copy-btn" onclick="copySummary()"><span class="zh">复制摘要</span><span class="en">Copy Summary</span></button>
</div>
TOOLBAR

# Generate test result sections grouped by module
current_group=""
detail_idx=0

get_group_count() {
    local group="$1" status="$2"
    # shellcheck disable=SC2016
    jq -sr --arg group "$group" --arg status "$status" \
        '[.[] | select((((.group // "default") | if . == "" or . == null then "default" else . end) == $group) and ((.status // "") == $status))] | length' \
        "$RESULTS_FILE" 2>/dev/null || printf '0'
}

# Second pass: generate HTML
current_group=""
while IFS= read -r line; do
    [[ -z "$line" ]] && continue
    grp=$(echo "$line" | jq -r '.group // "default"' 2>/dev/null)
    [[ -z "$grp" || "$grp" == "null" ]] && grp="default"
    status=$(echo "$line" | jq -r '.status // empty' 2>/dev/null)
    name=$(echo "$line" | jq -r '.name // ""' 2>/dev/null)
    method=$(echo "$line" | jq -r '.method // ""' 2>/dev/null)
    url=$(echo "$line" | jq -r '.url // ""' 2>/dev/null)
    expected=$(echo "$line" | jq -r '.expected // ""' 2>/dev/null)
    actual=$(echo "$line" | jq -r '.actual // ""' 2>/dev/null)
    detail=$(echo "$line" | jq -r '.detail // ""' 2>/dev/null)
    timestamp=$(echo "$line" | jq -r '.timestamp // ""' 2>/dev/null)
    error_logs=$(echo "$line" | jq -r '.error_logs // ""' 2>/dev/null)
    request_payload=$(echo "$line" | jq -r '.request_payload // ""' 2>/dev/null)
    st_class=$(echo "$status" | tr '[:upper:]' '[:lower:]')

    if [[ "$grp" != "$current_group" ]]; then
        # Close previous section
        if [[ -n "$current_group" ]]; then
            echo "</table></div></div>" >> "$OUTPUT_HTML"
        fi
        current_group="$grp"
        local_pass=$(get_group_count "$grp" "PASS")
        local_fail=$(get_group_count "$grp" "FAIL")
        local_skip=$(get_group_count "$grp" "SKIP")

        local_has_fail=""
        [[ $local_fail -gt 0 ]] && local_has_fail=" style=\"border-color:rgba(239,68,68,0.3)\""

        cat >> "$OUTPUT_HTML" << SECHEAD
<div class="section" data-group="${grp}"${local_has_fail}>
<div class="section-header" onclick="toggleSection(this)">
<span>${grp}</span>
<div style="display:flex;align-items:center;gap:0.6rem">
<div class="badge-group">
SECHEAD
        [[ $local_pass -gt 0 ]] && echo "<span class=\"mini-badge pass\">${local_pass} pass</span>" >> "$OUTPUT_HTML"
        [[ $local_fail -gt 0 ]] && echo "<span class=\"mini-badge fail\">${local_fail} fail</span>" >> "$OUTPUT_HTML"
        [[ $local_skip -gt 0 ]] && echo "<span class=\"mini-badge skip\">${local_skip} skip</span>" >> "$OUTPUT_HTML"
        cat >> "$OUTPUT_HTML" << SECHEAD2
</div>
<span class="chevron">▼</span>
</div>
</div>
<div class="section-body${local_fail:+ open}">
<table>
<tr><th><span class="zh">状态</span><span class="en">Status</span></th><th><span class="zh">测试名称</span><span class="en">Test Name</span></th><th><span class="zh">方法</span><span class="en">Method</span></th><th><span class="zh">接口</span><span class="en">Endpoint</span></th><th><span class="zh">时间</span><span class="en">Time</span></th><th><span class="zh">详情</span><span class="en">Details</span></th></tr>
SECHEAD2
    fi

    # Write test row
    has_detail=""
    detail_content=""
    if [[ "$status" == "FAIL" && ( -n "$detail" || -n "$error_logs" || -n "$request_payload" ) ]]; then
        has_detail="1"
        detail_content=""
        [[ -n "$expected" || -n "$actual" ]] && detail_content+="Expected: ${expected} | Actual: ${actual}"$'\n'
        [[ -n "$request_payload" && "$request_payload" != "null" ]] && detail_content+="--- Request Payload ---"$'\n'"${request_payload}"$'\n'
        [[ -n "$detail" && "$detail" != "null" ]] && detail_content+="--- Response ---"$'\n'"${detail}"$'\n'
        [[ -n "$error_logs" && "$error_logs" != "null" ]] && detail_content+="--- Service Logs ---"$'\n'"${error_logs}"
        # Escape HTML
        detail_content=$(html_escape "$detail_content")
    elif [[ "$status" == "SKIP" && -n "$detail" && "$detail" != "null" ]]; then
        has_detail="1"
        detail_content=$(html_escape "$detail")
    fi

    echo "<tr class=\"test-row\" data-status=\"${status}\" data-group=\"${grp}\" data-name=\"${name}\" data-url=\"${url}\">" >> "$OUTPUT_HTML"
    echo "<td><span class=\"badge ${st_class}\">${status}</span></td>" >> "$OUTPUT_HTML"
    echo "<td>${name}</td><td>${method}</td><td><code>${url}</code></td>" >> "$OUTPUT_HTML"
    echo "<td><span class=\"timestamp\">${timestamp}</span></td>" >> "$OUTPUT_HTML"
    if [[ -n "$has_detail" ]]; then
        echo "<td><a class=\"detail-toggle\" onclick=\"toggleDetail('dp${detail_idx}')\"><span class=\"zh\">查看</span><span class=\"en\">View</span></a></td></tr>" >> "$OUTPUT_HTML"
        echo "<tr class=\"test-row detail-row\" data-status=\"${status}\" data-group=\"${grp}\"><td colspan=\"6\"><div class=\"detail-panel\" id=\"dp${detail_idx}\">${detail_content}</div></td></tr>" >> "$OUTPUT_HTML"
        detail_idx=$((detail_idx + 1))
    else
        echo "<td>-</td></tr>" >> "$OUTPUT_HTML"
    fi
done < "$RESULTS_FILE"

# Close last section
[[ -n "$current_group" ]] && echo "</table></div></div>" >> "$OUTPUT_HTML"

# Service log section
if [[ -n "$SERVICE_LOGS" ]]; then
    cat >> "$OUTPUT_HTML" << LOGSEC
<div class="log-section">
<div class="log-header" onclick="toggleSection(this)">
<span><span class="zh">服务日志 (错误/警告)</span><span class="en">Service Logs (Errors/Warnings)</span></span>
<span class="chevron">▼</span>
</div>
<div class="section-body">
<div class="log-body open">${SERVICE_LOGS}</div>
</div>
</div>
LOGSEC
fi

# Footer and JavaScript
cat >> "$OUTPUT_HTML" << 'HTMLFOOT'
<footer>
<p><span class="zh">OneClickVirt 集成测试报告</span><span class="en">OneClickVirt Integration Test Report</span></p>
<p><a href="https://github.com/oneclickvirt/oneclickvirt" target="_blank" rel="noopener">github.com/oneclickvirt/oneclickvirt</a></p>
</footer>
</div>

<script>
const TOTAL_VAL='__TOTAL__',PASSED_VAL='__PASSED__',FAILED_VAL='__FAILED__',SKIPPED_VAL='__SKIPPED__',RATE_VAL='__RATE__';
const HISTORY_DATA=__HISTORY__;

/* ── Theme toggle ── */
function getTheme(){return localStorage.getItem('ocv-theme')||'light'}
function applyTheme(t){
  document.documentElement.setAttribute('data-theme',t);
  document.getElementById('themeIcon').textContent=t==='dark'?'🌙':'☀️';
  document.getElementById('themeLabel').innerHTML=t==='dark'
    ? '<span class="zh">暗色</span><span class="en">Dark</span>'
    : '<span class="zh">亮色</span><span class="en">Light</span>';
  localStorage.setItem('ocv-theme',t);
}
function toggleTheme(){applyTheme(getTheme()==='dark'?'light':'dark')}
applyTheme(getTheme());

/* ── Language toggle ── */
function getLang(){return localStorage.getItem('ocv-lang')||'zh'}
function applyLang(l){
  document.documentElement.setAttribute('data-lang',l);
  document.getElementById('langLabel').textContent=l==='zh'?'EN':'中文';
  document.getElementById('searchBox').placeholder=l==='zh'?'搜索测试名称、接口路径、错误详情... (按 / 聚焦)':'Search test name, endpoint, details... (press / to focus)';
  document.querySelectorAll('.i18n-all-modules').forEach(el=>{el.textContent=l==='zh'?'所有模块':'All Modules'});
  localStorage.setItem('ocv-lang',l);
}
function toggleLang(){applyLang(getLang()==='zh'?'en':'zh')}
applyLang(getLang());

/* ── History rendering ── */
if(HISTORY_DATA.length>0){
  const grid=document.getElementById('historyGrid');
  if(grid){
    // Current report card
    let html='<div class="history-card" style="border-color:var(--accent)"><div class="h-name"><span class="zh">当前</span><span class="en">Current</span></div><div class="h-stats">';
    html+=`<span style="color:var(--pass)">✓ ${PASSED_VAL}</span>`;
    html+=`<span style="color:var(--fail)">✗ ${FAILED_VAL}</span>`;
    html+=`<span style="color:var(--skip)">○ ${SKIPPED_VAL}</span>`;
    html+=`<span style="color:var(--accent)">${RATE_VAL}%</span></div></div>`;
    HISTORY_DATA.forEach(h=>{
      html+=`<div class="history-card"><div class="h-name">${h.name}</div><div class="h-stats">`;
      html+=`<span style="color:var(--pass)">✓ ${h.pass}</span>`;
      html+=`<span style="color:var(--fail)">✗ ${h.fail}</span>`;
      html+=`<span style="color:var(--skip)">○ ${h.skip}</span>`;
      html+=`<span style="color:var(--accent)">${h.rate}%</span></div></div>`;
    });
    grid.innerHTML=html;
  }
}

/* ── Filter & Search ── */
let currentStatus='all',currentGroup='all',currentSearch='';
const searchBox=document.getElementById('searchBox');
const groupFilter=document.getElementById('groupFilter');

// Populate group filter
const groups=new Set();
document.querySelectorAll('.section[data-group]').forEach(s=>{groups.add(s.dataset.group)});
groups.forEach(g=>{const opt=document.createElement('option');opt.value=g;opt.textContent=g;groupFilter.appendChild(opt)});

function setStatusFilter(s){
  currentStatus=s;
  document.querySelectorAll('.filter-btn').forEach(b=>{b.classList.toggle('active',b.dataset.filter===s)});
  applyFilters();
}

function applyFilters(){
  currentGroup=groupFilter.value;
  currentSearch=searchBox.value.toLowerCase();
  document.querySelectorAll('tr.test-row').forEach(r=>{
    if(r.classList.contains('detail-row'))return;
    const st=r.dataset.status||'';
    const grp=r.dataset.group||'';
    const name=(r.dataset.name||'').toLowerCase();
    const url=(r.dataset.url||'').toLowerCase();
    const text=r.textContent.toLowerCase();
    let show=true;
    if(currentStatus!=='all'&&st!==currentStatus)show=false;
    if(currentGroup!=='all'&&grp!==currentGroup)show=false;
    if(currentSearch&&!name.includes(currentSearch)&&!url.includes(currentSearch)&&!text.includes(currentSearch))show=false;
    r.classList.toggle('hidden',!show);
    const next=r.nextElementSibling;
    if(next&&next.classList.contains('detail-row'))next.classList.toggle('hidden',!show);
  });
  document.querySelectorAll('.section[data-group]').forEach(s=>{
    s.style.display=(currentGroup!=='all'&&s.dataset.group!==currentGroup)?'none':'';
  });
}

searchBox.addEventListener('input',()=>applyFilters());

function toggleSection(el){
  const body=el.nextElementSibling;
  const chev=el.querySelector('.chevron');
  if(body){body.classList.toggle('open');if(chev)chev.classList.toggle('open')}
}

function toggleDetail(id){const el=document.getElementById(id);if(el)el.classList.toggle('open')}

function copySummary(){
  const l=getLang();
  const env=document.querySelector('header strong').textContent;
  const t=l==='zh'
    ?`OneClickVirt 测试报告\n环境: ${env}\n总计: ${TOTAL_VAL} | 通过: ${PASSED_VAL} | 失败: ${FAILED_VAL} | 跳过: ${SKIPPED_VAL} | 通过率: ${RATE_VAL}%`
    :`OneClickVirt Test Report\nEnv: ${env}\nTotal: ${TOTAL_VAL} | Pass: ${PASSED_VAL} | Fail: ${FAILED_VAL} | Skip: ${SKIPPED_VAL} | Rate: ${RATE_VAL}%`;
  navigator.clipboard.writeText(t).then(()=>alert(l==='zh'?'已复制到剪贴板':'Copied to clipboard')).catch(()=>{});
}

// Keyboard shortcuts
document.addEventListener('keydown',e=>{
  if(e.target.tagName==='INPUT'||e.target.tagName==='TEXTAREA')return;
  if(e.key==='/'){e.preventDefault();searchBox.focus()}
  if(e.key==='1')setStatusFilter('all');
  if(e.key==='2')setStatusFilter('PASS');
  if(e.key==='3')setStatusFilter('FAIL');
  if(e.key==='4')setStatusFilter('SKIP');
  if(e.key==='t'||e.key==='T')toggleTheme();
  if(e.key==='l'||e.key==='L')toggleLang();
  if(e.key==='Escape'){searchBox.value='';currentSearch='';applyFilters()}
});

// Auto-open sections with failures
document.querySelectorAll('.section').forEach(s=>{
  if(s.querySelector('.badge.fail')){
    const body=s.querySelector('.section-body');if(body)body.classList.add('open');
    const chev=s.querySelector('.chevron');if(chev)chev.classList.add('open');
  }
});
</script>
HTMLFOOT

# Replace JavaScript template variables with actual values
if [[ "$(uname)" == "Darwin" ]]; then
    sed -i '' "s/__TOTAL__/${TOTAL}/g;s/__PASSED__/${PASSED}/g;s/__FAILED__/${FAILED}/g;s/__SKIPPED__/${SKIPPED}/g;s/__RATE__/${RATE}/g;s|__HISTORY__|${HISTORY_JSON}|g" "$OUTPUT_HTML"
else
    sed -i "s/__TOTAL__/${TOTAL}/g;s/__PASSED__/${PASSED}/g;s/__FAILED__/${FAILED}/g;s/__SKIPPED__/${SKIPPED}/g;s/__RATE__/${RATE}/g;s|__HISTORY__|${HISTORY_JSON}|g" "$OUTPUT_HTML"
fi

echo "HTML report generated: ${OUTPUT_HTML} (Total=${TOTAL} Pass=${PASSED} Fail=${FAILED} Skip=${SKIPPED})"
