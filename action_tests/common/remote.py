#!/usr/bin/env python3
"""Remote SSH execution helper for testing QEMU scripts."""
import argparse
import codecs
import os
try:
    import paramiko
except ImportError:  # SSH is an optional action-test dependency.
    paramiko = None
import sys
import time

# Default connection parameters (can be overridden via env vars or CLI flags)
HOST = os.environ.get("REMOTE_HOST", "")
PORT = 22
USER = os.environ.get("REMOTE_USER", "root")
PASS = os.environ.get("REMOTE_PASS", "")
# Optional SSH private key file path; takes priority over password when set
KEY_FILE = os.environ.get("REMOTE_KEY_FILE", "")


def _configured_port():
    """Parse REMOTE_PORT only when a remote operation is actually requested."""
    raw = os.environ.get("REMOTE_PORT", "22").strip()
    try:
        port = int(raw)
    except (TypeError, ValueError) as exc:
        raise ValueError("REMOTE_PORT must be an integer between 1 and 65535") from exc
    if not 1 <= port <= 65535:
        raise ValueError("REMOTE_PORT must be an integer between 1 and 65535")
    return port


def _configure_host_keys(client):
    """Load optional trust material and choose an explicit host-key policy."""
    if paramiko is None:
        raise RuntimeError("Missing optional dependency: install scripts/tests/requirements-live.txt")
    # Provider-created instances do not have a trusted key until they are
    # reached for the first time, so keep the historical permissive default
    # for that path.  Destructive/live callers can opt into strict checking by
    # setting REMOTE_STRICT_HOST_KEY=yes or by supplying REMOTE_KNOWN_HOSTS.
    # Supplying a known_hosts file implicitly enables strict mode so a typo or
    # changed host key fails closed instead of silently accepting a new key.
    known_hosts = os.environ.get("REMOTE_KNOWN_HOSTS", "").strip()
    strict_host_key = os.environ.get("REMOTE_STRICT_HOST_KEY", "").strip().lower() in {
        "1", "true", "yes", "on"
    } or bool(known_hosts)
    client.load_system_host_keys()
    if known_hosts:
        if not os.path.isfile(known_hosts):
            raise RuntimeError(f"REMOTE_KNOWN_HOSTS does not exist: {known_hosts}")
        client.load_host_keys(known_hosts)
    client.set_missing_host_key_policy(
        paramiko.RejectPolicy() if strict_host_key else paramiko.AutoAddPolicy()
    )


def _make_client(host=None, port=None, user=None, password=None,
                 key_filename=None, connect_timeout=15):
    """Create and return a connected paramiko SSHClient.

    Authentication priority:
      1. key_filename (explicit arg) or REMOTE_KEY_FILE env var – key-based auth.
         The password/PASS value is used as the key passphrase when the key is
         encrypted; it is silently ignored when the key has no passphrase.
      2. password / REMOTE_PASS env var – password-based auth.
      3. No credentials supplied – let paramiko try the SSH agent / ~/.ssh keys
         as a last resort (useful for interactive/dev environments).
    """
    if paramiko is None:
        raise RuntimeError("Missing optional dependency: install scripts/tests/requirements-live.txt")
    h = host or HOST
    p = port if port is not None else _configured_port()
    u = user or USER
    pw = password if password is not None else PASS
    kf = key_filename if key_filename is not None else (KEY_FILE if KEY_FILE else None)

    client = paramiko.SSHClient()
    _configure_host_keys(client)

    if kf and os.path.isfile(kf):
        # Load key explicitly to handle both legacy RSA PEM (-----BEGIN RSA PRIVATE KEY-----)
        # and OpenSSH format (-----BEGIN OPENSSH PRIVATE KEY-----), working around
        # paramiko version-specific auto-detection issues that raise
        # "encountered RSA key, expected OPENSSH key".
        pkey = None
        _kpass = pw.encode('utf-8') if pw else None
        # Build a list of supported key classes; DSSKey was removed in paramiko 3.x
        _key_classes = [paramiko.RSAKey, paramiko.Ed25519Key, paramiko.ECDSAKey]
        try:
            _key_classes.append(paramiko.DSSKey)
        except AttributeError:
            pass  # DSSKey not available in this paramiko version
        for key_class in _key_classes:
            try:
                pkey = key_class.from_private_key_file(kf, password=_kpass)
                break
            except Exception:
                continue
        if pkey is not None:
            try:
                client.connect(
                    h, port=p, username=u,
                    pkey=pkey,
                    look_for_keys=False,
                    allow_agent=False,
                    timeout=connect_timeout,
                )
            except paramiko.AuthenticationException:
                # Key-based auth rejected by the server; fall back to password if available.
                if not pw:
                    raise
                client.connect(
                    h, port=p, username=u,
                    password=pw,
                    look_for_keys=False,
                    allow_agent=False,
                    timeout=connect_timeout,
                )
        elif pw:
            # Key file could not be parsed; fall back to password auth.
            client.connect(
                h, port=p, username=u,
                password=pw,
                look_for_keys=False,
                allow_agent=False,
                timeout=connect_timeout,
            )
        else:
            raise ValueError(
                f"SSH key file {kf!r} could not be parsed as any supported key type "
                "and no password was provided for fallback"
            )
    elif pw:
        # Password auth
        client.connect(
            h, port=p, username=u,
            password=pw,
            look_for_keys=False,
            allow_agent=False,
            timeout=connect_timeout,
        )
    else:
        # No credentials – fall back to SSH agent / ~/.ssh keys
        client.connect(
            h, port=p, username=u,
            look_for_keys=True,
            allow_agent=True,
            timeout=connect_timeout,
        )
    keep_ssh_alive(client)
    return client


