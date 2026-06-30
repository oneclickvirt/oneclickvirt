#!/usr/bin/env python3
"""Static audit for action_tests and API route coverage.

This script is intentionally conservative: it produces a report by default and
only exits non-zero when --strict is supplied. It does not call live services.
"""

from __future__ import annotations

import argparse
import json
import re
from collections import Counter
from dataclasses import asdict, dataclass
from pathlib import Path


HTTP_METHODS = ("GET", "POST", "PUT", "DELETE", "PATCH")
ROUTE_RE = re.compile(r"\.(GET|POST|PUT|DELETE|PATCH)\(\s*\"([^\"]*)\"")
PATH_RE = re.compile(
    r"[\"']((?:/api/v1)?/(?:admin|user|public|auth|oauth2|dashboard|providers|virtualization|stats|announcements|system-images)[^\"'\s)]*)[\"']"
)
METHOD_PATH_RE = re.compile(
    r"[\"'](GET|POST|PUT|DELETE|PATCH)[\"']\s+"
    r"[\"']((?:/api/v1)?/[^\"'\s)]*)[\"']",
    re.S,
)
JQ_INVOKE_RE = re.compile(r"(^|[\s|;&(<`])jq(\s|$)")


@dataclass(frozen=True)
class Finding:
    file: str
    line: int
    kind: str
    detail: str


@dataclass(frozen=True)
class Endpoint:
    method: str
    path: str
    file: str
    line: int


def rel(path: Path, root: Path) -> str:
    try:
        return str(path.relative_to(root))
    except ValueError:
        return str(path)


def iter_text_files(root: Path, *patterns: str) -> list[Path]:
    files: list[Path] = []
    for pattern in patterns:
        files.extend(root.glob(pattern))
    return sorted({p for p in files if p.is_file()})


def normalize_path(path: str) -> str:
    path = path.strip()
    path = path.split("?", 1)[0].split("#", 1)[0]
    if path and not path.startswith("/"):
        path = "/" + path
    path = re.sub(r"\$\{[^}]+\}", ":var", path)
    path = re.sub(r"\$[A-Za-z_][A-Za-z0-9_]*", ":var", path)
    path = re.sub(r":[A-Za-z_][A-Za-z0-9_]*", ":var", path)
    path = re.sub(r"/[0-9]+(?=/|$)", "/:var", path)
    path = re.sub(r"//+", "/", path)
    if path.startswith("/api/v1/"):
        path = path[len("/api/v1") :]
    return path.rstrip("/") or "/"


def scan_routes(root: Path) -> list[Endpoint]:
    routes: list[Endpoint] = []
    for path in iter_text_files(root, "server/service/router/**/*.go", "server/api/**/*.go"):
        for idx, line in enumerate(path.read_text(errors="ignore").splitlines(), 1):
            for match in ROUTE_RE.finditer(line):
                routes.append(Endpoint(match.group(1), match.group(2) or "/", rel(path, root), idx))
    return routes


def scan_test_paths(root: Path) -> tuple[list[Endpoint], set[str]]:
    endpoints: list[Endpoint] = []
    paths: set[str] = set()
    for path in iter_text_files(root, "action_tests/**/*.sh"):
        text = path.read_text(errors="ignore")
        for match in PATH_RE.finditer(text):
            paths.add(match.group(1))
        compact = re.sub(r"\\\n\s*", " ", text)
        line_starts = [0]
        for match in re.finditer(r"\n", compact):
            line_starts.append(match.end())
        for match in METHOD_PATH_RE.finditer(compact):
            line = 1 + sum(1 for start in line_starts if start <= match.start())
            endpoints.append(Endpoint(match.group(1), match.group(2), rel(path, root), line))
            paths.add(match.group(2))
    return endpoints, paths


