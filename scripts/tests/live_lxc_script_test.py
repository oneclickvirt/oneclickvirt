#!/usr/bin/env python3
"""Run current Incus/LXD container scripts on an empty disposable runtime.

Exercises the real unattended buildct entry and PTY add_more prompts, public
SSH, then runtime CLI deletion/port reuse. There is no standalone per-container
delete script in these repositories; CLI deletion is not installer coverage.
Requires OCV_LIVE_DISPOSABLE=yes, OCV_LIVE_HOST/PASSWORD and OCV_SCRIPT_REPO.
Success restores replaced helper files. Failure retains named fixtures.
"""
import hashlib
import getpass
import ipaddress
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
from live_external_ipv6 import ExternalIPv6Probe
from live_node_shell import node_command
from live_ssh import connect_strict_node, pinned_guest_client, strict_node_client


def listed_device_names(output):
    """Return local device names from Incus/LXD `config device show` YAML.

    Incus 6.0 LTS does not support `config show --format json`. Device names in
    this dedicated command are the unindented mapping keys, so parsing them
    avoids a PyYAML dependency while remaining compatible with old LXD/Incus.
    """
    names = set()
    for line in output.splitlines():
        if line and not line[0].isspace() and line.rstrip().endswith(":"):
            names.add(line.rstrip()[:-1])
    return names


def parse_global_ipv6_egress(output):
    address = ipaddress.ip_address(output.strip())
    if address.version != 6 or not address.is_global:
        raise ValueError("IPv6 egress result is not a global IPv6 address")
    return str(address)


