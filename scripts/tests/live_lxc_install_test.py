#!/usr/bin/env python3
"""Actual Incus/LXD installer, with an SSH-loopback mirror of current helpers.

Only this repository's raw main URLs are redirected. Package/image downloads
and runtime commands remain real. The source and transported hashes are logged;
installed mirrored helpers are restored to their original source on success.
"""
import hashlib
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
import json
import os
from pathlib import Path
import shlex
import sys
import threading
import time
from urllib.parse import unquote, urlsplit

try:
    import paramiko
except ImportError:  # Optional dependency for explicitly requested live runs.
    paramiko = None
from live_agent_fixture import ReversePanelForwarder
from live_node_shell import node_command
from live_ssh import connect_strict_node, strict_node_client

sys.path.insert(0, str(Path(__file__).resolve().parents[2] / "action_tests/common"))
from remote import _collect_output, keep_ssh_alive


def verify_storage_pools(pools, expected_driver=""):
    if not isinstance(pools, list) or not pools:
        raise RuntimeError("Installer returned no valid storage pools")
    names = set()
    for pool in pools:
        if (not isinstance(pool, dict)
                or not isinstance(pool.get("name"), str) or not pool["name"]
                or not isinstance(pool.get("driver"), str) or not pool["driver"]
                or pool["name"] in names):
            raise RuntimeError("Installer returned invalid or duplicate storage pools")
        names.add(pool["name"])
    if expected_driver:
        default = [pool for pool in pools if pool["name"] == "default"]
        if len(default) != 1 or default[0]["driver"] != expected_driver:
            raise RuntimeError("Expected default storage driver " + expected_driver
                               + "; fallback is not acceptance for this specific fixture")


