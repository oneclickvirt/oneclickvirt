#!/bin/bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
REPORT_DIR=$(mktemp -d "${TMPDIR:-/tmp}/ocv-report-history-test.XXXXXX")
trap 'rm -rf "$REPORT_DIR"' EXIT

write_result() {
    local name="$1" path="${REPORT_DIR}/${1}-results.jsonl"
    printf '{"name":"%s","status":"PASS"}\n' "$name" > "$path"
}

for name in incus lxd podman; do
    write_result "$name"
done
write_result docker

"${BASH:-bash}" "${ROOT_DIR}/action_tests/report/generate_report.sh" \
    "${REPORT_DIR}/docker-results.jsonl" "${REPORT_DIR}/docker-report.html" docker

[[ -s "${REPORT_DIR}/docker-results.jsonl" ]] || {
    echo "Current results JSONL was removed during report generation" >&2
    exit 1
}
[[ "$(jq -r '.status' "${REPORT_DIR}/docker-results.jsonl")" == "PASS" ]] || {
    echo "Current results JSONL content changed during report generation" >&2
    exit 1
}
[[ -s "${REPORT_DIR}/docker-report.html" ]] || {
    echo "Current HTML report was not generated" >&2
    exit 1
}

echo "Report history pruning preserves the active results JSONL"
