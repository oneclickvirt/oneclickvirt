# --help Integration Test Report

Test Time: 2026-09-18 19:11:23 UTC
Environment: --help
Instance Types: container

## Summary

| Metric | Value |
|--------|-------|
| Total | 6 |
| Passed | 5 |
| Failed | 0 |
| Skipped | 1 |
| Pass Rate | 83% |

## Test Details

| PASS | Platform resolution | PREFLIGHT | `platforms` | - |
| PASS | Required commands | PREFLIGHT | `commands` | - |
| PASS | Runner resources | PREFLIGHT | `runner` | - |
| PASS | Master port availability | PREFLIGHT | `port:8888` | - |
| PASS | MySQL readiness | PREFLIGHT | `mysql` | - |
| SKIP | Worker node provisioning | HARNESS | `create_test_node` | Worker node provisioning failed with exit 1 |

---

Completed: Total=6 Passed=5 Failed=0 Skipped=1 Rate=83%
