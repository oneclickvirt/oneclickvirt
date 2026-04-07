#!/bin/bash
# HTML Test Report Generator
# Reads JSON Lines results file and generates a comprehensive HTML report
# Usage: bash generate_report.sh <results_file> <output_html> [env_name] [service_log_file]
set -uo pipefail

RESULTS_FILE="${1:-}"
OUTPUT_HTML="${2:-}"
ENV_NAME="${3:-unknown}"
SERVICE_LOG_FILE="${4:-}"

if [[ -z "$RESULTS_FILE" || -z "$OUTPUT_HTML" ]]; then
    echo "Usage: $0 <results.jsonl> <output.html> [env_name] [service_log_file]"
    exit 1
fi

if [[ ! -f "$RESULTS_FILE" ]]; then
    echo "Error: Results file not found: $RESULTS_FILE"
    exit 1
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

# Read service logs if available
SERVICE_LOGS=""
if [[ -n "$SERVICE_LOG_FILE" && -f "$SERVICE_LOG_FILE" ]]; then
    SERVICE_LOGS=$(cat "$SERVICE_LOG_FILE" | head -2000 | sed 's/</\&lt;/g; s/>/\&gt;/g; s/"/\&quot;/g')
fi

# Generate HTML
cat > "$OUTPUT_HTML" << 'HTMLHEAD'
<!DOCTYPE html>
<html lang="zh-CN">
<head>
<meta charset="UTF-8">
<meta name="viewport" content="width=device-width, initial-scale=1.0">
<title>OneClickVirt 集成测试报告</title>
<style>
:root{--pass:#22c55e;--fail:#ef4444;--skip:#eab308;--bg:#0f172a;--card:#1e293b;--text:#e2e8f0;--border:#334155;--hover:#283548;--accent:#60a5fa}
*{margin:0;padding:0;box-sizing:border-box}
body{font-family:-apple-system,BlinkMacSystemFont,'Segoe UI',Roboto,'Noto Sans SC',sans-serif;background:var(--bg);color:var(--text);line-height:1.6}
.container{max-width:1400px;margin:0 auto;padding:1.5rem}
header{margin-bottom:2rem}
h1{font-size:1.8rem;margin-bottom:0.3rem}
.meta{color:#94a3b8;font-size:0.9rem}
.dashboard{display:grid;grid-template-columns:repeat(auto-fit,minmax(140px,1fr));gap:1rem;margin-bottom:2rem}
.stat-card{background:var(--card);border-radius:10px;padding:1.2rem;text-align:center;border:1px solid var(--border);transition:border-color 0.2s}
.stat-card:hover{border-color:var(--accent)}
.stat-card .value{font-size:2.2rem;font-weight:700}
.stat-card .label{color:#94a3b8;font-size:0.8rem;margin-top:0.2rem;text-transform:uppercase;letter-spacing:0.05em}
.stat-card.pass .value{color:var(--pass)}
.stat-card.fail .value{color:var(--fail)}
.stat-card.skip .value{color:var(--skip)}
.stat-card.rate .value{color:var(--accent)}
.progress-bar{width:100%;height:6px;background:#334155;border-radius:3px;margin-top:1.5rem;overflow:hidden}
.progress-bar .fill{height:100%;border-radius:3px;transition:width 0.5s}
.toolbar{display:flex;gap:0.8rem;flex-wrap:wrap;align-items:center;margin-bottom:1.5rem;background:var(--card);padding:1rem;border-radius:10px;border:1px solid var(--border)}
.search-box{flex:1;min-width:200px;background:var(--bg);border:1px solid var(--border);color:var(--text);padding:0.5rem 1rem;border-radius:6px;font-size:0.9rem;outline:none}
.search-box:focus{border-color:var(--accent)}
.search-box::placeholder{color:#64748b}
.filter-btn{background:var(--bg);border:1px solid var(--border);color:var(--text);padding:0.4rem 1rem;border-radius:6px;cursor:pointer;font-size:0.85rem;transition:all 0.2s}
.filter-btn:hover,.filter-btn.active{background:#334155;border-color:var(--accent);color:var(--accent)}
.filter-btn .count{margin-left:0.3rem;opacity:0.7;font-size:0.75rem}
.group-select{background:var(--bg);border:1px solid var(--border);color:var(--text);padding:0.4rem 0.8rem;border-radius:6px;font-size:0.85rem;outline:none;cursor:pointer}
.section{background:var(--card);border-radius:10px;margin-bottom:1rem;border:1px solid var(--border);overflow:hidden}
.section-header{padding:0.8rem 1.2rem;background:var(--hover);font-weight:600;cursor:pointer;display:flex;justify-content:space-between;align-items:center;user-select:none;font-size:0.95rem}
.section-header:hover{background:#334155}
.section-header .badge-group{display:flex;gap:0.4rem}
.section-header .mini-badge{font-size:0.7rem;padding:2px 6px;border-radius:3px;font-weight:600}
.section-header .mini-badge.pass{background:rgba(34,197,94,0.15);color:var(--pass)}
.section-header .mini-badge.fail{background:rgba(239,68,68,0.15);color:var(--fail)}
.section-header .mini-badge.skip{background:rgba(234,179,8,0.15);color:var(--skip)}
.section-body{display:none}
.section-body.open{display:block}
.chevron{transition:transform 0.2s;font-size:0.8rem}
.chevron.open{transform:rotate(180deg)}
table{width:100%;border-collapse:collapse}
th{background:var(--hover);padding:0.5rem 1rem;text-align:left;font-size:0.75rem;text-transform:uppercase;color:#94a3b8;letter-spacing:0.05em;position:sticky;top:0}
td{padding:0.45rem 1rem;border-top:1px solid var(--border);font-size:0.85rem;vertical-align:top}
tr.test-row:hover{background:var(--hover)}
tr.test-row.hidden{display:none}
.badge{display:inline-block;padding:2px 10px;border-radius:4px;font-size:0.75rem;font-weight:600;min-width:42px;text-align:center}
.badge.pass{background:rgba(34,197,94,0.15);color:var(--pass)}
.badge.fail{background:rgba(239,68,68,0.15);color:var(--fail)}
.badge.skip{background:rgba(234,179,8,0.15);color:var(--skip)}
.detail-toggle{cursor:pointer;color:var(--accent);font-size:0.78rem;text-decoration:none}
.detail-toggle:hover{text-decoration:underline}
.detail-panel{padding:0.8rem 1rem;background:var(--bg);font-family:'SF Mono',Consolas,'Liberation Mono',Menlo,monospace;font-size:0.78rem;white-space:pre-wrap;word-break:break-all;max-height:300px;overflow:auto;border-top:1px solid var(--border);display:none;line-height:1.5}
.detail-panel.open{display:block}
.detail-panel .err-line{color:var(--fail);font-weight:600}
.detail-panel .warn-line{color:var(--skip)}
.timestamp{color:#64748b;font-size:0.75rem;font-family:monospace}
.log-section{background:var(--card);border-radius:10px;margin-top:2rem;border:1px solid var(--border);overflow:hidden}
.log-header{padding:0.8rem 1.2rem;background:var(--hover);font-weight:600;cursor:pointer;display:flex;justify-content:space-between;align-items:center}
.log-body{display:none;padding:1rem;font-family:monospace;font-size:0.78rem;white-space:pre-wrap;max-height:500px;overflow:auto;background:var(--bg);line-height:1.5}
.log-body.open{display:block}
.copy-btn{background:var(--bg);border:1px solid var(--border);color:var(--accent);padding:0.3rem 0.8rem;border-radius:4px;cursor:pointer;font-size:0.75rem}
.copy-btn:hover{background:#334155}
footer{margin-top:2rem;text-align:center;color:#64748b;font-size:0.8rem;padding:1rem}
kbd{background:#334155;border:1px solid #475569;padding:1px 5px;border-radius:3px;font-size:0.75rem;font-family:monospace}
@media(max-width:768px){.container{padding:1rem}.dashboard{grid-template-columns:repeat(2,1fr)}.toolbar{flex-direction:column}.search-box{min-width:100%}}
</style>
</head>
<body>
<div class="container">
HTMLHEAD

# Write header with dynamic values
cat >> "$OUTPUT_HTML" << HEADER
<header>
<h1>OneClickVirt 集成测试报告</h1>
<p class="meta">环境: <strong>${ENV_NAME}</strong> | 生成时间: ${TS} | 快捷键: <kbd>/</kbd> 搜索 <kbd>1-4</kbd> 筛选</p>
</header>

<div class="dashboard">
<div class="stat-card"><div class="value">${TOTAL}</div><div class="label">总计</div></div>
<div class="stat-card pass"><div class="value">${PASSED}</div><div class="label">通过</div></div>
<div class="stat-card fail"><div class="value">${FAILED}</div><div class="label">失败</div></div>
<div class="stat-card skip"><div class="value">${SKIPPED}</div><div class="label">跳过</div></div>
<div class="stat-card rate"><div class="value">${RATE}%</div><div class="label">通过率</div></div>
</div>
<div class="progress-bar"><div class="fill" style="width:${RATE}%;background:linear-gradient(90deg,var(--pass) 0%,var(--pass) 100%)"></div></div>

<div class="toolbar">
<input type="text" class="search-box" id="searchBox" placeholder="搜索测试名称、接口路径、错误详情... (按 / 聚焦)" />
<select class="group-select" id="groupFilter" onchange="applyFilters()"><option value="all">所有模块</option></select>
<button class="filter-btn active" data-filter="all" onclick="setStatusFilter('all')">全部 <span class="count">${TOTAL}</span></button>
<button class="filter-btn" data-filter="PASS" onclick="setStatusFilter('PASS')">通过 <span class="count">${PASSED}</span></button>
<button class="filter-btn" data-filter="FAIL" onclick="setStatusFilter('FAIL')">失败 <span class="count">${FAILED}</span></button>
<button class="filter-btn" data-filter="SKIP" onclick="setStatusFilter('SKIP')">跳过 <span class="count">${SKIPPED}</span></button>
<button class="copy-btn" onclick="copySummary()">复制摘要</button>
</div>
HEADER

# Generate test result sections grouped by module
current_group=""
detail_idx=0
group_stats=""

# First pass: collect groups and their stats
declare -A group_pass group_fail group_skip
while IFS= read -r line; do
    [[ -z "$line" ]] && continue
    grp=$(echo "$line" | jq -r '.group // "default"' 2>/dev/null)
    status=$(echo "$line" | jq -r '.status // empty' 2>/dev/null)
    if [[ "$status" == "PASS" ]]; then
        group_pass[$grp]=$(( ${group_pass[$grp]:-0} + 1 ))
    elif [[ "$status" == "FAIL" ]]; then
        group_fail[$grp]=$(( ${group_fail[$grp]:-0} + 1 ))
    elif [[ "$status" == "SKIP" ]]; then
        group_skip[$grp]=$(( ${group_skip[$grp]:-0} + 1 ))
    fi
done < "$RESULTS_FILE"

# Second pass: generate HTML
current_group=""
while IFS= read -r line; do
    [[ -z "$line" ]] && continue
    grp=$(echo "$line" | jq -r '.group // "default"' 2>/dev/null)
    status=$(echo "$line" | jq -r '.status // empty' 2>/dev/null)
    name=$(echo "$line" | jq -r '.name // ""' 2>/dev/null)
    method=$(echo "$line" | jq -r '.method // ""' 2>/dev/null)
    url=$(echo "$line" | jq -r '.url // ""' 2>/dev/null)
    expected=$(echo "$line" | jq -r '.expected // ""' 2>/dev/null)
    actual=$(echo "$line" | jq -r '.actual // ""' 2>/dev/null)
    detail=$(echo "$line" | jq -r '.detail // ""' 2>/dev/null)
    timestamp=$(echo "$line" | jq -r '.timestamp // ""' 2>/dev/null)
    error_logs=$(echo "$line" | jq -r '.error_logs // ""' 2>/dev/null)
    st_class=$(echo "$status" | tr '[:upper:]' '[:lower:]')

    if [[ "$grp" != "$current_group" ]]; then
        # Close previous section
        if [[ -n "$current_group" ]]; then
            echo "</table></div></div>" >> "$OUTPUT_HTML"
        fi
        current_group="$grp"
        # Calculate group stats
        local_pass=0; local_fail=0; local_skip=0
        while IFS= read -r l2; do
            [[ -z "$l2" ]] && continue
            g2=$(echo "$l2" | jq -r '.group // "default"' 2>/dev/null)
            s2=$(echo "$l2" | jq -r '.status // empty' 2>/dev/null)
            if [[ "$g2" == "$grp" ]]; then
                case "$s2" in PASS) local_pass=$((local_pass+1));; FAIL) local_fail=$((local_fail+1));; SKIP) local_skip=$((local_skip+1));; esac
            fi
        done < "$RESULTS_FILE"

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
<tr><th>状态</th><th>测试名称</th><th>方法</th><th>接口</th><th>时间</th><th>详情</th></tr>
SECHEAD2
    fi

    # Write test row
    has_detail=""
    detail_content=""
    if [[ "$status" == "FAIL" && ( -n "$detail" || -n "$error_logs" ) ]]; then
        has_detail="1"
        detail_content=""
        [[ -n "$expected" || -n "$actual" ]] && detail_content+="期望: ${expected} | 实际: ${actual}\n"
        [[ -n "$detail" && "$detail" != "null" ]] && detail_content+="--- 响应 ---\n${detail}\n"
        [[ -n "$error_logs" && "$error_logs" != "null" ]] && detail_content+="--- 服务日志 ---\n${error_logs}"
        # Escape HTML
        detail_content=$(echo -e "$detail_content" | sed 's/</\&lt;/g; s/>/\&gt;/g')
    elif [[ "$status" == "SKIP" && -n "$detail" && "$detail" != "null" ]]; then
        has_detail="1"
        detail_content=$(echo "$detail" | sed 's/</\&lt;/g; s/>/\&gt;/g')
    fi

    echo "<tr class=\"test-row\" data-status=\"${status}\" data-group=\"${grp}\" data-name=\"${name}\" data-url=\"${url}\">" >> "$OUTPUT_HTML"
    echo "<td><span class=\"badge ${st_class}\">${status}</span></td>" >> "$OUTPUT_HTML"
    echo "<td>${name}</td><td>${method}</td><td><code>${url}</code></td>" >> "$OUTPUT_HTML"
    echo "<td><span class=\"timestamp\">${timestamp}</span></td>" >> "$OUTPUT_HTML"
    if [[ -n "$has_detail" ]]; then
        echo "<td><a class=\"detail-toggle\" onclick=\"toggleDetail('dp${detail_idx}')\">查看</a></td></tr>" >> "$OUTPUT_HTML"
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
<span>服务日志 (错误/警告)</span>
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
OneClickVirt Integration Test Report | Generated by action_tests/report/generate_report.sh
</footer>
</div>

<script>
let currentStatus='all',currentGroup='all',currentSearch='';
const searchBox=document.getElementById('searchBox');
const groupFilter=document.getElementById('groupFilter');

// Populate group filter
const groups=new Set();
document.querySelectorAll('.section[data-group]').forEach(s=>{
  const g=s.dataset.group;
  groups.add(g);
});
groups.forEach(g=>{
  const opt=document.createElement('option');
  opt.value=g;opt.textContent=g;
  groupFilter.appendChild(opt);
});

function setStatusFilter(s){
  currentStatus=s;
  document.querySelectorAll('.filter-btn').forEach(b=>{
    b.classList.toggle('active',b.dataset.filter===s);
  });
  applyFilters();
}

function applyFilters(){
  currentGroup=groupFilter.value;
  currentSearch=searchBox.value.toLowerCase();
  document.querySelectorAll('tr.test-row').forEach(r=>{
    if(r.classList.contains('detail-row')){
      // Detail rows follow their parent's visibility
      return;
    }
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
    // Hide associated detail row
    const next=r.nextElementSibling;
    if(next&&next.classList.contains('detail-row')){
      next.classList.toggle('hidden',!show);
    }
  });
  // Show/hide sections
  document.querySelectorAll('.section[data-group]').forEach(s=>{
    if(currentGroup!=='all'&&s.dataset.group!==currentGroup){
      s.style.display='none';
    }else{
      s.style.display='';
    }
  });
}

searchBox.addEventListener('input',()=>applyFilters());

function toggleSection(el){
  const body=el.nextElementSibling;
  const chev=el.querySelector('.chevron');
  if(body){
    body.classList.toggle('open');
    if(chev)chev.classList.toggle('open');
  }
}

function toggleDetail(id){
  const el=document.getElementById(id);
  if(el)el.classList.toggle('open');
}

function copySummary(){
  const t=`OneClickVirt 测试报告\n环境: ENVNAME\n总计: TOTAL | 通过: PASSED | 失败: FAILED | 跳过: SKIPPED | 通过率: RATE%`.replace('ENVNAME',document.querySelector('header strong').textContent).replace('TOTAL',TOTAL_VAL).replace('PASSED',PASSED_VAL).replace('FAILED',FAILED_VAL).replace('SKIPPED',SKIPPED_VAL).replace('RATE',RATE_VAL);
  navigator.clipboard.writeText(t).then(()=>alert('已复制到剪贴板')).catch(()=>{});
}

// Keyboard shortcuts
document.addEventListener('keydown',e=>{
  if(e.target.tagName==='INPUT'||e.target.tagName==='TEXTAREA')return;
  if(e.key==='/'){e.preventDefault();searchBox.focus();}
  if(e.key==='1')setStatusFilter('all');
  if(e.key==='2')setStatusFilter('PASS');
  if(e.key==='3')setStatusFilter('FAIL');
  if(e.key==='4')setStatusFilter('SKIP');
  if(e.key==='Escape'){searchBox.value='';currentSearch='';applyFilters();}
});

// Auto-open sections with failures
document.querySelectorAll('.section').forEach(s=>{
  if(s.querySelector('.badge.fail')){
    const body=s.querySelector('.section-body');
    if(body)body.classList.add('open');
    const chev=s.querySelector('.chevron');
    if(chev)chev.classList.add('open');
  }
});
</script>
HTMLFOOT

# Replace JavaScript template variables with actual values
if [[ "$(uname)" == "Darwin" ]]; then
    sed -i '' "s/TOTAL_VAL/${TOTAL}/g;s/PASSED_VAL/${PASSED}/g;s/FAILED_VAL/${FAILED}/g;s/SKIPPED_VAL/${SKIPPED}/g;s/RATE_VAL/${RATE}/g" "$OUTPUT_HTML"
else
    sed -i "s/TOTAL_VAL/${TOTAL}/g;s/PASSED_VAL/${PASSED}/g;s/FAILED_VAL/${FAILED}/g;s/SKIPPED_VAL/${SKIPPED}/g;s/RATE_VAL/${RATE}/g" "$OUTPUT_HTML"
fi

echo "HTML report generated: ${OUTPUT_HTML} (Total=${TOTAL} Pass=${PASSED} Fail=${FAILED} Skip=${SKIPPED})"