def main():
    if os.environ.get("OCV_LIVE_DISPOSABLE") != "yes":
        raise SystemExit("Require OCV_LIVE_DISPOSABLE=yes")
    for name in ("OCV_LIVE_HOST", "OCV_SCRIPT_REPO"):
        if not os.environ.get(name):
            raise SystemExit(f"Missing required live-test variable: {name}")
    if paramiko is None:
        raise SystemExit("Missing optional dependency: install scripts/tests/requirements-live.txt")
    runtime = os.environ.get("OCV_LIVE_RUNTIME", "incus")
    mode = os.environ.get("OCV_LIVE_MODE", "noninteractive")
    expected_driver = os.environ.get("OCV_LIVE_EXPECT_STORAGE_DRIVER", "")
    if expected_driver not in ("", "dir", "btrfs", "lvm", "zfs", "ceph"):
        raise SystemExit("Invalid OCV_LIVE_EXPECT_STORAGE_DRIVER")
    if runtime not in ("incus", "lxd") or mode not in ("interactive", "noninteractive"):
        raise SystemExit("Invalid runtime or mode")
    repository = Path(os.environ["OCV_SCRIPT_REPO"]).resolve()
    entry = "scripts/" + ("incus_install.sh" if runtime == "incus" else "lxdinstall.sh")
    sources = {str(path.relative_to(repository)): path.read_bytes()
               for path in (repository / "scripts").iterdir() if path.suffix in (".sh", ".service")}
    if entry not in sources:
        raise SystemExit("Installer entry missing")
    cli = "incus" if runtime == "incus" else "lxc"
    def connect():
        client = strict_node_client()
        try:
            connect_strict_node(
                client,
                os.environ["OCV_LIVE_HOST"],
                port=int(os.environ.get("OCV_LIVE_SSH_PORT", "22")),
            )
            keep_ssh_alive(client)
        except BaseException:
            client.close()
            raise
        return client

    ssh = connect()

    def remote(command, timeout=120):
        stdin, stdout, _ = ssh.exec_command(node_command(command), timeout=timeout)
        stdin.channel.shutdown_write()
        out, err, status = _collect_output(stdout.channel, timeout)
        if status:
            raise RuntimeError(f"remote exit {status}: " + out[-3000:] + err[-3000:])
        return out.strip()

    fetched = set()
    lock = threading.Lock()
    mirror_prefix = b""
    original_prefix = f"https://raw.githubusercontent.com/oneclickvirt/{runtime}/main/".encode()

    def transported(path):
        return sources[path].replace(original_prefix, mirror_prefix)

    class Handler(BaseHTTPRequestHandler):
        def do_GET(self):
            path = unquote(urlsplit(self.path).path).lstrip("/")
            if path not in sources:
                self.send_error(404)
                return
            body = transported(path)
            with lock:
                fetched.add(path)
            self.send_response(200)
            self.send_header("Content-Length", str(len(body)))
            self.end_headers()
            self.wfile.write(body)

        def log_message(self, *_args):
            pass

    server = ThreadingHTTPServer(("127.0.0.1", 0), Handler)
    worker = threading.Thread(target=server.serve_forever, daemon=True)
    worker.start()
    forwarder = None
    stage = ""
    success = False

    def restore_helpers():
        with lock:
            downloaded = sorted(fetched)
        with ssh.open_sftp() as sftp:
            for path in downloaded:
                digest = hashlib.sha256(transported(path)).hexdigest()
                print("Downloaded current helper " + path + " source_sha256=" + hashlib.sha256(sources[path]).hexdigest(), flush=True)
                for directory in ("/root", "/usr/local/bin", "/etc/systemd/system"):
                    target = directory + "/" + Path(path).name
                    try:
                        with sftp.file(target, "rb") as handle:
                            existing = handle.read()
                    except FileNotFoundError:
                        continue
                    if hashlib.sha256(existing).hexdigest() != digest:
                        continue
                    with sftp.file(target, "wb") as handle:
                        handle.write(sources[path])

    try:
        # Reinstallation on an empty runtime is the default contract.  A
        # dedicated mixed-runtime probe may opt in explicitly when the node
        # contains an unrelated Docker/Podman/containerd installation that
        # must be preserved.  Never silently treat that probe as clean-OS
        # evidence.
        if os.environ.get("OCV_LIVE_ALLOW_EXISTING_RUNTIMES") != "yes":
            remote("set -eu; test ! -d /opt/oneclickvirt/agent; ! command -v docker; ! command -v podman; ! command -v containerd")
        else:
            remote("set -eu; test ! -d /opt/oneclickvirt/agent")
        if runtime == "incus":
            remote("! command -v lxc")
        else:
            remote("! command -v incus")
        names = remote(f"if command -v {cli} >/dev/null 2>&1; then {cli} list --format csv -c n; fi")
        if names:
            raise RuntimeError("existing guests prevent installer acceptance")
        print("Node OS: " + remote(". /etc/os-release; printf '%s %s' \"$ID\" \"$VERSION_ID\""), flush=True)
        original_boot_id = remote("cat /proc/sys/kernel/random/boot_id")
        stage = remote("mktemp -d /opt/ocv-live-install.XXXXXX")
        forwarder = ReversePanelForwarder(ssh, server.server_port)
        mirror_prefix = f"http://127.0.0.1:{forwarder.port}/".encode()
        with ssh.open_sftp() as sftp:
            with sftp.file(stage + "/installer.sh", "wb") as handle:
                handle.write(transported(entry))
        print("Installer original SHA-256=" + hashlib.sha256(sources[entry]).hexdigest()
              + " transported=" + hashlib.sha256(transported(entry)).hexdigest() + " fixture=" + stage, flush=True)
        args = ["env"]
        for variable in ("noninteractive", "NONINTERACTIVE", "INCUS_NONINTERACTIVE", "INCUS_STORAGE_PATH",
                         "INCUS_DISK_SIZE", "INCUS_STORAGE_BACKEND", "DISK_NUMS", "STORAGE_PATH"):
            args += ["-u", variable]
        args += ["WITHOUTCDN=true", "CN=false"]
        if mode == "noninteractive":
            args += ["noninteractive=true", ("INCUS_DISK_SIZE=" if runtime == "incus" else "DISK_NUMS=") + "12"]
        args += ["bash", stage + "/installer.sh"]
        channel = ssh.get_transport().open_session(timeout=15)
        if mode == "interactive":
            channel.get_pty(term="xterm", width=140, height=40)
        channel.exec_command(node_command(shlex.join(args)))
        if mode == "noninteractive":
            channel.shutdown_write()
        buffer, prompt = "", 0
        expects_reboot = runtime == "incus" and mode == "interactive"
        reboot_announced = False
        prompts = [("是否需要指定存储池的自定义路径", "n"), ("宿主机需要开设多大的存储池", "12")]
        def installer_output(index, chunk):
            nonlocal buffer, prompt, reboot_announced
            print(chunk, end="", file=sys.stdout if index == 0 else sys.stderr, flush=True)
            if index != 0:
                return
            buffer += chunk
            if "The machine will restart automatically after 15 seconds" in buffer:
                reboot_announced = True
            while mode == "interactive" and prompt < len(prompts) and prompts[prompt][0] in buffer:
                matched, answer = prompts[prompt]
                buffer = buffer.split(matched, 1)[1]
                channel.sendall(answer + "\n")
                print("\nAnswered actual installer prompt " + str(prompt+1), flush=True)
                prompt += 1
            buffer = buffer[-16384:]

        try:
            _, _, status = _collect_output(channel, 2400, on_output=installer_output, capture=False)
        finally:
            channel.close()
        # Incus intentionally reboots after a successful interactive install.
        # Require its final announcement, every prompt and a changed boot ID.
        valid_status = status == 0 or (expects_reboot and reboot_announced and status == -1)
        if not valid_status or (mode == "interactive" and prompt != len(prompts)):
            raise RuntimeError(f"installer failed status={status} prompts={prompt}")
        if expects_reboot:
            if not reboot_announced:
                raise RuntimeError("interactive Incus installer did not announce its expected reboot")
            forwarder.close()
            forwarder = None
            ssh.close()
            reboot_deadline = time.monotonic() + 300
            while time.monotonic() < reboot_deadline:
                try:
                    ssh = connect()
                    boot_id = remote("cat /proc/sys/kernel/random/boot_id")
                    if boot_id != original_boot_id:
                        print("Verified installer reboot: " + original_boot_id + " -> " + boot_id, flush=True)
                        break
                except (OSError, EOFError, paramiko.SSHException):
                    pass
                ssh.close()
                time.sleep(3)
            else:
                raise TimeoutError("installer reboot was not verified within 5 minutes")
        # Do not leave temporary mirror URLs in installed production scripts.
        restore_helpers()
        with ssh.open_sftp() as sftp:
            sftp.put(str(Path(__file__).resolve().parents[2] / "action_tests/common/runtime_readiness.sh"), stage + "/readiness.sh")
        remote("bash -c " + shlex.quote("source " + stage + "/readiness.sh; verify_lxc_runtime " + cli))
        print("Runtime version: " + remote(cli + " --version"), flush=True)
        pools = json.loads(remote(cli + " storage list --format json"))
        verify_storage_pools(pools, expected_driver)
        print("Storage: " + json.dumps([{"name": pool["name"], "driver": pool["driver"]} for pool in pools]), flush=True)
        if expected_driver:
            print("Verified expected default storage driver: " + expected_driver, flush=True)
        remote("rm -- " + shlex.quote(stage + "/installer.sh") + " " + shlex.quote(stage + "/readiness.sh") + " && rmdir -- " + shlex.quote(stage))
        success = True
        print("PASS: real " + runtime + " " + mode + " installer and runtime initialization", flush=True)
    finally:
        if not success and ssh.get_transport() and ssh.get_transport().is_active():
            restore_helpers()
        if forwarder:
            forwarder.close()
        server.shutdown()
        server.server_close()
        ssh.close()
        if not success:
            print("FAIL: retained installer fixture " + stage + "; helper URLs may still refer to the stopped test mirror", flush=True)


if __name__ == "__main__":
    main()
