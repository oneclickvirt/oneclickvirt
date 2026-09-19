#!/usr/bin/env python3
"""Execute the live driver's environment setup in real non-login shells."""
from pathlib import Path
import shlex
import subprocess
import tempfile
import unittest
from unittest.mock import patch

from live_node_shell import node_command


class NodeShellTests(unittest.TestCase):
    def run_shell(self, command, path="/usr/bin:/bin"):
        return subprocess.run(["/bin/sh", "-c", command], env={"PATH": path},
                              text=True, capture_output=True, timeout=5)

    def test_standard_snap_paths_and_original_path_are_kept(self):
        result = self.run_shell(node_command('printf "%s" "$PATH"'), "/custom/bin:/usr/bin:/bin")
        self.assertEqual(result.returncode, 0)
        self.assertEqual(result.stdout, "/custom/bin:/usr/bin:/bin:/snap/bin:/var/lib/snapd/snap/bin")

    def test_empty_path_has_system_utilities(self):
        result = self.run_shell(node_command('command -v sh; command -v cat'), "")
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(len(result.stdout.splitlines()), 2)

    def test_next_exec_finds_snap_cli_without_installer_exports(self):
        # The executable only models command discovery, not a LXD daemon.
        with tempfile.TemporaryDirectory(prefix="ocv snap-path ") as directory:
            binary = Path(directory) / "lxc"
            binary.write_text('#!/bin/sh\nprintf "fixture:%s\\n" "$*"\n')
            binary.chmod(0o700)
            first = self.run_shell("export PATH=\"$PATH\":" + shlex.quote(directory) + "; lxc --version")
            self.assertEqual(first.returncode, 0)
            self.assertNotEqual(self.run_shell("lxc --version").returncode, 0)
            with patch("live_node_shell.SNAP_PATHS", (directory,)):
                result = self.run_shell(node_command("lxc --version"))
            self.assertEqual(result.returncode, 0, result.stderr)
            self.assertEqual(result.stdout, "fixture:--version\n")

    def test_stderr_and_failure_are_not_masked(self):
        result = self.run_shell(node_command("printf 'failed' >&2; exit 19"))
        self.assertEqual(result.returncode, 19)
        self.assertEqual(result.stderr, "failed")

    def test_no_user_startup_files_are_sourced(self):
        with tempfile.TemporaryDirectory() as directory:
            startup = Path(directory) / "startup"
            startup.write_text("echo unexpected-startup >&2; exit 31\n")
            result = subprocess.run(["/bin/sh", "-c", node_command("printf ready")],
                                    env={"PATH": "/usr/bin:/bin", "ENV": str(startup), "BASH_ENV": str(startup)},
                                    text=True, capture_output=True, timeout=5)
            self.assertEqual((result.returncode, result.stdout, result.stderr), (0, "ready", ""))


if __name__ == "__main__":
    unittest.main(verbosity=2)