def keep_ssh_alive(client, interval=15):
    """Keep idle control transports usable while a separate API task runs.

    This does not retry commands or hide a disconnected transport. Command
    deadlines still belong to _collect_output and close only their channel.
    """
    if interval <= 0:
        raise ValueError("SSH keepalive interval must be positive")
    transport = client.get_transport()
    if transport is None or not transport.is_active():
        raise RuntimeError("SSH transport is not connected")
    transport.set_keepalive(interval)


def _collect_output(channel, timeout, stream=False, *, on_output=None, capture=True):
    """Drain both SSH streams before waiting for exit status.

    SSH shares a finite receive window between stdout and stderr. Waiting for
    the process first, or draining only stdout, deadlocks verbose installers.
    A monotonic deadline also bounds commands that keep producing output.
    Interactive callers can handle decoded (stream_index, text) chunks through
    on_output and disable capture to avoid retaining entire installer logs.
    """
    if timeout <= 0:
        raise ValueError("SSH command timeout must be positive")
    deadline = time.monotonic() + timeout
    chunks = [[], []]
    decoders = [codecs.getincrementaldecoder("utf-8")("replace") for _ in range(2)]
    streams = [sys.stdout, sys.stderr]

    def append(index, data, final=False):
        value = decoders[index].decode(data, final=final)
        if value:
            if capture:
                chunks[index].append(value)
            if stream:
                print(value, end="", file=streams[index], flush=True)
            if on_output is not None:
                remaining = deadline - time.monotonic()
                if remaining <= 0:
                    raise TimeoutError(f"SSH command exceeded {timeout:g} seconds")
                # A PTY response also uses the finite SSH window. Bound a
                # callback's sendall if the peer stops consuming stdin.
                channel.settimeout(remaining)
                on_output(index, value)

    while True:
        if time.monotonic() >= deadline:
            raise TimeoutError(f"SSH command exceeded {timeout:g} seconds")
        received = False
        # Bound each turn so a continuously busy stream cannot starve the
        # other stream or the deadline check.
        if channel.recv_ready():
            append(0, channel.recv(65536))
            received = True
        if channel.recv_stderr_ready():
            append(1, channel.recv_stderr(65536))
            received = True
        # A server can send exit-status before EOF. Wait until all output has
        # arrived, and do not interpret a closed transport as exit code zero.
        if (channel.eof_received or channel.closed) and not (
                channel.recv_ready() or channel.recv_stderr_ready()):
            if channel.exit_status_ready():
                break
        if not received:
            time.sleep(min(0.01, max(0, deadline - time.monotonic())))

    for index in range(2):
        append(index, b"", final=True)
    return "".join(chunks[0]), "".join(chunks[1]), channel.recv_exit_status()


def _run_command(cmd, timeout, host, port, user, password, key_filename, stream):
    if timeout <= 0:
        raise ValueError("SSH command timeout must be positive")
    client = _make_client(host=host, port=port, user=user, password=password,
                          key_filename=key_filename)
    try:
        stdin, stdout, _ = client.exec_command(cmd, timeout=timeout)
        # These helpers are noninteractive; commands waiting for stdin must
        # receive EOF instead of hanging until the command deadline.
        stdin.channel.shutdown_write()
        return _collect_output(stdout.channel, timeout, stream)
    finally:
        client.close()


def ssh_exec(cmd, timeout=120, host=None, port=None, user=None, password=None,
             key_filename=None):
    """Execute command on remote server, return (stdout, stderr, exit_code)."""
    return _run_command(cmd, timeout, host, port, user, password, key_filename, False)


def ssh_exec_stream(cmd, timeout=600, host=None, port=None, user=None, password=None,
                    key_filename=None):
    """Execute command with streaming output."""
    return _run_command(cmd, timeout, host, port, user, password, key_filename, True)


