#!/usr/bin/env python3
"""One-shot production Incus IPv6 NAT acceptance; all fixtures are temporary."""
import getpass
import ipaddress
import json
import secrets
import shlex
import socket
import sys
import time
import urllib.request
import os
from pathlib import Path

import paramiko

ROOT = Path(__file__).resolve().parents[2]
sys.path.insert(0, str(ROOT / "scripts" / "tests"))
from live_ssh import strict_node_client
from webssh_external_probe import verify_webssh

HOST = os.environ.get("OCV_LIVE_HOST", "")
NODE_PORT = int(os.environ.get("OCV_LIVE_SSH_PORT", "1777"))
WEBSSH = os.environ.get("OCV_WEBSSH_URL", "")
HOST_V6 = os.environ.get("OCV_WEBSSH_IPV6_TARGET", "")
SSH_PORT = int(os.environ.get("OCV_LIVE_IPV6_SSH_PORT", "29987"))
HTTP_PORT = int(os.environ.get("OCV_LIVE_IPV6_HTTP_PORT", "29988"))
GUEST_V4 = os.environ.get("OCV_LIVE_GUEST_IPV4", "10.101.179.240")
GUEST_V6 = os.environ.get("OCV_LIVE_GUEST_IPV6", "")
IMAGE = os.environ.get("OCV_LIVE_IMAGE", "")


def collect(channel, timeout=90):
    channel.settimeout(timeout)
    out = bytearray()
    while True:
        if channel.recv_ready():
            out.extend(channel.recv(65536))
        elif channel.exit_status_ready():
            while channel.recv_ready():
                out.extend(channel.recv(65536))
            return out.decode(errors="replace"), channel.recv_exit_status()
        else:
            time.sleep(0.05)


