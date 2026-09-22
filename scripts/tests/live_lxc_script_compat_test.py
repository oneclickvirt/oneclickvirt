import unittest

from live_lxc_script_test import listed_device_names, parse_global_ipv6_egress


class LiveLXCScriptCompatibilityTests(unittest.TestCase):
    def test_parses_old_incus_and_lxd_device_output(self):
        output = """eth0:
  name: eth0
  network: incusbr0
  type: nic
eth1:
  ipv6.address: 2001:db8::10
  nictype: routed
  type: nic
root:
  path: /
  pool: default
  type: disk
"""
        self.assertEqual(listed_device_names(output), {"eth0", "eth1", "root"})

    def test_detects_forbidden_ipv4_proxy_names(self):
        output = """ssh-port:
  connect: tcp:0.0.0.0:22
  listen: tcp:192.0.2.10:29800
  type: proxy
nattcp-ports:
  type: proxy
"""
        self.assertTrue(
            {"ssh-port", "nattcp-ports", "natudp-ports"}
            & listed_device_names(output)
        )

    def test_ignores_indented_property_values_ending_in_colon(self):
        output = """eth1:
  parent: eth0
  description: label:
  type: nic
"""
        self.assertEqual(listed_device_names(output), {"eth1"})

    def test_accepts_only_one_global_ipv6_egress_address(self):
        self.assertEqual(parse_global_ipv6_egress("2001:4860::1\n"), "2001:4860::1")
        for invalid in ("192.0.2.1", "fd42::1", "not-an-address", "2001:db8::1\nextra"):
            with self.subTest(invalid=invalid), self.assertRaises(ValueError):
                parse_global_ipv6_egress(invalid)


if __name__ == "__main__":
    unittest.main()
