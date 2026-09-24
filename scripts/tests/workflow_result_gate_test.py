#!/usr/bin/env python3
"""Execute the actual workflow gates against isolated JSONL fixtures."""
import json
import os
from pathlib import Path
import re
import subprocess
import tempfile
import textwrap
import unittest

ROOT = Path(__file__).resolve().parents[2]


def gate_script(workflow, name):
    source = (ROOT / ".github/workflows" / workflow).read_text()
    step = source.split(f"      - name: {name}\n", 1)[1].split("\n      - name:", 1)[0]
    script = textwrap.dedent(step.split("        run: |\n", 1)[1])
    values = {
        "env.REPORT_DIR": '"$REPORT_DIR"',
        "matrix.environment": "docker",
        "steps.resolve-platforms.outputs.platform_priority": "fixture",
    }

    def replace(match):
        return values[match.group(1).strip()]

    return re.sub(r"\$\{\{(.*?)\}\}", replace, script)


class WorkflowGateTests(unittest.TestCase):
    def check_gate(self, workflow, step, filename, cases):
        script = gate_script(workflow, step)
        for label, code, rows, expected in cases:
            with self.subTest(workflow=workflow, case=label), tempfile.TemporaryDirectory(prefix="ocv gate ") as directory:
                root = Path(directory)
                report = root / "action_tests/reports/current"
                report.mkdir(parents=True)
                # Tracked/stale reports must not contribute assertions.
                (report.parent / "stale-results.jsonl").write_text('{"status":"PASS","group":"old"}\n')
                target = report / filename if workflow == "integration-tests.yml" else report.parent / filename
                if rows is not None:
                    target.write_text(rows)
                env = dict(os.environ, TEST_EXIT=code, REPORT_DIR=str(report),
                           GITHUB_STEP_SUMMARY=str(root / "summary.md"))
                result = subprocess.run(["bash", "-c", script], cwd=root, env=env,
                                        text=True, capture_output=True, timeout=15)
                self.assertEqual(result.returncode == 0, expected, result.stdout + result.stderr)

    def test_integration_gate(self):
        passed = json.dumps({"status": "PASS", "group": "providers"})
        failed = json.dumps({"status": "FAIL", "group": "providers", "name": "last assertion"})
        self.check_gate("integration-tests.yml", "Validate integration test results", "docker-results.jsonl", [
            ("complete", "0", passed + "\n", True),
            ("valid final line without newline", "0", passed, True),
            ("partial success then abort", "1", passed + "\n", False),
            ("partial success then infra abort", "75", passed + "\n", False),
            ("missing exit code", "", passed + "\n", False),
            ("missing current results with stale success", "0", None, False),
            ("empty current results", "0", "", False),
            ("harness only", "0", '{"status":"PASS","group":"HARNESS"}\n', False),
            ("all skipped", "0", '{"status":"SKIP","group":"providers"}\n', False),
            ("failure without final newline", "0", passed + "\n" + failed, False),
            ("corrupt final line", "0", passed + "\n{" , False),
            ("unknown status", "0", passed + '\n{"status":"UNKNOWN"}\n', False),
            ("supported feature skip after pass", "0", passed + '\n{"status":"SKIP"}\n', True),
        ])

    def test_arm_gate(self):
        passed = '{"status":"PASS"}\n'
        self.check_gate("arm-controller-tests.yml", "Validate ARM controller test results", "arm-controller-results.jsonl", [
            ("complete", "0", passed, True),
            ("abort after pass", "1", passed, False),
            ("failure without final newline", "0", passed + '{"status":"FAIL"}', False),
            ("corrupt final line", "0", passed + '{', False),
            ("missing results", "0", None, False),
            ("all skipped", "0", '{"status":"SKIP"}\n', False),
        ])


if __name__ == "__main__":
    unittest.main()