def main():
    required = {
        "OCV_LIVE_DISPOSABLE": "yes", "OCV_LIVE_HOST": HOST,
        "OCV_WEBSSH_URL": WEBSSH, "OCV_WEBSSH_IPV6_TARGET": HOST_V6,
        "OCV_LIVE_GUEST_IPV6": GUEST_V6, "OCV_LIVE_IMAGE": IMAGE,
    }
    for key, expected in required.items():
        if not expected or (key == "OCV_LIVE_DISPOSABLE" and expected != os.environ.get(key)):
            raise SystemExit("Set " + key + ("=yes" if key == "OCV_LIVE_DISPOSABLE" else " before running this destructive live probe"))
    try:
        if ipaddress.ip_address(HOST_V6).version != 6 or not ipaddress.ip_address(HOST_V6).is_global:
            raise ValueError
    except ValueError as exc:
        raise SystemExit("OCV_WEBSSH_IPV6_TARGET must be a public IPv6 literal") from exc
    node_password = getpass.getpass("Production node root password: ")
    guest_password = "OcvIPv6Nat!" + secrets.token_urlsafe(10)
    name = "ocv-ipv6-nat-" + secrets.token_hex(4)
    node = strict_node_client()
    node.connect(HOST, port=NODE_PORT, username="root", password=node_password,
                 timeout=15, auth_timeout=15, banner_timeout=15,
                 allow_agent=False, look_for_keys=False)
    created = False
    devices = []

    def run(command, timeout=120, check=True):
        wrapped = "export PATH=\"$PATH:/snap/bin:/var/lib/snapd/snap/bin\"; " + command
        stdin, stdout, stderr = node.exec_command(wrapped, timeout=timeout)
        stdout.channel.set_combine_stderr(True)
        stdin.channel.shutdown_write()
        output, status = collect(stdout.channel, timeout)
        if check and status:
            raise RuntimeError(f"remote command failed ({status}): {output[-2400:]}")
        return output.strip()

    try:
        # Reserve exact addresses/ports before creating anything.
        if run(f"incus list --format csv -c n | grep -Fx -- {shlex.quote(name)}", check=False):
            raise RuntimeError("random test name unexpectedly exists")
        if run(f"ss -H -lntup | grep -E '(:{SSH_PORT}|:{HTTP_PORT})([[:space:]]|$)'", check=False):
            raise RuntimeError("temporary production test port is occupied")
        existing = run("incus list --format csv -c 4,6")
        if GUEST_V4 in existing or GUEST_V6 in existing:
            raise RuntimeError("temporary address is already assigned")

        run(f"incus init {shlex.quote(IMAGE)} {shlex.quote(name)}")
        created = True
        run(f"incus config device override {shlex.quote(name)} eth0")
        run(f"incus config device set {shlex.quote(name)} eth0 ipv4.address={GUEST_V4} ipv6.address={GUEST_V6}")
        run(f"incus start {shlex.quote(name)}")
        # Configure credentials and a deterministic identity/service in the guest.
        run("incus exec " + shlex.quote(name) + " -- sh -ceu " + shlex.quote(
            "printf '%s\\n' " + shlex.quote("root:" + guest_password) + " | chpasswd; "
            "apt-get update -qq; DEBIAN_FRONTEND=noninteractive apt-get install -y -qq openssh-server python3; "
            "systemctl enable --now ssh; "
            "printf '%s\\n' " + shlex.quote(name) + " > /etc/ocv-ipv6-nat-identity"), 300)
        run("incus config device add " + shlex.quote(name) + " v6ssh proxy "
            + "listen=tcp:[" + HOST_V6 + "]:" + str(SSH_PORT)
            + " connect=tcp:[" + GUEST_V6 + "]:22 nat=true")
        devices.append("v6ssh")
        run("incus exec " + shlex.quote(name) + " -- sh -ceu " + shlex.quote(
            "cat > /tmp/ocv-v6-http.py <<'PY'\n"
            "from http.server import BaseHTTPRequestHandler,ThreadingHTTPServer\n"
            "import socket\n"
            "class S(ThreadingHTTPServer): address_family=socket.AF_INET6\n"
            "class H(BaseHTTPRequestHandler):\n"
            " def do_GET(self):\n"
            "  body=open('/etc/ocv-ipv6-nat-identity','rb').read()\n"
            "  self.send_response(200 if self.path=='/identity' else 404); self.send_header('Content-Length',str(len(body))); self.end_headers(); self.wfile.write(body)\n"
            "S(('::',18080),H).serve_forever()\nPY\n"
            "nohup python3 /tmp/ocv-v6-http.py >/tmp/ocv-v6-http.log 2>&1 &"), 120)
        run("incus config device add " + shlex.quote(name) + " v6http proxy "
            + "listen=tcp:[" + HOST_V6 + "]:" + str(HTTP_PORT)
            + " connect=tcp:[" + GUEST_V6 + "]:18080 nat=true")
        devices.append("v6http")
        time.sleep(3)

        # Prove host-side IPv6 TCP forwarding before independent checks.
        local = run(f"curl --noproxy '*' -g -6 -fsS --connect-timeout 5 --max-time 15 http://[{HOST_V6}]:{HTTP_PORT}/identity")
        if local != name:
            raise RuntimeError("host IPv6 proxy returned wrong guest identity")
        print("PASS host IPv6 proxy reached the temporary guest HTTP identity")

        # Independent HTTP verification from two networks via Globalping.
        payload = {"type": "http", "target": HOST_V6,
                   "locations": [{"magic": "AS24940", "limit": 1}, {"magic": "AS197540", "limit": 1}],
                   "measurementOptions": {"protocol": "HTTP", "port": HTTP_PORT,
                                          "request": {"method": "GET", "path": "/identity"}}}
        req = urllib.request.Request("https://api.globalping.io/v1/measurements", json.dumps(payload).encode(),
                                     {"Content-Type": "application/json", "User-Agent": "oneclickvirt-live-acceptance/1.0"})
        with urllib.request.urlopen(req, timeout=30) as response:
            measurement = json.load(response)
        deadline = time.monotonic() + 120
        while time.monotonic() < deadline:
            with urllib.request.urlopen("https://api.globalping.io/v1/measurements/" + measurement["id"], timeout=30) as response:
                result = json.load(response)
            if result.get("status") != "in-progress":
                break
            time.sleep(3)
        if result.get("status") != "finished" or len(result.get("results", [])) != 2:
            raise RuntimeError("independent IPv6 HTTP measurement did not finish with two probes")
        for item in result["results"]:
            value = item.get("result", {})
            if value.get("statusCode") != 200 or value.get("rawBody", "").strip() != name:
                raise RuntimeError("independent IPv6 HTTP probe returned the wrong identity")
        print("PASS two independent Globalping networks reached the guest through host IPv6 NAT")

        source = socket.gethostbyname("216.126.233.222")
        record = verify_webssh(WEBSSH, HOST_V6, SSH_PORT, guest_password, name, source,
                               require_target_ipv6=True)
        print("PASS WebSSH IPv4 source reached exact public IPv6 NAT endpoint: " + record)
    finally:
        if created:
            for device in reversed(devices):
                run("incus config device remove " + shlex.quote(name) + " " + shlex.quote(device), check=False)
            run("incus delete --force " + shlex.quote(name), timeout=180, check=False)
            leftovers = run("incus list --format csv -c n | grep -Fx -- " + shlex.quote(name), check=False)
            if leftovers:
                raise RuntimeError("temporary production fixture survived cleanup")
            print("PASS temporary production container and IPv6 proxy devices removed")
        node.close()


if __name__ == "__main__":
    main()
