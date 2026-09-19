#!/usr/bin/env python3
"""Exercise the action harness against a real, loopback-only SSH transport."""
import contextlib
import importlib.util
import io
import os
from pathlib import Path
import socket
import subprocess
import sys
import threading
import tempfile
import time
import unittest
from unittest import mock

try:
    import paramiko
except ImportError:  # Optional dependency; discovery must remain usable offline.
    paramiko = None

REMOTE_PATH = Path(__file__).resolve().parents[2] / "action_tests/common/remote.py"
SPEC = importlib.util.spec_from_file_location("ocv_remote", REMOTE_PATH)
remote = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(remote)
HOST_KEY = paramiko.RSAKey.generate(2048) if paramiko is not None else None


class SSHServer((paramiko.ServerInterface if paramiko is not None else object)):
    def __init__(self):
        self.command = None
        self.ready = threading.Event()
        self.pty_requested = False
        self.keepalive_received = threading.Event()

    def check_global_request(self, kind, msg):
        if kind == "keepalive@lag.net":
            self.keepalive_received.set()
        return False

    def check_auth_password(self, username, password):
        if username == "regression" and password == "test-only":
            return paramiko.AUTH_SUCCESSFUL
        return paramiko.AUTH_FAILED

    def get_allowed_auths(self, username):
        return "password"

    def check_channel_request(self, kind, chanid):
        return paramiko.OPEN_SUCCEEDED if kind == "session" else paramiko.OPEN_FAILED_ADMINISTRATIVELY_PROHIBITED

    def check_channel_exec_request(self, channel, command):
        self.command = command.decode()
        self.ready.set()
        return True

    def check_channel_pty_request(self, channel, term, width, height, pixelwidth, pixelheight, modes):
        self.pty_requested = True
        return True


@contextlib.contextmanager
def endpoint(handler):
    listener = socket.socket()
    listener.bind(("127.0.0.1", 0))
    listener.listen(1)
    listener.settimeout(8)
    state = {"transport": None, "errors": []}

    def serve():
        try:
            conn, _ = listener.accept()
            with conn:
                transport = paramiko.Transport(conn)
                state["transport"] = transport
                transport.add_server_key(HOST_KEY)
                server = SSHServer()
                state["server"] = server
                transport.start_server(server=server)
                channel = transport.accept(5)
                if channel is None or not server.ready.wait(5):
                    raise RuntimeError("SSH test command not received")
                handler(channel, server.command)
                # Keep the transport alive long enough for the peer to read
                # all channel packets, then observe client cleanup.
                limit = time.monotonic() + 5
                while transport.is_active() and time.monotonic() < limit:
                    time.sleep(0.01)
        except (EOFError, OSError):
            pass  # Expected when the client cancels a command or disconnects.
        except Exception as exc:
            state["errors"].append(exc)

    thread = threading.Thread(target=serve, daemon=True)
    thread.start()
    try:
        yield dict(host="127.0.0.1", port=listener.getsockname()[1],
                   user="regression", password="test-only"), state
    finally:
        if state["transport"] is not None:
            state["transport"].close()
        listener.close()
        thread.join(6)
        if thread.is_alive():
            raise AssertionError("SSH server did not terminate")
        if state["errors"]:
            raise state["errors"][0]


def finish(channel, status=0):
    channel.send_exit_status(status)
    channel.shutdown_write()
    channel.close()


@unittest.skipUnless(paramiko is not None,
                     "optional dependency paramiko is required for SSH transport tests")
