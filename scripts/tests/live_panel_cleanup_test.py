#!/usr/bin/env python3
"""Fixture cleanup contracts; --docker also runs real Docker volume checks."""
import json
import secrets
import subprocess
import sys
import unittest
from unittest.mock import Mock

from live_panel_cleanup import remove_owned_panel


class CleanupTests(unittest.TestCase):
    def test_removes_by_identity_and_requests_anonymous_volume_cleanup(self):
        docker = Mock(side_effect=[json.dumps([{
            "Id": "immutable-id", "Config": {"Labels": {"ocv.live.run": "run"}}
        }]), "immutable-id"])
        remove_owned_panel(docker, "reusable-name", "run")
        self.assertEqual(docker.call_args_list[1].args, ("rm", "-f", "-v", "immutable-id"))

    def test_wrong_owner_is_preserved(self):
        docker = Mock(return_value=json.dumps([{
            "Id": "immutable-id", "Config": {"Labels": {"ocv.live.run": "other"}}
        }]))
        with self.assertRaisesRegex(RuntimeError, "ownership"):
            remove_owned_panel(docker, "name", "run")
        self.assertEqual(docker.call_count, 1)

    def test_inspection_failure_does_not_delete(self):
        docker = Mock(side_effect=RuntimeError("daemon unavailable"))
        with self.assertRaisesRegex(RuntimeError, "unavailable"):
            remove_owned_panel(docker, "name", "run")
        self.assertEqual(docker.call_count, 1)

    def test_delete_failure_is_not_success(self):
        docker = Mock(side_effect=[json.dumps([{
            "Id": "immutable-id", "Config": {"Labels": {"ocv.live.run": "run"}}
        }]), RuntimeError("delete failed")])
        with self.assertRaisesRegex(RuntimeError, "delete failed"):
            remove_owned_panel(docker, "name", "run")


def real_docker_regression():
    def docker(*args):
        return subprocess.check_output(["docker", *args], text=True, stderr=subprocess.PIPE).strip()

    run_id = "ocv-cleanup-" + secrets.token_hex(12)
    named = run_id + "-named"
    docker("volume", "create", "--label", "ocv.live.run=" + run_id, named)
    container_id = ""
    try:
        container_id = docker("create", "--label", "ocv.live.run=" + run_id,
                              "--volume", "/anonymous", "--volume", named + ":/named",
                              "alpine:3.22", "true")
        mounts = json.loads(docker("inspect", container_id))[0]["Mounts"]
        anonymous = next(m["Name"] for m in mounts if m["Destination"] == "/anonymous")
        try:
            remove_owned_panel(docker, container_id, "wrong-owner")
        except RuntimeError:
            pass
        else:
            raise AssertionError("mismatched owner was accepted")
        docker("inspect", container_id)
        remove_owned_panel(docker, container_id, run_id)
        container_id = ""
        remaining = docker("volume", "ls", "--format", "{{.Name}}").splitlines()
        assert anonymous not in remaining, "anonymous volume survived cleanup"
        assert named in remaining, "named volume was incorrectly deleted"
        print("PASS: real Docker anonymous removal, named-volume and wrong-owner preservation")
    finally:
        if container_id:
            remove_owned_panel(docker, container_id, run_id)
        info = json.loads(docker("volume", "inspect", named))[0]
        if info.get("Labels", {}).get("ocv.live.run") != run_id:
            raise RuntimeError("test volume ownership changed")
        if docker("ps", "-aq", "--filter", "volume=" + named):
            raise RuntimeError("test volume acquired a container reference")
        docker("volume", "rm", named)


if __name__ == "__main__":
    run_docker = "--docker" in sys.argv
    if run_docker:
        sys.argv.remove("--docker")
    result = unittest.main(verbosity=2, exit=False).result
    if not result.wasSuccessful():
        raise SystemExit(1)
    if run_docker:
        real_docker_regression()
