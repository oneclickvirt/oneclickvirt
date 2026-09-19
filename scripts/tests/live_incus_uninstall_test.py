#!/usr/bin/env python3
"""Actual Incus/LXD uninstall acceptance on an empty disposable node.

The historical filename is retained for existing callers. Select LXD with
OCV_LIVE_RUNTIME=lxd; the default is incus.
"""
import hashlib
import json
import os
from pathlib import Path
import shlex
import sys

try:
    import paramiko
except ImportError:  # Optional dependency for explicitly requested live runs.
    paramiko = None
from live_node_shell import node_command
from live_ssh import strict_node_client

sys.path.insert(0, str(Path(__file__).resolve().parents[2] / "action_tests/common"))
from remote import _collect_output, keep_ssh_alive


class UninstallPrompts:
    def __init__(self, runtime):
        if runtime == "incus":
            self.prompts = [("type 'yes' to continue", "yes\n")]
        elif runtime == "lxd":
            self.prompts = [("Confirm continue? (y/n)", "y\n"),
                            ("Also remove backing storage files? (y/n)", "y\n")]
        else:
            raise ValueError("unknown uninstall runtime")
        self.buffer = ""
        self.answered = 0

    @property
    def complete(self):
        return self.answered == len(self.prompts)

    def feed(self, text, send):
        self.buffer += text
        while not self.complete:
            prompt, answer = self.prompts[self.answered]
            if prompt not in self.buffer:
                break
            self.buffer = self.buffer.split(prompt, 1)[1]
            send(answer)
            self.answered += 1
        self.buffer = self.buffer[-16384:]