class RemoteSSHTests(unittest.TestCase):
    def test_host_key_policy_is_strict_when_trust_file_is_supplied(self):
        class FakeClient:
            def __init__(self):
                self.policy = None
                self.loaded = []

            def load_system_host_keys(self):
                self.loaded.append("system")

            def load_host_keys(self, path):
                self.loaded.append(path)

            def set_missing_host_key_policy(self, policy):
                self.policy = policy

        with tempfile.NamedTemporaryFile() as known_hosts, mock.patch.dict(
                os.environ, {"REMOTE_KNOWN_HOSTS": known_hosts.name}, clear=True):
            client = FakeClient()
            remote._configure_host_keys(client)
            self.assertIsInstance(client.policy, paramiko.RejectPolicy)
            self.assertEqual(client.loaded, ["system", known_hosts.name])

    def test_host_key_policy_keeps_dynamic_instance_default_permissive(self):
        class FakeClient:
            def __init__(self):
                self.policy = None

            def load_system_host_keys(self):
                pass

            def set_missing_host_key_policy(self, policy):
                self.policy = policy

        with mock.patch.dict(os.environ, {}, clear=True):
            client = FakeClient()
            remote._configure_host_keys(client)
            self.assertIsInstance(client.policy, paramiko.AutoAddPolicy)

    def test_idle_control_connection_sends_real_keepalive_after_command_eof(self):
        def command(channel, _):
            finish(channel)

        with endpoint(command) as (credentials, state):
            client = remote._make_client(**credentials)
            try:
                remote.keep_ssh_alive(client, interval=0.1)
                stdin, stdout, _ = client.exec_command("completed task", timeout=3)
                stdin.channel.shutdown_write()
                self.assertEqual(remote._collect_output(stdout.channel, 3), ("", "", 0))
                stdout.channel.close()
                self.assertTrue(state["server"].keepalive_received.wait(3))
                self.assertTrue(client.get_transport().is_active())
                with self.assertRaises(ValueError):
                    remote.keep_ssh_alive(client, interval=0)
            finally:
                client.close()
            with self.assertRaises(RuntimeError):
                remote.keep_ssh_alive(client)

    def test_pty_prompts_drain_both_streams_and_keep_output_after_status(self):
        prompt = "请输入确认✓:"
        tail = "安装完成，随后重启"

        def command(channel, _):
            # Exceed the receive window before showing the actual prompt.
            channel.sendall_stderr(b"e" * (3 * 1024 * 1024))
            for byte in prompt.encode():
                channel.sendall(bytes([byte]))
                time.sleep(0.005)
            self.assertEqual(channel.recv(32), b"yes\n")
            channel.send_exit_status(0)
            time.sleep(0.1)
            for byte in tail.encode():
                channel.sendall(bytes([byte]))
            channel.sendall_stderr("尾部诊断".encode())
            channel.shutdown_write()
            channel.close()

        with endpoint(command) as (credentials, state):
            client = remote._make_client(**credentials)
            try:
                channel = client.get_transport().open_session(timeout=3)
                channel.get_pty()
                channel.exec_command("interactive")
                received = ["", ""]
                answered = False

                def output(index, text):
                    nonlocal answered
                    received[index] += text
                    if index == 0 and not answered and prompt in received[0]:
                        channel.sendall("yes\n")
                        answered = True

                try:
                    result = remote._collect_output(channel, 8, on_output=output, capture=False)
                finally:
                    channel.close()
                self.assertEqual(result, ("", "", 0))
                self.assertTrue(answered)
                self.assertTrue(state["server"].pty_requested)
                self.assertEqual(received[0], prompt + tail)
                self.assertEqual(received[1], "e" * (3 * 1024 * 1024) + "尾部诊断")
            finally:
                client.close()

    def test_pty_busy_output_cannot_bypass_timeout_or_close_shared_transport(self):
        def command(channel, _):
            while not channel.closed:
                channel.sendall(b"still running\n" * 4096)
                channel.sendall_stderr(b"diagnostic\n")

        with endpoint(command) as (credentials, _):
            client = remote._make_client(**credentials)
            try:
                channel = client.get_transport().open_session(timeout=3)
                channel.get_pty()
                channel.exec_command("endless interactive")
                counts = [0, 0]

                def output(index, text):
                    counts[index] += len(text)

                start = time.monotonic()
                try:
                    with self.assertRaises(TimeoutError):
                        remote._collect_output(channel, 0.3, on_output=output, capture=False)
                finally:
                    channel.close()
                self.assertLess(time.monotonic() - start, 2)
                self.assertGreater(counts[0], 0)
                self.assertGreater(counts[1], 0)
                self.assertTrue(client.get_transport().is_active())
            finally:
                client.close()

    def test_pty_status_without_eof_does_not_report_success(self):
        def command(channel, _):
            channel.send_exit_status(0)
            while not channel.closed:
                channel.sendall(b"not finished\n")
                time.sleep(0.005)

        with endpoint(command) as (credentials, _):
            client = remote._make_client(**credentials)
            try:
                channel = client.get_transport().open_session(timeout=3)
                channel.get_pty()
                channel.exec_command("unfinished")
                try:
                    with self.assertRaises(TimeoutError):
                        remote._collect_output(channel, 0.2, capture=False)
                finally:
                    channel.close()
            finally:
                client.close()

    def test_large_stderr_and_stdout_do_not_fill_the_shared_window(self):
        expected_err = b"e" * (3 * 1024 * 1024)
        expected_out = b"o" * (3 * 1024 * 1024)

        def command(channel, _):
            channel.sendall_stderr(expected_err)
            channel.sendall(expected_out)
            finish(channel, 7)

        with endpoint(command) as (credentials, _):
            out, err, status = remote.ssh_exec("verbose", timeout=8, **credentials)
        self.assertEqual((len(out), len(err), status), (len(expected_out), len(expected_err), 7))
        self.assertEqual(out, expected_out.decode())
        self.assertEqual(err, expected_err.decode())

    def test_stream_consumes_both_outputs_and_preserves_split_utf8(self):
        text = "安装完成✓"

        def command(channel, _):
            channel.sendall_stderr(b"e" * (3 * 1024 * 1024))
            for byte in text.encode():
                channel.sendall(bytes([byte]))
                time.sleep(0.015)
            finish(channel)

        stdout, stderr = io.StringIO(), io.StringIO()
        with endpoint(command) as (credentials, _):
            with contextlib.redirect_stdout(stdout), contextlib.redirect_stderr(stderr):
                out, err, status = remote.ssh_exec_stream("stream", timeout=8, **credentials)
        self.assertEqual((out, status), (text, 0))
        self.assertEqual(stdout.getvalue(), out)
        self.assertEqual(stderr.getvalue(), err)
        self.assertEqual(len(err), 3 * 1024 * 1024)

    def test_stdin_eof_allows_a_waiting_command_to_finish(self):
        def command(channel, _):
            self.assertEqual(channel.recv(1), b"")
            channel.sendall(b"eof received")
            finish(channel)

        with endpoint(command) as (credentials, _):
            self.assertEqual(remote.ssh_exec("stdin", timeout=3, **credentials), ("eof received", "", 0))

    def test_output_after_exit_status_is_preserved(self):
        def command(channel, _):
            channel.send_exit_status(0)
            time.sleep(0.1)
            channel.sendall(b"late stdout")
            channel.sendall_stderr(b"late stderr")
            channel.shutdown_write()
            channel.close()

        with endpoint(command) as (credentials, _):
            self.assertEqual(remote.ssh_exec("late", timeout=3, **credentials), ("late stdout", "late stderr", 0))

    def test_deadline_and_connection_cleanup_with_continuous_output(self):
        def command(channel, _):
            while not channel.closed:
                channel.sendall(b"still running\n")
                time.sleep(0.01)

        with endpoint(command) as (credentials, state):
            start = time.monotonic()
            with self.assertRaises(TimeoutError):
                remote.ssh_exec("endless", timeout=0.2, **credentials)
            self.assertLess(time.monotonic() - start, 2)
            deadline = time.monotonic() + 2
            while state["transport"].is_active() and time.monotonic() < deadline:
                time.sleep(0.01)
            self.assertFalse(state["transport"].is_active())

    def test_disconnect_without_status_is_failure(self):
        def command(channel, _):
            channel.sendall(b"partial")
            channel.shutdown_write()
            channel.close()

        with endpoint(command) as (credentials, _):
            self.assertEqual(remote.ssh_exec("disconnect", timeout=3, **credentials), ("partial", "", -1))

    def test_cli_stream_prints_each_stream_once(self):
        def command(channel, _):
            channel.sendall(b"one stdout\n")
            channel.sendall_stderr(b"one stderr\n")
            finish(channel, 7)

        with endpoint(command) as (credentials, _):
            env = dict(os.environ, REMOTE_HOST=credentials["host"],
                       REMOTE_PORT=str(credentials["port"]), REMOTE_USER=credentials["user"],
                       REMOTE_PASS=credentials["password"], REMOTE_KEY_FILE="")
            result = subprocess.run([sys.executable, str(REMOTE_PATH), "--stream", "example"],
                                    env=env, capture_output=True, text=True, timeout=8)
        self.assertEqual(result.returncode, 7)
        self.assertEqual(result.stdout, "one stdout\n")
        self.assertEqual(result.stderr, "one stderr\n")


if __name__ == "__main__":
    unittest.main(verbosity=2)
