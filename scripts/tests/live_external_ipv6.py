"""Independent IPv6 HTTP/SSH probe for live panel acceptance.

The probe is an already-authorized, separate SSH host (for example the
production node).  It never accepts a guest host key from the network: the
guest's public keys are read through the already trusted provider connection
and pinned before the probe opens its IPv6 socket.
"""
import ipaddress
import os
import shlex
import sys

try:
    import paramiko
except ImportError:  # Optional dependency for explicitly requested live runs.
    paramiko = None

sys.path.insert(0, os.path.join(os.path.dirname(__file__), "..", "..", "action_tests", "common"))
from remote import _collect_output


def _alias(host, port):
    return f"[{host}]:{port}" if ":" in host or port != 22 else host


def _require_paramiko():
    if paramiko is None:
        raise RuntimeError("Missing optional dependency: install scripts/tests/requirements-live.txt")


class ExternalIPv6Probe:
    def __init__(self, host, port, username, password, known_hosts):
        if not host or not 1 <= int(port) <= 65535:
            raise ValueError("external probe SSH endpoint is invalid")
        if not username or not password:
            raise ValueError("external probe credentials are required")
        if not known_hosts:
            raise ValueError("external probe known_hosts is required")
        self.host = host
        self.port = int(port)
        self.username = username
        self.password = password
        self.known_hosts = known_hosts
        self.client = None

    def connect(self):
        _require_paramiko()
        client = paramiko.SSHClient()
        client.load_host_keys(self.known_hosts)
        client.set_missing_host_key_policy(paramiko.RejectPolicy())
        client.connect(self.host, port=self.port, username=self.username,
                       password=self.password, timeout=20, auth_timeout=20,
                       banner_timeout=20, allow_agent=False, look_for_keys=False)
        client.get_transport().set_keepalive(15)
        self.client = client
        # Require the probe itself to have a working public IPv6 route.  This
        # prevents an IPv4-only host from being mistaken for an external v6
        # acceptance result.
        output = self.run("curl --noproxy '*' -6 -fsS --connect-timeout 10 --max-time 20 https://ipv6.ip.sb", 45)
        address = ipaddress.ip_address(output.strip())
        if not address.is_global:
            raise RuntimeError("external probe IPv6 egress is not global")
        return str(address)

    def run(self, command, timeout=45):
        if self.client is None or self.client.get_transport() is None or not self.client.get_transport().is_active():
            raise RuntimeError("external probe SSH transport is not active")
        stdin, stdout, _ = self.client.exec_command(command, timeout=timeout)
        stdin.channel.shutdown_write()
        output, error, status = _collect_output(stdout.channel, timeout)
        if status:
            raise RuntimeError("external probe command failed: " + error[-1200:])
        return output.strip()

    def http_identity(self, target, port, nonce):
        address = ipaddress.ip_address(target)
        if address.version != 6 or not address.is_global:
            raise ValueError("external IPv6 HTTP target must be a global IPv6 address")
        url = f"http://[{address}]:{int(port)}/identity"
        command = "curl --noproxy '*' -g -6 -fsS --connect-timeout 10 --max-time 20 " + shlex.quote(url)
        body = self.run(command, 45)
        if body != nonce:
            raise RuntimeError("external IPv6 HTTP reached the wrong guest identity")
        return body

    def ssh_identity(self, target, port, host_keys, username, password, expected):
        _require_paramiko()
        address = ipaddress.ip_address(target)
        if address.version != 6 or not address.is_global:
            raise ValueError("external IPv6 SSH target must be a global IPv6 address")
        mapped_port = int(port)
        if not 1 <= mapped_port <= 65535:
            raise ValueError("external IPv6 SSH port is invalid")
        if not host_keys:
            raise ValueError("guest SSH host keys are required")
        transport = self.client.get_transport()
        channel = transport.open_channel("direct-tcpip", (str(address), mapped_port),
                                         (self.host, self.port), timeout=20)
        client = paramiko.SSHClient()
        alias = _alias(str(address), mapped_port)
        aliases = [alias]
        if address.compressed not in aliases:
            aliases.append(address.compressed)
        parsed = 0
        for line in host_keys.splitlines():
            fields = line.strip().split()
            if len(fields) < 2:
                continue
            try:
                entry = paramiko.hostkeys.HostKeyEntry.from_line(alias + " " + line.strip())
            except (ValueError, TypeError, UnicodeError, paramiko.SSHException):
                continue
            if entry is not None and entry.key is not None:
                for key_alias in aliases:
                    client.get_host_keys().add(key_alias, entry.key.get_name(), entry.key)
                parsed += 1
        if not parsed:
            channel.close()
            raise RuntimeError("guest exposed no supported SSH host key")
        client.set_missing_host_key_policy(paramiko.RejectPolicy())
        try:
            client.connect(str(address), port=mapped_port, username=username, password=password,
                           sock=channel, timeout=25, auth_timeout=25, banner_timeout=25,
                           allow_agent=False, look_for_keys=False)
            stdin, stdout, _ = client.exec_command("hostname", timeout=30)
            stdin.channel.shutdown_write()
            output, error, status = _collect_output(stdout.channel, 30)
            if status:
                raise RuntimeError("external IPv6 SSH command failed: " + error[-1200:])
            identity = output.strip()
            if identity != expected:
                raise RuntimeError("external IPv6 SSH reached the wrong guest")
            return identity
        finally:
            client.close()

    def close(self):
        if self.client is not None:
            self.client.close()
            self.client = None
