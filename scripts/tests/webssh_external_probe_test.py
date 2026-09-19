#!/usr/bin/env python3
"""Verify that WebSSH evidence cannot turn IPv4/ULA into public IPv6 success."""
import unittest

from webssh_external_probe import validate_ssh_connection


class WebSSHIdentityTests(unittest.TestCase):
    def test_ipv4_nat_keeps_the_guest_private_destination(self):
        validate_ssh_connection("192.0.2.10 45000 10.42.0.8 22", "192.0.2.20", 29800, "192.0.2.10")

    def test_ipv6_addresses_are_compared_after_normalization(self):
        validate_ssh_connection("2606:4700::1111 45000 2001:4860:4860::8888 22",
                                "2001:4860:4860:0:0:0:0:8888", 22,
                                "2606:4700:0:0:0:0:0:1111", require_ipv6=True)

    def test_ipv6_target_implicitly_requires_ipv6_even_without_flag(self):
        with self.assertRaises(RuntimeError):
            validate_ssh_connection("192.0.2.10 45000 10.42.0.8 22",
                                    "2001:4860:4860::8888", 22, "192.0.2.10")

    def test_ipv6_rejects_wrong_family_target_source_and_port(self):
        cases = [
            ("192.0.2.10 45000 2001:4860:4860::8888 22", "192.0.2.10"),
            ("2606:4700::1111 45000 10.42.0.8 22", "2606:4700::1111"),
            ("fd00::1 45000 2001:4860:4860::8888 22", "fd00::1"),
            ("ff02::1 45000 2001:4860:4860::8888 22", "ff02::1"),
            ("fec0::1 45000 2001:4860:4860::8888 22", "fec0::1"),
            ("4000::1 45000 2001:4860:4860::8888 22", "4000::1"),
            ("::ffff:8.8.8.8 45000 2001:4860:4860::8888 22", "::ffff:8.8.8.8"),
            ("2606:4700::1111 45000 2001:4860:4860::8844 22", "2606:4700::1111"),
            ("2606:4700::1111 45000 2001:4860:4860::8888 2222", "2606:4700::1111"),
            ("2606:4700::1111 45000 2001:4860:4860::8888 22", "2606:4700::1001"),
            ("2001:4860:4860::8888 45000 2001:4860:4860::8888 22", "2001:4860:4860::8888"),
        ]
        for connection, source in cases:
            with self.subTest(connection=connection, source=source), self.assertRaises(RuntimeError):
                validate_ssh_connection(connection, "2001:4860:4860::8888", 22, source, require_ipv6=True)

    def test_ipv6_rejects_nonpublic_or_nonliteral_target(self):
        for target in ("fd00::2", "fe80::2", "::1", "ff02::1", "fec0::1", "4000::1",
                       "::ffff:8.8.8.8", "192.0.2.20", "guest.example"):
            with self.subTest(target=target), self.assertRaises(ValueError):
                validate_ssh_connection("2606:4700::1111 45000 2001:4860:4860::8888 22",
                                        target, 22, "2606:4700::1111", require_ipv6=True)

    def test_invalid_connection_is_failure(self):
        for connection in ("", "192.0.2.10", "bad 1 10.42.0.8 22", "192.0.2.10 invalid 10.42.0.8 22",
                           "192.0.2.10 0 10.42.0.8 22", "192.0.2.10 45000 10.42.0.8 65536",
                           "192.0.2.10 45000 10.42.0.8 22 extra"):
            with self.subTest(connection=connection), self.assertRaises(RuntimeError):
                validate_ssh_connection(connection, "192.0.2.20", 29800, "192.0.2.10")


if __name__ == "__main__":
    unittest.main(verbosity=2)
