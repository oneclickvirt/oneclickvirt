#!/usr/bin/env bash
set -euo pipefail
claw_script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
claw_pack_dir=$(mktemp -d)
trap 'rm -rf "$claw_pack_dir"' EXIT
for file in SKILL.md skill.json run.sh analyze.sh README.md; do
    cp "$claw_script_dir/$file" "$claw_pack_dir/"
done
cp -R "$claw_script_dir/agent" "$claw_script_dir/scripts" "$claw_script_dir/skills" "$claw_pack_dir/"
chmod +x "$claw_pack_dir/run.sh" "$claw_pack_dir/analyze.sh"
python3 - "$claw_pack_dir" "$claw_script_dir/clawchimera.zip" <<'PY'
from pathlib import Path
import sys, zipfile
root = Path(sys.argv[1])
with zipfile.ZipFile(sys.argv[2], 'w', zipfile.ZIP_DEFLATED) as archive:
    for path in sorted(root.rglob('*')):
        if path.is_file() and '__pycache__' not in path.parts:
            archive.write(path, path.relative_to(root))
print('clawchimera.zip')
PY
