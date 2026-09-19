#!/usr/bin/env python3
"""Production panel API acceptance for Incus/LXD managed IPv6 NAT.

This driver is deliberately narrower than the disposable-node matrix. It uses
an existing provider, creates one uniquely named container through the panel
API, verifies SSH and HTTP from the project's single Hetzner IPv6 probe, then
deletes the mapping and container through the panel API. It never creates,
rebuilds, or deletes a Hetzner server and never changes existing instances.
"""

import getpass
import ipaddress
import json
import os
import secrets
import shlex
import time
import urllib.error
import urllib.request

import paramiko

from hetzner_ipv6_external_probe import (
    fingerprint,
    parse_keyscan,
    read_channel,
    request as hetzner_request,
    wait_action,
)
from live_ssh import pinned_guest_client, strict_node_client


PANEL_BASE = os.environ.get("OCV_PANEL_BASE", "").rstrip("/")
PROVIDER_ID = int(os.environ.get("OCV_LIVE_PROVIDER_ID", "0"))
PROD_HOST = os.environ.get("OCV_LIVE_HOST", "")
PROD_PORT = int(os.environ.get("OCV_LIVE_SSH_PORT", "1777"))
PROD_V6 = os.environ.get("OCV_LIVE_IPV6_TARGET", "")
SERVER_ID = os.environ.get("OCV_HETZNER_SERVER_ID", "")
IMAGE = os.environ.get("OCV_LIVE_IMAGE", "debian-12-cloud")
GUEST_HTTP_PORT = int(os.environ.get("OCV_LIVE_GUEST_HTTP_PORT", "18080"))


def require_config():
    if os.environ.get("OCV_PRODUCTION_ACCEPTANCE") != "yes":
        raise SystemExit("Set OCV_PRODUCTION_ACCEPTANCE=yes for this authorized production acceptance")
    for name, value in (
        ("OCV_PANEL_BASE", PANEL_BASE),
        ("OCV_LIVE_PROVIDER_ID", PROVIDER_ID),
        ("OCV_LIVE_HOST", PROD_HOST),
        ("OCV_LIVE_IPV6_TARGET", PROD_V6),
        ("OCV_HETZNER_SERVER_ID", SERVER_ID),
    ):
        if not value:
            raise SystemExit("Missing " + name)
    if not ipaddress.ip_address(PROD_V6).is_global:
        raise SystemExit("OCV_LIVE_IPV6_TARGET must be a public IPv6 literal")
    if not 1 <= GUEST_HTTP_PORT <= 65535:
        raise SystemExit("OCV_LIVE_GUEST_HTTP_PORT must be in 1..65535")


