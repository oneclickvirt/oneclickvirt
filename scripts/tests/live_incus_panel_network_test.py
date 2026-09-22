#!/usr/bin/env python3
"""Pure endpoint-contract tests for the destructive Incus/LXD live driver."""
import unittest

from live_incus_panel_test import (independent_ipv6_endpoint, validate_ipv6_only_dns,
                                   validate_ipv6_only_guest, validate_panel_image_contract)


class IndependentIPv6EndpointTests(unittest.TestCase):
    def test_managed_nat_uses_host_ipv6_and_reserved_port(self):
        self.assertEqual(
            independent_ipv6_endpoint(
                {"publicIPv6": "2a01:4f8::99", "sshPort": 29900},
                "nat_ipv4_ipv6", "device_proxy", "2a01:4f8::1", range(29900, 29904)),
            ("2a01:4f8::1", 29900, 29901),
        )

    def test_native_dual_stack_uses_guest_ipv6_native_ports(self):
        self.assertEqual(
            independent_ipv6_endpoint(
                {"publicIPv6": "2a01:4f8::99", "sshPort": 29900},
                "nat_ipv4_ipv6", "native", "2a01:4f8::1", range(29900, 29904)),
            ("2a01:4f8::99", 22, 18080),
        )

    def test_native_dual_stack_rejects_missing_guest_address(self):
        with self.assertRaisesRegex(RuntimeError, "public IPv6 target"):
            independent_ipv6_endpoint(
                {"sshPort": 29900}, "nat_ipv4_ipv6", "native",
                "2a01:4f8::1", range(29900, 29904))

    def test_managed_nat_rejects_unreserved_port(self):
        with self.assertRaisesRegex(RuntimeError, "escaped the reserved range"):
            independent_ipv6_endpoint(
                {"sshPort": 31000}, "nat_ipv4_ipv6", "iptables",
                "2a01:4f8::1", range(29900, 29904))

    def test_ipv6_only_rejects_private_ipv4(self):
        with self.assertRaisesRegex(RuntimeError, "private IPv4"):
            validate_ipv6_only_guest(
                {"privateIP": "10.0.0.2", "publicIPv6": "2a01:4f8::99"},
                "", "2a01:4f8::99/128")

    def test_ipv6_only_rejects_ula(self):
        with self.assertRaisesRegex(RuntimeError, "non-public"):
            validate_ipv6_only_guest(
                {"privateIP": "", "publicIPv6": "2a01:4f8::99"},
                "", "2a01:4f8::99/128 fd42::2/64")

    def test_ipv6_only_accepts_only_assigned_public_address(self):
        validate_ipv6_only_guest(
            {"privateIP": "", "publicIPv6": "2a01:4f8::99"},
            "", "2a01:4f8::99/128")

    def test_ipv6_only_dns_accepts_only_ipv6_nameservers(self):
        self.assertEqual(
            validate_ipv6_only_dns(
                "search example.invalid\nnameserver 2606:4700:4700::1111\n"
                "nameserver 2001:4860:4860::8888\n"),
            ["2606:4700:4700::1111", "2001:4860:4860::8888"],
        )

    def test_ipv6_only_dns_rejects_ipv4_nameserver(self):
        with self.assertRaisesRegex(RuntimeError, "IPv4 nameserver"):
            validate_ipv6_only_dns("nameserver 2606:4700:4700::1111\nnameserver 8.8.8.8\n")

    def test_ipv6_only_dns_rejects_missing_nameserver(self):
        with self.assertRaisesRegex(RuntimeError, "no IPv6 nameserver"):
            validate_ipv6_only_dns("search example.invalid\n")

    def test_panel_image_contract_accepts_all_in_one_port(self):
        validate_panel_image_contract({"Config": {"ExposedPorts": {"80/tcp": {}}}})

    def test_panel_image_contract_rejects_backend_only_image(self):
        with self.assertRaisesRegex(RuntimeError, "all-in-one"):
            validate_panel_image_contract({"Config": {"ExposedPorts": {"8080/tcp": {}}}})


if __name__ == "__main__":
    unittest.main()
