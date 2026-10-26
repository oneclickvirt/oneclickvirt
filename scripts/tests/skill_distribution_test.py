#!/usr/bin/env python3
"""Exercise packaging, release selection and installation with isolated assets."""
import argparse
import contextlib
import hashlib
import io
import json
from pathlib import Path
import subprocess
import sys
import tarfile
import tempfile
import unittest
from unittest.mock import patch
import zipfile

ROOT = Path(__file__).resolve().parents[2]
sys.path.insert(0, str(ROOT / "scripts"))
import install_skill as installer
from package_skill import package


def tar_bytes(name, data, link=None):
    output = io.BytesIO()
    with tarfile.open(fileobj=output, mode="w:gz") as archive:
        member = tarfile.TarInfo(name)
        if link:
            member.type, member.linkname = tarfile.SYMTYPE, link
        else:
            member.size = len(data)
        archive.addfile(member, None if link else io.BytesIO(data))
    return output.getvalue()


class DistributionTests(unittest.TestCase):
    def setUp(self):
        self.temporary = tempfile.TemporaryDirectory()
        self.addCleanup(self.temporary.cleanup)
        self.root = Path(self.temporary.name)
        self.args = argparse.Namespace(version="latest", skill_dir=self.root / "skill",
            bin_dir=self.root / "bin", skip_binary=False, mcp_command=None)
        self.assets = {}
        self.releases = {}
        self.calls = []
        self.latest = "v20261021-061200"
        self.add_release(self.latest)
        self.addCleanup(patch.stopall)
        patch.object(installer.platform, "system", return_value="Linux").start()
        patch.object(installer.platform, "machine", return_value="x86_64").start()

    def add_release(self, tag, binary=b"release-binary"):
        output = self.root / tag
        with contextlib.redirect_stdout(io.StringIO()):
            package(ROOT, tag, output)
        values = {p.name: p.read_bytes() for p in output.iterdir()}
        for system in ("linux", "windows"):
            for machine in ("amd64", "arm64"):
                asset, member = installer.binary_asset(system, machine)
                if asset.endswith(".zip"):
                    buffer = io.BytesIO()
                    with zipfile.ZipFile(buffer, "w") as archive:
                        archive.writestr(member, binary)
                    values[asset] = buffer.getvalue()
                else:
                    values[asset] = tar_bytes(member, binary)
        values["SHA256SUMS"] = "".join(hashlib.sha256(data).hexdigest() + "  " + name + "\n"
                                        for name, data in values.items() if name.startswith("server-")).encode()
        prefix = "https://github.com/" + installer.REPOSITORY + "/releases/download/" + tag + "/"
        self.assets.update({prefix + name: value for name, value in values.items()})
        self.releases[tag] = {"tag_name": tag, "draft": False, "prerelease": False,
            "assets": [{"name": name, "browser_download_url": prefix + name} for name in values]}
        return prefix

    def fetch(self, url, limit):
        self.calls.append(url)
        if url == installer.API + "latest":
            return json.dumps(self.releases[self.latest]).encode()
        if url.startswith(installer.API + "tags/"):
            return json.dumps(self.releases[url.rsplit("/", 1)[1]]).encode()
        value = self.assets[url]
        self.assertLessEqual(len(value), limit)
        return value

    def install(self, fetch=None):
        with contextlib.redirect_stdout(io.StringIO()):
            return installer.install(self.args, fetch or self.fetch)

    def test_latest_installs_one_directory_and_absolute_mcp_path(self):
        record = self.install()
        self.assertEqual(record["tag"], self.latest)
        self.assertTrue((self.args.skill_dir / "SKILL.md").is_file())
        self.assertFalse((self.args.skill_dir / "oneclickvirt").exists())
        binary = self.args.bin_dir / "oneclickvirt-mcp"
        self.assertEqual(binary.read_bytes(), b"release-binary")
        self.assertTrue(binary.stat().st_mode & 0o100)
        config = json.loads((self.args.skill_dir / "mcp.example.json").read_text())
        self.assertEqual(config["mcpServers"]["oneclickvirt"]["command"], str(binary.resolve()))

    def test_update_preserves_local_extras_and_existing_credentials(self):
        self.install()
        custom = self.args.skill_dir / "mcp.json"
        custom.write_text('{"local_token": "preserve-this-fixture"}')
        notes = self.args.skill_dir / "local-notes.md"
        notes.write_text("local work")
        self.add_release("v20261101-000001", b"new-binary")
        self.latest = "v20261101-000001"
        self.install()
        self.assertIn("preserve-this-fixture", custom.read_text())
        self.assertEqual(notes.read_text(), "local work")
        self.assertEqual((self.args.bin_dir / "oneclickvirt-mcp").read_bytes(), b"new-binary")
        self.assertEqual(json.loads((self.args.skill_dir / "install.json").read_text())["tag"], self.latest)

    def test_generated_client_examples_have_native_shapes(self):
        self.install()
        directory = self.args.skill_dir
        claude = json.loads((directory / "mcp.claude.json").read_text())["mcpServers"]["oneclickvirt"]
        claw = json.loads((directory / "mcp.openclaw.json").read_text())["mcp"]["servers"]["oneclickvirt"]
        self.assertEqual(claude["command"], claw["command"])
        try:
            import tomllib
        except ImportError:
            return
        codex = tomllib.loads((directory / "mcp.codex.toml").read_text(encoding="utf-8"))["mcp_servers"]["oneclickvirt"]
        self.assertEqual(codex["args"], claude["args"])
        self.assertEqual(codex["env"], claude["env"])

    def test_exact_tag_can_return_to_an_older_release(self):
        old = self.latest
        self.add_release("v20261101-000001", b"new-binary")
        self.latest = "v20261101-000001"
        self.install()
        self.args.version = old
        self.assertEqual(self.install()["tag"], old)
        self.assertEqual((self.args.bin_dir / "oneclickvirt-mcp").read_bytes(), b"release-binary")

    def test_latest_is_resolved_once_even_if_a_release_arrives_mid_download(self):
        original = self.latest
        self.add_release("v20261101-000001", b"new-binary")
        def advancing(url, limit):
            value = self.fetch(url, limit)
            if url == installer.API + "latest":
                self.latest = "v20261101-000001"
            return value
        self.assertEqual(self.install(advancing)["tag"], original)
        self.assertEqual(self.calls.count(installer.API + "latest"), 1)
        self.assertFalse(any("v20261101-000001/" in call for call in self.calls))

    def test_bad_binary_checksum_preserves_existing_installation(self):
        self.install()
        old = (self.args.skill_dir / "SKILL.md").read_bytes()
        prefix = self.add_release("v20261101-000001", b"new-binary")
        self.latest = "v20261101-000001"
        self.assets[prefix + "server-linux-amd64.tar.gz"] += b"corrupt"
        with self.assertRaisesRegex(ValueError, "SHA-256"):
            self.install()
        self.assertEqual((self.args.skill_dir / "SKILL.md").read_bytes(), old)
        self.assertEqual((self.args.bin_dir / "oneclickvirt-mcp").read_bytes(), b"release-binary")

    def test_bad_skill_checksum_and_missing_assets_write_nothing(self):
        for failure in ("checksum", "missing"):
            with self.subTest(failure=failure):
                prefix = self.add_release(self.latest)
                if failure == "checksum":
                    self.assets[prefix + "oneclickvirt-skill-" + self.latest + ".tar.gz"] += b"corrupt"
                else:
                    self.releases[self.latest]["assets"] = []
                with self.assertRaises(ValueError):
                    self.install()
                self.assertFalse(self.args.skill_dir.exists())
                self.assertFalse(self.args.bin_dir.exists())

    def test_archive_cannot_escape_create_links_or_mix_release_versions(self):
        for name, link in (("../escape", None), ("/escape", None), ("oneclickvirt/SKILL.md", "/etc/passwd")):
            with self.subTest(name=name):
                with self.assertRaises(ValueError):
                    installer.skill_files(tar_bytes(name, b"x", link), self.latest)
        package_data = self.assets[next(k for k in self.assets if k.endswith("oneclickvirt-skill.tar.gz"))]
        with self.assertRaisesRegex(ValueError, "another release"):
            installer.skill_files(package_data, "v20990101-000001")

    def test_archive_rejects_duplicate_members(self):
        output = io.BytesIO()
        with tarfile.open(fileobj=output, mode="w:gz") as archive:
            for _ in range(2):
                entry = tarfile.TarInfo("oneclickvirt/SKILL.md")
                entry.size = 1
                archive.addfile(entry, io.BytesIO(b"x"))
        with self.assertRaisesRegex(ValueError, "duplicate"):
            installer.skill_files(output.getvalue(), self.latest)

    def test_windows_and_arm_clients_select_the_actual_release_filenames(self):
        for system, architecture, filename in (("Windows", "AMD64", "server-windows-amd64.zip"),
                                               ("Linux", "aarch64", "server-linux-arm64.tar.gz")):
            with self.subTest(system=system):
                installer.platform.system.return_value = system
                installer.platform.machine.return_value = architecture
                self.assertEqual(self.install()["binary_asset"], filename)

    def test_unsupported_platform_requires_explicit_skill_only_mode(self):
        installer.platform.system.return_value = "Darwin"
        with self.assertRaisesRegex(ValueError, "--skip-binary"):
            self.install()
        self.assertFalse(self.args.skill_dir.exists())
        self.args.skip_binary = True
        self.assertIsNone(self.install()["binary_asset"])
        self.assertFalse(self.args.bin_dir.exists())

    def test_existing_controller_path_does_not_download_or_replace_it(self):
        binary = self.root / "existing-controller"
        binary.write_bytes(b"existing-controller")
        self.args.mcp_command = str(binary)
        self.install()
        self.assertEqual(binary.read_bytes(), b"existing-controller")
        self.assertFalse(any(url.endswith("server-linux-amd64.tar.gz") for url in self.calls))

    def test_draft_prerelease_and_wrong_origin_assets_are_rejected(self):
        release = self.releases[self.latest]
        for field in ("draft", "prerelease"):
            release[field] = True
            with self.assertRaises(ValueError):
                self.install()
            release[field] = False
        release["assets"][0]["browser_download_url"] = "https://example.invalid/unrelated"
        with self.assertRaisesRegex(ValueError, "Unexpected release asset"):
            self.install()
        self.assertFalse(self.args.skill_dir.exists())

    def test_packaging_is_repeatable_and_matches_release_source_not_current_main(self):
        source = self.root / "release-source"
        (source / "server/mcp").mkdir(parents=True)
        text = (ROOT / "server/mcp/server.go").read_text().replace('"list_instances"', '"release_only_instances"')
        (source / "server/mcp/server.go").write_text(text)
        subprocess.run(["git", "init", "-q", str(source)], check=True)
        subprocess.run(["git", "-C", str(source), "add", "."], check=True)
        subprocess.run(["git", "-C", str(source), "-c", "user.name=fixture", "-c", "user.email=fixture@example.invalid",
                        "-c", "commit.gpgsign=false", "commit", "-qm", "release"], check=True)
        with contextlib.redirect_stdout(io.StringIO()):
            for destination in (self.root / "first", self.root / "second"):
                package(source, self.latest, destination)
        first = (self.root / "first/oneclickvirt-skill.tar.gz").read_bytes()
        second = (self.root / "second/oneclickvirt-skill.tar.gz").read_bytes()
        self.assertEqual(first, second)
        files = installer.skill_files(first, self.latest)
        self.assertIn(b"release_only_instances", files["SKILL.md"])
        self.assertNotIn(b"`list_instances`", files["SKILL.md"])


if __name__ == "__main__":
    unittest.main()
