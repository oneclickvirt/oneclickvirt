# Action Test Static Audit

## Summary

| Metric | Value |
|---|---:|
| Registered route calls | 295 |
| Comparable route literals | 292 |
| Approx. covered route literals | 282 |
| Approx. route literal coverage | 96.58% |
| Distinct test paths | 430 |
| Test endpoint call sites | 929 |
| High-risk jq lines | 0 |
| Pipe risk lines | 0 |
| Workflow findings | 0 |
| Retry hygiene findings | 0 |
| Minimum route literal coverage | 85.0% |

## HTTP Method Coverage

| Method | Routes | Tests |
|---|---:|---:|
| GET | 150 | 409 |
| POST | 97 | 364 |
| PUT | 27 | 91 |
| DELETE | 21 | 64 |
| PATCH | 0 | 1 |

## Uncovered Route Literals (sample)

- `POST /providers/import-csv` at `server/service/router/admin.go:49`
- `GET callback` at `server/service/router/oauth2.go:18`
- `POST /instances/:name/start` at `server/service/router/provider.go:44`
- `POST /instances/:name/stop` at `server/service/router/provider.go:45`
- `DELETE /images/:image` at `server/service/router/provider.go:49`
- `GET /swagger/*any` at `server/service/router/setup.go:282`
- `GET /swagger/*any` at `server/service/router/setup.go:284`
- `GET /v1/health` at `server/service/router/setup.go:292`
- `GET agent/releases/:filename` at `server/service/router/setup.go:311`
- `GET /v1/ws/agent` at `server/service/router/setup.go:362`

## Unguarded jq Findings

- none

## Pipe Findings

- none

## Workflow Findings

- none

## Retry Hygiene Findings

- none
