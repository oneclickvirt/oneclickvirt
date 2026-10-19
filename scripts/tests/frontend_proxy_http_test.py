#!/usr/bin/env python3
"""Exercise installer-generated Caddy and Nginx configs with real HTTP servers."""
import http.server
import http.client
import os
from pathlib import Path
import shutil
import socket
import subprocess
import tempfile
import threading
import time

ROOT = Path(__file__).resolve().parents[2]


class Backend(http.server.BaseHTTPRequestHandler):
    def do_GET(self):
        if self.path.endswith(('agent', '/ssh', '/terminal')):
            if self.headers.get('Upgrade', '').lower() != 'websocket' or 'upgrade' not in self.headers.get('Connection', '').lower():
                self.send_error(400, 'WebSocket upgrade headers were not forwarded')
                return
        self.send_response(200)
        self.send_header("Content-Type", "application/json")
        self.end_headers()
        self.wfile.write(b'{"backend":true}')

    def log_message(self, *_args):
        pass


def free_port():
    with socket.socket() as sock:
        sock.bind(("127.0.0.1", 0))
        return sock.getsockname()[1]


def request(port, path, headers=None):
    connection = http.client.HTTPConnection("127.0.0.1", port, timeout=3)
    try:
        connection.request("GET", path, headers=headers or {})
        with connection.getresponse() as response:
            return response.status, response.headers, response.read()
    finally:
        connection.close()


