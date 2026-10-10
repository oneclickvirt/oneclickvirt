#!/usr/bin/env python3
"""Run the explicit local suite. Cloud/node drivers are never auto-discovered."""
import argparse
import ast
import os
from pathlib import Path
import signal
import subprocess
import sys
import tempfile

ROOT = Path(__file__).resolve().parents[2]

LOCAL_SHELL = """
action_harness_classification_test.sh action_prerequisite_test.sh
agent_egress_boot_guard_test.sh agent_env_reinstall_test.sh
all_in_one_entrypoint_test.sh api_contract_sync_test.sh api_retry_safety_test.sh
cnb_snapshot_test.sh config_source_consistency_test.sh
containerd_runtime_verifier_test.sh database_config_apply_test.sh
database_install_safety_test.sh docker_fixture_cleanup_test.sh download_atomic_test.sh
firewall_runner_prerequisite_test.sh hetzner_password_rebuild_test.sh
host_ipv6_harness_probe_test.sh http_status_contract_test.sh
install_full_service_lifecycle_test.sh install_lifecycle_commands_test.sh
install_upgrade_rollback_test.sh instance_creation_result_gate_test.sh
instance_task_lookup_test.sh ipv6_acceptance_cleanup_test.sh
ipv6_tunnel_ci_isolation_test.sh local_install_modes_test.sh
lxd_dns_refresh_test.sh module_runner_integrity_test.sh module_selection_test.sh
no_db_entrypoint_test.sh non_pve_initialization_test.sh
platform_preservation_test.sh platform_ssh_credentials_test.sh
port_mapping_agent_readiness_test.sh port_mapping_result_gate_test.sh
provider_exec_status_contract_test.sh provider_image_instance_preservation_test.sh
provider_image_result_gate_test.sh provider_image_selection_test.sh
provider_worker_preflight_test.sh provider_storage_budget_test.sh report_history_preservation_test.sh
result_integrity_test.sh start_script_safety_test.sh user_password_rotation_test.sh
worker_ssh_auth_test.sh workflow_result_gate_test.sh
""".split()

LOCAL_PYTHON = """
ci_agent_assets_test.py live_incus_panel_network_test.py
live_install_storage_test.py live_lxc_script_compat_test.py
live_nested_docker_test.py live_node_shell_test.py live_panel_cleanup_test.py
live_ssh_auth_test.py live_uninstall_prompts_test.py remote_ssh_io_test.py
webssh_external_probe_test.py local_test_inventory_test.py
""".split()

# These entrypoints require their own dedicated fixture/workflow. Keeping
# them explicit prevents a newly named remote test from entering a local run.
DEDICATED = {
    "database_compat_integration_test.sh": "isolated Docker databases",
    "embedded_database_integration_test.sh": "isolated Docker databases",
    "embedded_database_live_test.sh": "inside the database test container",
    "firewall_integration_test.sh": "privileged Docker firewall fixture",
    "docker_arm_lifecycle_test.sh": "prepared ARM Docker images",
    "ipv6_external_acceptance_test.sh": "explicit disposable node",
    "live_incus_panel_test.py": "explicit disposable node",
    "live_incus_uninstall_test.py": "explicit disposable node",
    "live_lxc_install_test.py": "explicit disposable node",
    "live_lxc_script_test.py": "explicit disposable node",
    # Called by workflow_result_gate_test.sh; do not run the same cases twice.
    "workflow_result_gate_test.py": "nested in workflow_result_gate_test.sh",
}


def validate_inventory(root=ROOT):
    directory = root / "scripts/tests"
    listed = LOCAL_SHELL + LOCAL_PYTHON + list(DEDICATED)
    if len(listed) != len(set(listed)):
        raise ValueError("test inventory contains duplicate entries")
    discovered = {p.name for pattern in ("*_test.sh", "*_test.py")
                  for p in directory.glob(pattern)}
    unknown = discovered - set(listed)
    missing = set(listed) - discovered
    if unknown or missing:
        raise ValueError(f"unclassified tests: {sorted(unknown)}; missing tests: {sorted(missing)}")
    return len(LOCAL_SHELL) + len(LOCAL_PYTHON)


def check_sources():
    for directory in (ROOT / "action_tests", ROOT / "scripts/tests"):
        for path in sorted(directory.rglob("*.sh")):
            subprocess.run(["bash", "-n", str(path)], check=True)
        for path in sorted(directory.rglob("*.py")):
            ast.parse(path.read_text(), filename=str(path))


def stop_group(process):
    # The process group is owned by this test, including any child it started.
    try:
        os.killpg(process.pid, signal.SIGKILL)
    except ProcessLookupError:
        pass
    process.wait()


def run_one(command, environment, timeout=180, cwd=ROOT):
    process = subprocess.Popen(command, cwd=cwd, env=environment, start_new_session=True)
    try:
        return process.wait(timeout=timeout)
    finally:
        # Also reap subprocesses left after an early assertion failure.
        stop_group(process)


def main():
    def interrupted(_signum, _frame):
        raise KeyboardInterrupt

    signal.signal(signal.SIGTERM, interrupted)
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--check-only", action="store_true")
    parser.add_argument("--tests", nargs="+", help="run only named local tests")
    args = parser.parse_args()
    try:
        count = validate_inventory()
        check_sources()
    except (ValueError, SyntaxError, subprocess.CalledProcessError) as error:
        parser.exit(1, f"Local test inventory/syntax check failed: {error}\n")
    if args.check_only:
        print(f"Local test inventory and syntax valid: {count} local entries; {len(DEDICATED)} dedicated entries")
        return 0
    selected = args.tests if args.tests is not None else LOCAL_SHELL + LOCAL_PYTHON
    if not selected or len(selected) != len(set(selected)) or set(selected) - set(LOCAL_SHELL + LOCAL_PYTHON):
        parser.error("select each local test once; remote/container entrypoints require their dedicated runner")
    environment = dict(os.environ, PYTHONDONTWRITEBYTECODE="1")
    # Unit discovery may skip an optional dependency. The local transport
    # suite requires it, so fail before execution rather than accept all-SKIP.
    if "remote_ssh_io_test.py" in selected:
        import importlib.util
        if importlib.util.find_spec("paramiko") is None:
            parser.exit(1, "Install scripts/tests/requirements-live.txt for loopback SSH tests\n")
    failures = []
    with tempfile.TemporaryDirectory(prefix="ocv-local-tests-") as directory:
        environment["REPORT_DIR"] = directory
        environment["TMPDIR"] = directory
        for name in selected:
            print(f"Running local test: {name}", flush=True)
            path = str(ROOT / "scripts/tests" / name)
            command = ["bash", path] if name.endswith(".sh") else [sys.executable, "-B", path]
            try:
                status = run_one(command, environment)
            except subprocess.TimeoutExpired:
                status = 124
            if status != 0:
                failures.append((name, status))
    if failures:
        print(f"Local regressions failed: {failures}", file=sys.stderr)
        return 1
    print(f"Local regressions passed: {len(selected)} entrypoints")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
