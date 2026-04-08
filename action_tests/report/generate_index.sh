#!/bin/bash
# HTML Index Report Generator for OneClickVirt Test Reports
# Generates a rich overview page with per-environment statistics
# Usage: bash generate_index.sh <reports_dir> <output_html>
set -uo pipefail

REPORTS_DIR="${1:-reports}"
OUTPUT_HTML="${2:-index.html}"
GEN_TS=$(date -u '+%Y-%m-%d %H:%M:%S UTC')

# ── Collect per-environment data ──────────────────────────────────────────────
env_json_parts=()

if [[ -d "$REPORTS_DIR" ]]; then
    for envdir in "$REPORTS_DIR"/*/; do
        [[ -d "$envdir" ]] || continue
        envname=$(basename "$envdir")
        runs_parts=()
        latest_ts=""; latest_total=0; latest_pass=0; latest_fail=0; latest_skip=0; latest_rate=0

        # Iterate timestamp dirs newest first
        while IFS= read -r tsdir; do
            [[ -d "$tsdir" ]] || continue
            ts=$(basename "$tsdir")

            # Find JSONL: prefer env-level, fall back to any *-results.jsonl
            jsonl_file="${tsdir}/${envname}-results.jsonl"
            if [[ ! -f "$jsonl_file" ]]; then
                jsonl_file=$(find "$tsdir" -maxdepth 1 -name "*-results.jsonl" 2>/dev/null | sort | head -1)
            fi

            total=0; pass_cnt=0; fail_cnt=0; skip_cnt=0
            if [[ -f "$jsonl_file" ]]; then
                while IFS= read -r line; do
                    [[ -z "$line" ]] && continue
                    st=$(echo "$line" | jq -r '.status // empty' 2>/dev/null)
                    case "$st" in
                        PASS) pass_cnt=$((pass_cnt + 1)); total=$((total + 1)) ;;
                        FAIL) fail_cnt=$((fail_cnt + 1)); total=$((total + 1)) ;;
                        SKIP) skip_cnt=$((skip_cnt + 1)); total=$((total + 1)) ;;
                    esac
                done < "$jsonl_file"
            fi
            rate=0; [[ $total -gt 0 ]] && rate=$((pass_cnt * 100 / total))

            # Collect HTML links for this timestamp dir
            links_parts=()
            for html_file in "$tsdir"*.html; do
                [[ -f "$html_file" ]] || continue
                rel="${html_file#./}"
                links_parts+=("\"${rel}\"")
            done
            links_str=""
            for lp in "${links_parts[@]+"${links_parts[@]}"}"; do
                [[ -n "$links_str" ]] && links_str+=","
                links_str+="$lp"
            done

            run_obj="{\"ts\":\"${ts}\",\"links\":[${links_str}],\"total\":${total},\"pass\":${pass_cnt},\"fail\":${fail_cnt},\"skip\":${skip_cnt},\"rate\":${rate}}"
            runs_parts+=("$run_obj")

            if [[ -z "$latest_ts" ]]; then
                latest_ts="$ts"
                latest_total=$total; latest_pass=$pass_cnt
                latest_fail=$fail_cnt; latest_skip=$skip_cnt; latest_rate=$rate
            fi
        done < <(ls -d "$envdir"*/ 2>/dev/null | sort -r)

        [[ -z "$latest_ts" ]] && continue

        runs_str=""
        for rp in "${runs_parts[@]+"${runs_parts[@]}"}"; do
            [[ -n "$runs_str" ]] && runs_str+=","
            runs_str+="$rp"
        done

        env_obj="{\"name\":\"${envname}\",\"latestTs\":\"${latest_ts}\",\"total\":${latest_total},\"pass\":${latest_pass},\"fail\":${latest_fail},\"skip\":${latest_skip},\"rate\":${latest_rate},\"runs\":[${runs_str}]}"
        env_json_parts+=("$env_obj")
    done
fi

all_data_json="["
first_env=true
for ep in "${env_json_parts[@]+"${env_json_parts[@]}"}"; do
    $first_env || all_data_json+=","
    all_data_json+="$ep"
    first_env=false