def main():
    if os.environ.get("OCV_LIVE_DISPOSABLE") != "yes":
        raise SystemExit("Require explicit OCV_LIVE_DISPOSABLE=yes")
    for name in ("OCV_LIVE_HOST", "OCV_LIVE_PASSWORD", "OCV_SCRIPT_REPO"):
        if not os.environ.get(name):
            raise SystemExit(f"Missing required live-test variable: {name}")
    if paramiko is None:
        raise SystemExit("Missing optional dependency: install scripts/tests/requirements-live.txt")
    mode = os.environ.get("OCV_LIVE_MODE", "interactive")
    if mode not in ("interactive", "noninteractive"):
        raise SystemExit("OCV_LIVE_MODE must be interactive or noninteractive")
    runtime = os.environ.get("OCV_LIVE_RUNTIME", "incus")
    if runtime not in ("incus", "lxd"):
        raise SystemExit("OCV_LIVE_RUNTIME must be incus or lxd")
    cli = "incus" if runtime == "incus" else "lxc --force-local --project default"
    entry = "uninstall_incus.sh" if runtime == "incus" else "lxduninstall.sh"
    script = Path(os.environ["OCV_SCRIPT_REPO"]) / "scripts" / entry
    source = script.read_bytes()
    ssh = strict_node_client()
    ssh.connect(os.environ["OCV_LIVE_HOST"], username="root", password=os.environ["OCV_LIVE_PASSWORD"],
                timeout=15, auth_timeout=15, banner_timeout=15, allow_agent=False, look_for_keys=False)
    keep_ssh_alive(ssh)

    def remote(command):
        stdin, stdout, _ = ssh.exec_command(node_command(command), timeout=60)
        stdin.channel.shutdown_write()
        out, err, code = _collect_output(stdout.channel, 60)
        if code:
            raise RuntimeError(f"pre/postcondition failed ({code}): " + out + err)
        return out.strip()

    stage = ""
    success = False
    try:
        if remote(cli + " list --format csv -c n"):
            raise RuntimeError("refusing to uninstall a runtime with existing guests")
        if runtime == "lxd":
            projects = json.loads(remote(cli + " project list --format json"))
            if not isinstance(projects, list) or len(projects) != 1 or projects[0].get("name") != "default":
                raise RuntimeError("refusing to uninstall LXD with other projects or invalid inventory")
        remote("test ! -d /opt/oneclickvirt/agent; test -z \"$(ss -H -lnt 'sport = :23782')\"")
        print("Verified empty " + runtime + " runtime before authorized uninstall", flush=True)
        stage = remote("mktemp -d /opt/ocv-live-uninstall.XXXXXX")
        target = stage + "/" + entry
        with ssh.open_sftp() as sftp:
            sftp.put(str(script), target)
            sftp.chmod(target, 0o700)
        digest = hashlib.sha256(source).hexdigest()
        if remote("sha256sum " + shlex.quote(target)).split()[0] != digest:
            raise RuntimeError("uninstaller hash mismatch")
        print("Verified current uninstaller SHA-256: " + digest, flush=True)
        channel = ssh.get_transport().open_session(timeout=15)
        args = ["env", "-u", "noninteractive", "-u", "NONINTERACTIVE", "-u", "INCUS_NONINTERACTIVE",
                "-u", "INCUS_FORCE_UNINSTALL", "-u", "FORCE", "-u", "REMOVE_STORAGE"]
        if runtime == "lxd" and mode == "noninteractive":
            args.append("REMOVE_STORAGE=true")
        if mode == "interactive":
            channel.get_pty(term="xterm", width=140, height=40)
        else:
            args.append("noninteractive=true")
        args += ["bash", target]
        channel.exec_command(node_command(shlex.join(args)))
        if mode == "noninteractive":
            channel.shutdown_write()
        dialogue = UninstallPrompts(runtime)
        def uninstall_output(index, text):
            print(text, end="", file=sys.stdout if index == 0 else sys.stderr, flush=True)
            if index != 0:
                return
            if mode == "interactive":
                before = dialogue.answered
                dialogue.feed(text, channel.sendall)
                if dialogue.answered != before:
                    print("\nAnswered actual uninstall prompts: " + str(dialogue.answered), flush=True)

        try:
            _, _, code = _collect_output(channel, 1200, on_output=uninstall_output, capture=False)
        finally:
            channel.close()
        if code or (mode == "interactive" and not dialogue.complete):
            raise RuntimeError(f"uninstaller failed: status={code}, prompts_answered={dialogue.answered}")
        if runtime == "incus":
            remote("set -eu; ! command -v incus; test ! -d /var/lib/incus; test ! -d /etc/incus")
            state = remote("dpkg-query -W -f='${binary:Package} ${db:Status-Status}\\n' | awk '$1 ~ /^incus(:|$|-)/ && $2 != \"not-installed\" {print}'")
            if state:
                raise RuntimeError("Incus packages survived uninstall: " + state)
            remote("test -z \"$(findmnt -rn -o TARGET | grep '^/var/lib/incus/' || true)\"")
            remote("if ! command -v lxcfs >/dev/null 2>&1; then ! findmnt -rn -M /var/lib/lxcfs; fi")
        else:
            # snapd and snapshots are shared/recovery facilities; their
            # existence alone does not mean the LXD runtime remains installed.
            inventory = remote("snap list")
            if any(line.split()[:1] == ["lxd"] for line in inventory.splitlines()):
                raise RuntimeError("LXD snap survived uninstall")
            remote("test ! -d /var/snap/lxd/common/lxd")
            remote("test -z \"$(findmnt -rn -o TARGET | grep -E '^(/var/snap/lxd/|/snap/lxd/)' || true)\"")
        remote(f"set -eu; test ! -f /etc/nftables.d/oneclickvirt-{runtime}.nft; test ! -f /usr/local/bin/{runtime}_storage_pool")
        owned_tables = {("inet", runtime), ("inet", runtime + "_block"),
                        ("inet", runtime + ("_masq" if runtime == "incus" else "_nat")),
                        ("ip6", runtime + "_ipv6_nat")}
        actual_tables = {tuple(part.strip('"') for part in table.split()[1:3])
                         for table in remote("nft list tables").splitlines() if table.startswith("table ")}
        if owned_tables & actual_tables:
            raise RuntimeError(runtime + " firewall table survived uninstall")
        remote(f"! ip link show {runtime}br0 >/dev/null 2>&1")
        remote(f"for policy in /etc/nftables.conf /etc/sysconfig/nftables.conf /etc/nftables.nft; do "
               f"test ! -e \"$policy\" || ! grep -F 'oneclickvirt-{runtime}.nft' \"$policy\" || exit 1; done")
        if remote("sha256sum " + shlex.quote(target)).split()[0] != digest:
            raise RuntimeError("staged uninstaller changed before cleanup")
        remote("rm -- " + shlex.quote(target) + " && rmdir -- " + shlex.quote(stage))
        success = True
        print("PASS: real " + runtime + " " + mode + " uninstall; packages, bridge, runtime data and persisted rules removed", flush=True)
    finally:
        ssh.close()
        if not success:
            print("FAIL: retained uninstall fixture " + stage, flush=True)


if __name__ == "__main__":
    main()
