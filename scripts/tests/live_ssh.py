"""Strict SSH client construction for disposable-node acceptance drivers.

Destructive live tests must never accept a changed node host key.  The node
key comes from the user's system known_hosts by default, or from the explicit
OCV_LIVE_KNOWN_HOSTS file used for an isolated test workspace.
"""
import os
from pathlib import Path

try:
    import paramiko
except ImportError:  # Loaded by unit-test helpers without live dependencies.
    paramiko = None


def strict_node_client():
    if paramiko is None:
        raise RuntimeError("Missing optional dependency: install scripts/tests/requirements-live.txt")
    client = paramiko.SSHClient()
    client.load_system_host_keys()
    explicit = os.environ.get("OCV_LIVE_KNOWN_HOSTS", "").strip()
    if explicit:
        path = Path(explicit).expanduser()
        if not path.is_file():
            raise RuntimeError("OCV_LIVE_KNOWN_HOSTS must point to an existing known_hosts file")
        client.load_host_keys(str(path))
    client.set_missing_host_key_policy(paramiko.RejectPolicy())
    return client


def pinned_guest_client(host, port, public_key_text):
    """Return an SSH client pinned to public keys read from a new guest.

    Live acceptance creates disposable guests whose keys cannot exist in the
    operator's system known_hosts yet.  Reading the guest's *public* host key
    through the already verified node connection lets us keep strict checking
    without accepting an arbitrary key from the network.
    """
    if paramiko is None:
        raise RuntimeError("Missing optional dependency: install scripts/tests/requirements-live.txt")
    if not public_key_text or port <= 0:
        raise ValueError("guest host keys and a valid guest port are required")
    client = paramiko.SSHClient()
    # OpenSSH/Paramiko use bracketed host:port tokens for IPv6 and for any
    # non-default port.  Keep the default IPv4 form unchanged for known_hosts
    # compatibility.
    alias = f"[{host}]:{port}" if ":" in host or port != 22 else host
    parsed = 0
    for line in public_key_text.splitlines():
        fields = line.strip().split()
        if len(fields) < 2:
            continue
        try:
            entry = paramiko.hostkeys.HostKeyEntry.from_line(f"{alias} {line.strip()}")
            key = entry.key if entry else None
        except (ValueError, TypeError, UnicodeError, paramiko.SSHException):
            continue
        if key is None:
            continue
        client.get_host_keys().add(alias, key.get_name(), key)
        parsed += 1
    if not parsed:
        raise RuntimeError("guest did not expose a supported SSH host public key")
    client.set_missing_host_key_policy(paramiko.RejectPolicy())
    return client
