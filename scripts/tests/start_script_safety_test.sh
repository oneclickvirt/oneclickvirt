#!/usr/bin/env bash
set -Eeuo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
TEST_DIR="$(mktemp -d)"
trap 'rm -rf "$TEST_DIR"' EXIT

cp "$ROOT_DIR/start.sh" "$TEST_DIR/start.sh"
chmod +x "$TEST_DIR/start.sh"
mkdir -p "$TEST_DIR/server/storage/logs" "$TEST_DIR/server/logs" "$TEST_DIR/web/logs" "$TEST_DIR/other"

# The script must resolve its own root rather than using the caller's cwd.
printf 'server log\n' > "$TEST_DIR/server/storage/logs/server.log"
printf 'keep this non-log file\n' > "$TEST_DIR/server/storage/logs/keep.txt"
printf 'hidden non-log\n' > "$TEST_DIR/server/storage/logs/.keep"
printf 'nested log\n' > "$TEST_DIR/other/nested.log"
printf 'web log\n' > "$TEST_DIR/web/logs/web.log"

(cd /tmp && bash "$TEST_DIR/start.sh" logs >/dev/null)

# Invalid or stale PID files must never be passed to kill, and status should
# clean only the PID marker while leaving the rest of the project untouched.
printf 'not-a-pid\n' > "$TEST_DIR/.server.pid"
printf '%s\n' "$$" > "$TEST_DIR/.web.pid"
bash "$TEST_DIR/start.sh" status >/dev/null
[[ ! -e "$TEST_DIR/.server.pid" && ! -e "$TEST_DIR/.web.pid" ]]

# cleanlogs removes only regular *.log files.  It must preserve arbitrary
# non-log files in the log directory and must also handle logs outside the
# three conventional directories.
printf 'yes\n' | bash "$TEST_DIR/start.sh" cleanlogs >/dev/null
[[ ! -e "$TEST_DIR/server/storage/logs/server.log" ]]
[[ ! -e "$TEST_DIR/web/logs/web.log" ]]
[[ ! -e "$TEST_DIR/other/nested.log" ]]
[[ -e "$TEST_DIR/server/storage/logs/keep.txt" ]]
[[ -e "$TEST_DIR/server/storage/logs/.keep" ]]

grep -Fq 'return 1' "$ROOT_DIR/start.sh"
grep -Fq 'if ! start_server' "$ROOT_DIR/start.sh"
grep -Fq 'if ! start_web' "$ROOT_DIR/start.sh"
grep -Fq 'local service="${1:-all}"' "$ROOT_DIR/start.sh"

echo 'start.sh safety and failure-propagation tests passed'
