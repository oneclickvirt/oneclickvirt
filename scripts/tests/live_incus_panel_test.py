#!/usr/bin/env python3
"""Live Incus/LXD panel lifecycle acceptance on a disposable node.

Requires paramiko (and websocket-client for Agent or OCV_WEBSSH_URL), a
locally built OCV_PANEL_IMAGE, OCV_LIVE_HOST/PASSWORD/IMAGE, and
OCV_LIVE_DISPOSABLE=yes. Failed fixtures remain for diagnosis and are named
in the output; successful runs delete their guests and isolated local panel.
This covers the token API, not interactive UI or clean-OS installation.
OCV_LIVE_CONNECTION=agent additionally requires OCV_AGENT_BINARY and exercises
the real Rust Agent, concurrent commands/WebSSH, traffic counters and restart.
OCV_LIVE_BOOTSTRAP_SSH=yes installs a real guest SSH daemon and probe tools in
minimal base images that intentionally do not ship sshd (for example images:
debian/12); it does not replace the panel's instance creation or networking.
"""
import json
import os
import secrets
import shlex
import subprocess
import sys
import time
import urllib.error
import urllib.request

try:
    import paramiko
except ImportError:  # Optional dependency for explicitly requested live runs.
    paramiko = None
from webssh_external_probe import verify_webssh
from live_node_shell import node_command
from live_ssh import pinned_guest_client, strict_node_client
from live_panel_cleanup import remove_owned_panel
from live_nested_docker import verify_nested_docker
from live_ipv6_probe import verify_guest_ipv6
from live_agent_fixture import (RealAgentFixture, read_monitor, verify_agent_commands,
                                verify_agent_traffic, verify_panel_sessions, wait_agent)

sys.path.insert(0, os.path.join(os.path.dirname(__file__), "..", "..", "action_tests", "common"))
from remote import _collect_output, keep_ssh_alive


def log(message):
    print(message, flush=True)