def audit_shell(root: Path) -> tuple[list[Finding], list[Finding]]:
    jq_findings: list[Finding] = []
    pipe_findings: list[Finding] = []
    for path in iter_text_files(root, "action_tests/**/*.sh"):
        for idx, line in logical_lines(path.read_text(errors="ignore")):
            stripped = line.strip()
            if not stripped or stripped.startswith("#"):
                continue
            if JQ_INVOKE_RE.search(line):
                if (
                    stripped.startswith("log_")
                    or "apt-get install" in line
                    or re.search(r"\bapt-get\b.*\binstall\b", line)
                    or "preflight_require_commands" in line
                ):
                    continue
                guarded = (
                    "safe_jq" in line
                    or "jq empty" in line
                    or "command -v jq" in line
                    or "jq -R" in line
                    or "jq -sR" in line
                    or "jq -cn" in line
                    or "2>/dev/null" in line
                    or "|| true" in line
                )
                if not guarded:
                    jq_findings.append(Finding(rel(path, root), idx, "unguarded-jq", stripped))
            if re.search(r"\bcat\b.*\|\s*(head|tail)\b", line):
                pipe_findings.append(Finding(rel(path, root), idx, "cat-head-tail-pipe", stripped))
            if "| tee " in line and "PIPESTATUS" not in line and "set +e" not in line:
                pipe_findings.append(Finding(rel(path, root), idx, "tee-pipe", stripped))
            if re.search(r"\bset\s+-[A-Za-z]*e[A-Za-z]*(?:\s|$)", line):
                pipe_findings.append(Finding(rel(path, root), idx, "set-e", stripped))
    return jq_findings, pipe_findings


def logical_lines(text: str) -> list[tuple[int, str]]:
    result: list[tuple[int, str]] = []
    pending = ""
    pending_line = 1
    for idx, raw in enumerate(text.splitlines(), 1):
        line = raw.rstrip()
        if pending:
            pending += " " + line.lstrip()
        else:
            pending = line
            pending_line = idx
        if pending.rstrip().endswith("\\"):
            pending = pending.rstrip()[:-1]
            continue
        result.append((pending_line, pending))
        pending = ""
    if pending:
        result.append((pending_line, pending))
    return result


def audit_workflows(root: Path) -> list[Finding]:
    findings: list[Finding] = []
    for path in iter_text_files(root, ".github/workflows/*.yml", ".github/workflows/*.yaml"):
        text = path.read_text(errors="ignore")
        name = rel(path, root)
        if "FORCE_JAVASCRIPT_ACTIONS_TO_NODE24" not in text:
            findings.append(Finding(name, 1, "workflow-node24", "missing FORCE_JAVASCRIPT_ACTIONS_TO_NODE24"))
        if "\nconcurrency:" not in text:
            findings.append(Finding(name, 1, "workflow-concurrency", "missing top-level concurrency"))
        runs_on = len(re.findall(r"^\s+runs-on:", text, re.M))
        timeouts = len(re.findall(r"^\s+timeout-minutes:", text, re.M))
        if runs_on and timeouts < runs_on:
            findings.append(
                Finding(name, 1, "workflow-timeout", f"jobs={runs_on}, timeout-minutes={timeouts}")
            )
    return findings


def audit_retry_hygiene(root: Path) -> list[Finding]:
    findings: list[Finding] = []
    create_instance_re = re.compile(
        r"\btest_api\s+['\"][^'\"]*Create[^'\"]*['\"]\s+['\"]POST['\"]\s+['\"]"
        r"/api/v1/admin/instances['\"]\s+['\"]([^'\"]+)['\"]"
    )
    for path in iter_text_files(root, "action_tests/**/*.sh"):
        for idx, line in logical_lines(path.read_text(errors="ignore")):
            stripped = line.strip()
            if not stripped or stripped.startswith("#") or "test_api_retry" in stripped:
                continue
            match = create_instance_re.search(stripped)
            if match and match.group(1) in {"200", "200|201"}:
                findings.append(
                    Finding(
                        rel(path, root),
                        idx,
                        "create-instance-without-retry",
                        stripped,
                    )
                )
    return findings


def route_coverage(routes: list[Endpoint], tested_paths: set[str]) -> tuple[int, list[Endpoint]]:
    tested_norm = {normalize_path(path) for path in tested_paths}
    covered = 0
    uncovered: list[Endpoint] = []
    for route in routes:
        normalized = normalize_path(route.path)
        if normalized in {"/", ""}:
            continue
        matched = any(path == normalized or path.endswith(normalized) for path in tested_norm)
        if matched:
            covered += 1
        else:
            uncovered.append(route)
    return covered, uncovered


