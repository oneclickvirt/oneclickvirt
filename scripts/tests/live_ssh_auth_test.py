#!/usr/bin/env python3
import os
from pathlib import Path
import tempfile
import unittest
from unittest import mock

from live_ssh import connect_strict_node


class DummyClient:
    def __init__(self):
        self.calls = []

    def connect(self, host, **kwargs):
        self.calls.append((host, kwargs))


class LiveSshAuthTest(unittest.TestCase):
    def test_requires_explicit_authentication(self):
        with mock.patch.dict(os.environ, {}, clear=True):
            with self.assertRaisesRegex(RuntimeError, "OCV_LIVE_PASSWORD or OCV_LIVE_SSH_KEY"):
                connect_strict_node(DummyClient(), "192.0.2.1")

    def test_explicit_key_disables_ambient_credentials(self):
        with tempfile.TemporaryDirectory() as directory:
            key = Path(directory) / "id_test"
            key.write_text("fixture")
            client = DummyClient()
            with mock.patch.dict(os.environ, {"OCV_LIVE_SSH_KEY": str(key)}, clear=True):
                connect_strict_node(client, "192.0.2.2", port=2222)
            host, options = client.calls[0]
            self.assertEqual(host, "192.0.2.2")
            self.assertEqual(options["port"], 2222)
            self.assertEqual(options["key_filename"], str(key))
            self.assertIsNone(options["password"])
            self.assertFalse(options["allow_agent"])
            self.assertFalse(options["look_for_keys"])

    def test_password_remains_supported(self):
        client = DummyClient()
        with mock.patch.dict(os.environ, {"OCV_LIVE_PASSWORD": "fixture-secret"}, clear=True):
            connect_strict_node(client, "192.0.2.3")
        _, options = client.calls[0]
        self.assertEqual(options["password"], "fixture-secret")
        self.assertIsNone(options["key_filename"])

    def test_live_driver_port_is_forwarded(self):
        client = DummyClient()
        with mock.patch.dict(os.environ, {"OCV_LIVE_PASSWORD": "fixture-secret"}, clear=True):
            connect_strict_node(client, "192.0.2.4", port=1777)
        _, options = client.calls[0]
        self.assertEqual(options["port"], 1777)


if __name__ == "__main__":
    unittest.main()