def main():
    if os.environ.get("OCV_LIVE_DISPOSABLE") != "yes":
        raise SystemExit("Set OCV_LIVE_DISPOSABLE=yes only for an authorized disposable node")
    for name in ("OCV_LIVE_HOST", "OCV_LIVE_PASSWORD", "OCV_LIVE_IMAGE", "OCV_PANEL_IMAGE"):
        if not os.environ.get(name):
            raise SystemExit(f"Missing required live-test variable: {name}")
    if paramiko is None:
        raise SystemExit("Missing optional dependency: install scripts/tests/requirements-live.txt")
    image = os.environ["OCV_LIVE_IMAGE"]
    panel_image = os.environ["OCV_PANEL_IMAGE"]
    runtime = os.environ.get("OCV_LIVE_RUNTIME", "incus")
    connection = os.environ.get("OCV_LIVE_CONNECTION", "ssh")
    nested_docker = os.environ.get("OCV_LIVE_NESTED_DOCKER", "no")
    bootstrap_ssh = os.environ.get("OCV_LIVE_BOOTSTRAP_SSH", "no")
    if bootstrap_ssh not in ("yes", "no"):
        raise SystemExit("OCV_LIVE_BOOTSTRAP_SSH must be yes/no")
    if nested_docker not in ("yes", "no"):
        raise SystemExit("OCV_LIVE_NESTED_DOCKER must be yes/no")
    network_type = os.environ.get("OCV_LIVE_NETWORK_TYPE", "nat_ipv4")
    if network_type not in ("nat_ipv4", "nat_ipv4_ipv6", "dedicated_ipv4_ipv6", "ipv6_only"):
        raise SystemExit("OCV_LIVE_NETWORK_TYPE is not a supported live network type")
    ipv6_acceptance = os.environ.get("OCV_LIVE_IPV6", "no")
    if ipv6_acceptance not in ("yes", "no"):
        raise SystemExit("OCV_LIVE_IPV6 must be yes/no")
    if ipv6_acceptance == "yes" and network_type not in ("nat_ipv4_ipv6", "dedicated_ipv4_ipv6", "ipv6_only"):
        raise SystemExit("OCV_LIVE_IPV6 requires an IPv6-enabled network type")
    siblings = os.environ.get("OCV_LIVE_SIBLING_SESSIONS", "no")
    if siblings not in ("yes", "no") or (siblings == "yes" and connection != "agent"):
        raise SystemExit("OCV_LIVE_SIBLING_SESSIONS must be yes/no and requires Agent mode")
    if runtime not in ("incus", "lxd") or connection not in ("ssh", "agent"):
        raise SystemExit("OCV_LIVE_RUNTIME must be incus/lxd and OCV_LIVE_CONNECTION must be ssh/agent")
    cli = "incus" if runtime == "incus" else "lxc"
    agent_binary = os.environ.get("OCV_AGENT_BINARY", "")
    if connection == "agent" and not os.path.isfile(agent_binary):
        raise SystemExit("OCV_AGENT_BINARY must be a locally built Linux node binary")
    base_port = int(os.environ.get("OCV_LIVE_PORT", "29900"))
    port_count = 8 if siblings == "yes" else 4
    if not 1024 <= base_port <= 65536 - port_count:
        raise SystemExit("OCV_LIVE_PORT must leave room for " + str(port_count) + " ports in 1024..65535")
    ports = range(base_port, base_port + port_count)
    run_id = "ocv-panel-" + str(int(time.time())) + "-" + secrets.token_hex(3)
    container = run_id
    host = os.environ["OCV_LIVE_HOST"]
    node_password = os.environ["OCV_LIVE_PASSWORD"]
    if os.environ.get("OCV_WEBSSH_URL") and not os.environ.get("OCV_WEBSSH_SOURCE_IP"):
        raise SystemExit("Set OCV_WEBSSH_SOURCE_IP before enabling the independent WebSSH probe")
    if connection == "agent" or os.environ.get("OCV_WEBSSH_URL"):
        import websocket  # Fail before creating remote fixtures if unavailable.
    db_password = secrets.token_urlsafe(26) + "Aa1!"
    admin_password = secrets.token_urlsafe(26) + "Aa1!"
    opener = urllib.request.build_opener(urllib.request.ProxyHandler({}))
    token = ""
    base = ""
    provider_id = None
    instance_id = None
    agent_fixture = None
    node = strict_node_client()
    node.connect(host, username="root", password=node_password, timeout=15,
                 auth_timeout=15, banner_timeout=15, allow_agent=False, look_for_keys=False)
    keep_ssh_alive(node)


    def remote(command, timeout=120):
        stdin, stdout, stderr = node.exec_command(node_command(command), timeout=timeout)
        stdin.channel.shutdown_write()
        output, error, status = _collect_output(stdout.channel, timeout)
        if status:
            raise RuntimeError("remote command failed: " + error[:1200])
        return output.strip()


    def docker(*args):
        return subprocess.check_output(["docker", *args], text=True).strip()


    prepared_image = ""


    def prepare_ssh_image():
        nonlocal prepared_image
        if bootstrap_ssh != "yes":
            return image
        base_name = run_id + "-ssh-base"
        alias = run_id + "-ssh-image"
        install = """set -eu
if command -v apt-get >/dev/null 2>&1; then
    export DEBIAN_FRONTEND=noninteractive
    apt-get update -qq
    apt-get install -y -qq openssh-server curl python3 iproute2
elif command -v dnf >/dev/null 2>&1; then
    dnf install -y openssh-server curl python3 iproute
elif command -v yum >/dev/null 2>&1; then
    yum install -y openssh-server curl python3 iproute
elif command -v apk >/dev/null 2>&1; then
    apk add --no-cache openssh curl python3 iproute2
else
    echo 'no supported package manager for SSH image preparation' >&2
    exit 1
fi
mkdir -p /run/sshd
ssh-keygen -A
if [ -f /etc/ssh/sshd_config ]; then
    sed -ri 's/^[#[:space:]]*PermitRootLogin[[:space:]].*/PermitRootLogin yes/' /etc/ssh/sshd_config
    sed -ri 's/^[#[:space:]]*PasswordAuthentication[[:space:]].*/PasswordAuthentication yes/' /etc/ssh/sshd_config
fi
if command -v systemctl >/dev/null 2>&1; then
    systemctl enable ssh 2>/dev/null || systemctl enable sshd 2>/dev/null || true
elif command -v rc-update >/dev/null 2>&1; then
    rc-update add sshd default 2>/dev/null || true
fi
"""
        remote(cli + " launch " + shlex.quote(image) + " " + shlex.quote(base_name) + " --quiet", timeout=600)
        try:
            remote(cli + " exec " + shlex.quote(base_name) + " -- sh -ceu " + shlex.quote(install), timeout=600)
            remote(cli + " stop " + shlex.quote(base_name) + " --force", timeout=180)
            remote(cli + " publish " + shlex.quote(base_name) + " --alias " + shlex.quote(alias), timeout=600)
            prepared_image = alias
            log("Prepared temporary SSH-enabled image: " + alias)
            return alias
        finally:
            remote(cli + " delete " + shlex.quote(base_name) + " --force", timeout=180)


    def cleanup_prepared_image():
        nonlocal prepared_image
        if prepared_image:
            remote(cli + " image delete " + shlex.quote(prepared_image), timeout=180)
            log("Removed temporary SSH-enabled image: " + prepared_image)
            prepared_image = ""


    def bootstrap_guest_ssh(name, password):
        if bootstrap_ssh != "yes" or prepared_image:
            return
        # Keep this explicit and image-neutral. The panel still owns instance
        # creation; this only supplies the SSH/probe packages absent from
        # minimal cloud/container images used for network acceptance.
        script = """set -eu
if command -v apt-get >/dev/null 2>&1; then
    export DEBIAN_FRONTEND=noninteractive
    apt-get update -qq
    apt-get install -y -qq openssh-server curl python3 iproute2
elif command -v dnf >/dev/null 2>&1; then
    dnf install -y openssh-server curl python3 iproute
elif command -v yum >/dev/null 2>&1; then
    yum install -y openssh-server curl python3 iproute
elif command -v apk >/dev/null 2>&1; then
    apk add --no-cache openssh curl python3 iproute2
else
    echo 'no supported package manager for SSH bootstrap' >&2
    exit 1
fi
mkdir -p /run/sshd
ssh-keygen -A
if [ -f /etc/ssh/sshd_config ]; then
    sed -ri 's/^[#[:space:]]*PermitRootLogin[[:space:]].*/PermitRootLogin yes/' /etc/ssh/sshd_config
    sed -ri 's/^[#[:space:]]*PasswordAuthentication[[:space:]].*/PasswordAuthentication yes/' /etc/ssh/sshd_config
fi
printf '%s\\n' ROOT_PASSWORD | chpasswd
if command -v systemctl >/dev/null 2>&1; then
    systemctl enable ssh 2>/dev/null || systemctl enable sshd 2>/dev/null || true
    systemctl restart ssh 2>/dev/null || systemctl restart sshd 2>/dev/null || true
fi
if ! pgrep -x sshd >/dev/null 2>&1; then
    /usr/sbin/sshd
fi
""".replace("ROOT_PASSWORD", shlex.quote("root:" + password))
        remote(cli + " exec " + shlex.quote(name) + " -- sh -ceu " + shlex.quote(script))
        log("Bootstrapped guest SSH and IPv6 probe tools: " + name)


    def api(method, path, body=None, allow_error=False):
        payload = None if body is None else json.dumps(body).encode()
        headers = {"Content-Type": "application/json"}
        if token:
            headers["Authorization"] = "Bearer " + token
        request = urllib.request.Request(base + "/api/v1" + path, payload, headers, method=method)
        try:
            with opener.open(request, timeout=120) as response:
                result = json.load(response)
        except urllib.error.HTTPError as error:
            result = json.load(error)
        if result.get("code") != 200 and not allow_error:
            message = str(result.get("msg", result.get("message"))) + ": " + str(result.get("details", ""))
            for sensitive in (node_password, admin_password, db_password, token):
                if sensitive:
                    message = message.replace(sensitive, "[redacted]")
            raise RuntimeError(method + " " + path + ": " + message)
        return result if allow_error else result.get("data")


    def wait_task(result, label):
        task_id = result.get("task_id", result.get("taskId", result.get("id")))
        if not task_id:
            raise RuntimeError(label + " did not return task ID")
        deadline = time.monotonic() + 900
        last_status = None
        while time.monotonic() < deadline:
            task = api("GET", "/admin/tasks/" + str(task_id))
            status = task.get("status")
            if status != last_status:
                log(label + ": " + str(status))
                last_status = status
            if status in ("completed", "success"):
                return task
            if status in ("failed", "cancelled", "timeout"):
                raise RuntimeError(label + ": " + str(task.get("errorMessage", task.get("statusMessage"))))
            time.sleep(3)
        raise TimeoutError(label + " task timed out")


    success = False
    try:
        # Read-only checks first; never reuse someone else's container or ports.
        initial_names = set(filter(None, remote(cli + " list --format csv -c n").splitlines()))
        for port in ports:
            existing = remote(f"ss -H -lntup 'sport = :{port}'; nft list ruleset 2>/dev/null | grep -w {port} || true; iptables-save -t nat 2>/dev/null | grep -w {port} || true")
            if existing:
                raise RuntimeError("reserved live test port is occupied: " + str(port))
        guest_image = prepare_ssh_image()
        docker("run", "-d", "--name", container, "--label", "ocv.live.run=" + run_id,
               "-p", "127.0.0.1::80", "-e", "MYSQL_ROOT_PASSWORD=" + db_password,
               panel_image)
        local_port = docker("port", container, "80/tcp").splitlines()[0].rsplit(":", 1)[1]
        base = "http://127.0.0.1:" + local_port
        log("Current-source isolated panel started: " + base + " container=" + container)
        deadline = time.monotonic() + 180
        while True:
            try:
                state = api("GET", "/public/init/check")
                break
            except (OSError, ValueError, RuntimeError):
                if time.monotonic() >= deadline:
                    raise
                time.sleep(2)
        if not state.get("needInit"):
            raise RuntimeError("expected a fresh isolated panel")
        api("POST", "/public/init", {
            "admin": {"username": "liveadmin", "password": admin_password, "email": "live@example.invalid"},
            "database": {"type": "mysql", "host": "127.0.0.1", "port": "3306",
                         "database": "oneclickvirt", "username": "root", "password": db_password}})
        deadline = time.monotonic() + 180
        while not api("GET", "/public/init/check").get("ready"):
            if time.monotonic() >= deadline:
                raise TimeoutError("panel initialization did not finish")
            time.sleep(2)
        token = api("POST", "/auth/login", {"username": "liveadmin", "password": admin_password})["token"]
        api_token = api("POST", "/user/api-tokens", {"name": run_id, "expireDays": 1})
        token = api_token["token"]
        api("GET", "/admin/providers?page=1&pageSize=10")
        log("Initialization, admin sign-in and one-day API token verified")
        provider_body = {
            "name": run_id, "type": runtime, "connectionType": connection, "executionRule": "ssh_only",
            "endpoint": host, "portIP": host,
            "architecture": "amd64", "networkType": network_type,
            "storagePool": os.environ.get("OCV_LIVE_STORAGE_POOL", "default"),
            "container_enabled": True, "vm_enabled": False, "totalQuota": 4,
            "portRangeStart": base_port, "portRangeEnd": base_port + port_count - 1, "defaultPortCount": 4,
            "fixedPorts": [22], "ipv4PortMappingMethod": "device_proxy", "containerAllowNesting": True,
            "containerPrivileged": False,
            "enableTrafficControl": connection == "agent", "enableResourceMonitoring": connection == "agent",
            "trafficSyncMethod": "agent",
            "discoverMode": False, "autoImport": False, "maxContainerInstances": 2}
        if connection == "ssh":
            provider_body.update(sshPort=22, username="root", password=node_password)
        provider = api("POST", "/admin/providers", provider_body)
        provider_id = provider["id"]
        log(connection + " provider created: " + str(provider_id))
        if connection == "agent":
            secret = api("POST", f"/admin/providers/{provider_id}/agent-secret", {})["agentSecret"]
            monitoring = api("GET", f"/admin/providers/{provider_id}/monitoring/config")
            agent_fixture = RealAgentFixture(node, remote, run_id)
            version = agent_fixture.start(agent_binary, local_port, secret, monitoring["agent_token"])
            wait_agent(api, provider_id)
            log("Real node Agent connected: " + version)
            verify_agent_commands(api, provider_id)
            log("Command A timeout preserved command B and the same Agent connection")
        health = api("POST", f"/admin/providers/{provider_id}/health-check-task", {}, allow_error=True)
        if health.get("code") == 409:
            # Provider creation already schedules health. Observe that existing
            # task instead of treating the duplicate-operation guard as a defect.
            tasks = api("GET", f"/admin/tasks?providerId={provider_id}&page=1&pageSize=50")
            candidates = [item for item in tasks["list"]
                          if item.get("taskType") == "provider-health-check"
                          and item.get("status") in ("pending", "running", "processing", "completed")]
            if not candidates:
                raise RuntimeError("cannot identify the existing provider health task")
            wait_task(max(candidates, key=lambda item: item["id"]), "provider automatic health")
        elif health.get("code") == 200:
            wait_task(health["data"], "provider health")
        else:
            raise RuntimeError("provider health request failed: " + str(health.get("msg")))
        for generation in range(2):
            name = run_id + "-" + str(generation)
            if name in initial_names:
                raise RuntimeError("test instance name already exists")
            task = wait_task(api("POST", "/admin/instances", {
                "name": name, "provider_id": provider_id, "instance_type": "container",
                "image": guest_image,
                "cpu": 1, "memory": 512 if nested_docker == "yes" else 256, "disk": 3, "bandwidth": 100,
                "network_type": network_type}), "create generation " + str(generation))
            instance_id = task.get("instanceId", task.get("instance_id"))
            detail = api("GET", "/admin/instances/" + str(instance_id))
            if detail.get("name") != name:
                raise RuntimeError("created instance identity mismatch: " + str(detail.get("name")))
            remote(cli + " config set " + shlex.quote(name) + " user.ocv.test=" + shlex.quote(run_id))
            log("Instance detail fields: " + ",".join(sorted(detail)))
            password = detail.get("password")
            ssh_port = detail.get("sshPort", detail.get("ssh_port"))
            if not password or not ssh_port:
                raise RuntimeError("panel did not return SSH connection details")
            if int(ssh_port) not in ports:
                raise RuntimeError("panel SSH endpoint escaped the reserved NAT port range")
            bootstrap_guest_ssh(name, password)
            guest_keys = remote(
                cli + " exec " + shlex.quote(name)
                + " -- sh -c " + shlex.quote(
                    "for key in /etc/ssh/ssh_host_*_key.pub; do "
                    "test -r \"$key\" && cat -- \"$key\"; done"
                )
            )
            guest = pinned_guest_client(host, int(ssh_port), guest_keys)
            try:
                guest.connect(host, port=int(ssh_port), username="root", password=password,
                              timeout=20, auth_timeout=20, banner_timeout=20,
                              allow_agent=False, look_for_keys=False)
                keep_ssh_alive(guest)
                _, stdout, _ = guest.exec_command("hostname; printf '%s\\n' \"$SSH_CONNECTION\"", timeout=30)
                identity = stdout.read().decode().strip()
                if identity.splitlines()[0] != name:
                    raise RuntimeError("SSH reached a stale or different guest")
                log("Panel-created guest public NAT SSH verified: " + identity.replace("\n", " | "))
                if connection == "agent":
                    verify_panel_sessions(api, base, token, provider_id, instance_id, name)
                    log("Panel WebSSH session B survived session A close and concurrent Agent timeout")
                    if siblings == "yes" and generation == 0:
                        sibling_name = run_id + "-sibling"
                        log("Creating a second live guest for cross-container session isolation: " + sibling_name)
                        sibling_task = wait_task(api("POST", "/admin/instances", {
                            "name": sibling_name, "provider_id": provider_id, "instance_type": "container",
                            "image": guest_image, "cpu": 1, "memory": 256, "disk": 3, "bandwidth": 100,
                            "network_type": network_type}), "create sibling")
                        sibling_id = sibling_task.get("instanceId", sibling_task.get("instance_id"))
                        sibling_detail = api("GET", "/admin/instances/" + str(sibling_id))
                        if sibling_id == instance_id or sibling_detail.get("name") != sibling_name:
                            raise RuntimeError("sibling instance identity mismatch")
                        sibling_port = int(sibling_detail["sshPort"])
                        if sibling_port not in ports or sibling_port == int(ssh_port):
                            raise RuntimeError("sibling SSH port overlaps the original guest")
                        bootstrap_guest_ssh(sibling_name, sibling_detail["password"])
                        sibling_keys = remote(
                            cli + " exec " + shlex.quote(sibling_name)
                            + " -- sh -c " + shlex.quote(
                                "for key in /etc/ssh/ssh_host_*_key.pub; do "
                                "test -r \"$key\" && cat -- \"$key\"; done"
                            )
                        )
                        sibling_ssh = pinned_guest_client(host, sibling_port, sibling_keys)
                        try:
                            sibling_ssh.connect(host, port=sibling_port, username="root", password=sibling_detail["password"],
                                                timeout=20, auth_timeout=20, banner_timeout=20,
                                                allow_agent=False, look_for_keys=False)
                            stdin, stdout, _ = sibling_ssh.exec_command("hostname", timeout=30)
                            stdin.channel.shutdown_write()
                            sibling_identity, _, code = _collect_output(stdout.channel, 30)
                            if code or sibling_identity.strip() != sibling_name:
                                raise RuntimeError("sibling public SSH reached the wrong guest")
                        finally:
                            sibling_ssh.close()
                        verify_panel_sessions(api, base, token, provider_id, instance_id, name,
                                              other_instance_id=sibling_id, other_expected_name=sibling_name)
                        log("Different guest B survived closing guest A WebSSH and concurrent command timeout")
                        api("DELETE", "/admin/instances/" + str(sibling_id))
                        sibling_deadline = time.monotonic() + 300
                        while api("GET", "/admin/instances/" + str(sibling_id), allow_error=True).get("code") != 404:
                            if time.monotonic() >= sibling_deadline:
                                raise TimeoutError("sibling deletion did not finish")
                            time.sleep(3)
                        if sibling_name in remote(cli + " list --format csv -c n").splitlines():
                            raise RuntimeError("deleted sibling remains in runtime")
                        retained = remote(f"ss -H -lntup 'sport = :{sibling_port}'; nft list ruleset 2>/dev/null | grep -w {sibling_port} || true; iptables-save -t nat 2>/dev/null | grep -w {sibling_port} || true")
                        if retained:
                            raise RuntimeError("deleted sibling retained its SSH mapping")
                        monitors = api("GET", f"/admin/providers/{provider_id}/monitoring/monitors")
                        if any(item["instance_id"] == sibling_id for item in monitors["list"]):
                            raise RuntimeError("deleted sibling retained an Agent monitor")
                        # Original guest and its existing SSH transport remain usable.
                        stdin, stdout, _ = guest.exec_command("hostname", timeout=30)
                        stdin.channel.shutdown_write()
                        original_identity, _, code = _collect_output(stdout.channel, 30)
                        if code or original_identity.strip() != name:
                            raise RuntimeError("sibling deletion damaged original guest SSH")
                        log("Sibling API deletion preserved the first guest and existing SSH connection")
                    delta_in, delta_out = verify_agent_traffic(api, provider_id, instance_id, guest, _collect_output)
                    log(f"Agent external traffic verified: inbound={delta_in} outbound={delta_out} bytes")
                    before = read_monitor(api, provider_id, instance_id)
                    connected_at = wait_agent(api, provider_id)
                    agent_fixture.restart()
                    wait_agent(api, provider_id, previous_connected_at=connected_at)
                    api("POST", f"/admin/traffic/sync/instance/{instance_id}", {})
                    after = read_monitor(api, provider_id, instance_id)
                    if any(after[key] < before[key] for key in ("last_traffic_bytes_in", "last_traffic_bytes_out")):
                        raise RuntimeError("Agent restart lost persisted traffic counters")
                    verify_agent_commands(api, provider_id)
                    log("Real Agent restart preserved counters and restored command execution")
                if nested_docker == "yes":
                    runtime_detail = json.loads(remote(cli + " query " + shlex.quote("/1.0/instances/" + name)))
                    expanded_config = runtime_detail["expanded_config"]
                    for key in ("security.privileged", "raw.apparmor", "raw.lxc"):
                        value = expanded_config.get(key, "")
                        if value not in (("", "false") if key == "security.privileged" else ("",)):
                            raise RuntimeError("nested acceptance must not bypass isolation: " + key)
                    log("Installing current official Docker inside the nonprivileged guest")
                    log(verify_nested_docker(guest, _collect_output))
                    log("Actual nested Docker hello-world and unprivileged-port sysctl passed")
                if ipv6_acceptance == "yes":
                    verify_guest_ipv6(
                        guest, _collect_output, detail, name,
                        os.environ.get("OCV_WEBSSH_URL", ""),
                        os.environ.get("OCV_WEBSSH_SOURCE_IPV6", ""), log)
            finally:
                guest.close()
            if os.environ.get("OCV_WEBSSH_URL"):
                webssh_connection = verify_webssh(os.environ["OCV_WEBSSH_URL"], host, ssh_port, password, name,
                                                  os.environ["OCV_WEBSSH_SOURCE_IP"])
                log("Independent WebSSH verified using panel-reported endpoint: " + webssh_connection)
            nesting = remote(cli + " config get " + shlex.quote(name) + " security.nesting")
            if nesting != "true":
                raise RuntimeError("panel nesting setting not applied: " + nesting)
            api("DELETE", "/admin/instances/" + str(instance_id))
            deadline = time.monotonic() + 300
            while api("GET", "/admin/instances/" + str(instance_id), allow_error=True).get("code") != 404:
                if time.monotonic() >= deadline:
                    raise TimeoutError("panel deletion did not remove the instance record")
                time.sleep(3)
            while name in remote(cli + " list --format csv -c n").splitlines():
                if time.monotonic() >= deadline:
                    raise TimeoutError("panel deletion left a live guest")
                time.sleep(3)
            for port in ports:
                rules = remote(f"ss -H -lntup 'sport = :{port}'; nft list ruleset 2>/dev/null | grep -w {port} || true; iptables-save -t nat 2>/dev/null | grep -w {port} || true")
                if rules:
                    raise RuntimeError("panel deletion retained mapping port " + str(port))
            if connection == "agent":
                monitors = api("GET", f"/admin/providers/{provider_id}/monitoring/monitors")
                if any(item["instance_id"] == instance_id for item in monitors["list"]):
                    raise RuntimeError("deleted instance retained its Agent monitor")
            instance_id = None
            log("Panel deleted generation " + str(generation) + " and released all reserved ports")
        api("DELETE", "/admin/providers/" + str(provider_id))
        provider_id = None
        if agent_fixture:
            agent_fixture.cleanup()
            agent_fixture = None
        cleanup_prepared_image()
        success = True
    finally:
        if prepared_image:
            try:
                cleanup_prepared_image()
            except Exception as cleanup_error:
                log("WARNING: temporary image cleanup failed: " + str(cleanup_error))
        node.close()
        if success:
            remove_owned_panel(docker, container, run_id)
            log("Isolated panel and its ephemeral database removed")
        else:
            log("FAIL: retained this run's fixture for diagnosis: " + container)
            if agent_fixture and agent_fixture.directory:
                log("Retained Agent unit=" + agent_fixture.unit + " directory=" + agent_fixture.directory)
    log("PASS: current-source panel API token / " + runtime + " " + connection
        + " / two guest generations / public SSH / nesting / delete / port reuse")


if __name__ == "__main__":
    main()
