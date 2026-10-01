#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
TMP_DIR="$(mktemp -d)"
trap 'rm -rf "$TMP_DIR"' EXIT

awk '
  /^write_agent_env\(\) \{/ { capture=1 }
  capture { print }
  capture && /^}$/ { exit }
' "$ROOT_DIR/scripts/install_agent.sh" > "$TMP_DIR/write_agent_env.sh"
sh -n "$TMP_DIR/write_agent_env.sh"

INSTALL_DIR="$TMP_DIR"
WS_URL='wss://controller.example.test/api/v1/ws/agent'
SECRET='new-install-secret'
AGENT_SOURCE='controller'
CONTROLLER_BASE_URL='https://controller.example.test/releases'
cat > "$TMP_DIR/env" <<'ENV'
WS_URL=wss://old.example.test/api/v1/ws/agent
AGENT_SECRET=old-secret
API_TOKEN=old-token
ENABLE_REVERSE_PROXY=true
PROXY_HTTP_ADDR=0.0.0.0:80
PROXY_TRUST_CLOUDFLARE_HEADERS=true
CUSTOM_SETTING=keep-me
ENV

# shellcheck disable=SC1091
. "$TMP_DIR/write_agent_env.sh"
write_agent_env
grep -Fxq 'WS_URL=wss://controller.example.test/api/v1/ws/agent' "$TMP_DIR/env"
grep -Fxq 'AGENT_SECRET=new-install-secret' "$TMP_DIR/env"
grep -Fxq 'API_TOKEN=new-install-secret' "$TMP_DIR/env"
grep -Fxq 'AGENT_SOURCE=controller' "$TMP_DIR/env"
grep -Fxq 'ENABLE_REVERSE_PROXY=true' "$TMP_DIR/env"
grep -Fxq 'PROXY_HTTP_ADDR=0.0.0.0:80' "$TMP_DIR/env"
grep -Fxq 'PROXY_TRUST_CLOUDFLARE_HEADERS=true' "$TMP_DIR/env"
grep -Fxq 'CUSTOM_SETTING=keep-me' "$TMP_DIR/env"
test "$(grep -c '^API_TOKEN=' "$TMP_DIR/env")" -eq 1
test "$(stat -c %a "$TMP_DIR/env" 2>/dev/null || stat -f %Lp "$TMP_DIR/env")" = 600

SECRET='second-install-secret'
AGENT_SOURCE='github'
CONTROLLER_BASE_URL=''
write_agent_env
grep -Fxq 'AGENT_SECRET=second-install-secret' "$TMP_DIR/env"
grep -Fxq 'API_TOKEN=second-install-secret' "$TMP_DIR/env"
grep -Fxq 'AGENT_SOURCE=github' "$TMP_DIR/env"
grep -Fxq 'ENABLE_REVERSE_PROXY=true' "$TMP_DIR/env"
test "$(grep -c '^PROXY_HTTP_ADDR=' "$TMP_DIR/env")" -eq 1
test "$(grep -c '^CUSTOM_SETTING=' "$TMP_DIR/env")" -eq 1
printf '%s\n' 'reverse Agent reinstall environment: PASS'
