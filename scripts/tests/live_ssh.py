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
    explicit = os.environ.get("OCV_LIVE_KNOWN_HOSTS", "").strip()
    if explicit:
        path = Path(explicit).expanduser()
        if not path.is_file():
            raise RuntimeError("OCV_LIVE_KNOWN_HOSTS must point to an existing known_hosts file")
        # A disposable node is deliberately rebuilt in place.  Its host key
        # must be taken exclusively from the per-run file; merging the
        # operator's system known_hosts first can leave an old key for the
        # same address and turn a valid rebuild into a false BadHostKeyError.
        client.load_host_keys(str(path))
    else:
        client.load_system_host_keys()
    client.set_missing_host_key_policy(paramiko.RejectPolicy())
    return client


def connect_strict_node(client, host, port=22, username="root"):
    """Connect using only an explicitly supplied password and/or key."""
    password = os.environ.get("OCV_LIVE_PASSWORD", "")
    key_value = os.environ.get("OCV_LIVE_SSH_KEY", "").strip()
    key_path = None
    if key_value:
        key_path = Path(key_value).expanduser()
        if not key_path.is_file():
            raise RuntimeError("OCV_LIVE_SSH_KEY must point to an existing private-key file")
    if not password and key_path is None:
        raise RuntimeError("set OCV_LIVE_PASSWORD or OCV_LIVE_SSH_KEY for live node authentication")
    client.connect(
        host,
        port=port,
        username=username,
        password=password or None,
        key_filename=str(key_path) if key_path is not None else None,
        timeout=15,
        auth_timeout=15,
        banner_timeout=15,
        allow_agent=False,
        look_for_keys=False,
    )


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
    # Paramiko uses the raw hostname when a pre-opened `sock` is supplied,
    # while OpenSSH known_hosts normally uses the bracketed host:port form.
    # Register both exact aliases so strict checking remains valid for direct
    # IPv6 channels and ordinary TCP connections.
    aliases = [alias]
    if ":" in host and host not in aliases:
        aliases.append(host)
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
        for key_alias in aliases:
            client.get_host_keys().add(key_alias, key.get_name(), key)
        parsed += 1
    if not parsed:
        raise RuntimeError("guest did not expose a supported SSH host public key")
    client.set_missing_host_key_policy(paramiko.RejectPolicy())
    return client