done
all_data_json+="]"

# ── A: Static head + CSS (single-quoted heredoc — no bash expansion) ──────────
cat > "$OUTPUT_HTML" << 'HTMLHEAD'
<!DOCTYPE html>
<html lang="zh-CN" data-lang="zh">
<head>
<meta charset="UTF-8">
<meta name="viewport" content="width=device-width, initial-scale=1.0">
<title>OneClickVirt 集成测试报告总览 / Integration Test Reports</title>
<style>
:root{--pass:#22c55e;--fail:#ef4444;--skip:#eab308;--accent:#60a5fa;--bg:#0f172a;--card:#1e293b;--text:#e2e8f0;--border:#334155;--hover:#283548;--text-muted:#94a3b8;--code-bg:#0f172a;--input-bg:#0f172a;--shadow:0 4px 24px rgba(0,0,0,.3)}
[data-theme=light]{--bg:#f8fafc;--card:#fff;--text:#1e293b;--border:#e2e8f0;--hover:#f1f5f9;--text-muted:#64748b;--code-bg:#f1f5f9;--input-bg:#fff;--shadow:0 4px 24px rgba(0,0,0,.08)}
*{margin:0;padding:0;box-sizing:border-box}
body{font-family:-apple-system,BlinkMacSystemFont,'Segoe UI',Roboto,'Noto Sans SC',sans-serif;background:var(--bg);color:var(--text);line-height:1.6;transition:background .3s,color .3s}
.container{max-width:1200px;margin:0 auto;padding:1.5rem}
header{margin-bottom:2rem;display:flex;justify-content:space-between;align-items:flex-start;flex-wrap:wrap;gap:1rem}
.header-left{flex:1}
h1{font-size:1.8rem;margin-bottom:.3rem;background:linear-gradient(135deg,var(--accent),#a78bfa);-webkit-background-clip:text;-webkit-text-fill-color:transparent;background-clip:text}
.meta{color:var(--text-muted);font-size:.9rem}
.header-btns{display:flex;gap:.5rem;align-items:flex-start;flex-wrap:wrap}
.btn{background:var(--card);border:1px solid var(--border);color:var(--text);padding:.5rem 1rem;border-radius:8px;cursor:pointer;font-size:.85rem;display:flex;align-items:center;gap:.4rem;transition:all .2s;box-shadow:var(--shadow)}
.btn:hover{border-color:var(--accent);transform:translateY(-1px)}
.summary-grid{display:grid;grid-template-columns:repeat(auto-fit,minmax(140px,1fr));gap:1rem;margin-bottom:2rem}
.stat-card{background:var(--card);border-radius:12px;padding:1.2rem;text-align:center;border:1px solid var(--border);transition:all .2s;box-shadow:var(--shadow)}
.stat-card:hover{border-color:var(--accent);transform:translateY(-2px)}
.stat-card .value{font-size:2rem;font-weight:700}
.stat-card .label{color:var(--text-muted);font-size:.78rem;margin-top:.2rem;text-transform:uppercase;letter-spacing:.05em}
.s-pass .value{color:var(--pass)}.s-fail .value{color:var(--fail)}.s-skip .value{color:var(--skip)}.s-rate .value{color:var(--accent)}
.env-grid{display:grid;grid-template-columns:repeat(auto-fill,minmax(340px,1fr));gap:1.5rem;margin-bottom:2rem}
.env-card{background:var(--card);border-radius:14px;padding:1.4rem;border:1px solid var(--border);box-shadow:var(--shadow);transition:all .2s}
.env-card:hover{border-color:rgba(96,165,250,.4);transform:translateY(-2px)}
.env-card-header{display:flex;justify-content:space-between;align-items:center;margin-bottom:.8rem}
.env-name{font-size:1.1rem;font-weight:700;display:flex;align-items:center;gap:.5rem}
.env-badge{display:inline-block;padding:2px 10px;border-radius:20px;font-size:.72rem;font-weight:600;border:1px solid}
.env-ts{color:var(--text-muted);font-size:.78rem;font-family:monospace;margin-bottom:.8rem}
.env-stats{display:flex;gap:.5rem;flex-wrap:wrap;margin-bottom:.8rem}
.stat-pill{padding:3px 10px;border-radius:6px;font-size:.8rem;font-weight:600}
.sp{background:rgba(34,197,94,.15);color:var(--pass)}
.sf{background:rgba(239,68,68,.15);color:var(--fail)}
.ss{background:rgba(234,179,8,.15);color:var(--skip)}
.progress-bar{width:100%;height:6px;background:var(--border);border-radius:3px;margin:.5rem 0;overflow:hidden}
.progress-fill{height:100%;border-radius:3px}
.rate-label{font-size:.75rem;color:var(--text-muted);display:flex;justify-content:space-between;margin-bottom:.8rem}
.expand-btn{width:100%;background:var(--hover);border:1px solid var(--border);color:var(--text);padding:.4rem .8rem;border-radius:8px;cursor:pointer;font-size:.82rem;text-align:left;display:flex;justify-content:space-between;align-items:center;transition:background .2s}
.expand-btn:hover{background:var(--border)}
.run-list{margin-top:.6rem;border:1px solid var(--border);border-radius:8px;overflow:hidden;display:none}
.run-list.open{display:block}
.run-item{padding:.5rem .8rem;border-top:1px solid var(--border);font-size:.82rem;display:flex;justify-content:space-between;align-items:center;gap:.5rem;flex-wrap:wrap}
.run-item:first-child{border-top:none}
.run-item:hover{background:var(--hover)}
.run-ts{color:var(--text-muted);font-family:monospace;font-size:.75rem;min-width:130px}
.run-links{display:flex;gap:.4rem;flex-wrap:wrap;flex:1}
.run-link{color:var(--accent);text-decoration:none;font-size:.78rem;padding:2px 8px;border:1px solid rgba(96,165,250,.2);border-radius:4px;transition:all .2s;white-space:nowrap}
.run-link:hover{background:rgba(96,165,250,.1);border-color:var(--accent)}
.run-stats{display:flex;gap:.3rem;flex-shrink:0}
.mp{padding:1px 6px;border-radius:4px;font-size:.7rem;font-weight:600;background:rgba(34,197,94,.15);color:var(--pass)}
.mf{padding:1px 6px;border-radius:4px;font-size:.7rem;font-weight:600;background:rgba(239,68,68,.15);color:var(--fail)}
.ms{padding:1px 6px;border-radius:4px;font-size:.7rem;font-weight:600;background:rgba(234,179,8,.15);color:var(--skip)}
.toolbar{background:var(--card);border:1px solid var(--border);border-radius:12px;padding:1rem;margin-bottom:1.5rem;display:flex;gap:.8rem;align-items:center;flex-wrap:wrap;box-shadow:var(--shadow)}
.search-box{flex:1;min-width:200px;background:var(--input-bg);border:1px solid var(--border);color:var(--text);padding:.5rem 1rem;border-radius:8px;font-size:.9rem;outline:none;transition:border-color .2s}
.search-box:focus{border-color:var(--accent);box-shadow:0 0 0 3px rgba(96,165,250,.15)}
.search-box::placeholder{color:var(--text-muted)}
.empty-state{text-align:center;padding:3rem;color:var(--text-muted);font-size:1rem}
[data-lang=en] .zh{display:none}
[data-lang=zh] .en{display:none}
footer{margin-top:3rem;text-align:center;color:var(--text-muted);font-size:.8rem;padding:1.5rem;border-top:1px solid var(--border)}
footer a{color:var(--accent);text-decoration:none}
footer a:hover{text-decoration:underline}
@media(max-width:768px){.container{padding:1rem}.env-grid{grid-template-columns:1fr}.summary-grid{grid-template-columns:repeat(2,1fr)}.header-btns{flex-wrap:wrap}}
</style>
</head>
<body>
<div class="container">
HTMLHEAD

# ── B: Dynamic header + embedded data (unquoted — bash vars expand here) ──────
cat >> "$OUTPUT_HTML" << DYNBLOCK
<header>
  <div class="header-left">
    <h1><span class="zh">集成测试报告总览</span><span class="en">Integration Test Reports</span></h1>
    <p class="meta"><span class="zh">生成时间：</span><span class="en">Generated: </span>${GEN_TS}</p>
  </div>
  <div class="header-btns">
    <button class="btn" onclick="toggleTheme()" id="theme-btn">🌙 <span class="zh">暗色</span><span class="en">Dark</span></button>
    <button class="btn" onclick="toggleLang()">中文 / EN</button>
  </div>
</header>
<div class="toolbar">
  <input class="search-box" type="search" id="search" placeholder="搜索环境… / Filter env…" oninput="filterCards(this.value)">
</div>
<div class="summary-grid" id="summary-grid"></div>
<div class="env-grid" id="env-grid">
  <div class="empty-state"><span class="zh">暂无测试报告</span><span class="en">No test reports yet</span></div>
</div>
<script>
const ALL_ENV_DATA = ${all_data_json};
DYNBLOCK

# ── C: Static JS (single-quoted — template literals safe from bash expansion) ──
cat >> "$OUTPUT_HTML" << 'STATICJS'
const ENV_ZH = {
  docker:'Docker', podman:'Podman', containerd:'Containerd',
  lxd:'LXD', incus:'Incus', proxmoxve:'Proxmox VE'
};

function rateColor(r) {
  return r >= 95 ? 'var(--pass)' : r >= 80 ? 'var(--skip)' : 'var(--fail)';
}

function formatTs(ts) {
  const m = ts.match(/^(\d{4})(\d{2})(\d{2})-(\d{2})(\d{2})(\d{2})$/);
  return m ? `${m[1]}-${m[2]}-${m[3]} ${m[4]}:${m[5]}:${m[6]}` : ts;
}

function renderSummary(data) {
  const totalEnvs  = data.length;
  const totalRuns  = data.reduce((s,e) => s + e.runs.length, 0);
  const totalPass  = data.reduce((s,e) => s + e.pass, 0);
  const totalFail  = data.reduce((s,e) => s + e.fail, 0);
  const totalSkip  = data.reduce((s,e) => s + e.skip, 0);
  const totalTests = data.reduce((s,e) => s + e.total, 0);
  const overallRate = totalTests > 0 ? Math.round(totalPass * 100 / totalTests) : 0;
  document.getElementById('summary-grid').innerHTML = `
    <div class="stat-card"><div class="value">${totalEnvs}</div><div class="label"><span class="zh">环境数</span><span class="en">Envs</span></div></div>
    <div class="stat-card"><div class="value">${totalRuns}</div><div class="label"><span class="zh">总运行次数</span><span class="en">Runs</span></div></div>
    <div class="stat-card s-pass"><div class="value">${totalPass}</div><div class="label"><span class="zh">通过</span><span class="en">Passed</span></div></div>
    <div class="stat-card s-fail"><div class="value">${totalFail}</div><div class="label"><span class="zh">失败</span><span class="en">Failed</span></div></div>
    <div class="stat-card s-skip"><div class="value">${totalSkip}</div><div class="label"><span class="zh">跳过</span><span class="en">Skipped</span></div></div>
    <div class="stat-card s-rate"><div class="value">${overallRate}%</div><div class="label"><span class="zh">综合通过率</span><span class="en">Pass Rate</span></div></div>
  `;
  applyLang();
}

function renderCards(data) {
  const g = document.getElementById('env-grid');
  if (!data.length) return;
  g.innerHTML = '';
  data.forEach(env => {
    const zh = ENV_ZH[env.name] || env.name;
    const rc = rateColor(env.rate);
    const div = document.createElement('div');
    div.className = 'env-card';
    div.dataset.name = env.name;

    const runsHtml = env.runs.map(r => {
      const lh = r.links.map(l => {
        const fn = l.replace(/.*\//, '');
        const label = fn.replace(/-report\.html$/, '').replace(/-/g, ' ');
        return `<a href="${l}" class="run-link">${label}</a>`;
      }).join('');
      return `<div class="run-item">
        <span class="run-ts">${formatTs(r.ts)}</span>
        <div class="run-links">${lh}</div>
        <div class="run-stats">
          <span class="mp">✓${r.pass}</span>
          ${r.fail > 0 ? `<span class="mf">✗${r.fail}</span>` : ''}
          ${r.skip > 0 ? `<span class="ms">⊘${r.skip}</span>` : ''}
        </div>
      </div>`;
    }).join('');

    const skipHtml = env.skip > 0
      ? `<span class="stat-pill ss">⊘ ${env.skip} <span class="zh">跳过</span><span class="en">Skip</span></span>`
      : '';

    div.innerHTML = `
      <div class="env-card-header">
        <div class="env-name">
          <span>${zh}</span>
          <span class="env-badge" style="color:${rc};border-color:${rc}">${env.rate}%</span>
        </div>
      </div>
      <div class="env-ts"><span class="zh">最新：</span><span class="en">Latest: </span>${formatTs(env.latestTs)}</div>
      <div class="env-stats">
        <span class="stat-pill sp">✓ ${env.pass} <span class="zh">通过</span><span class="en">Pass</span></span>
        <span class="stat-pill sf">✗ ${env.fail} <span class="zh">失败</span><span class="en">Fail</span></span>
        ${skipHtml}
      </div>
      <div class="progress-bar">
        <div class="progress-fill" style="width:${env.rate}%;background:${rc}"></div>
      </div>
      <div class="rate-label">
        <span>${env.total} <span class="zh">项测试</span><span class="en">tests</span></span>
        <span style="color:${rc};font-weight:600">${env.rate}%</span>
      </div>
      <button class="expand-btn" onclick="toggleRuns(this)">
        <span><span class="zh">历史记录</span><span class="en">Run History</span> (${env.runs.length})</span>
        <span class="chev" style="transition:transform .3s">▼</span>
      </button>
      <div class="run-list">${runsHtml}</div>
    `;
    g.appendChild(div);
  });
  applyLang();
}

function toggleRuns(btn) {
  const list = btn.nextElementSibling;
  const chev = btn.querySelector('.chev');
  list.classList.toggle('open');
  chev.style.transform = list.classList.contains('open') ? 'rotate(180deg)' : '';
}

function filterCards(q) {
  const ql = q.toLowerCase();
  document.querySelectorAll('.env-card').forEach(c => {
    c.style.display = c.dataset.name.toLowerCase().includes(ql) ? '' : 'none';
  });
}

let lang = 'zh';
function applyLang() { document.documentElement.dataset.lang = lang; }
function toggleLang() { lang = lang === 'zh' ? 'en' : 'zh'; applyLang(); }

let dark = true;
function toggleTheme() {
  dark = !dark;
  document.documentElement.dataset.theme = dark ? '' : 'light';
  const btn = document.getElementById('theme-btn');
  btn.innerHTML = dark
    ? '🌙 <span class="zh">暗色</span><span class="en">Dark</span>'
    : '☀️ <span class="zh">亮色</span><span class="en">Light</span>';
  applyLang();
}

renderSummary(ALL_ENV_DATA);
renderCards(ALL_ENV_DATA);
</script>
STATICJS

# ── D: Footer (unquoted — expands ${GEN_TS}) ──────────────────────────────────
cat >> "$OUTPUT_HTML" << HTMLFOOTER
<footer>
  <p>
    <span class="zh">由 GitHub Actions 自动生成 · </span>
    <span class="en">Auto-generated by GitHub Actions · </span>
    <a href="https://github.com/oneclickvirt/oneclickvirt">OneClickVirt</a>
    <span class="zh"> · 最后更新：${GEN_TS}</span>
    <span class="en"> · Last updated: ${GEN_TS}</span>
  </p>
</footer>
</div>
</body>
</html>
HTMLFOOTER

echo "Index generated: ${OUTPUT_HTML} (${#env_json_parts[@]} environments)"
