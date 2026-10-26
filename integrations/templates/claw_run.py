#!/usr/bin/env python3
"""Resolve and verify the current ECS artifact before forwarding CLI arguments."""
import json
import os
from pathlib import Path
import subprocess
import sys
import install_agent


def main():
    root = Path(__file__).resolve().parents[1]
    profile = json.loads((root / 'agent/profile.json').read_text(encoding='utf-8'))
    content, record = install_agent.release_binary(profile, os.environ.get('ECS_VERSION', 'latest'))
    cache = Path.home() / '.cache/clawchimera' / record['binary_sha256']
    binary = cache / ('goecs.exe' if os.name == 'nt' else 'goecs')
    install_agent.atomic_write(binary, content)
    if os.name != 'nt':
        binary.chmod(0o755)
    arguments = sys.argv[1:]
    if not any(a.startswith('-menu') for a in arguments):
        arguments.insert(0, '-menu=false')
    # Flags remain the native ECS flags, including explicit benchmark/upload
    # choices. The MCP system-only preset is a separate interface.
    return subprocess.call([str(binary)] + arguments)


if __name__ == '__main__':
    try:
        sys.exit(main())
    except (ValueError, OSError, subprocess.SubprocessError) as exc:
        print('ClawChimera: ' + str(exc), file=sys.stderr)
        sys.exit(1)
