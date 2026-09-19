"""Real Rust Agent fixture for an explicitly authorized disposable test node.

The reverse SSH forward exposes the local test panel only on node loopback.
It transports real Agent WebSocket traffic; it does not emulate the Agent.
"""
import json
import os
import re
import secrets
import select
import shlex
import socket
import threading
import time
from concurrent.futures import ThreadPoolExecutor
from urllib.parse import urlsplit


class ReversePanelForwarder:
    def __init__(self, ssh, local_port):
        self.transport = ssh.get_transport()
        self.local_port = int(local_port)
        self.stopping = threading.Event()
        self.lock = threading.Lock()
        self.streams = set()
        self.workers = []
        self.port = self.transport.request_port_forward("127.0.0.1", 0, self.accept)

    def accept(self, channel, origin, server):
        worker = threading.Thread(target=self.relay, args=(channel,), daemon=True)
        with self.lock:
            self.workers.append(worker)
        worker.start()

    def relay(self, channel):
        local = None
        try:
            local = socket.create_connection(("127.0.0.1", self.local_port), timeout=10)
            local.settimeout(10)
            channel.settimeout(10)
            with self.lock:
                self.streams.update((channel, local))
            while not self.stopping.is_set():
                ready, _, _ = select.select([channel, local], [], [], 1)
                for source in ready:
                    chunk = source.recv(65536)
                    if not chunk:
                        return
                    (local if source is channel else channel).sendall(chunk)
        except (OSError, EOFError):
            # The panel and Agent own reconnect semantics; this fixture only
            # relays bytes. Their health checks surface unexpected disconnects.
            pass
        finally:
            with self.lock:
                self.streams.discard(channel)
                self.streams.discard(local)
            channel.close()
            if local is not None:
                local.close()

    def close(self):
        self.stopping.set()
        try:
            # A reboot already removed the server-side listener. Still close
            # local relay sockets, including when live cancellation fails.
            if self.transport.is_active():
                self.transport.cancel_port_forward("127.0.0.1", self.port)
        finally:
            with self.lock:
                streams, workers = list(self.streams), list(self.workers)
            for stream in streams:
                stream.close()
            for worker in workers:
                worker.join(timeout=2)