def check_proxy(binary, kind, base, backend_port):
    port = free_port()
    folder = base / kind
    folder.mkdir()
    web = folder / "web"
    (web / "js").mkdir(parents=True)
    (web / "index.html").write_text("<!doctype html><title>entry-fixture</title>")
    (web / "js/current.js").write_text("export const current = true;")
    env = dict(os.environ, TEST_ROOT=str(ROOT), TEST_DIR=str(folder), TEST_PORT=str(port))
    # Extract production functions; never invoke main or install any services.
    subprocess.run(["bash", "-c", '''
set -euo pipefail
source <(sed '/^main "\\$@"$/d' "$TEST_ROOT/scripts/install_full.sh")
TLS_METHOD=off DOMAIN=127.0.0.1 OS_FAMILY=test
WEB_DIR="$TEST_DIR/web" CADDY_CONFIG_DIR="$TEST_DIR" CADDY_LOG_DIR="$TEST_DIR/logs" NGINX_CONF_DIR="$TEST_DIR"
configure_nginx
DOMAIN="127.0.0.1:$TEST_PORT"
configure_caddy
'''], env=env, check=True, stdout=subprocess.DEVNULL)
    if kind == "caddy":
        config = folder / "Caddyfile"
        text = config.read_text().replace("127.0.0.1:8888", f"127.0.0.1:{backend_port}")
        config.write_text("{\n admin off\n auto_https off\n}\n" + text)
        command = [binary, "run", "--config", str(config), "--adapter", "caddyfile"]
    elif kind == "nginx":
        site = folder / "oneclickvirt.conf"
        text = site.read_text().replace("listen 80;", f"listen 127.0.0.1:{port};")
        site.write_text(text.replace("127.0.0.1:8888", f"127.0.0.1:{backend_port}"))
        config = folder / "nginx.conf"
        user = "user root;\n" if hasattr(os, "geteuid") and os.geteuid() == 0 else ""
        config.write_text(user + f'''daemon off;
pid {folder}/nginx.pid;
error_log stderr;
events {{}}
http {{
  access_log off;
  client_body_temp_path {folder}/client_body;
  proxy_temp_path {folder}/proxy;
  fastcgi_temp_path {folder}/fastcgi;
  uwsgi_temp_path {folder}/uwsgi;
  scgi_temp_path {folder}/scgi;
  types {{ text/html html; application/javascript js; }}
  include {site};
}}
''')
        command = [binary, "-p", str(folder), "-c", str(config)]
    else:
        config = folder / 'nginx.conf'
        text = (ROOT / 'deploy/all-in-one-nginx.conf').read_text()
        user = 'user root;' if hasattr(os, 'geteuid') and os.geteuid() == 0 else ''
        text = text.replace('user www-data;', user).replace('worker_processes auto;', 'worker_processes 1;')
        text = text.replace('pid /run/nginx.pid;', f'pid {folder}/nginx.pid;')
        text = text.replace('include /etc/nginx/mime.types;', 'types { text/html html; application/javascript js; }')
        text = text.replace('listen 80;', f'listen 127.0.0.1:{port};').replace('root /var/www/html;', f'root {web};')
        text = text.replace('127.0.0.1:8888', f'127.0.0.1:{backend_port}')
        text = text.replace('http {', f'''http {{
    client_body_temp_path {folder}/client_body;
    proxy_temp_path {folder}/proxy;
    fastcgi_temp_path {folder}/fastcgi;
    uwsgi_temp_path {folder}/uwsgi;
    scgi_temp_path {folder}/scgi;
''', 1)
        config.write_text('daemon off;\n' + text)
        command = [binary, '-p', str(folder), '-c', str(config)]
    with (folder / "server.log").open("w+") as log:
        process = subprocess.Popen(command, stdout=log, stderr=log)
        try:
            deadline = time.monotonic() + 15
            while True:
                try:
                    request(port, "/")
                    break
                except (OSError, http.client.HTTPException):
                    if process.poll() is not None or time.monotonic() > deadline:
                        log.seek(0)
                        raise AssertionError(f"{kind} did not start: {log.read()}")
                    time.sleep(0.1)
            for path in ["/", "/index.html", "/admin/instances"]:
                code, headers, body = request(port, path)
                assert code == 200 and b"entry-fixture" in body, (kind, path, code)
                assert "no-cache" in headers.get("Cache-Control", ""), (kind, path, headers)
            code, _, body = request(port, "/js/current.js")
            assert code == 200 and b"export const current" in body
            for path in ["/js/stale.js", "/assets/stale.css", "/favicon-missing.ico", "/controller-agent/missing", "/controller-agent/stale.tar.gz"]:
                code, _, body = request(port, path)
                assert code == 404 and b"entry-fixture" not in body, (kind, path, code)
            for path in ["/api/module.json", "/swagger/spec.json"]:
                code, _, body = request(port, path)
                assert code == 200 and body == b'{"backend":true}', (kind, path, code)
            ws_paths = ['/api/v1/ws/agent', '/api/v1/admin/instances/1/ssh']
            if kind != 'nginx-container':
                ws_paths.append('/ws/terminal')
            for path in ws_paths:
                code, _, body = request(port, path, {"Connection": "Upgrade", "Upgrade": "websocket"})
                assert code == 200 and body == b'{"backend":true}', (kind, path, code)
        finally:
            if process.poll() is None:
                process.terminate()
                try:
                    process.wait(timeout=8)
                except subprocess.TimeoutExpired:
                    process.kill()
                    process.wait()
    print(f"{kind}: entry revalidation, static 404, API and WebSocket forwarding passed")


def main():
    backend = http.server.ThreadingHTTPServer(("127.0.0.1", 0), Backend)
    thread = threading.Thread(target=backend.serve_forever, daemon=True)
    thread.start()
    try:
        with tempfile.TemporaryDirectory(prefix="ocv-proxy-http-") as directory:
            for kind in ["caddy", "nginx", "nginx-container"]:
                binary_kind = 'nginx' if kind == 'nginx-container' else kind
                binary = shutil.which(os.environ.get(binary_kind.upper() + "_BIN", binary_kind))
                if not binary:
                    raise RuntimeError(f"{kind} is required for the real proxy regression")
                check_proxy(binary, kind, Path(directory), backend.server_port)
    finally:
        backend.shutdown()
        backend.server_close()
        thread.join(timeout=3)


if __name__ == "__main__":
    main()
