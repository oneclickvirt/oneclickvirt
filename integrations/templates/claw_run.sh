#!/usr/bin/env bash
# Keep the existing entry point; release resolution and checksums are shared
# with the maintained agent installer.
set -euo pipefail
claw_script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
exec python3 "$claw_script_dir/scripts/run_goecs.py" "$@"
