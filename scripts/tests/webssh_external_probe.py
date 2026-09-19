import codecs
import http.cookiejar
import ipaddress
import json
import re
import secrets
import socket
import ssl
import time
import urllib.parse
import urllib.request


def _public_ipv6(address):
    return (address.version == 6 and address.is_global and not address.is_multicast
            and not address.is_reserved and not address.is_site_local and address.ipv4_mapped is None)


def validate_ssh_connection(connection, target_host, target_port, expected_source_ip, *, require_ipv6=False):
    """Verify the connection the guest actually accepted, including IPv6 family."""
    fields = connection.split()
    if len(fields) != 4:
        raise RuntimeError("WebSSH returned an invalid SSH_CONNECTION record")
    try:
        source, destination = ipaddress.ip_address(fields[0]), ipaddress.ip_address(fields[2])
        source_port, destination_port = int(fields[1]), int(fields[3])
        expected_source = ipaddress.ip_address(expected_source_ip)
    except ValueError as exc:
        raise RuntimeError("WebSSH returned an invalid SSH_CONNECTION address or port") from exc
    if not (1 <= source_port <= 65535 and 1 <= destination_port <= 65535):
        raise RuntimeError("WebSSH returned an invalid SSH_CONNECTION port")
    if source != expected_source:
        raise RuntimeError("SSH did not originate from the authorized independent probe")
    try:
        target = ipaddress.ip_address(target_host)
    except ValueError:
        target = None
    if require_ipv6 or (target is not None and target.version == 6):
        if target is None or not _public_ipv6(target):
            raise ValueError("IPv6 acceptance requires an explicit public IPv6 target")
        if not _public_ipv6(source) or destination.version != 6:
            raise RuntimeError("WebSSH did not establish a public IPv6 SSH connection")
        if destination != target or destination_port != int(target_port):
            raise RuntimeError("WebSSH reached a different IPv6 endpoint")
        if source == destination:
            raise RuntimeError("WebSSH probe must be independent of the target")


def verify_webssh(site, target_host, target_port, password, expected_name, expected_source_ip, *, require_ipv6=False):
    import websocket

    if not expected_source_ip:
        raise ValueError("The independent probe source IP is required")
    ipaddress.ip_address(expected_source_ip)
    if not 1 <= int(target_port) <= 65535:
        raise ValueError("Invalid WebSSH target port")
    if require_ipv6:
        target = ipaddress.ip_address(target_host)
        if not _public_ipv6(target):
            raise ValueError("IPv6 acceptance requires an explicit public IPv6 target")
    parsed = urllib.parse.urlsplit(site)
    if parsed.scheme not in ("http", "https") or not parsed.hostname:
        raise ValueError("WebSSH requires an HTTP(S) base URL")
    cookies = http.cookiejar.CookieJar()
    opener = urllib.request.build_opener(urllib.request.ProxyHandler({}), urllib.request.HTTPCookieProcessor(cookies))
    with opener.open(site, timeout=20) as response:
        html = response.read().decode()
    xsrf = re.search(r'name="_xsrf" value="([^"]+)"', html)
    if not xsrf:
        raise RuntimeError("WebSSH form did not supply CSRF token")
    form = {"hostname": target_host, "port": str(target_port), "username": "root", "password": password,
            "term": "xterm-256color", "_xsrf": xsrf.group(1)}
    origin = parsed.scheme + "://" + parsed.netloc
    request = urllib.request.Request(site, urllib.parse.urlencode(form).encode(), {"Origin": origin, "Referer": site})
    with opener.open(request, timeout=40) as response:
        result = json.load(response)
    if not result.get("id"):
        raise RuntimeError("WebSSH failed to connect: " + str(result.get("status")))
    cookie = "; ".join(item.name + "=" + item.value for item in cookies)
    ws_base = site.replace("https://", "wss://").replace("http://", "ws://")
    # HTTP and WebSocket must use the same network route: WebSSH binds the
    # terminal worker to the initiating client's source IP.
    transport = socket.create_connection((parsed.hostname, parsed.port or (443 if parsed.scheme == "https" else 80)), timeout=20)
    try:
        if parsed.scheme == "https":
            transport = ssl.create_default_context().wrap_socket(transport, server_hostname=parsed.hostname)
        sock = websocket.create_connection(
            ws_base.rstrip("/") + "/ws?id=" + urllib.parse.quote(result["id"]),
            origin=origin, cookie=cookie, timeout=25,
            socket=transport)
    except BaseException:
        transport.close()
        raise
    output = ""
    decoder = codecs.getincrementaldecoder("utf-8")("replace")
    nonce = secrets.token_hex(16)
    marker = re.compile(r"\r?\nOCV_" + nonce + r"_BEGIN\r?\n([^\r\n]+)\r?\n([^\r\n]+)\r?\nOCV_" + nonce + r"_END")
    try:
        sock.send(json.dumps({"data": "printf '\\nOCV_%s_BEGIN\\n' " + nonce + "; hostname; printf '%s\\n' \"$SSH_CONNECTION\"; printf 'OCV_%s_END\\n' " + nonce + "; exit\r"}))
        deadline = time.monotonic() + 30
        while time.monotonic() < deadline:
            sock.settimeout(max(0.001, deadline - time.monotonic()))
            try:
                frame = sock.recv()
            except websocket.WebSocketTimeoutException as exc:
                raise TimeoutError("WebSSH identity verification timed out") from exc
            if not frame:
                break
            output += decoder.decode(frame) if isinstance(frame, bytes) else frame
            match = marker.search(output)
            if match:
                if match.group(1) != expected_name:
                    raise RuntimeError("WebSSH reached a different or stale guest")
                validate_ssh_connection(match.group(2), target_host, target_port, expected_source_ip,
                                        require_ipv6=require_ipv6)
                return match.group(2)
            output = output[-65536:]
        raise RuntimeError("WebSSH terminal ended without verified guest identity")
    finally:
        sock.close()
