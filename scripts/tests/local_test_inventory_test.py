import os
from pathlib import Path
import subprocess
import sys
import tempfile
import unittest

from run_local_tests import DEDICATED, LOCAL_PYTHON, LOCAL_SHELL, ROOT, run_one, validate_inventory


class LocalInventoryTests(unittest.TestCase):
    def test_real_inventory_covers_every_script_entrypoint(self):
        self.assertGreater(validate_inventory(), 0)

    def test_new_or_missing_tests_are_rejected(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            tests = root / "scripts/tests"
            tests.mkdir(parents=True)
            for name in LOCAL_SHELL + LOCAL_PYTHON + list(DEDICATED):
                (tests / name).touch()
            self.assertEqual(validate_inventory(root), len(LOCAL_SHELL + LOCAL_PYTHON))
            unknown = tests / "new_remote_test.sh"
            unknown.touch()
            with self.assertRaisesRegex(ValueError, "unclassified"):
                validate_inventory(root)
            unknown.unlink()
            (tests / LOCAL_SHELL[0]).unlink()
            with self.assertRaisesRegex(ValueError, "missing"):
                validate_inventory(root)

    def test_remote_selection_is_rejected_before_running(self):
        process = subprocess.run([sys.executable, "-B", str(ROOT / "scripts/tests/run_local_tests.py"),
                                  "--tests", "ipv6_external_acceptance_test.sh"],
                                 capture_output=True, text=True, timeout=20)
        self.assertNotEqual(process.returncode, 0)
        self.assertIn("dedicated runner", process.stderr)

    def test_timeout_cleans_up_the_owned_process_group(self):
        with tempfile.TemporaryDirectory() as directory:
            marker = Path(directory) / "descendant-done"
            # If the sleeping descendant survives the timeout, it creates a
            # marker. This exercises the real process group cleanup.
            child = "import time,pathlib; time.sleep(0.6); pathlib.Path(" + repr(str(marker)) + ").touch()"
            parent = "import subprocess,sys,time; subprocess.Popen([sys.executable,'-c'," + repr(child) + "]); time.sleep(30)"
            with self.assertRaises(subprocess.TimeoutExpired):
                run_one([sys.executable, "-c", parent], dict(os.environ), timeout=0.2)
            import time
            time.sleep(0.7)
            self.assertFalse(marker.exists(), "test left a running descendant")


if __name__ == "__main__":
    unittest.main()