class RealAgentFixture:
    def __init__(self, ssh, remote, run_id):
        self.ssh = ssh
        self.remote = remote
        self.run_id = run_id
        self.unit = run_id + "-agent"
        self.directory = None
        self.forwarder = None
        self.had_empty_table = False

    def start(self, binary, local_port, secret, api_token):
        if not os.path.isfile(binary):
            raise RuntimeError("OCV_AGENT_BINARY must name a locally built Linux node binary")
        # A new Agent database garbage-collects orphan counters. Refuse a node
        # with an existing Agent installation/table, even if its daemon is down.
        self.remote("set -eu; test ! -d /opt/oneclickvirt/agent; test -z \"$(ss -H -lnt 'sport = :23782')\"")
        tables = json.loads(self.remote("nft -j list tables"))["nftables"]
        if any(item.get("table", {}).get("name") == "vm_traffic_monitor" for item in tables):
            existing = json.loads(self.remote("nft -j list table inet vm_traffic_monitor"))["nftables"]
            objects = [item for item in existing if "metainfo" not in item]
            chains = [item["chain"] for item in objects if "chain" in item]
            expected = {"family": "inet", "table": "vm_traffic_monitor", "name": "forward",
                        "type": "filter", "hook": "forward", "prio": 0, "policy": "accept"}
            if (len(objects) != 2 or len(chains) != 1
                    or any(chains[0].get(key) != value for key, value in expected.items())):
                raise RuntimeError("refusing to replace an existing Agent traffic table with managed state")
            # An empty standard chain can remain after a previous uninstall.
            # Reuse it without deleting it during this fixture's cleanup.
            self.had_empty_table = True
        self.forwarder = ReversePanelForwarder(self.ssh, local_port)
        self.directory = self.remote("mktemp -d /opt/ocv-live-agent.XXXXXX")
        if not self.directory.startswith("/opt/ocv-live-agent.") or "/" in self.directory[len("/opt/"):]:
            raise RuntimeError("unexpected remote fixture directory")
        environment = {
            "API_TOKEN": api_token,
            "WS_URL": "ws://127.0.0.1:" + str(self.forwarder.port) + "/api/v1/ws/agent",
            "AGENT_SECRET": secret,
            "TRAFFIC_COLLECT_INTERVAL": "2",
            "RESOURCE_COLLECT_INTERVAL": "10",
            "TRAFFIC_RECONCILE_INTERVAL": "10",
            "RUST_LOG": "info",
        }
        if any("\n" in value or "\r" in value or "'" in value for value in environment.values()):
            raise RuntimeError("unsupported character in generated Agent fixture configuration")
        with self.ssh.open_sftp() as sftp:
            with sftp.file(self.directory + "/owner", "w") as owner:
                owner.write(self.run_id)
            sftp.put(binary, self.directory + "/oneclickvirt-agent")
            sftp.chmod(self.directory + "/oneclickvirt-agent", 0o700)
            with sftp.file(self.directory + "/.env", "w") as config:
                config.chmod(0o600)
                config.write("".join(key + "='" + value + "'\n" for key, value in environment.items()))
        self.remote("systemd-run --quiet --unit=" + shlex.quote(self.unit)
                    + " --property=WorkingDirectory=" + shlex.quote(self.directory)
                    + " --property=EnvironmentFile=" + shlex.quote(self.directory + "/.env")
                    + " " + shlex.quote(self.directory + "/oneclickvirt-agent"))
        return self.remote(shlex.quote(self.directory + "/oneclickvirt-agent") + " --version")

    def restart(self):
        self.remote("systemctl restart " + shlex.quote(self.unit))

    def cleanup(self):
        if self.directory is None:
            if self.forwarder:
                self.forwarder.close()
            return
        self.remote("test \"$(cat " + shlex.quote(self.directory + "/owner") + ")\" = "
                    + shlex.quote(self.run_id))
        unit_dir = self.remote("systemctl show -p WorkingDirectory --value " + shlex.quote(self.unit))
        if unit_dir != self.directory:
            raise RuntimeError("Agent unit ownership changed; retained fixture")
        self.remote("systemctl stop " + shlex.quote(self.unit))
        self.remote("if [ \"$(systemctl show -p LoadState --value " + shlex.quote(self.unit)
                    + ")\" != not-found ]; then systemctl reset-failed " + shlex.quote(self.unit) + "; fi")
        # Instance deletion must have removed all monitor counters first.
        table = json.loads(self.remote("nft -j list table inet vm_traffic_monitor"))["nftables"]
        if any("counter" in item or "rule" in item for item in table):
            raise RuntimeError("Agent retained monitor counters/rules; preserved table for diagnosis")
        if not self.had_empty_table:
            self.remote("nft delete table inet vm_traffic_monitor")
        if self.forwarder:
            self.forwarder.close()
        with self.ssh.open_sftp() as sftp:
            # Remove only this run's known files; unexpected files prevent rmdir.
            for name in (".env", "owner", "traffic.db", "traffic.db-wal", "traffic.db-shm", "oneclickvirt-agent"):
                try:
                    sftp.remove(self.directory + "/" + name)
                except FileNotFoundError:
                    pass
            sftp.rmdir(self.directory)
        self.directory = None


def wait_agent(api, provider_id, timeout=90, previous_connected_at=None):
    deadline = time.monotonic() + timeout
    while time.monotonic() < deadline:
        status = api("GET", f"/admin/providers/{provider_id}/monitoring/status")
        if status.get("is_running"):
            provider = api("GET", f"/admin/providers/{provider_id}")
            if (provider.get("agentConnectedAt") and provider.get("agentVersion")
                    and provider.get("agentConnectedAt") != previous_connected_at):
                return provider["agentConnectedAt"]
        time.sleep(2)
    raise TimeoutError("real Agent did not connect to the isolated panel")


def verify_agent_commands(api, provider_id):
    """A deliberately timed-out command must not cancel B or reconnect Agent."""
    connected_at = wait_agent(api, provider_id)
    with ThreadPoolExecutor(max_workers=2) as pool:
        command_a = pool.submit(api, "POST", f"/admin/providers/{provider_id}/exec",
                                {"command": "sleep 8; printf unexpected", "timeout": 1}, True)
        command_b = pool.submit(api, "POST", f"/admin/providers/{provider_id}/exec",
                                {"command": "sleep 3; printf OCV_COMMAND_B", "timeout": 20})
        result_a, result_b = command_a.result(), command_b.result()
    if result_a.get("code") != 502 or result_b.get("stdout", "").strip() != "OCV_COMMAND_B":
        raise RuntimeError("real Agent timeout isolation failed")
    if wait_agent(api, provider_id) != connected_at:
        raise RuntimeError("one command timeout reconnected the shared Agent")
    failed = api("POST", f"/admin/providers/{provider_id}/exec",
                 {"command": "printf 'websocket application error\\n' >&2; exit 1", "timeout": 10}, True)
    provider = api("GET", f"/admin/providers/{provider_id}")
    if failed.get("code") != 502:
        raise RuntimeError("nonzero Agent command exit was not reported as failure")
    if provider.get("agentStatus") != "online" or provider.get("agentConnectedAt") != connected_at:
        raise RuntimeError("command stderr containing websocket corrupted the live Agent connection status")