def render_markdown(summary: dict, sample_uncovered: list[Endpoint], findings: dict[str, list[Finding]]) -> str:
    lines = [
        "# Action Test Static Audit",
        "",
        "## Summary",
        "",
        "| Metric | Value |",
        "|---|---:|",
    ]
    for key, value in summary.items():
        if key.startswith("_"):
            continue
        lines.append(f"| {key} | {value} |")

    lines.extend(["", "## HTTP Method Coverage", "", "| Method | Routes | Tests |", "|---|---:|---:|"])
    route_methods = summary.get("_route_methods", {})
    test_methods = summary.get("_test_methods", {})
    for method in HTTP_METHODS:
        lines.append(f"| {method} | {route_methods.get(method, 0)} | {test_methods.get(method, 0)} |")

    lines.extend(["", "## Uncovered Route Literals (sample)", ""])
    if sample_uncovered:
        for item in sample_uncovered[:50]:
            lines.append(f"- `{item.method} {item.path}` at `{item.file}:{item.line}`")
    else:
        lines.append("- none")

    for title, items in findings.items():
        lines.extend(["", f"## {title}", ""])
        if not items:
            lines.append("- none")
            continue
        for item in items[:120]:
            lines.append(f"- `{item.file}:{item.line}` {item.kind}: `{item.detail}`")
        if len(items) > 120:
            lines.append(f"- ... {len(items) - 120} more")
    lines.append("")
    return "\n".join(lines)


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--root", default=".", help="Repository root")
    parser.add_argument("--output-dir", default="action_tests/reports", help="Report output directory")
    parser.add_argument("--strict", action="store_true", help="Exit non-zero on high-risk findings")
    parser.add_argument(
        "--min-route-coverage",
        type=float,
        default=0.0,
        help="Minimum approximate route literal coverage percentage required in strict mode",
    )
    args = parser.parse_args()

    root = Path(args.root).resolve()
    out_dir = (root / args.output_dir).resolve()
    out_dir.mkdir(parents=True, exist_ok=True)

    routes = scan_routes(root)
    test_endpoints, test_paths = scan_test_paths(root)
    jq_findings, pipe_findings = audit_shell(root)
    workflow_findings = audit_workflows(root)
    retry_findings = audit_retry_hygiene(root)
    covered, uncovered = route_coverage(routes, test_paths)

    route_methods = Counter(item.method for item in routes)
    test_methods = Counter(item.method for item in test_endpoints)
    comparable_routes = sum(1 for route in routes if normalize_path(route.path) not in {"/", ""})
    coverage_pct = round((covered / comparable_routes) * 100, 2) if comparable_routes else 100.0

    summary = {
        "Registered route calls": len(routes),
        "Comparable route literals": comparable_routes,
        "Approx. covered route literals": covered,
        "Approx. route literal coverage": f"{coverage_pct}%",
        "Distinct test paths": len(test_paths),
        "Test endpoint call sites": len(test_endpoints),
        "High-risk jq lines": len(jq_findings),
        "Pipe risk lines": len(pipe_findings),
        "Workflow findings": len(workflow_findings),
        "Retry hygiene findings": len(retry_findings),
        "Minimum route literal coverage": f"{args.min_route_coverage}%" if args.min_route_coverage else "not enforced",
        "_route_methods": dict(route_methods),
        "_test_methods": dict(test_methods),
    }

    findings = {
        "Unguarded jq Findings": jq_findings,
        "Pipe Findings": pipe_findings,
        "Workflow Findings": workflow_findings,
        "Retry Hygiene Findings": retry_findings,
    }

    serializable_summary = {k: v for k, v in summary.items() if not k.startswith("_")}
    json_report = {
        "summary": serializable_summary,
        "route_methods": dict(route_methods),
        "test_methods": dict(test_methods),
        "uncovered_sample": [asdict(item) for item in uncovered[:100]],
        "findings": {key: [asdict(item) for item in items] for key, items in findings.items()},
    }

    (out_dir / "static-audit.json").write_text(json.dumps(json_report, indent=2, ensure_ascii=False) + "\n")
    (out_dir / "static-audit.md").write_text(render_markdown(summary, uncovered, findings))

    print(f"Static audit written to {out_dir / 'static-audit.md'}")
    print(json.dumps(serializable_summary, ensure_ascii=False))

    coverage_failed = args.min_route_coverage > 0 and coverage_pct < args.min_route_coverage
    if coverage_failed:
        print(
            f"ERROR: route literal coverage {coverage_pct}% is below required {args.min_route_coverage}%",
            flush=True,
        )

    if args.strict and (jq_findings or pipe_findings or workflow_findings or retry_findings or coverage_failed):
        return 1
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
