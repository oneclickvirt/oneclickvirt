"""Strict public IPv6 guest probes; internal SSH is never external evidence."""
import ipaddress
import json
import secrets
import shlex
import time
import urllib.request

from webssh_external_probe import _public_ipv6, verify_webssh


def guest_ipv6(detail):
    values = []
    for key in ("publicIPv6", "ipv6Address"):
        if not detail.get(key):
            continue
        try:
            address = ipaddress.ip_address(detail[key])
        except ValueError as exc:
            raise RuntimeError("Invalid panel IPv6 field: " + key) from exc
        if not _public_ipv6(address):
            raise RuntimeError("Panel IPv6 is not a public unicast address: " + key)
        values.append(str(address))
    if not values or len(set(values)) != 1:
        raise RuntimeError("Missing or conflicting panel public IPv6 addresses")
    return values[0]


def panel_ssh_endpoint(detail, network_type, host, ports):
    target = detail.get("sshHost")
    try:
        port = int(detail.get("sshPort", 0))
    except (TypeError, ValueError) as exc:
        raise RuntimeError("Panel returned an invalid SSH port") from exc
    if not target or not 1 <= port <= 65535:
        raise RuntimeError("Panel did not return an explicit SSH endpoint")
    if network_type == "ipv6_only":
        expected = guest_ipv6(detail)
        try:
            actual = str(ipaddress.ip_address(target))
        except ValueError as exc:
            raise RuntimeError("IPv6-only SSH endpoint is not an IPv6 address") from exc
        if actual != expected or port != 22:
            raise RuntimeError("IPv6-only SSH endpoint is not the guest's native port 22")
    elif network_type in ("nat_ipv4", "nat_ipv4_ipv6"):
        if target != host or port not in ports:
            raise RuntimeError("Panel SSH endpoint escaped the reserved node/NAT port range")
    else:
        raise ValueError("Unsupported live network type")
    return target, port


def validate_http_measurement(result, target, nonce, required=2):
    if result.get("status") != "finished" or len(result.get("results", [])) != required:
        raise RuntimeError("Incomplete independent IPv6 HTTP measurement")
    networks = set()
    for entry in result["results"]:
        probe, value = entry.get("probe", {}), entry.get("result", {})
        try:
            resolved = ipaddress.ip_address(value.get("resolvedAddress", ""))
        except ValueError as exc:
            raise RuntimeError("HTTP probe did not resolve the requested IPv6 address") from exc
        if (value.get("status") != "finished" or value.get("statusCode") != 200
                or resolved != ipaddress.ip_address(target) or value.get("rawBody", "").strip() != nonce):
            raise RuntimeError("Independent IPv6 HTTP probe failed or reached the wrong guest")
        if not probe.get("asn"):
            raise RuntimeError("HTTP probe lacks independent network identity")
        networks.add(probe["asn"])
    if len(networks) != required:
        raise RuntimeError("HTTP probes must use distinct independent networks")


