# Action Test Static Audit

## Summary

| Metric | Value |
|---|---:|
| Registered route calls | 363 |
| Comparable route literals | 360 |
| Approx. covered route literals | 305 |
| Approx. route literal coverage | 84.72% |
| Distinct test paths | 488 |
| Test endpoint call sites | 1058 |
| High-risk jq lines | 0 |
| Pipe risk lines | 0 |
| Workflow findings | 0 |
| Retry hygiene findings | 0 |
| Minimum route literal coverage | 80.0% |

## HTTP Method Coverage

| Method | Routes | Tests |
|---|---:|---:|
| GET | 188 | 494 |
| POST | 117 | 389 |
| PUT | 33 | 109 |
| DELETE | 25 | 65 |
| PATCH | 0 | 1 |

## Uncovered Route Literals (sample)

- `GET /instances/:id/snapshots` at `server/service/router/admin.go:28`
- `POST /instances/:id/snapshots` at `server/service/router/admin.go:29`
- `POST /snapshot-batches` at `server/service/router/admin.go:30`
- `GET /snapshots/overview` at `server/service/router/admin.go:39`
- `GET /snapshots` at `server/service/router/admin.go:40`
- `GET /snapshot-tasks/:id` at `server/service/router/admin.go:41`
- `DELETE /snapshots/:id` at `server/service/router/admin.go:42`
- `POST /snapshots/:id/restore` at `server/service/router/admin.go:43`
- `GET /snapshots/download/:id` at `server/service/router/admin.go:44`
- `GET /snapshot-schedules` at `server/service/router/admin.go:45`
- `POST /snapshot-schedules` at `server/service/router/admin.go:46`
- `PUT /snapshot-schedules/:id` at `server/service/router/admin.go:47`
- `DELETE /snapshot-schedules/:id` at `server/service/router/admin.go:48`
- `GET /providers/local/detect` at `server/service/router/admin.go:65`
- `POST /providers/import-csv` at `server/service/router/admin.go:66`
- `POST /providers/:id/cleanup-orphans` at `server/service/router/admin.go:82`
- `GET /providers/:id/monitoring/sync/latest` at `server/service/router/admin.go:169`
- `GET /providers/:id/monitoring/sync/:taskId` at `server/service/router/admin.go:170`
- `POST /domains/sync-proxies` at `server/service/router/admin.go:203`
- `POST /domains/:id/sync` at `server/service/router/admin.go:205`
- `POST /system-images/sync` at `server/service/router/admin.go:242`
- `PUT /users/:id/reset-password-notify` at `server/service/router/admin.go:257`
- `GET /monitoring/logs` at `server/service/router/admin.go:287`
- `GET /monitoring/provider` at `server/service/router/admin.go:288`
- `GET /logs/read` at `server/service/router/admin.go:300`
- `POST /logs/cleanup` at `server/service/router/admin.go:301`
- `POST /storage/init` at `server/service/router/admin.go:305`
- `POST /storage/cleanup` at `server/service/router/admin.go:306`
- `GET callback` at `server/service/router/oauth2.go:18`
- `POST /instances/:name/start` at `server/service/router/provider.go:46`
- `POST /instances/:name/stop` at `server/service/router/provider.go:47`
- `DELETE /images/:image` at `server/service/router/provider.go:51`
- `GET instance-shares/:token/snapshots` at `server/service/router/public.go:28`
- `GET instance-shares/:token/snapshots/:snapshotId/download` at `server/service/router/public.go:29`
- `GET instance-shares/:token/ssh` at `server/service/router/public.go:30`
- `GET instance-shares/:token/exec` at `server/service/router/public.go:31`
- `GET instance-shares/:token/sftp/list` at `server/service/router/public.go:32`
- `GET instance-shares/:token/sftp/download` at `server/service/router/public.go:33`
- `POST instance-shares/:token/sftp/upload` at `server/service/router/public.go:34`
- `GET instance-shares/:token/sftp/upload/status` at `server/service/router/public.go:35`
- `POST instance-shares/:token/sftp/upload/abort` at `server/service/router/public.go:36`
- `GET /swagger/*any` at `server/service/router/setup.go:282`
- `GET /swagger/*any` at `server/service/router/setup.go:284`
- `GET /v1/health` at `server/service/router/setup.go:292`
- `GET agent/releases/:filename` at `server/service/router/setup.go:311`
- `GET /v1/ws/agent` at `server/service/router/setup.go:362`
- `GET /user/instances/:id/snapshots` at `server/service/router/user.go:41`
- `POST /user/instances/:id/snapshots` at `server/service/router/user.go:42`
- `POST /user/instances/:id/snapshots/upload` at `server/service/router/user.go:43`
- `POST /user/snapshots/:id/restore` at `server/service/router/user.go:44`

## Unguarded jq Findings

- none

## Pipe Findings

- none

## Workflow Findings

- none

## Retry Hygiene Findings

- none
