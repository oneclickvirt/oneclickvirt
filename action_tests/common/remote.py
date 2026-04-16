#!/usr/bin/env python3
"""Remote SSH execution helper for testing QEMU scripts."""
import argparse
import os
import paramiko
import sys
import time

# Default connection parameters (can be overridden via env vars or CLI flags)
HOST = os.environ.get("REMOTE_HOST", "")
PORT = int(os.environ.get("REMOTE_PORT", "22"))
USER = os.environ.get("REMOTE_USER", "root")
PASS = os.environ.get("REMOTE_PASS", "")
# Optional SSH private key file path; takes priority over password when set
KEY_FILE = os.environ.get("REMOTE_KEY_FILE", "")


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
    h = host or HOST
    p = port if port is not None else PORT
    u = user or USER
    pw = password if password is not None else PASS
    kf = key_filename if key_filename is not None else (KEY_FILE if KEY_FILE else None)

    client = paramiko.SSHClient()
    client.set_missing_host_key_policy(paramiko.AutoAddPolicy())

    if kf and os.path.isfile(kf):
        # Key-based auth; passphrase is pw if the key is encrypted (no-op otherwise)
        client.connect(
            h, port=p, username=u,
            key_filename=kf,
            passphrase=pw if pw else None,
            look_for_keys=False,
            allow_agent=False,
            timeout=connect_timeout,
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
    return client


def ssh_exec(cmd, timeout=120, host=None, port=None, user=None, password=None,
             key_filename=None):
    """Execute command on remote server, return (stdout, stderr, exit_code)."""
    client = _make_client(host=host, port=port, user=user, password=password,
                          key_filename=key_filename)
    stdin, stdout, stderr = client.exec_command(cmd, timeout=timeout)
    exit_code = stdout.channel.recv_exit_status()
    out = stdout.read().decode('utf-8', errors='replace')
    err = stderr.read().decode('utf-8', errors='replace')
    client.close()
    return out, err, exit_code


def ssh_exec_stream(cmd, timeout=600, host=None, port=None, user=None, password=None,
                    key_filename=None):
    """Execute command with streaming output."""
    client = _make_client(host=host, port=port, user=user, password=password,
                          key_filename=key_filename)
    stdin, stdout, stderr = client.exec_command(cmd, timeout=timeout)

    output = []
    while not stdout.channel.exit_status_ready():
        if stdout.channel.recv_ready():
            chunk = stdout.channel.recv(4096).decode('utf-8', errors='replace')
            print(chunk, end='', flush=True)
            output.append(chunk)
        time.sleep(0.1)
    # Read remaining
    remaining = stdout.read().decode('utf-8', errors='replace')
    if remaining:
        print(remaining, end='', flush=True)
        output.append(remaining)

    err = stderr.read().decode('utf-8', errors='replace')
    exit_code = stdout.channel.recv_exit_status()
    client.close()
    return ''.join(output), err, exit_code


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
    p = port if port is not None else PORT
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
    parser.add_argument("--port", type=int, default=PORT, help="SSH port (default: REMOTE_PORT env or 22)")
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
    if out:
        print(out, end='')
    if err:
        print(err, end='', file=sys.stderr)
    sys.exit(rc)