def verify_guest_ipv6(guest, collect, detail, name, site, source, log):
    """Check exact-source egress, external HTTP, and external SSH; all mandatory.

    Globalping sees only the public target and temporary random HTTP identity.
    Only the user-selected WebSSH service receives temporary guest credentials.
    Each layer runs even when another fails, but any failure fails the test.
    """
    target = guest_ipv6(detail)
    if not site or not _public_ipv6(ipaddress.ip_address(source)):
        raise ValueError("IPv6 acceptance requires WebSSH and its actual public IPv6 source")
    nonce = secrets.token_hex(16)
    unit = "ocv-ipv6-http-" + nonce
    port = 18080
    errors = []
    opener = urllib.request.build_opener(urllib.request.ProxyHandler({}))

    def command(script, timeout=120):
        stdin, stdout, _ = guest.exec_command(script, timeout=timeout)
        stdin.channel.shutdown_write()
        out, err, code = collect(stdout.channel, timeout)
        if code:
            raise RuntimeError("Guest IPv6 probe command failed: " + err[-1200:])
        return out.strip()

    def api(path, body=None):
        request = urllib.request.Request("https://api.globalping.io/v1/" + path,
            data=None if body is None else json.dumps(body).encode(),
            headers={"Content-Type": "application/json", "User-Agent": "oneclickvirt-live-acceptance/1.0"})
        with opener.open(request, timeout=30) as response:
            return json.load(response)

    try:
        actual = command("curl --noproxy '*' -6 --interface " + shlex.quote(target)
                         + " -fsS --connect-timeout 10 --max-time 25 https://ipv6.ip.sb")
        if actual != target:
            raise RuntimeError("IPv6 egress source differs from the assigned guest address")
        log("PASS IPv6 guest egress: " + actual)
    except (OSError, RuntimeError) as exc:
        errors.append("egress: " + str(exc))
        log("FAIL IPv6 guest egress: " + str(exc))

    # Use a bounded transient service; never leave a public listener on failure.
    program = '''from http.server import ThreadingHTTPServer, BaseHTTPRequestHandler
import socket
class Server(ThreadingHTTPServer):
    address_family = socket.AF_INET6
class Handler(BaseHTTPRequestHandler):
    def do_GET(self):
        body = NONCE.encode()
        self.send_response(200 if self.path == '/identity' else 404)
        self.send_header('Content-Type', 'text/plain')
        self.send_header('Content-Length', str(len(body)))
        self.end_headers()
        self.wfile.write(body)
Server((TARGET, PORT), Handler).serve_forever()
'''.replace("NONCE", repr(nonce)).replace("TARGET", repr(target)).replace("PORT", str(port))
    attempted = False
    try:
        command("command -v python3 >/dev/null || { apt-get update -qq && DEBIAN_FRONTEND=noninteractive apt-get install -y -qq python3; }", 300)
        if command(f"ss -H -lnt 'sport = :{port}'"):
            raise RuntimeError("Guest IPv6 test port is already occupied")
        attempted = True
        command(shlex.join(["systemd-run", "--unit=" + unit, "--description=" + unit, "--collect",
                            "--property=RuntimeMaxSec=180", "/usr/bin/python3", "-u", "-c", program]))
        actual = command("curl --noproxy '*' -g -6 -fsS --retry 3 --retry-connrefused --connect-timeout 3 --max-time 15 "
                         + shlex.quote(f"http://[{target}]:{port}/identity"))
        if actual != nonce:
            raise RuntimeError("Wrong local IPv6 HTTP service identity")
        # Per-location limits are used instead of a global limit so the API
        # guarantees two different networks and does not reject the request.
        created = api("measurements", {"type": "http", "target": target,
            "locations": [{"magic": "AS24940", "limit": 1}, {"magic": "AS197540", "limit": 1}],
            "measurementOptions": {"protocol": "HTTP", "port": port, "request": {"method": "GET", "path": "/identity"}}})
        log("Public guest IPv6 HTTP measurement: " + created["id"])
        deadline = time.monotonic() + 90
        while time.monotonic() < deadline:
            result = api("measurements/" + created["id"])
            if result.get("status") != "in-progress":
                break
            time.sleep(3)
        else:
            raise TimeoutError("Independent IPv6 HTTP measurement timed out")
        for entry in result.get("results", []):
            log("IPv6 HTTP probe: " + json.dumps(entry, ensure_ascii=False))
        validate_http_measurement(result, target, nonce)
        log("PASS IPv6 guest HTTP: two independent public networks returned this guest's identity")
    except (OSError, RuntimeError, ValueError, KeyError) as exc:
        errors.append("HTTP: " + str(exc))
        log("FAIL IPv6 guest HTTP: " + str(exc))
    finally:
        if attempted:
            try:
                command("systemctl stop " + shlex.quote(unit))
                if command(f"ss -H -lnt 'sport = :{port}'"):
                    raise RuntimeError("Guest IPv6 HTTP listener survived cleanup")
            except (OSError, RuntimeError) as exc:
                errors.append("HTTP cleanup: " + str(exc))

    try:
        record = verify_webssh(site, target, 22, detail["password"], name, source, require_ipv6=True)
        log("PASS IPv6 guest external authenticated SSH: " + record)
    except (OSError, RuntimeError, ValueError) as exc:
        errors.append("SSH: " + str(exc))
        log("FAIL IPv6 guest external SSH: " + str(exc))
    if errors:
        raise RuntimeError("Public guest IPv6 acceptance failed: " + "; ".join(errors))