def scp_upload(local_path, remote_path, host=None, port=None, user=None, password=None,
               key_filename=None):
    """Upload a file via SFTP."""
    client = _make_client(host=host, port=port, user=user, password=password,
                          key_filename=key_filename)
    sftp = client.open_sftp()
    sftp.put(local_path, remote_path)
    sftp.close()
    client.close()


def scp_download(remote_path, local_path, host=None, port=None, user=None, password=None,
                 key_filename=None):
    """Download a file via SFTP."""
    client = _make_client(host=host, port=port, user=user, password=password,
                          key_filename=key_filename)
    sftp = client.open_sftp()
    sftp.get(remote_path, local_path)
    sftp.close()
    client.close()


def speedtest_download(urls, min_mb=1, time_limit=60,
                       host=None, port=None, user=None, password=None,
                       key_filename=None):
    """
    Try downloading from each URL in order inside the remote instance.
    Returns (success: bool, url: str, downloaded_mb: float) for the first
    URL that delivers > min_mb within time_limit seconds, or (False, '', 0)
    if none succeed.
    """
    h = host or HOST
    p = port if port is not None else _configured_port()
    u = user or USER
    pw = password if password is not None else PASS
    kf = key_filename if key_filename is not None else (KEY_FILE if KEY_FILE else None)

    for url in urls:
        # Download with wget/curl, send to /dev/null, capture bytes received.
        # Use a pipe so we can interrupt after time_limit seconds.
        cmd = (
            f"timeout {time_limit} wget -q --limit-rate=0 -O /dev/null '{url}' 2>&1 || "
            f"timeout {time_limit} curl -s -o /dev/null -w '%{{size_download}}' '{url}' 2>&1"
        )
        # Alternative: measure bytes written more reliably
        cmd = (
            f"tmp=$(mktemp); "
            f"timeout {time_limit} wget -q -O \"$tmp\" '{url}' 2>/dev/null; "
            f"sz=$(stat -c%s \"$tmp\" 2>/dev/null || stat -f%z \"$tmp\" 2>/dev/null || echo 0); "
            f"rm -f \"$tmp\"; "
            f"echo \"$sz\""
        )
        try:
            out, err, rc = ssh_exec(cmd,
                                    timeout=time_limit + 30,
                                    host=h, port=p, user=u, password=pw,
                                    key_filename=kf)
            downloaded_bytes = int(out.strip()) if out.strip().isdigit() else 0
            downloaded_mb = downloaded_bytes / (1024 * 1024)
            if downloaded_mb >= min_mb:
                return True, url, downloaded_mb
        except Exception:
            pass

    return False, '', 0.0


if __name__ == "__main__":
    parser = argparse.ArgumentParser(
        description="Remote SSH execution helper - run a command on a remote host."
    )
    parser.add_argument("--host", default=HOST, help="Remote host (default: REMOTE_HOST env)")
    try:
        default_port = _configured_port()
    except ValueError as exc:
        parser.error(str(exc))
    parser.add_argument("--port", type=int, default=default_port,
                        help="SSH port (default: REMOTE_PORT env or 22)")
    parser.add_argument("--user", default=USER, help="SSH username (default: REMOTE_USER env or root)")
    parser.add_argument("--password", default=PASS, help="SSH password (default: REMOTE_PASS env)")
    parser.add_argument("--key-file", default=KEY_FILE,
                        help="Path to SSH private key file (default: REMOTE_KEY_FILE env)")
    parser.add_argument("--timeout", type=int, default=120, help="Command timeout in seconds (default: 120)")
    parser.add_argument("--stream", action="store_true", help="Stream output instead of buffering")
    parser.add_argument("command", nargs=argparse.REMAINDER, help="Command to run on remote host")

    args = parser.parse_args()

    if not args.command:
        parser.print_help()
        sys.exit(1)

    if not args.host:
        print("Error: --host / REMOTE_HOST is required", file=sys.stderr)
        sys.exit(1)

    cmd_str = ' '.join(args.command)
    try:
        if args.stream:
            out, err, rc = ssh_exec_stream(
                cmd_str, timeout=args.timeout,
                host=args.host, port=args.port,
                user=args.user, password=args.password,
                key_filename=args.key_file if args.key_file else None,
            )
        else:
            out, err, rc = ssh_exec(
                cmd_str, timeout=args.timeout,
                host=args.host, port=args.port,
                user=args.user, password=args.password,
                key_filename=args.key_file if args.key_file else None,
            )
    except Exception as exc:
        print(f"SSH connection failed: {exc}", file=sys.stderr)
        sys.exit(1)
    if out and not args.stream:
        print(out, end='')
    if err and not args.stream:
        print(err, end='', file=sys.stderr)
    sys.exit(rc)
