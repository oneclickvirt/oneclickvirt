#!/usr/bin/env python3
"""Run current Incus/LXD container scripts on an empty disposable runtime.

Exercises the real unattended buildct entry and PTY add_more prompts, public
SSH, then runtime CLI deletion/port reuse. There is no standalone per-container
delete script in these repositories; CLI deletion is not installer coverage.
Requires OCV_LIVE_DISPOSABLE=yes, OCV_LIVE_HOST/PASSWORD and OCV_SCRIPT_REPO.
Success restores replaced helper files. Failure retains named fixtures.
"""
import hashlib
import json
import os
from pathlib import Path
import re
import secrets
import shlex
import sys
import time

try:
    import paramiko
except ImportError:  # Optional dependency for explicitly requested live runs.
    paramiko = None

sys.path.insert(0, str(Path(__file__).resolve().parents[2] / "action_tests/common"))
from remote import _collect_output, keep_ssh_alive
from webssh_external_probe import verify_webssh
from live_node_shell import node_command
from live_ssh import pinned_guest_client, strict_node_client


def main():
    if os.environ.get("OCV_LIVE_DISPOSABLE") != "yes":
        raise SystemExit("Require OCV_LIVE_DISPOSABLE=yes for an empty authorized node")
    for name in ("OCV_LIVE_HOST", "OCV_LIVE_PASSWORD", "OCV_SCRIPT_REPO"):
        if not os.environ.get(name):
            raise SystemExit(f"Missing required live-test variable: {name}")
    if paramiko is None:
        raise SystemExit("Missing optional dependency: install scripts/tests/requirements-live.txt")
    runtime = os.environ.get("OCV_LIVE_RUNTIME", "incus")
    if runtime not in ("incus", "lxd"):
        raise SystemExit("OCV_LIVE_RUNTIME must be incus or lxd")
    cli = "incus" if runtime == "incus" else "lxc"
    repository = Path(os.environ["OCV_SCRIPT_REPO"]).resolve()
    host, password = os.environ["OCV_LIVE_HOST"], os.environ["OCV_LIVE_PASSWORD"]
    guest_system = os.environ.get("OCV_SCRIPT_SYSTEM", "debian13")
    if os.environ.get("OCV_WEBSSH_URL"):
        if not os.environ.get("OCV_WEBSSH_SOURCE_IP"):
            raise SystemExit("OCV_WEBSSH_SOURCE_IP is required for the external probe")
        import websocket  # Check optional dependencies before touching the node.
    files = ["buildct.sh", "add_more.sh", "instance_ownership.sh", "ssh_bash.sh", "ssh_sh.sh",
             "config.sh", "build_ipv6_network.sh", "check-dns.sh"]
    if runtime == "incus":
        files.append("image_lookup.sh")
    # Validate and snapshot all helpers before creating remote fixtures.
    sources = {filename: (repository / "scripts" / filename).read_bytes() for filename in files}
    # add_more derives the prefix by stripping at the first digit.
    prefix = "ocvscript" + "".join(secrets.choice("abcdefghijklmnopqrstuvwxyz") for _ in range(8))
    names = [prefix + "1", prefix + "2"]
    first_port = int(os.environ.get("OCV_LIVE_PORT", "29800"))
    if not 1024 <= first_port <= 65509:
        raise SystemExit("Need room for SSH plus 25 contiguous NAT ports")
    ports = range(first_port, first_port + 26)
    ssh = strict_node_client()
    ssh.connect(host, username="root", password=password, timeout=15,
                banner_timeout=15, auth_timeout=15, allow_agent=False, look_for_keys=False)
    keep_ssh_alive(ssh)
    stage = ""
    success = False
    replacements = []

    def remote(command, timeout=120):
        stdin, stdout, _ = ssh.exec_command(node_command(command), timeout=timeout)
        stdin.channel.shutdown_write()
        out, err, status = _collect_output(stdout.channel, timeout)
        if status:
            # buildct prints a credential record; never forward its raw output.
            tail = re.sub(r"(" + re.escape(prefix) + r"\d+\s+\d+\s+)\S+", r"\1[redacted]", (out + err)[-6000:])
            raise RuntimeError(f"remote exit {status}: " + tail.replace(password, "[redacted]"))
        return out.strip()

    def check_ports():
        snapshots = [remote(command) for command in
                     ("ss -H -lntup", "nft list ruleset", "iptables-save -t nat")]
        for port in ports:
            if any(re.search(r"\b" + str(port) + r"\b", snapshot) for snapshot in snapshots):
                raise RuntimeError(f"port {port} is occupied or retained firewall rules")

    try:
        if remote(cli + " list --format csv -c n"):
            raise RuntimeError("This script requires an empty runtime; existing guests are protected")
        check_ports()
        stage = remote("mktemp -d /opt/ocv-live-scripts.XXXXXX")
        sftp = ssh.open_sftp()
        with sftp.file(stage + "/owner", "w") as handle:
            handle.write(prefix)
        manifest = {}
        for filename in files:
            source = sources[filename]
            digest = hashlib.sha256(source).hexdigest()
            target = stage + "/" + filename
            with sftp.file(target, "wb") as handle:
                handle.write(source)
            sftp.chmod(target, 0o755)
            if remote("sha256sum " + shlex.quote(target)).split()[0] != digest:
                raise RuntimeError("uploaded source hash mismatch: " + filename)
            manifest[filename] = digest
        # Both entries intentionally work in /root; preserve those exact files.
        for filename in files:
            targets = ["/root/" + filename]
            if filename in ("ssh_bash.sh", "ssh_sh.sh", "config.sh", "check-dns.sh"):
                targets.append("/usr/local/bin/" + filename)
            for target in targets:
                backup = stage + "/backup-" + str(len(replacements))
                if remote("if test -L " + shlex.quote(target) + "; then echo yes; fi") == "yes":
                    raise RuntimeError("refusing to overwrite a helper symlink: " + target)
                exists = remote("if test -e " + shlex.quote(target) + " || test -L " + shlex.quote(target) + "; then echo yes; fi") == "yes"
                if exists:
                    remote("cp -a -- " + shlex.quote(target) + " " + shlex.quote(backup))
                replacements.append({"target": target, "backup": backup if exists else None})
                remote("cp -- " + shlex.quote(stage + "/" + filename) + " " + shlex.quote(target))
        log_target = "/root/log"
        log_backup = stage + "/backup-log"
        exists = remote("if test -e /root/log; then echo yes; fi") == "yes"
        if exists:
            remote("cp -a -- /root/log " + shlex.quote(log_backup))
        replacements.append({"target": log_target, "backup": log_backup if exists else None})
        with sftp.file(stage + "/restore.json", "w") as handle:
            handle.write(json.dumps(replacements))
        print("Current helper hashes verified: " + json.dumps(manifest, sort_keys=True), flush=True)
        print("Fixture " + prefix + " stage=" + stage, flush=True)
        for generation, name in enumerate(names):
            print("Starting real " + ("noninteractive buildct" if generation == 0 else "interactive add_more PTY"), flush=True)
            if generation == 0:
                remote("cd /root && env -u NONINTERACTIVE -u INCUS_NONINTERACTIVE noninteractive=true WITHOUTCDN=true CN=false bash ./buildct.sh "
                       + shlex.join([name, "1", "256", "3", str(first_port), str(first_port+1), str(first_port+25), "100", "100", "N", guest_system]) + " </dev/null", 1500)
                record = remote("cat -- /root/" + shlex.quote(name))
            else:
                # Seed the real existing-log interface so add_more chooses this
                # run's unique name and the same released ports.
                with sftp.file(log_target, "w") as handle:
                    handle.write(f"{names[0]} {first_port-1} unused {first_port-25} {first_port}\n")
                channel = ssh.get_transport().open_session(timeout=15)
                channel.get_pty(term="xterm", width=140, height=40)
                channel.exec_command(node_command("cd /root && env -u noninteractive -u NONINTERACTIVE -u INCUS_NONINTERACTIVE WITHOUTCDN=true CN=false bash ./add_more.sh"))
                answers = [("输入新增几个容器", "1"), ("每个容器CPU核数", "1"), ("每个容器内存大小", "256"),
                           ("每个容器硬盘大小", "3"), ("若需要限制为300Mbit", "100"), ("若需要限制为300Mbit", "100"),
                           ("不设置V6地址", "N"), ("ubuntu20、centos7", guest_system)]
                buffer, index, errors = "", 0, ""

                def script_output(stream, text):
                    nonlocal buffer, index, errors
                    if stream == 1:
                        errors = (errors + text)[-6000:]
                        return
                    buffer += text
                    while index < len(answers) and answers[index][0] in buffer:
                        matched, answer = answers[index]
                        buffer = buffer.split(matched, 1)[1]
                        channel.sendall(answer + "\n")
                        print("Answered actual PTY prompt " + str(index+1), flush=True)
                        index += 1
                    buffer = buffer[-16384:]

                try:
                    _, _, status = _collect_output(channel, 1500, on_output=script_output, capture=False)
                finally:
                    channel.close()
                if status or index != len(answers):
                    tail = re.sub(r"(" + re.escape(prefix) + r"\d+\s+\d+\s+)\S+", r"\1[redacted]", buffer[-6000:] + errors)
                    raise RuntimeError(f"interactive entry failed: status={status} prompts={index}\n" + tail.replace(password, "[redacted]"))
                record = remote("tail -n 1 /root/log")
            fields = record.split()
            if len(fields) != 5 or fields[0] != name or int(fields[1]) != first_port:
                raise RuntimeError("script did not commit the expected connection record")
            remote(cli + " config set " + name + " user.ocv.test=" + prefix)
            guest_keys = remote(
                cli + " exec " + shlex.quote(name)
                + " -- sh -c " + shlex.quote(
                    "for key in /etc/ssh/ssh_host_*_key.pub; do "
                    "test -r \"$key\" && cat -- \"$key\"; done"
                )
            )
            guest = pinned_guest_client(host, first_port, guest_keys)
            deadline = time.monotonic() + 90
            while True:
                try:
                    guest.connect(host, port=first_port, username="root", password=fields[2], timeout=10,
                                  auth_timeout=10, banner_timeout=10, allow_agent=False, look_for_keys=False)
                    break
                except (OSError, paramiko.SSHException):
                    guest.close()
                    if time.monotonic() >= deadline:
                        raise
                    time.sleep(2)
            try:
                stdin, stdout, _ = guest.exec_command("set -eu; hostname; getent hosts deb.debian.org; curl -fsS -o /dev/null --max-time 15 http://deb.debian.org/debian/README", timeout=30)
                stdin.channel.shutdown_write()
                out, _, status = _collect_output(stdout.channel, 30)
                if status or not out.splitlines() or out.splitlines()[0] != name:
                    raise RuntimeError("script-created guest SSH/DNS/outbound access failed")
            finally:
                guest.close()
            if os.environ.get("OCV_WEBSSH_URL"):
                origin = verify_webssh(os.environ["OCV_WEBSSH_URL"], host, first_port, fields[2], name,
                                       os.environ["OCV_WEBSSH_SOURCE_IP"])
                print("Independent WebSSH verified: " + origin, flush=True)
            if remote(cli + " config get " + name + " user.ocv.test") != prefix:
                raise RuntimeError("guest ownership changed before deletion")
            remote(cli + " delete --force " + name)
            if name in remote(cli + " list --format csv -c n").splitlines():
                raise RuntimeError("deleted guest still present")
            check_ports()
            remote("rm -f -- /root/" + shlex.quote(name))
            print("PASS generation " + str(generation) + ": real creation / public SSH / DNS / outbound / CLI deletion / port release", flush=True)
        for item in reversed(replacements):
            target = shlex.quote(item["target"])
            if item["backup"]:
                remote("mv -f -- " + shlex.quote(item["backup"]) + " " + target)
            else:
                remote("rm -f -- " + target)
        if remote("cat " + shlex.quote(stage + "/owner")) != prefix:
            raise RuntimeError("staging ownership changed")
        remote("rm -rf -- " + shlex.quote(stage))
        success = True
        print("PASS: both script modes verified; original helpers restored", flush=True)
    finally:
        ssh.close()
        if not success:
            print("FAIL: retained fixtures " + ",".join(names) + " stage=" + stage, flush=True)


if __name__ == "__main__":
    main()
