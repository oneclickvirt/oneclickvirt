#!/usr/bin/env python3
"""Use the single authorized Hetzner server as an independent IPv6 probe.

The server is rebuilt in place (never deleted or duplicated), then production
and Hetzner authenticate to one another over their public IPv6 paths.  Host
keys are pinned in memory/temporary files for this controlled rebuild; the
operator's known_hosts is never changed.
"""
import getpass
import hashlib
import ipaddress
import json
import os
import shlex
import time
import urllib.request

import paramiko

from live_ssh import strict_node_client

API = "https://api.hetzner.cloud/v1"
SERVER_ID = os.environ.get("OCV_HETZNER_SERVER_ID", "")
PROD_HOST = os.environ.get("OCV_LIVE_HOST", "")
PROD_PORT = int(os.environ.get("OCV_LIVE_SSH_PORT", "1777"))
PROD_V6 = os.environ.get("OCV_LIVE_IPV6_TARGET", "")
HTTP_PORT = int(os.environ.get("OCV_HETZNER_HTTP_PORT", "29990"))
PROD_IMAGE = os.environ.get("OCV_LIVE_IMAGE", "")
GUEST_V4 = os.environ.get("OCV_LIVE_GUEST_IPV4", "10.101.179.241")
GUEST_V6 = os.environ.get("OCV_LIVE_GUEST_IPV6", "")
NAT_SSH_PORT = int(os.environ.get("OCV_LIVE_IPV6_SSH_PORT", "29987"))
NAT_HTTP_PORT = int(os.environ.get("OCV_LIVE_IPV6_HTTP_PORT", "29988"))


def read_channel(channel, timeout=45):
    channel.settimeout(timeout)
    data = bytearray()
    while True:
        if channel.recv_ready():
            data.extend(channel.recv(65536))
        elif channel.exit_status_ready():
            while channel.recv_ready():
                data.extend(channel.recv(65536))
            return data.decode(errors="replace"), channel.recv_exit_status()
        else:
            time.sleep(0.05)


def request(token, method, path, body=None):
    payload = None if body is None else json.dumps(body).encode()
    req = urllib.request.Request(API + path, payload,
                                 {"Authorization": "Bearer " + token,
                                  "Content-Type": "application/json"}, method=method)
    with urllib.request.urlopen(req, timeout=30) as response:
        return json.load(response)


def fingerprint(key):
    return hashlib.sha256(key.asbytes()).hexdigest()[:32]


def parse_keyscan(text, alias):
    keys = []
    for line in text.splitlines():
        if not line or line.startswith("#"):
            continue
        entry = paramiko.hostkeys.HostKeyEntry.from_line(line)
        if entry and entry.key:
            keys.append(entry.key)
    if not keys:
        raise RuntimeError("ssh-keyscan returned no host key for " + alias)
    return keys


def wait_action(token, action_id):
    deadline = time.monotonic() + 900
    while time.monotonic() < deadline:
        action = request(token, "GET", "/actions/" + str(action_id))["action"]
        if action["status"] == "success":
            return
        if action["status"] == "error":
            raise RuntimeError("Hetzner rebuild failed: " + str(action.get("error")))
        time.sleep(5)
    raise TimeoutError("Hetzner rebuild action timed out")


