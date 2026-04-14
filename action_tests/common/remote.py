#!/usr/bin/env python3
"""Remote SSH execution helper for testing QEMU scripts."""
import paramiko
import sys
import time

HOST = ""
USER = "root"
PASS = ""

def ssh_exec(cmd, timeout=120):
    """Execute command on remote server, return (stdout, stderr, exit_code)."""
    client = paramiko.SSHClient()
    client.set_missing_host_key_policy(paramiko.AutoAddPolicy())
    client.connect(HOST, username=USER, password=PASS, timeout=15)
    stdin, stdout, stderr = client.exec_command(cmd, timeout=timeout)
    exit_code = stdout.channel.recv_exit_status()
    out = stdout.read().decode('utf-8', errors='replace')
    err = stderr.read().decode('utf-8', errors='replace')
    client.close()
    return out, err, exit_code

def ssh_exec_stream(cmd, timeout=600):
    """Execute command with streaming output."""
    client = paramiko.SSHClient()
    client.set_missing_host_key_policy(paramiko.AutoAddPolicy())
    client.connect(HOST, username=USER, password=PASS, timeout=15)
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

def scp_upload(local_path, remote_path):
    """Upload a file via SFTP."""
    client = paramiko.SSHClient()
    client.set_missing_host_key_policy(paramiko.AutoAddPolicy())
    client.connect(HOST, username=USER, password=PASS, timeout=15)
    sftp = client.open_sftp()
    sftp.put(local_path, remote_path)
    sftp.close()
    client.close()

if __name__ == "__main__":
    if len(sys.argv) > 1:
        cmd = ' '.join(sys.argv[1:])
        out, err, rc = ssh_exec(cmd)
        if out:
            print(out)
        if err:
            print(err, file=sys.stderr)
        sys.exit(rc)
    else:
        print("Usage: python3 remote_exec.py <command>")
