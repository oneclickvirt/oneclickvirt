#!/usr/bin/env python3
"""Nested probe failure contracts, not Docker runtime acceptance."""
import subprocess
import unittest
from unittest.mock import Mock, patch

from live_nested_docker import nested_docker_command, verify_nested_docker


class NestedTests(unittest.TestCase):
    def setUp(self):
        self.guest = Mock()
        self.stdin, self.stdout = Mock(), Mock()
        self.guest.exec_command.return_value = (self.stdin, self.stdout, Mock())
        self.marker = "OCV_NESTED_DOCKER_OK_fixed"

    def verify(self, output, status=0):
        with patch("live_nested_docker.secrets.token_hex", return_value="fixed"):
            return verify_nested_docker(self.guest, Mock(return_value=(output, "", status)))

    def test_success_needs_real_program_output_and_terminal_marker(self):
        result = self.verify('StorageDriver=overlay2\nHello from Docker!\n' + self.marker + '\n')
        self.assertIn("Hello from Docker!", result)
        self.stdin.channel.shutdown_write.assert_called_once()
        self.stdout.channel.close.assert_called_once()

    def test_nonzero_status_is_not_masked_by_marker(self):
        with self.assertRaisesRegex(RuntimeError, "failed"):
            self.verify("Hello from Docker!\n" + self.marker, 1)

    def test_missing_marker_or_hello_is_not_success(self):
        for output in (self.marker, "Hello from Docker!", "prefix" + self.marker + "Hello from Docker!"):
            with self.subTest(output=output), self.assertRaises(RuntimeError):
                self.verify(output)

    def test_timeout_propagates_and_closes_only_channel(self):
        with self.assertRaises(TimeoutError):
            verify_nested_docker(self.guest, Mock(side_effect=TimeoutError("deadline")))
        self.stdout.channel.close.assert_called_once()
        self.guest.close.assert_not_called()

    def test_shell_syntax_and_marker_quoting(self):
        command = nested_docker_command("marker ' ; $(touch unwanted)")
        result = subprocess.run(["sh", "-n"], input=command, text=True, capture_output=True)
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertIn("--sysctl net.ipv4.ip_unprivileged_port_start=0", command)
        self.assertNotIn("--privileged", command)


if __name__ == "__main__":
    unittest.main(verbosity=2)
