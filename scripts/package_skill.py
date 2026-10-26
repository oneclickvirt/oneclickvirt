#!/usr/bin/env python3
"""Package the current installer with MCP definitions from an exact release source."""
import argparse
import hashlib
import io
import json
from pathlib import Path
import subprocess
import tarfile

from generate_skills import generate_skill_md, parse_prompts, parse_resources, parse_tools
from install_skill import REPOSITORY, valid_tag

ROOT = Path(__file__).resolve().parents[1]


def package(source, tag, output):
    valid_tag(tag)
    source = Path(source).resolve()
    source_commit = subprocess.check_output(["git", "-C", str(source), "rev-parse", "HEAD"], text=True).strip()
    text = (source / "server/mcp/server.go").read_text()
    tools, resources, prompts = parse_tools(text), parse_resources(text), parse_prompts(text)
    if len(tools) < 5 or len(resources) < 3:
        raise ValueError("Release MCP definitions could not be parsed")
    files = {
        "SKILL.md": generate_skill_md(tag.removeprefix("v"), tools, resources, prompts).encode(),
        "SKILL_ZH.md": generate_skill_md(tag.removeprefix("v"), tools, resources, prompts, lang="zh").encode(),
        "mcp.example.json": (ROOT / "mcp.json").read_bytes(),
        "scripts/install_skill.py": (ROOT / "scripts/install_skill.py").read_bytes(),
        "release.json": (json.dumps({"schema_version": 1, "repository": REPOSITORY,
                                     "tag": tag, "source_commit": source_commit}, indent=2) + "\n").encode(),
    }
    # Fixed tar metadata makes repeated reconciliation idempotent.
    buffer = io.BytesIO()
    with tarfile.open(fileobj=buffer, mode="w", format=tarfile.PAX_FORMAT) as archive:
        for name, value in sorted(files.items()):
            entry = tarfile.TarInfo("oneclickvirt/" + name)
            entry.size = len(value)
            entry.mode = 0o644
            entry.mtime = 0
            archive.addfile(entry, io.BytesIO(value))
    import gzip
    data = gzip.compress(buffer.getvalue(), mtime=0)
    output = Path(output)
    output.mkdir(parents=True, exist_ok=True)
    for stem in ("oneclickvirt-skill-" + tag, "oneclickvirt-skill"):
        filename = stem + ".tar.gz"
        (output / filename).write_bytes(data)
        (output / (stem + ".sha256")).write_text(hashlib.sha256(data).hexdigest() + "  " + filename + "\n")
    print("Packaged Skill " + tag + " from source " + source_commit)
    return files


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--source-root", type=Path, default=ROOT)
    parser.add_argument("--version", required=True)
    parser.add_argument("--output-dir", type=Path, default=ROOT / "dist")
    args = parser.parse_args()
    package(args.source_root, args.version, args.output_dir)


if __name__ == "__main__":
    main()