def main():
    require_config()
    admin_password = getpass.getpass("Production panel admin password: ")
    production_password = getpass.getpass("Production node root password: ")
    hetzner_token = getpass.getpass("Hetzner API token: ")
    opener = urllib.request.build_opener(urllib.request.ProxyHandler({}))
    token = ""

    def api(method, path, body=None, allow_error=False):
        payload = None if body is None else json.dumps(body).encode()
        headers = {"Content-Type": "application/json"}
        if token:
            headers["Authorization"] = "Bearer " + token
        request = urllib.request.Request(PANEL_BASE + "/api/v1" + path, payload, headers, method=method)
        try:
            with opener.open(request, timeout=120) as response:
                result = json.load(response)
        except urllib.error.HTTPError as error:
            result = json.load(error)
        if result.get("code") != 200 and not allow_error:
            message = str(result.get("msg", result.get("message", "API request failed")))
            if result.get("details"):
                message += ": " + str(result["details"])
            raise RuntimeError(method + " " + path + ": " + message)
        return result if allow_error else result.get("data")

    def wait_task(task_or_id, label, timeout=900):
        task_id = task_or_id
        if isinstance(task_or_id, dict):
            task_id = task_or_id.get("taskId", task_or_id.get("task_id", task_or_id.get("id")))
        if not task_id:
            raise RuntimeError(label + " did not return a task ID")
        deadline = time.monotonic() + timeout
        last_status = None
        while time.monotonic() < deadline:
            task = api("GET", "/admin/tasks/" + str(task_id))
            status = task.get("status")
            if status != last_status:
                print(label + ": " + str(status), flush=True)
                last_status = status
            if status in ("completed", "success"):
                return task
            if status in ("failed", "cancelled", "timeout"):
                raise RuntimeError(label + ": " + str(task.get("errorMessage", task.get("statusMessage"))))
            time.sleep(3)
        raise TimeoutError(label + " task timed out")

    def connect_node():
        client = strict_node_client()
        client.connect(PROD_HOST, port=PROD_PORT, username="root", password=production_password,
                       timeout=20, auth_timeout=20, banner_timeout=20,
                       allow_agent=False, look_for_keys=False)
        client.get_transport().set_keepalive(15)
        return client

    node = connect_node()

    def prod_run(command, timeout=180, check=True):
        nonlocal node
        transport = node.get_transport()
        if transport is None or not transport.is_active():
            node.close()
            node = connect_node()
        stdin, stdout, _ = node.exec_command(command, timeout=timeout)
        stdout.channel.set_combine_stderr(True)
        stdin.channel.shutdown_write()
        output, status = read_channel(stdout.channel, timeout)
        if check and status:
            raise RuntimeError("production command failed: " + output[-1600:])
        return output.strip()

    run_id = "ocv-panel-v6-" + str(int(time.time())) + "-" + secrets.token_hex(3)
    instance_id = None
    mapping_id = None
    ssh_port = None
    http_host_port = None
    initial_names = set()
    original_limits = None
    limits_changed = False
    primary_error = None
    cleanup_errors = []
    hetzner = None
    try:
        login = api("POST", "/auth/login", {"username": "admin", "password": admin_password})
        token = login["token"]
        provider = api("GET", "/admin/providers/" + str(PROVIDER_ID))
        if provider.get("type") not in ("incus", "lxd"):
            raise RuntimeError("production provider is not Incus/LXD")
        if provider.get("networkType") != "nat_ipv4_ipv6":
            raise RuntimeError("production provider is not using nat_ipv4_ipv6")
        file_path = provider.get("ipv6AddressFilePath", "")
        if not file_path:
            raise RuntimeError("production provider no longer has the reported IPv6 pool file configured")
        print("PASS provider uses managed Incus/LXD NAT-v6 while retaining IPv6 file configuration", flush=True)

        original_limits = {
            "containerLimitCpu": bool(provider.get("containerLimitCpu")),
            "containerLimitMemory": bool(provider.get("containerLimitMemory")),
            "containerLimitDisk": bool(provider.get("containerLimitDisk")),
        }
        if any(original_limits.values()):
            update = api("PUT", "/admin/providers/" + str(PROVIDER_ID), {
                "containerLimitCpu": False,
                "containerLimitMemory": False,
                "containerLimitDisk": False,
            })
            limits_changed = True
            if update and update.get("runtimeReloadTask"):
                wait_task(update["runtimeReloadTask"], "temporary provider budget refresh", timeout=600)
            print("Temporarily disabled container budget accounting for the owned acceptance fixture", flush=True)

        instances = api("GET", "/admin/instances?page=1&pageSize=100")
        initial_names = {item["name"] for item in instances.get("list", [])}
        runtime_names = set(prod_run("incus list --format csv -c n").splitlines())
        if run_id in initial_names or run_id in runtime_names:
            raise RuntimeError("random acceptance name already exists")

        task = wait_task(api("POST", "/admin/instances", {
            "name": run_id,
            "provider_id": PROVIDER_ID,
            "instance_type": "container",
            "image": IMAGE,
            "cpu": 1,
            "memory": 256,
            "disk": 3,
            "bandwidth": 100,
            "network_type": "nat_ipv4_ipv6",
        }), "panel create")
        instance_id = task.get("instanceId", task.get("instance_id"))
        if not instance_id:
            raise RuntimeError("completed create task did not expose its instance ID")
        detail = api("GET", "/admin/instances/" + str(instance_id))
        if detail.get("name") != run_id:
            raise RuntimeError("panel created a different instance")
        guest_v6 = detail.get("ipv6Address", "")
        if not guest_v6 or not ipaddress.ip_address(guest_v6).is_private:
            raise RuntimeError("managed NAT-v6 guest did not receive a ULA address: " + guest_v6)
        password = detail.get("password")
        ssh_port = int(detail.get("sshPort", 0))
        if not password or not ssh_port:
            raise RuntimeError("panel did not return disposable guest SSH credentials")
        prod_run("incus config set " + shlex.quote(run_id) + " user.ocv.test=" + shlex.quote(run_id))
        print("PASS panel created the managed NAT-v6 container without consuming the configured /64 file", flush=True)

        setup = (
            "command -v python3 >/dev/null || { apt-get update -qq; "
            "DEBIAN_FRONTEND=noninteractive apt-get install -y -qq python3; }; "
            "printf %s " + shlex.quote(run_id) + " > /tmp/ocv-panel-v6-identity; "
            "nohup python3 -u -m http.server " + str(GUEST_HTTP_PORT)
            + " --bind :: --directory /tmp >/tmp/ocv-panel-v6-http.log 2>&1 </dev/null &"
        )
        prod_run("incus exec " + shlex.quote(run_id) + " -- sh -ceu " + shlex.quote(setup), timeout=360)
        prod_run("incus exec " + shlex.quote(run_id) + " -- sh -ceu " + shlex.quote(
            "for i in $(seq 1 30); do ss -H -lnt 'sport = :" + str(GUEST_HTTP_PORT)
            + "' | grep -q . && exit 0; sleep 1; done; cat /tmp/ocv-panel-v6-http.log >&2; exit 1"), timeout=45)

        mapping = api("POST", "/admin/port-mappings", {
            "instanceId": instance_id,
            "guestPort": GUEST_HTTP_PORT,
            "hostPort": 0,
            "portCount": 1,
            "protocol": "tcp",
            "description": run_id,
            "mappingType": "node",
            "internalHost": "",
            "ipv6Enabled": True,
        })
        mapping_id = mapping["portId"]
        wait_task(mapping, "panel HTTP mapping")
        mappings = api("GET", "/admin/instances/" + str(instance_id) + "/port-mappings")
        row = next((item for item in mappings if int(item.get("id", 0)) == int(mapping_id)), None)
        if not row or not row.get("ipv6Enabled"):
            raise RuntimeError("panel HTTP mapping did not retain its IPv6 family contract")
        http_host_port = int(row["hostPort"])

        servers = hetzner_request(hetzner_token, "GET", "/servers").get("servers", [])
        selected = [server for server in servers if str(server.get("id")) == str(SERVER_ID)]
        if len(servers) != 1 or len(selected) != 1:
            raise RuntimeError("refusing to operate unless the selected Hetzner server is the project's only server")
        server = selected[0]
        prefix = ipaddress.ip_network(server["public_net"]["ipv6"]["ip"], strict=False)
        hetzner_v6 = str(prefix.network_address + 1)
        reset = hetzner_request(hetzner_token, "POST", "/servers/" + str(SERVER_ID) + "/actions/reset_password")
        wait_action(hetzner_token, reset["action"]["id"])
        root_password = reset["root_password"]
        keyscan = ""
        deadline = time.monotonic() + 300
        while time.monotonic() < deadline:
            keyscan = prod_run("ssh-keyscan -6 -T 5 -t ed25519,ecdsa,rsa " + shlex.quote(hetzner_v6),
                               timeout=20, check=False)
            if keyscan:
                break
            time.sleep(5)
        keys = parse_keyscan(keyscan, hetzner_v6)
        print("Pinned Hetzner host key fingerprints: " + ",".join(fingerprint(key) for key in keys), flush=True)
        channel = node.get_transport().open_channel("direct-tcpip", (hetzner_v6, 22), (PROD_HOST, PROD_PORT), timeout=25)
        hetzner = paramiko.SSHClient()
        for key in keys:
            hetzner.get_host_keys().add(hetzner_v6, key.get_name(), key)
        hetzner.set_missing_host_key_policy(paramiko.RejectPolicy())
        hetzner.connect(hetzner_v6, username="root", password=root_password, sock=channel,
                        timeout=25, auth_timeout=25, banner_timeout=25,
                        allow_agent=False, look_for_keys=False)

        def hz_run(command, timeout=120):
            stdin, stdout, _ = hetzner.exec_command(command, timeout=timeout)
            stdout.channel.set_combine_stderr(True)
            stdin.channel.shutdown_write()
            output, status = read_channel(stdout.channel, timeout)
            if status:
                raise RuntimeError("Hetzner command failed: " + output[-1600:])
            return output.strip()

        body = hz_run("curl --noproxy '*' -g -6 -fsS --retry 8 --retry-all-errors --retry-delay 2 "
                      "--connect-timeout 8 --max-time 20 http://[" + PROD_V6 + "]:"
                      + str(http_host_port) + "/ocv-panel-v6-identity", timeout=90)
        if body != run_id:
            raise RuntimeError("Hetzner reached the wrong panel HTTP guest")
        print("PASS Hetzner reached the panel-created guest HTTP service through public IPv6 NAT", flush=True)

        host_keys = prod_run("incus exec " + shlex.quote(run_id) + " -- sh -c "
                             + shlex.quote("cat /etc/ssh/ssh_host_*_key.pub"))
        guest = pinned_guest_client(PROD_V6, ssh_port, host_keys)
        guest_channel = hetzner.get_transport().open_channel(
            "direct-tcpip", (PROD_V6, ssh_port), (hetzner_v6, 0), timeout=25)
        try:
            guest.connect(PROD_V6, port=ssh_port, username="root", password=password, sock=guest_channel,
                          timeout=25, auth_timeout=25, banner_timeout=25,
                          allow_agent=False, look_for_keys=False)
            stdin, stdout, _ = guest.exec_command(
                "set -eu; hostname; printf '%s\\n' \"$SSH_CONNECTION\"; "
                "curl --noproxy '*' -6 -fsS --connect-timeout 10 --max-time 20 https://ipv6.ip.sb",
                timeout=45)
            stdin.channel.shutdown_write()
            result, status = read_channel(stdout.channel, 45)
            lines = result.splitlines()
            if status or len(lines) < 3 or lines[0] != run_id:
                raise RuntimeError("Hetzner IPv6 SSH reached the wrong guest: " + result[-800:])
            if ipaddress.ip_address(lines[-1]) != ipaddress.ip_address(PROD_V6):
                raise RuntimeError("panel guest IPv6 egress did not use the production public IPv6")
            print("PASS Hetzner authenticated to the exact panel-created guest through public IPv6 NAT", flush=True)
            print("PASS panel-created guest IPv6 egress=" + lines[-1], flush=True)
        finally:
            guest.close()
    except Exception as error:
        primary_error = error
    finally:
        if hetzner is not None:
            hetzner.close()
        if token and instance_id is None:
            try:
                current = api("GET", "/admin/instances?page=1&pageSize=100")
                owned = [item for item in current.get("list", []) if item.get("name") == run_id]
                if len(owned) > 1:
                    raise RuntimeError("more than one panel record has the owned random name")
                if owned:
                    instance_id = owned[0]["id"]
            except Exception as error:
                cleanup_errors.append("owned instance lookup: " + str(error))
        if token and mapping_id is not None:
            try:
                response = api("DELETE", "/admin/port-mappings/" + str(mapping_id), allow_error=True)
                if response.get("code") == 200:
                    wait_task(response["data"], "panel HTTP mapping delete", timeout=600)
                elif response.get("code") != 404:
                    raise RuntimeError(str(response.get("msg")))
                mapping_id = None
            except Exception as error:
                cleanup_errors.append("mapping cleanup: " + str(error))
        if token and instance_id is not None:
            try:
                response = api("DELETE", "/admin/instances/" + str(instance_id), allow_error=True)
                if response.get("code") not in (200, 404):
                    raise RuntimeError(str(response.get("msg")))
                deadline = time.monotonic() + 600
                while time.monotonic() < deadline:
                    if api("GET", "/admin/instances/" + str(instance_id), allow_error=True).get("code") == 404:
                        break
                    time.sleep(3)
                else:
                    raise TimeoutError("panel instance deletion did not finish")
                instance_id = None
            except Exception as error:
                cleanup_errors.append("instance cleanup: " + str(error))
        if token and limits_changed and original_limits is not None:
            try:
                update = api("PUT", "/admin/providers/" + str(PROVIDER_ID), original_limits)
                if update and update.get("runtimeReloadTask"):
                    wait_task(update["runtimeReloadTask"], "restore provider budget settings", timeout=600)
                restored = api("GET", "/admin/providers/" + str(PROVIDER_ID))
                for key, expected in original_limits.items():
                    if bool(restored.get(key)) != expected:
                        raise RuntimeError(key + " was not restored")
                limits_changed = False
            except Exception as error:
                cleanup_errors.append("provider budget restore: " + str(error))
        try:
            marker = prod_run("incus config get " + shlex.quote(run_id) + " user.ocv.test 2>/dev/null || true",
                              check=False)
            if marker == run_id:
                cleanup_errors.append("exact test-marked runtime guest remains: " + run_id)
            final_names = set(prod_run("incus list --format csv -c n", check=False).splitlines())
            if run_id in final_names:
                cleanup_errors.append("runtime guest remains: " + run_id)
            if http_host_port:
                residue = prod_run("ss -H -lntup 'sport = :" + str(http_host_port)
                                   + "'; nft list ruleset 2>/dev/null | grep -w " + str(http_host_port)
                                   + " || true", check=False)
                if residue:
                    cleanup_errors.append("HTTP mapping port remains: " + str(http_host_port))
            if ssh_port:
                residue = prod_run("ss -H -lntup 'sport = :" + str(ssh_port)
                                   + "'; nft list ruleset 2>/dev/null | grep -w " + str(ssh_port)
                                   + " || true", check=False)
                if residue:
                    cleanup_errors.append("SSH mapping port remains: " + str(ssh_port))
            if token and initial_names:
                final = api("GET", "/admin/instances?page=1&pageSize=100")
                if {item["name"] for item in final.get("list", [])} != initial_names:
                    cleanup_errors.append("panel instance set changed outside the owned fixture")
        finally:
            node.close()

    if cleanup_errors:
        raise RuntimeError("; ".join(cleanup_errors))
    if primary_error is not None:
        raise primary_error
    print("PASS panel mapping and container were deleted; existing production instances were unchanged", flush=True)
    print("PASS production panel API Incus managed IPv6 NAT acceptance", flush=True)


if __name__ == "__main__":
    main()