def main():
    if os.environ.get("OCV_LIVE_DISPOSABLE") != "yes":
        raise SystemExit("Set OCV_LIVE_DISPOSABLE=yes for the authorized live probe")
    for key, value in (("OCV_HETZNER_SERVER_ID", SERVER_ID), ("OCV_LIVE_HOST", PROD_HOST),
                       ("OCV_LIVE_IPV6_TARGET", PROD_V6), ("OCV_LIVE_IMAGE", PROD_IMAGE),
                       ("OCV_LIVE_GUEST_IPV6", GUEST_V6)):
        if not value:
            raise SystemExit("Missing " + key)
    if not ipaddress.ip_address(PROD_V6).is_global:
        raise SystemExit("OCV_LIVE_IPV6_TARGET must be a public IPv6 literal")
    token = getpass.getpass("Hetzner API token: ")
    production_password = getpass.getpass("Production node root password: ")
    server_id = str(SERVER_ID)
    servers = request(token, "GET", "/servers").get("servers", [])
    selected = [server for server in servers if str(server.get("id")) == server_id]
    if len(selected) != 1:
        raise RuntimeError("Expected exactly one explicitly selected Hetzner server")
    if len(servers) != 1:
        raise RuntimeError("Refusing to operate while the token project has more than one server")
    server = selected[0]
    ipv6_prefix = ipaddress.ip_network(server["public_net"]["ipv6"]["ip"], strict=False)
    hetzner_v6 = str(ipv6_prefix.network_address + 1)
    production = strict_node_client()
    production.connect(PROD_HOST, port=PROD_PORT, username="root", password=production_password,
                       timeout=15, auth_timeout=15, banner_timeout=15,
                       allow_agent=False, look_for_keys=False)

    def prod_run(command, timeout=120, check=True):
        stdin, stdout, _ = production.exec_command(command, timeout=timeout)
        stdout.channel.set_combine_stderr(True)
        stdin.channel.shutdown_write()
        output, status = read_channel(stdout.channel, timeout)
        if check and status:
            raise RuntimeError("production command failed: " + output[-1200:])
        return output.strip()

    fixture_name = "ocv-hz-nat-" + os.urandom(4).hex()
    fixture_created = False
    fixture_devices = []
    try:
        reset = request(token, "POST", "/servers/" + server_id + "/actions/reset_password")
        root_password = reset["root_password"]
        action = reset["action"]
        print("Password reset requested for the existing Hetzner server " + server_id, flush=True)
        wait_action(token, action["id"])
        print("PASS Hetzner API password reset completed; no server was created or deleted", flush=True)

        # Obtain the rebuilt host key through the already trusted production
        # connection, then use RejectPolicy with that exact temporary key.
        deadline = time.monotonic() + 300
        keyscan = ""
        while time.monotonic() < deadline:
            keyscan = prod_run("ssh-keyscan -6 -T 5 -t ed25519,ecdsa,rsa " + shlex.quote(hetzner_v6),
                               timeout=20, check=False)
            if keyscan.strip():
                break
            time.sleep(5)
        keys = parse_keyscan(keyscan, hetzner_v6)
        print("Pinned rebuilt Hetzner host key fingerprints: " + ",".join(fingerprint(k) for k in keys), flush=True)

        transport = production.get_transport()
        channel = transport.open_channel("direct-tcpip", (hetzner_v6, 22), (PROD_HOST, PROD_PORT), timeout=20)
        hetzner = paramiko.SSHClient()
        for key in keys:
            hetzner.get_host_keys().add(hetzner_v6, key.get_name(), key)
        hetzner.set_missing_host_key_policy(paramiko.RejectPolicy())
        try:
            hetzner.connect(hetzner_v6, username="root", password=root_password, sock=channel,
                            timeout=25, auth_timeout=25, banner_timeout=25,
                            allow_agent=False, look_for_keys=False)

            def hz_run(command, timeout=120, check=True):
                stdin, stdout, _ = hetzner.exec_command(command, timeout=timeout)
                stdout.channel.set_combine_stderr(True)
                stdin.channel.shutdown_write()
                output, status = read_channel(stdout.channel, timeout)
                if check and status:
                    raise RuntimeError("Hetzner command failed: " + output[-1200:])
                return output.strip()

            stdin, stdout, _ = hetzner.exec_command("set -eu; hostname; curl --noproxy '*' -6 -fsS --connect-timeout 10 --max-time 20 https://ipv6.ip.sb", timeout=45)
            stdin.channel.shutdown_write()
            output, status = read_channel(stdout.channel, 45)
            if status or hetzner_v6 not in output:
                raise RuntimeError("Hetzner IPv6 egress did not return its assigned public address: " + output[-500:])
            print("PASS production node authenticated to Hetzner over IPv6; Hetzner egress=" + hetzner_v6, flush=True)

            stdin, stdout, _ = hetzner.exec_command(
                "set -eu; command -v python3 >/dev/null || { apt-get update -qq; DEBIAN_FRONTEND=noninteractive apt-get install -y -qq python3; }",
                timeout=300)
            stdin.channel.shutdown_write()
            dependency_output, status = read_channel(stdout.channel, 300)
            if status:
                raise RuntimeError("could not prepare the temporary HTTP probe: " + dependency_output[-1200:])

            # Serve a temporary identity on Hetzner and fetch it from the
            # production node over the public IPv6 path.
            identity = "ocv-hz-ipv6-" + os.urandom(8).hex()
            script = ("from http.server import BaseHTTPRequestHandler,ThreadingHTTPServer\n"
                      "import socket\n"
                      "class S(ThreadingHTTPServer): address_family=socket.AF_INET6\n"
                      "class H(BaseHTTPRequestHandler):\n"
                      " def do_GET(self):\n"
                      "  b=" + repr(identity.encode()) + "; self.send_response(200); self.send_header('Content-Length',str(len(b))); self.end_headers(); self.wfile.write(b)\n"
                      "S(('::'," + str(HTTP_PORT) + "),H).serve_forever()\n")
            stdin, stdout, _ = hetzner.exec_command("nohup python3 -u -c " + shlex.quote(script) + " >/tmp/ocv-hz-ipv6.log 2>&1 & echo $!", timeout=30)
            stdin.channel.shutdown_write()
            pid, status = read_channel(stdout.channel, 30)
            if status or not pid.strip().isdigit():
                raise RuntimeError("could not start temporary Hetzner HTTP service")
            try:
                time.sleep(2)
                stdin, stdout, _ = hetzner.exec_command("ss -H -lnt 'sport = :" + str(HTTP_PORT) + "'", timeout=15)
                stdin.channel.shutdown_write()
                listener, status = read_channel(stdout.channel, 15)
                if status or not listener.strip():
                    stdin, stdout, _ = hetzner.exec_command("tail -n 30 /tmp/ocv-hz-ipv6.log", timeout=15)
                    stdin.channel.shutdown_write()
                    diagnostic, _ = read_channel(stdout.channel, 15)
                    raise RuntimeError("temporary Hetzner HTTP service did not listen: " + diagnostic[-1200:])
                body = prod_run("curl --noproxy '*' -g -6 -fsS --connect-timeout 10 --max-time 20 http://[" + hetzner_v6 + "]:" + str(HTTP_PORT), 45)
                if body != identity:
                    raise RuntimeError("production node received the wrong Hetzner HTTP identity")
                print("PASS production node fetched temporary HTTP service over Hetzner public IPv6", flush=True)
            finally:
                hetzner.exec_command("kill " + pid.strip(), timeout=15)

            # Authenticate in the reverse direction over production IPv6 using
            # an interactive OpenSSH client on Hetzner.  The host key is pinned
            # in a temporary file and never changes the operator's known_hosts.
            remote_known = "/tmp/ocv-prod-known-hosts"
            stdin, stdout, _ = hetzner.exec_command(
                "set -eu; ssh-keyscan -6 -T 5 -p " + str(PROD_PORT)
                + " -t ed25519,ecdsa,rsa " + shlex.quote(PROD_V6)
                + " > " + remote_known + "; test -s " + remote_known, timeout=30)
            stdin.channel.shutdown_write()
            keyscan_output, status = read_channel(stdout.channel, 30)
            if status:
                raise RuntimeError("Hetzner could not pin the production IPv6 SSH host key: " + keyscan_output[-800:])
            command = ("ssh -tt -6 -o StrictHostKeyChecking=yes -o UserKnownHostsFile=" + remote_known
                       + " -o PreferredAuthentications=password -o PubkeyAuthentication=no -p " + str(PROD_PORT)
                       + " root@" + PROD_V6 + " 'hostname'")
            channel = hetzner.get_transport().open_session(timeout=20)
            channel.get_pty(term="xterm", width=120, height=30)
            channel.exec_command(command)
            data = bytearray()
            sent = False
            deadline = time.monotonic() + 45
            while time.monotonic() < deadline and not channel.exit_status_ready():
                if channel.recv_ready():
                    data.extend(channel.recv(4096))
                    lower = bytes(data).lower()
                    if b"password:" in lower and not sent:
                        channel.send((production_password + "\n").encode())
                        sent = True
                else:
                    time.sleep(0.1)
            data.extend(channel.recv(4096) if channel.recv_ready() else b"")
            result = bytes(data).decode(errors="replace")
            if channel.exit_status_ready() and channel.recv_exit_status() == 0 and "localhost" in result:
                print("PASS Hetzner authenticated to the production node over public IPv6", flush=True)
            else:
                raise RuntimeError("reverse IPv6 SSH authentication failed: " + result[-800:])

            # End-to-end production Incus NAT-v6 acceptance.  The external TCP
            # connections originate on Hetzner; the guest host key is read via
            # the already authenticated production control channel and pinned.
            occupied = prod_run(
                "ss -H -lntup | grep -E '(:" + str(NAT_SSH_PORT) + "|:" + str(NAT_HTTP_PORT)
                + ")([[:space:]]|$)' || true")
            if occupied:
                raise RuntimeError("production NAT-v6 acceptance ports are occupied")
            addresses = prod_run("incus list --format csv -c 4,6")
            if GUEST_V4 in addresses or GUEST_V6 in addresses:
                raise RuntimeError("production NAT-v6 acceptance address is already assigned")
            prod_run("incus init " + shlex.quote(PROD_IMAGE) + " " + shlex.quote(fixture_name), 300)
            fixture_created = True
            prod_run("incus config device override " + shlex.quote(fixture_name) + " eth0")
            prod_run("incus config device set " + shlex.quote(fixture_name)
                     + " eth0 ipv4.address=" + shlex.quote(GUEST_V4)
                     + " ipv6.address=" + shlex.quote(GUEST_V6))
            prod_run("incus start " + shlex.quote(fixture_name), 180)
            guest_password = "OcvNatV6!" + os.urandom(10).hex()
            guest_identity = "ocv-nat-v6-" + os.urandom(8).hex()
            setup = (
                "printf '%s\\n' " + shlex.quote("root:" + guest_password) + " | chpasswd; "
                "apt-get update -qq; DEBIAN_FRONTEND=noninteractive apt-get install -y -qq openssh-server python3 curl; "
                "install -d -m 0755 /etc/ssh/sshd_config.d; "
                "printf 'PasswordAuthentication yes\\nPermitRootLogin yes\\n' > /etc/ssh/sshd_config.d/90-ocv-live.conf; "
                "systemctl restart ssh; printf '%s' " + shlex.quote(guest_identity) + " > /etc/ocv-live-identity")
            prod_run("incus exec " + shlex.quote(fixture_name) + " -- sh -ceu " + shlex.quote(setup), 300)
            prod_run("incus config device add " + shlex.quote(fixture_name) + " v6ssh proxy listen=tcp:["
                     + PROD_V6 + "]:" + str(NAT_SSH_PORT) + " connect=tcp:[" + GUEST_V6 + "]:22 nat=true")
            fixture_devices.append("v6ssh")
            http_program = (
                "from http.server import BaseHTTPRequestHandler,ThreadingHTTPServer\n"
                "import socket\n"
                "class S(ThreadingHTTPServer): address_family=socket.AF_INET6\n"
                "class H(BaseHTTPRequestHandler):\n"
                " def do_GET(self):\n"
                "  b=open('/etc/ocv-live-identity','rb').read(); self.send_response(200); self.send_header('Content-Length',str(len(b))); self.end_headers(); self.wfile.write(b)\n"
                "S(('::',18080),H).serve_forever()\n")
            prod_run("incus exec " + shlex.quote(fixture_name) + " -- sh -ceu " + shlex.quote(
                "nohup python3 -u -c " + shlex.quote(http_program) + " >/tmp/ocv-live-http.log 2>&1 &"))
            prod_run("incus config device add " + shlex.quote(fixture_name) + " v6http proxy listen=tcp:["
                     + PROD_V6 + "]:" + str(NAT_HTTP_PORT) + " connect=tcp:[" + GUEST_V6 + "]:18080 nat=true")
            fixture_devices.append("v6http")
            prod_run("incus exec " + shlex.quote(fixture_name)
                     + " -- sh -ceu " + shlex.quote(
                         "for i in $(seq 1 20); do ss -H -lnt 'sport = :18080' | grep -q . && exit 0; sleep 1; done; "
                         "cat /tmp/ocv-live-http.log >&2; exit 1"), 30)
            local_body = prod_run("curl --noproxy '*' -g -6 -fsS --retry 5 --retry-all-errors --retry-delay 1 "
                                  "--connect-timeout 5 --max-time 15 http://[" + PROD_V6 + "]:"
                                  + str(NAT_HTTP_PORT))
            if local_body != guest_identity:
                raise RuntimeError("production host reached the wrong NAT-v6 HTTP identity")
            body = hz_run("curl --noproxy '*' -g -6 -fsS --retry 5 --retry-all-errors --retry-delay 2 "
                          "--retry-max-time 60 --connect-timeout 5 --max-time 15 http://["
                          + PROD_V6 + "]:" + str(NAT_HTTP_PORT))
            if body != guest_identity:
                raise RuntimeError("Hetzner reached the wrong production NAT-v6 HTTP identity")
            print("PASS Hetzner reached the temporary Incus HTTP service through production IPv6 NAT", flush=True)

            guest_keys = prod_run("incus exec " + shlex.quote(fixture_name)
                                  + " -- sh -c 'cat /etc/ssh/ssh_host_*_key.pub'")
            guest_client = paramiko.SSHClient()
            guest_alias = "[" + PROD_V6 + "]:" + str(NAT_SSH_PORT)
            parsed = 0
            for line in guest_keys.splitlines():
                entry = paramiko.hostkeys.HostKeyEntry.from_line(guest_alias + " " + line.strip())
                if entry and entry.key:
                    guest_client.get_host_keys().add(guest_alias, entry.key.get_name(), entry.key)
                    parsed += 1
            if not parsed:
                raise RuntimeError("temporary Incus guest exposed no supported SSH host key")
            guest_client.set_missing_host_key_policy(paramiko.RejectPolicy())
            nat_channel = hetzner.get_transport().open_channel(
                "direct-tcpip", (PROD_V6, NAT_SSH_PORT), (hetzner_v6, 0), timeout=20)
            try:
                guest_client.connect(PROD_V6, port=NAT_SSH_PORT, username="root", password=guest_password,
                                     sock=nat_channel, timeout=25, auth_timeout=25, banner_timeout=25,
                                     allow_agent=False, look_for_keys=False)
                stdin, stdout, _ = guest_client.exec_command(
                    "set -eu; hostname; printf '%s\\n' \"$(cat /etc/ocv-live-identity)\"; printf '%s\\n' \"$SSH_CONNECTION\"; curl --noproxy '*' -6 -fsS --connect-timeout 10 --max-time 20 https://ipv6.ip.sb",
                    timeout=45)
                stdin.channel.shutdown_write()
                guest_result, status = read_channel(stdout.channel, 45)
                lines = guest_result.splitlines()
                if status or len(lines) < 4 or lines[0] != fixture_name or lines[1] != guest_identity:
                    raise RuntimeError("external NAT-v6 SSH reached the wrong guest: " + guest_result[-800:])
                if ipaddress.ip_address(lines[-1]).version != 6:
                    raise RuntimeError("temporary Incus guest did not have IPv6 egress")
                print("PASS Hetzner authenticated to the exact Incus guest through production IPv6 NAT", flush=True)
                print("PASS temporary Incus guest IPv6 egress=" + lines[-1], flush=True)
            finally:
                guest_client.close()
        finally:
            hetzner.close()
    finally:
        if fixture_created:
            for device in reversed(fixture_devices):
                prod_run("incus config device remove " + shlex.quote(fixture_name) + " " + shlex.quote(device),
                         check=False)
            prod_run("incus delete --force " + shlex.quote(fixture_name), timeout=180, check=False)
            if fixture_name in prod_run("incus list --format csv -c n", check=False).splitlines():
                raise RuntimeError("temporary production NAT-v6 fixture survived cleanup")
            print("PASS temporary production Incus guest and NAT-v6 devices removed", flush=True)
        production.close()


if __name__ == "__main__":
    main()