def verify_panel_sessions(api, base, token, provider_id, instance_id, expected_name,
                          *, other_instance_id=None, other_expected_name=None):
    """Exercise real SSH tunnels and commands through the shared Agent."""
    import websocket

    parsed = urlsplit(base)
    if parsed.scheme != "http" or parsed.hostname != "127.0.0.1":
        raise ValueError("panel fixture requires its isolated loopback HTTP endpoint")
    sessions = []
    if (other_instance_id is None) != (other_expected_name is None):
        raise ValueError("Both other guest identity fields are required")

    def connect(target_id):
        transport = socket.create_connection((parsed.hostname, parsed.port), timeout=15)
        try:
            session = websocket.create_connection(
                base.replace("http://", "ws://") + f"/api/v1/admin/instances/{target_id}/ssh",
                header={"Authorization": "Bearer " + token}, origin=base,
                timeout=30, socket=transport)
        except BaseException:
            transport.close()
            raise
        sessions.append(session)
        return session

    def identity(session, label, guest_name):
        label += "_" + secrets.token_hex(12)
        session.send("printf '\\nOCV_%s\\n' " + label + "; hostname; printf 'OCV_%s\\n' END\r")
        output = ""
        deadline = time.monotonic() + 40
        while time.monotonic() < deadline:
            session.settimeout(max(0.1, deadline - time.monotonic()))
            frame = session.recv()
            if not frame:
                break
            output += frame.decode(errors="replace") if isinstance(frame, bytes) else frame
            match = re.search(r"\r?\nOCV_" + label + r"\r?\n([^\r\n]+)\r?\nOCV_END", output)
            if match:
                if match.group(1) != guest_name:
                    raise RuntimeError("panel WebSSH reached a different guest")
                return
            output = output[-65536:]
        raise RuntimeError("panel WebSSH did not return verified guest identity")

    try:
        session_a = connect(instance_id)
        session_b = connect(instance_id if other_instance_id is None else other_instance_id)
        guest_b = expected_name if other_expected_name is None else other_expected_name
        identity(session_a, "SESSION_A", expected_name)
        identity(session_b, "SESSION_B", guest_b)
        session_a.close()
        identity(session_b, "B_AFTER_A_CLOSE", guest_b)
        with ThreadPoolExecutor(max_workers=1) as pool:
            check = pool.submit(verify_agent_commands, api, provider_id)
            identity(session_b, "B_DURING_COMMANDS", guest_b)
            check.result()
        identity(session_b, "B_AFTER_TIMEOUT", guest_b)
    finally:
        for session in sessions:
            session.close()


def read_monitor(api, provider_id, instance_id):
    data = api("GET", f"/admin/providers/{provider_id}/monitoring/monitors?page=1&pageSize=100")
    matches = [item for item in data["list"] if item["instance_id"] == instance_id]
    if len(matches) != 1 or not matches[0]["is_enabled"]:
        raise RuntimeError("expected one enabled Agent monitor for the new instance")
    return matches[0]


def verify_agent_traffic(api, provider_id, instance_id, guest, collect_output):
    api("POST", f"/admin/traffic/sync/instance/{instance_id}", {})
    before = read_monitor(api, provider_id, instance_id)
    size = 1024 * 1024
    stdin, stdout, _ = guest.exec_command("head -c 1048576 /dev/zero", timeout=45)
    stdin.channel.shutdown_write()
    data, error, status = collect_output(stdout.channel, 45)
    if status or len(data) != size:
        raise RuntimeError("guest download did not complete")
    stdin, stdout, _ = guest.exec_command("dd of=/dev/null bs=65536 status=none", timeout=45)
    stdin.channel.sendall(b"x" * size)
    stdin.channel.shutdown_write()
    _, error, status = collect_output(stdout.channel, 45)
    if status:
        raise RuntimeError("guest upload did not complete")
    deadline = time.monotonic() + 60
    while time.monotonic() < deadline:
        api("POST", f"/admin/traffic/sync/instance/{instance_id}", {})
        after = read_monitor(api, provider_id, instance_id)
        delta_in = after["last_traffic_bytes_in"] - before["last_traffic_bytes_in"]
        delta_out = after["last_traffic_bytes_out"] - before["last_traffic_bytes_out"]
        if delta_in >= size and delta_out >= size:
            return delta_in, delta_out
        time.sleep(3)
    raise RuntimeError("Agent counters did not record both external SSH transfer directions")
