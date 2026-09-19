#!/usr/bin/env python3
"""Acceptance contract only; this does not install an actual storage backend."""
import unittest

from live_lxc_install_test import verify_storage_pools


class StorageAcceptanceTests(unittest.TestCase):
    def test_expected_backend(self):
        verify_storage_pools([{"name": "default", "driver": "btrfs"}], "btrfs")

    def test_fallback_is_not_expected_backend(self):
        with self.assertRaises(RuntimeError):
            verify_storage_pools([{"name": "default", "driver": "dir"}], "btrfs")

    def test_generic_fixture_keeps_legitimate_fallback(self):
        verify_storage_pools([{"name": "default", "driver": "dir"}])

    def test_generic_fixture_keeps_custom_pool(self):
        verify_storage_pools([{"name": "local", "driver": "zfs"}])

    def test_wrong_pool_does_not_prove_default_backend(self):
        with self.assertRaises(RuntimeError):
            verify_storage_pools([{"name": "other", "driver": "btrfs"}], "btrfs")

    def test_ambiguous_pools(self):
        with self.assertRaises(RuntimeError):
            verify_storage_pools([{"name": "default", "driver": "btrfs"},
                                  {"name": "default", "driver": "dir"}], "btrfs")

    def test_invalid_inventory(self):
        for pools in (None, {}, [], [None], [{}], [{"name": "default", "driver": 1}],
                      [{"name": "", "driver": "dir"}], [{"name": "local", "driver": ""}]):
            with self.subTest(pools=pools), self.assertRaises(RuntimeError):
                verify_storage_pools(pools)


if __name__ == "__main__":
    unittest.main()
