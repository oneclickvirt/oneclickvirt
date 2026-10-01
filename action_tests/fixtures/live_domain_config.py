#!/usr/bin/env python3
"""Configure temporary per-domain routes on a live standalone Agent."""

import json
import pathlib
import subprocess
import sys
import urllib.request


BASE = pathlib.Path("/root/ocv-domain-live")
DOMAINS = ("beta2.spiritlhl.net", "beta3.spiritlhl.net")
API = "http://127.0.0.1:23782/api/v1/domain-proxy"


def token():
    for line in (BASE / "env").read_text().splitlines():
        if line.startswith("API_TOKEN="):
            return line.split("=", 1)[1].strip().strip('"')
    raise RuntimeError("API_TOKEN is missing")


def request(method, payload=None):
    data = json.dumps(payload).encode() if payload is not None else None
    req = urllib.request.Request(
        API,
        data=data,
        method=method,
        headers={"x-token": token(), "Content-Type": "application/json"},
    )
    with urllib.request.urlopen(req, timeout=10) as response:
        return json.load(response)


def add():
    cert, key = BASE / "domain-cert.pem", BASE / "domain-key.pem"
    if not cert.exists() or not key.exists():
        subprocess.run(
            [
                "openssl", "req", "-x509", "-newkey", "rsa:2048", "-nodes",
                "-days", "1", "-subj", "/CN=beta2.spiritlhl.net",
                "-addext", "subjectAltName=DNS:beta2.spiritlhl.net,DNS:beta3.spiritlhl.net",
                "-keyout", str(key), "-out", str(cert),
            ],
            check=True, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL,
        )
        key.chmod(0o600)
    for domain in DOMAINS:
        response = request("POST", {
            "domain": domain,
            "internal_ip": "172.16.1.2",
            "internal_port": 18080,
            "protocol": "http",
            "enable_ssl": True,
            "ssl_cert": cert.read_text(),
            "ssl_key": key.read_text(),
        })
        print(json.dumps({"domain": domain, "status": response.get("status")}))


def remove():
    for domain in DOMAINS:
        response = request("DELETE", {"domain": domain})
        print(json.dumps({"domain": domain, "removed": response.get("removed")}))


if __name__ == "__main__":
    command = sys.argv[1] if len(sys.argv) > 1 else "list"
    if command == "add":
        add()
    elif command == "list":
        rows = request("GET")
        print(json.dumps({"total": rows.get("total"), "domains": [x.get("domain") for x in rows.get("proxies", [])]}))
    elif command == "remove":
        remove()
    else:
        raise SystemExit("expected add, list, or remove")
