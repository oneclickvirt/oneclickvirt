#!/usr/bin/env python3
"""Install a release-matched Skill and optional MCP binary without changing services."""
import argparse
import hashlib
import io
import json
import os
from pathlib import Path, PurePosixPath
import platform
import re
import sys
import tarfile
import tempfile
import time
import urllib.error
import urllib.parse
import urllib.request
import zipfile

REPOSITORY = "oneclickvirt/oneclickvirt"
API = "https://api.github.com/repos/" + REPOSITORY + "/releases/"
FILES = {"SKILL.md", "SKILL_ZH.md", "mcp.example.json", "release.json",
         "scripts/install_skill.py"}


def download(url, limit=128 * 1024 * 1024):
    if not url.startswith("https://"):
        raise ValueError("Downloads require HTTPS")
    url += ("&" if "?" in url else "?") + "_skill_check=" + str(time.time_ns())
    for attempt in range(3):
        try:
            request = urllib.request.Request(url, headers={
                "User-Agent": "oneclickvirt-skill-installer", "Cache-Control": "no-cache"})
            with urllib.request.urlopen(request, timeout=45) as response:
                value = response.read(limit + 1)
            if len(value) > limit:
                raise ValueError("Download exceeds size limit")
            return value
        except (urllib.error.URLError, TimeoutError):
            if attempt == 2:
                raise
            time.sleep(2 ** attempt)


def valid_tag(tag):
    if not isinstance(tag, str) or not re.fullmatch(r"v?[0-9][A-Za-z0-9._+-]*", tag):
        raise ValueError("Invalid release tag")
    return tag


def checksum(data, manifest, name):
    matches = []
    for line in manifest.decode("utf-8").splitlines():
        match = re.fullmatch(r"([0-9a-fA-F]{64}) [ *](.+)", line)
        if match and match[2] == name:
            matches.append(match[1].lower())
    if len(matches) != 1 or hashlib.sha256(data).hexdigest() != matches[0]:
        raise ValueError("SHA-256 verification failed for " + name)


def skill_files(data, tag):
    files = {}
    with tarfile.open(fileobj=io.BytesIO(data), mode="r:gz") as archive:
        for member in archive.getmembers():
            path = PurePosixPath(member.name)
            if path.is_absolute() or ".." in path.parts or path.parts[0] != "oneclickvirt":
                raise ValueError("Invalid Skill archive path")
            if member.isdir():
                continue
            name = str(PurePosixPath(*path.parts[1:]))
            if not member.isfile() or name not in FILES or name in files or member.size > 2 * 1024 * 1024:
                raise ValueError("Invalid or duplicate Skill archive member")
            files[name] = archive.extractfile(member).read()
    if files.keys() != FILES:
        raise ValueError("Incomplete Skill archive")
    metadata = json.loads(files["release.json"])
    if metadata.get("tag") != tag or metadata.get("repository") != REPOSITORY:
        raise ValueError("Skill archive belongs to another release")
    for name in ("SKILL.md", "SKILL_ZH.md"):
        text = files[name].decode("utf-8")
        if not text.startswith("---\n") or "name: oneclickvirt\n" not in text.split("---\n", 2)[1]:
            raise ValueError("Invalid Skill entrypoint")
    return files


def binary_asset(system=None, machine=None):
    system = (system or platform.system()).lower()
    machine = (machine or platform.machine()).lower()
    architecture = {"x86_64": "amd64", "amd64": "amd64", "aarch64": "arm64", "arm64": "arm64"}.get(machine)
    if system not in ("linux", "windows") or architecture is None:
        raise ValueError("Prebuilt MCP binaries support Linux/Windows amd64/arm64. "
                         "Use --skip-binary and configure an existing client or SSH transport on other platforms.")
    stem = "server-" + system + "-" + architecture
    return stem + (".zip" if system == "windows" else ".tar.gz"), stem + (".exe" if system == "windows" else "")


def unpack_binary(data, name, member):
    if name.endswith(".zip"):
        with zipfile.ZipFile(io.BytesIO(data)) as archive:
            entries = archive.infolist()
            if len(entries) != 1 or entries[0].filename != member or entries[0].file_size > 256 * 1024 * 1024:
                raise ValueError("Unexpected binary archive contents")
            return archive.read(member)
    with tarfile.open(fileobj=io.BytesIO(data), mode="r:gz") as archive:
        entries = archive.getmembers()
        if len(entries) != 1 or entries[0].name != member or not entries[0].isfile() or entries[0].size > 256 * 1024 * 1024:
            raise ValueError("Unexpected binary archive contents")
        return archive.extractfile(entries[0]).read()


