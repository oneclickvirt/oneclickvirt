#!/usr/bin/env bash
set -euo pipefail
python3 - "${SKILL_DIR:-skills}" "${1:-}" <<'PY'
import json
from pathlib import Path
import re
import sys

root, requested = Path(sys.argv[1]), sys.argv[2].removeprefix('v')
versions = []
for name in ('SKILL.md', 'SKILL_ZH.md'):
    text = (root / name).read_text()
    assert text.startswith('---\n'), 'missing frontmatter: ' + name
    meta = text.split('---\n', 2)[1]
    assert re.search(r'^name: oneclickvirt$', meta, re.M), 'invalid Skill name'
    assert re.search(r'^description: .+', meta, re.M), 'missing description'
    keys = re.findall(r'^([A-Za-z][\w-]*):', meta, re.M)
    assert set(keys) <= {'name', 'description', 'metadata'}, 'unsupported frontmatter keys'
    version = re.search(r'^  version: "([^"]+)"$', meta, re.M)
    assert version, 'missing version metadata'
    versions.append(version[1])
    assert 'install_skill.py' in text and '--version' in text, 'missing release-aware installer'
    assert 'get_metrics' in text and 'oneclickvirt://config/system' in text, 'missing MCP capabilities'
assert versions[0] == versions[1], 'EN/ZH versions differ'
assert not requested or versions[0] == requested, 'Skill version differs from release tag'
config = json.loads(Path('mcp.json').read_text())['mcpServers']['oneclickvirt']
assert config['command'] == 'oneclickvirt-mcp' and config['args'] == ['mcp']
assert set(config['env']) == {'ONE_CLICK_VIRT_API_URL', 'ONE_CLICK_VIRT_API_TOKEN'}
print('Skill release checks passed: ' + versions[0])
PY