def main():
    if os.environ.get("OCV_LIVE_DISPOSABLE") != "yes":
        raise SystemExit("Require OCV_LIVE_DISPOSABLE=yes for an empty authorized node")
    for name in ("OCV_LIVE_HOST", "OCV_SCRIPT_REPO"):
        if not os.environ.get(name):
            raise SystemExit(f"Missing required live-test variable: {name}")
    if paramiko is None:
        raise SystemExit("Missing optional dependency: install scripts/tests/requirements-live.txt")
    runtime = os.environ.get("OCV_LIVE_RUNTIME", "incus")
    if runtime not in ("incus", "lxd"):
        raise SystemExit("OCV_LIVE_RUNTIME must be incus or lxd")
    cli = "incus" if runtime == "incus" else "lxc"
    network_type = os.environ.get("OCV_LIVE_NETWORK_TYPE", "nat_ipv4")
    if network_type not in ("nat_ipv4", "nat_ipv4_ipv6", "ipv6_only"):
        raise SystemExit("OCV_LIVE_NETWORK_TYPE must be nat_ipv4, nat_ipv4_ipv6, or ipv6_only")
    has_ipv4 = network_type != "ipv6_only"
    has_ipv6 = network_type != "nat_ipv4"
    repository = Path(os.environ["OCV_SCRIPT_REPO"]).resolve()
    host, password = os.environ["OCV_LIVE_HOST"], os.environ.get("OCV_LIVE_PASSWORD", "")
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
    connect_strict_node(ssh, host, port=int(os.environ.get("OCV_LIVE_SSH_PORT", "22")))
    keep_ssh_alive(ssh)
    stage = ""
    success = False
    replacements = []
    external_probe = None

    def remote(command, timeout=120):
        stdin, stdout, _ = ssh.exec_command(node_command(command), timeout=timeout)
        stdin.channel.shutdown_write()
        out, err, status = _collect_output(stdout.channel, timeout)
        if status:
            # buildct prints a credential record; never forward its raw output.
            tail = re.sub(r"(" + re.escape(prefix) + r"\d+\s+\d+\s+)\S+", r"\1[redacted]", (out + err)[-6000:])
            if password:
                tail = tail.replace(password, "[redacted]")
            raise RuntimeError(f"remote exit {status}: " + tail)
        return out.strip()

    def check_ports():
        snapshots = [remote(command) for command in
                     ("ss -H -lntup", "nft list ruleset", "iptables-save -t nat")]
        for port in ports:
            if any(re.search(r"\b" + str(port) + r"\b", snapshot) for snapshot in snapshots):
                raise RuntimeError(f"port {port} is occupied or retained firewall rules")

    def ensure_guest_http_runtime(name):
        """Ensure a real HTTP process can run in a disposable minimal guest.

        Incus/LXD minimal images often omit Python. A shell still reports the
        background PID when `python3` immediately fails, which used to turn a
        missing test dependency into a false IPv6 product failure. Install the
        ordinary distro package only when needed; the guest is deleted after
        this generation.
        """
        install = r"""set -eu
if command -v python3 >/dev/null 2>&1; then exit 0; fi
if command -v apt-get >/dev/null 2>&1; then
    apt-get update
    DEBIAN_FRONTEND=noninteractive apt-get install -y python3
elif command -v dnf >/dev/null 2>&1; then
    dnf -y install python3
elif command -v yum >/dev/null 2>&1; then
    yum -y install python3
elif command -v apk >/dev/null 2>&1; then
    apk add --no-cache python3
elif command -v zypper >/dev/null 2>&1; then
    zypper --non-interactive install python3
else
    echo 'no supported package manager for the IPv6 HTTP probe' >&2
    exit 127
fi
command -v python3 >/dev/null 2>&1
"""
        remote(cli + " exec " + shlex.quote(name) + " -- sh -c " + shlex.quote(install), 600)

    def wait_guest_ipv6_egress(name):
        # systemd-resolved can briefly return EAI_NONAME immediately after the
        # final container restart while link DNS scopes are being rebuilt.
        # Retry the same read-only probe; never retry container creation.
        deadline = time.monotonic() + 90
        last_error = None
        while time.monotonic() < deadline:
            try:
                output = remote(
                    cli + " exec " + shlex.quote(name)
                    + " -- sh -c " + shlex.quote(
                        "curl --noproxy '*' -6 -fsS --connect-timeout 10 --max-time 20 https://ipv6.ip.sb"
                    ), 45
                )
                return parse_global_ipv6_egress(output)
            except (RuntimeError, ValueError) as error:
                last_error = error
                time.sleep(2)
        raise RuntimeError("script-created guest IPv6 DNS/egress did not become ready") from last_error

    try:
        if has_ipv6:
            probe_host = os.environ.get("OCV_LIVE_EXTERNAL_PROBE_HOST", "")
            probe_port = int(os.environ.get("OCV_LIVE_EXTERNAL_PROBE_PORT", "22"))
            probe_user = os.environ.get("OCV_LIVE_EXTERNAL_PROBE_USER", "root")
            probe_known_hosts = os.environ.get("OCV_LIVE_EXTERNAL_PROBE_KNOWN_HOSTS", "")
            probe_password = os.environ.get("OCV_LIVE_EXTERNAL_PROBE_PASSWORD", "")
            if not probe_password:
                probe_password = getpass.getpass("External IPv6 probe root password: ")
            if not probe_host or not probe_known_hosts:
                raise RuntimeError(
                    "IPv6 shell acceptance requires OCV_LIVE_EXTERNAL_PROBE_HOST and "
                    "OCV_LIVE_EXTERNAL_PROBE_KNOWN_HOSTS"
                )
            external_probe = ExternalIPv6Probe(
                probe_host, probe_port, probe_user, probe_password, probe_known_hosts
            )
            probe_egress = external_probe.connect()
            print("Independent IPv6 probe connected; egress=" + probe_egress, flush=True)
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
                ipv6_flag = "Y" if has_ipv6 else "N"
                strict_ipv6 = "yes" if has_ipv6 else "no"
                remote("cd /root && env -u NONINTERACTIVE -u INCUS_NONINTERACTIVE noninteractive=true WITHOUTCDN=true CN=false "
                       + "OCV_NETWORK_TYPE=" + shlex.quote(network_type) + " OCV_REQUIRE_PUBLIC_IPV6=" + strict_ipv6
                       + " bash ./buildct.sh "
                       + shlex.join([name, "1", "256", "3", str(first_port), str(first_port+1), str(first_port+25), "100", "100", ipv6_flag, guest_system]) + " </dev/null", 1500)
                record = remote("cat -- /root/" + shlex.quote(name))
            else:
                # Seed the real existing-log interface so add_more chooses this
                # run's unique name and the same released ports.
                with sftp.file(log_target, "w") as handle:
                    handle.write(f"{names[0]} {first_port-1} unused {first_port-25} {first_port}\n")
                channel = ssh.get_transport().open_session(timeout=15)
                channel.get_pty(term="xterm", width=140, height=40)
                channel.exec_command(node_command(
                    "cd /root && env -u noninteractive -u NONINTERACTIVE -u INCUS_NONINTERACTIVE "
                    + "WITHOUTCDN=true CN=false OCV_NETWORK_TYPE=" + shlex.quote(network_type)
                    + " OCV_REQUIRE_PUBLIC_IPV6=" + ("yes" if has_ipv6 else "no")
                    + " bash ./add_more.sh"
                ))
                answers = [("输入新增几个容器", "1"), ("每个容器CPU核数", "1"), ("每个容器内存大小", "256"),
                           ("每个容器硬盘大小", "3"), ("若需要限制为300Mbit", "100"), ("若需要限制为300Mbit", "100"),
                           ("不设置V6地址", "Y" if has_ipv6 else "N"), ("ubuntu20、centos7", guest_system)]
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
            if has_ipv4:
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
                        raise RuntimeError("script-created guest IPv4 SSH/DNS/outbound access failed")
                finally:
                    guest.close()
            else:
                devices = listed_device_names(remote(cli + " config device show " + shlex.quote(name)))
                for device in ("ssh-port", "nattcp-ports", "natudp-ports"):
                    if device in devices:
                        raise RuntimeError("ipv6_only unexpectedly exposed IPv4 device " + device)
                guest_ipv4 = remote(
                    cli + " exec " + shlex.quote(name)
                    + " -- sh -c " + shlex.quote(
                        "ip -o -4 addr show scope global; ip -4 route show default"
                    )
                )
                if guest_ipv4.strip():
                    raise RuntimeError(
                        "ipv6_only guest retained a non-loopback IPv4 address or default route: "
                        + guest_ipv4.replace("\n", " | ")
                    )
            if has_ipv6:
                raw_v6 = remote("tail -n 1 /root/" + shlex.quote(name + "_v6"))
                guest_v6 = ipaddress.ip_address(raw_v6.strip())
                if guest_v6.version != 6 or not guest_v6.is_global:
                    raise RuntimeError("script did not assign an independent global IPv6 address")
                guest_egress = wait_guest_ipv6_egress(name)
                nonce = secrets.token_hex(16)
                directory = "/tmp/ocv-script-ipv6-" + nonce
                ensure_guest_http_runtime(name)
                pid = remote(
                    cli + " exec " + shlex.quote(name) + " -- sh -c " + shlex.quote(
                        "set -eu; mkdir -p " + directory + "; printf %s " + shlex.quote(nonce)
                        + " >" + directory + "/identity; nohup python3 -u -m http.server 18080 --bind :: --directory "
                        + directory + " >/tmp/ocv-script-ipv6-http.log 2>&1 </dev/null & echo $!"
                    ), 45
                ).splitlines()[-1]
                if not pid.isdigit():
                    raise RuntimeError("guest IPv6 HTTP service did not return a valid process id")
                try:
                    # Do not ask the independent host to race a background
                    # process. Verify the random identity over IPv6 loopback;
                    # this also catches immediate exec failures despite a PID.
                    readiness = (
                        "set -eu; i=0; while [ $i -lt 100 ]; do "
                        "if kill -0 " + pid + " 2>/dev/null && "
                        "test \"$(curl --noproxy '*' -g -6 -fsS --connect-timeout 1 --max-time 2 "
                        "http://[::1]:18080/identity 2>/dev/null || true)\" = " + shlex.quote(nonce) + "; then "
                        "exit 0; fi; i=$((i + 1)); sleep 0.1; done; "
                        "cat /tmp/ocv-script-ipv6-http.log >&2 2>/dev/null || true; exit 1"
                    )
                    remote(cli + " exec " + shlex.quote(name) + " -- sh -c " + shlex.quote(readiness), 30)
                    external_probe.http_identity(str(guest_v6), 18080, nonce)
                    external_probe.ssh_identity(str(guest_v6), 22, guest_keys, "root", fields[2], name)
                finally:
                    remote(cli + " exec " + shlex.quote(name) + " -- sh -c "
                           + shlex.quote("kill " + pid + " 2>/dev/null || true; rm -rf " + directory), 30)
                print("PASS independent IPv6 SSH/HTTP and guest IPv6 egress: " + str(guest_v6), flush=True)
            if has_ipv4 and os.environ.get("OCV_WEBSSH_URL"):
                origin = verify_webssh(os.environ["OCV_WEBSSH_URL"], host, first_port, fields[2], name,
                                       os.environ["OCV_WEBSSH_SOURCE_IP"])
                print("Independent WebSSH verified: " + origin, flush=True)
            if remote(cli + " config get " + name + " user.ocv.test") != prefix:
                raise RuntimeError("guest ownership changed before deletion")
            remote(cli + " delete --force " + name)
            if name in remote(cli + " list --format csv -c n").splitlines():
                raise RuntimeError("deleted guest still present")
            check_ports()
            remote("rm -f -- /root/" + shlex.quote(name) + " /root/" + shlex.quote(name + "_v6"))
            print("PASS generation " + str(generation) + ": " + network_type
                  + " creation / SSH / DNS / outbound / CLI deletion / port release", flush=True)
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
        if external_probe:
            external_probe.close()
        ssh.close()
        if not success:
            print("FAIL: retained fixtures " + ",".join(names) + " stage=" + stage, flush=True)


if __name__ == "__main__":
    main()
