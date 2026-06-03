#!/bin/bash
set -uo pipefail

SKILL_DIR="${SKILL_DIR:-skills}"
VERSION="${1:-}"
ERRORS=0

log_pass() { printf '[PASS] %s\n' "$*"; }
log_fail() { printf '[FAIL] %s\n' "$*" >&2; ERRORS=$((ERRORS + 1)); }

check_file() {
    local file="$1"
    [[ -f "$file" ]] && log_pass "$file exists" || log_fail "$file missing"
}

frontmatter_value() {
    local file="$1" key="$2"
    awk -v key="$key" '
        NR == 1 && $0 == "---" { in_meta=1; next }
        in_meta && $0 == "---" { exit }
        in_meta && $0 ~ "^" key ":" {
            sub("^" key ":[[:space:]]*", "")
            gsub(/^"|"$/, "")
            print
            exit
        }
    ' "$file"
}

validate_frontmatter() {
    local file="$1"
    python3 - "$file" <<'PY'
import re
import sys

path = sys.argv[1]
text = open(path, encoding="utf-8").read()
if not text.startswith("---\n"):
    raise SystemExit("missing opening frontmatter")
parts = text.split("---\n", 2)
if len(parts) < 3:
    raise SystemExit("missing closing frontmatter")
meta = parts[1]
required = ["name", "description", "version", "author", "homepage", "tags", "applyTo", "platforms", "mcpServers"]
for key in required:
    if not re.search(rf"^{re.escape(key)}:", meta, re.MULTILINE):
        raise SystemExit(f"missing {key}")
if "oneclickvirt:" not in meta or "ONE_CLICK_VIRT_API_URL:" not in meta or "ONE_CLICK_VIRT_API_TOKEN:" not in meta:
    raise SystemExit("missing oneclickvirt MCP server env")
PY
}

echo "=== 1. File completeness ==="
check_file "$SKILL_DIR/SKILL.md"
check_file "$SKILL_DIR/SKILL_ZH.md"
check_file "mcp.json"

echo "=== 2. Frontmatter ==="
for file in "$SKILL_DIR/SKILL.md" "$SKILL_DIR/SKILL_ZH.md"; do
    if validate_frontmatter "$file"; then
        log_pass "$file frontmatter valid"
    else
        log_fail "$file frontmatter invalid"
    fi
done

echo "=== 3. MCP config ==="
if python3 -m json.tool mcp.json >/dev/null; then
    log_pass "mcp.json valid JSON"
else
    log_fail "mcp.json invalid JSON"
fi

echo "=== 4. EN/ZH structure ==="
en_headers=$(grep -c '^##' "$SKILL_DIR/SKILL.md" 2>/dev/null || true)
zh_headers=$(grep -c '^##' "$SKILL_DIR/SKILL_ZH.md" 2>/dev/null || true)
if [[ "$en_headers" -eq "$zh_headers" && "$en_headers" -ge 5 ]]; then
    log_pass "header count match: $en_headers"
else
    log_fail "header mismatch or too few headers: EN=$en_headers ZH=$zh_headers"
fi

echo "=== 5. Version consistency ==="
skill_version=$(frontmatter_value "$SKILL_DIR/SKILL.md" "version")
skill_zh_version=$(frontmatter_value "$SKILL_DIR/SKILL_ZH.md" "version")
if [[ "$skill_version" == "$skill_zh_version" && -n "$skill_version" ]]; then
    log_pass "skill versions match: $skill_version"
else
    log_fail "skill version mismatch: EN=$skill_version ZH=$skill_zh_version"
fi
if [[ -n "$VERSION" ]]; then
    normalized="${VERSION#v}"
    [[ "$skill_version" == "$normalized" || "$skill_version" == "$VERSION" ]] && log_pass "tag version matches skill" || log_fail "tag=$VERSION skill=$skill_version"
fi

echo "=== 6. Required content ==="
grep -q 'get_metrics' "$SKILL_DIR/SKILL.md" && log_pass "tools documented" || log_fail "get_metrics missing"
grep -q 'oneclickvirt://config/system' "$SKILL_DIR/SKILL.md" && log_pass "resources documented" || log_fail "config resource missing"
grep -q 'Troubleshooting' "$SKILL_DIR/SKILL.md" && log_pass "troubleshooting documented" || log_fail "troubleshooting missing"

echo ""
if [[ "$ERRORS" -eq 0 ]]; then
    echo "All skill release checks passed."
else
    echo "$ERRORS skill release check(s) failed." >&2
    exit 1
fi
