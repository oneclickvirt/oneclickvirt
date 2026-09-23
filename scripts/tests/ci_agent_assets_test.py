#!/usr/bin/env python3
"""Asset identity and explicit stub authentication regressions (no remote nodes)."""
import importlib.util
import io
import os
import pathlib
import socket
import struct
import subprocess
import tarfile
import tempfile
import time
import unittest
import urllib.error
import urllib.request

ROOT = pathlib.Path(__file__).resolve().parents[2]
spec = importlib.util.spec_from_file_location("assets", ROOT / "scripts/validate_agent_assets.py")
assets = importlib.util.module_from_spec(spec)
spec.loader.exec_module(assets)


class AgentAssetsTest(unittest.TestCase):
    def package(self, directory, arch, body):
        name = f"oneclickvirt-agent-linux-{arch}"
        with tarfile.open(pathlib.Path(directory) / f"{name}.tar.gz", "w:gz") as tar:
            info = tarfile.TarInfo(name)
            info.size = len(body)
            tar.addfile(info, io.BytesIO(body))

    def test_real_header_and_wrong_arch_or_stub(self):
        with tempfile.TemporaryDirectory() as directory:
            for arch, machine in (("amd64", 62), ("arm64", 183)):
                header = bytearray(64)
                header[:6] = b"\x7fELF\x02\x01"
                struct.pack_into("<H", header, 18, machine)
                self.package(directory, arch, header)
            assets.validate_assets(directory)
            self.package(directory, "amd64", header)  # ARM ELF under AMD64 name
            with self.assertRaises(ValueError):
                assets.validate_assets(directory)
            self.package(directory, "amd64", b"#!/bin/sh\necho fake-agent\n")
            with self.assertRaises(ValueError):
                assets.validate_assets(directory)

    def test_explicit_local_stub_auth_and_actions_rejection(self):
        with tempfile.TemporaryDirectory() as directory:
            env = dict(os.environ, GITHUB_ACTIONS="false", ACTION_TEST_GENERATE_STUB_AGENT="true")
            command = 'source "$1/action_tests/common/test_framework.sh"; source "$1/action_tests/common/node_manager.sh"; ensure_ci_agent_assets "$2/server"'
            subprocess.run(["bash", "-c", command, "fixture", str(ROOT), directory], env=env,
                           check=True, stdout=subprocess.DEVNULL, timeout=30)
            with self.assertRaises(ValueError):
                assets.validate_assets(pathlib.Path(directory) / "server/assets/agent")
            archive = pathlib.Path(directory) / "server/assets/agent/oneclickvirt-agent-linux-amd64.tar.gz"
            with tarfile.open(archive) as tar:
                binary = pathlib.Path(directory) / "stub.sh"
                binary.write_bytes(tar.extractfile("oneclickvirt-agent-linux-amd64").read())
            with socket.socket() as sock:
                sock.bind(("127.0.0.1", 0))
                port = sock.getsockname()[1]
            env.update(API_TOKEN="fixture-token", AGENT_PORT=str(port))
            process = subprocess.Popen(["sh", str(binary)], env=env, stdout=subprocess.DEVNULL,
                                       stderr=subprocess.DEVNULL)
            opener = urllib.request.build_opener(urllib.request.ProxyHandler({}))
            try:
                url = f"http://127.0.0.1:{port}/api/v1/list"
                for attempt in range(50):
                    try:
                        request = urllib.request.Request(url, headers={"x-token": "fixture-token"})
                        with opener.open(request, timeout=1) as response:
                            self.assertEqual(response.status, 200)
                        break
                    except urllib.error.URLError:
                        if process.poll() is not None or attempt == 49:
                            raise
                        time.sleep(0.05)
                with self.assertRaises(urllib.error.HTTPError) as error:
                    opener.open(urllib.request.Request(url, headers={"x-token": "wrong-token"}), timeout=1)
                self.assertEqual(error.exception.code, 401)
                error.exception.close()
            finally:
                process.terminate()
                process.wait(timeout=5)
            env["GITHUB_ACTIONS"] = "true"
            result = subprocess.run(["bash", "-c", command, "fixture", str(ROOT), directory],
                                    env=env, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL, timeout=30)
            self.assertNotEqual(result.returncode, 0)


if __name__ == "__main__":
    unittest.main()