def atomic_write(path, data, mode):
    path.parent.mkdir(parents=True, exist_ok=True)
    descriptor, temporary = tempfile.mkstemp(prefix="." + path.name + ".", dir=path.parent)
    try:
        with os.fdopen(descriptor, "wb") as output:
            output.write(data)
        os.chmod(temporary, mode)
        os.replace(temporary, path)
    finally:
        if os.path.exists(temporary):
            os.unlink(temporary)


def install(args, fetch=download):
    # Resolve once. All archives/checksums below come from this exact release,
    # even if another release becomes latest while the installation is running.
    selected = "latest" if args.version == "latest" else "tags/" + urllib.parse.quote(valid_tag(args.version), safe="")
    release = json.loads(fetch(API + selected, 2 * 1024 * 1024))
    tag = valid_tag(release["tag_name"])
    if release.get("draft") or (args.version == "latest" and release.get("prerelease")):
        raise ValueError("Expected a published stable release")
    if args.version != "latest" and tag != args.version:
        raise ValueError("Release tag mismatch")
    assets = {}
    for asset in release.get("assets", []):
        name = asset["name"]
        expected = "https://github.com/" + REPOSITORY + "/releases/download/" + urllib.parse.quote(tag, safe="") + "/"
        if name in assets or not asset["browser_download_url"].startswith(expected):
            raise ValueError("Unexpected release asset")
        assets[name] = asset["browser_download_url"]

    def get(name, limit=128 * 1024 * 1024):
        if name not in assets:
            raise ValueError("Release " + tag + " is missing " + name)
        return fetch(assets[name], limit)

    archive_name = "oneclickvirt-skill-" + tag + ".tar.gz"
    package = get(archive_name, 5 * 1024 * 1024)
    checksum(package, get("oneclickvirt-skill-" + tag + ".sha256", 16384), archive_name)
    files = skill_files(package, tag)
    runtime = None
    command = args.mcp_command
    runtime_name = None
    if not args.skip_binary and not command:
        runtime_name, member = binary_asset()
        compressed = get(runtime_name)
        checksum(compressed, get("SHA256SUMS", 65536), runtime_name)
        runtime = unpack_binary(compressed, runtime_name, member)
        command = str(args.bin_dir.expanduser().resolve() / ("oneclickvirt-mcp.exe" if member.endswith(".exe") else "oneclickvirt-mcp"))
    elif command:
        command = str(Path(command).expanduser().resolve())
        if not Path(command).is_file():
            raise ValueError("--mcp-command must name an existing local binary")
    config = json.loads(files["mcp.example.json"])
    config["mcpServers"]["oneclickvirt"]["command"] = command or "oneclickvirt-mcp"
    files["mcp.example.json"] = (json.dumps(config, indent=2) + "\n").encode()
    record = {"tag": tag, "repository": REPOSITORY, "binary_asset": runtime_name,
              "binary_sha256": hashlib.sha256(runtime).hexdigest() if runtime else None}
    # No files are changed until every selected archive has been verified.
    if runtime is not None:
        atomic_write(Path(command), runtime, 0o755)
    destination = args.skill_dir.expanduser().resolve()
    for name, value in files.items():
        atomic_write(destination / name, value, 0o644)
    atomic_write(destination / "install.json", (json.dumps(record, indent=2) + "\n").encode(), 0o644)
    print("Installed OneClickVirt Skill " + tag + " into " + str(destination))
    print("MCP configuration example: " + str(destination / "mcp.example.json"))
    print("Set your API URL/token in your client's MCP configuration; existing client configuration was not edited.")
    if args.skip_binary and not command:
        print("Binary installation skipped. Configure an existing MCP binary or an SSH transport before use.")
    return record


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--version", default="latest", help="latest stable release, or an exact published tag")
    parser.add_argument("--client", choices=("claude", "codex", "openclaw"), default="claude")
    parser.add_argument("--skill-dir", type=Path, help="explicit Skill directory; overrides --client")
    parser.add_argument("--bin-dir", type=Path, default=Path.home() / ".local/bin")
    parser.add_argument("--skip-binary", action="store_true", help="install only Skill files, without a prebuilt MCP client")
    parser.add_argument("--mcp-command", help="reuse an existing binary by absolute path instead of downloading one")
    args = parser.parse_args()
    if args.skill_dir is None:
        root = {"claude": ".claude", "codex": ".agents", "openclaw": ".openclaw"}[args.client]
        args.skill_dir = Path.home() / root / "skills/oneclickvirt"
    try:
        install(args)
    except (ValueError, KeyError, OSError, tarfile.TarError, zipfile.BadZipFile) as error:
        print("Installation failed: " + str(error), file=sys.stderr)
        return 1
    return 0


if __name__ == "__main__":
    sys.exit(main())
