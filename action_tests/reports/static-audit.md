# Action Test Static Audit

## Summary

| Metric | Value |
|---|---:|
| Registered route calls | 383 |
| Comparable route literals | 380 |
| Approx. covered route literals | 324 |
| Approx. route literal coverage | 85.26% |
| Distinct test paths | 511 |
| Test endpoint call sites | 1139 |
| High-risk jq lines | 0 |
| Pipe risk lines | 0 |
| Workflow findings | 0 |
| Retry hygiene findings | 0 |
| Minimum route literal coverage | 82.0% |

## HTTP Method Coverage

| Method | Routes | Tests |
|---|---:|---:|
| GET | 191 | 539 |
| POST | 128 | 418 |
| PUT | 35 | 113 |
| DELETE | 29 | 68 |
| PATCH | 0 | 1 |

## Uncovered Route Literals (sample)

- `GET /instances/:id/snapshots` at `server/service/router/admin.go:28`
- `POST /instances/:id/snapshots` at `server/service/router/admin.go:29`
- `POST /snapshot-batches` at `server/service/router/admin.go:30`
- `GET /snapshots/overview` at `server/service/router/admin.go:45`
- `GET /snapshots` at `server/service/router/admin.go:46`
- `GET /snapshot-tasks/:id` at `server/service/router/admin.go:47`
- `DELETE /snapshots/:id` at `server/service/router/admin.go:48`
- `POST /snapshots/:id/restore` at `server/service/router/admin.go:49`
- `GET /snapshots/download/:id` at `server/service/router/admin.go:50`
- `GET /snapshot-schedules` at `server/service/router/admin.go:51`
- `POST /snapshot-schedules` at `server/service/router/admin.go:52`
- `PUT /snapshot-schedules/:id` at `server/service/router/admin.go:53`
- `DELETE /snapshot-schedules/:id` at `server/service/router/admin.go:54`
- `GET /providers/local/detect` at `server/service/router/admin.go:71`
- `POST /providers/import-csv` at `server/service/router/admin.go:72`
- `POST /providers/:id/cleanup-orphans` at `server/service/router/admin.go:89`
- `POST /configuration-tasks/:id/cancel` at `server/service/router/admin.go:122`
- `GET /providers/:id/monitoring/sync/latest` at `server/service/router/admin.go:188`
- `GET /providers/:id/monitoring/sync/:taskId` at `server/service/router/admin.go:189`
- `POST /domains/sync-proxies` at `server/service/router/admin.go:219`
- `POST /domains/:id/sync` at `server/service/router/admin.go:221`
- `POST /system-images/sync` at `server/service/router/admin.go:260`
- `PUT /users/:id/reset-password-notify` at `server/service/router/admin.go:275`
- `GET /monitoring/logs` at `server/service/router/admin.go:305`
- `GET /monitoring/provider` at `server/service/router/admin.go:306`
- `GET /logs/read` at `server/service/router/admin.go:318`
- `POST /logs/cleanup` at `server/service/router/admin.go:319`
- `POST /storage/init` at `server/service/router/admin.go:323`
- `POST /storage/cleanup` at `server/service/router/admin.go:324`
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
- `GET /swagger/*any` at `server/service/router/setup.go:284`
- `GET /swagger/*any` at `server/service/router/setup.go:286`
- `GET /v1/health` at `server/service/router/setup.go:294`
- `GET agent/releases/:filename` at `server/service/router/setup.go:313`
- `GET /v1/ws/agent` at `server/service/router/setup.go:364`
- `GET /user/instances/:id/snapshots` at `server/service/router/user.go:41`
- `POST /user/instances/:id/snapshots` at `server/service/router/user.go:42`
- `POST /user/instances/:id/snapshots/upload` at `server/service/router/user.go:43`

## Unguarded jq Findings

- none

## Pipe Findings

- none

## Workflow Findings

- none

## Retry Hygiene Findings

- none
