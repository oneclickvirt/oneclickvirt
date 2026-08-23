# Action Test Static Audit

## Summary

| Metric | Value |
|---|---:|
| Registered route calls | 408 |
| Comparable route literals | 405 |
| Approx. covered route literals | 343 |
| Approx. route literal coverage | 84.69% |
| Distinct test paths | 536 |
| Test endpoint call sites | 1168 |
| High-risk jq lines | 0 |
| Pipe risk lines | 0 |
| Workflow findings | 0 |
| Retry hygiene findings | 0 |
| Minimum route literal coverage | 82.0% |

## HTTP Method Coverage

| Method | Routes | Tests |
|---|---:|---:|
| GET | 209 | 555 |
| POST | 135 | 431 |
| PUT | 35 | 113 |
| DELETE | 29 | 68 |
| PATCH | 0 | 1 |

## Uncovered Route Literals (sample)

- `GET /instances/:id/snapshots` at `server/service/router/admin.go:40`
- `POST /instances/:id/snapshots` at `server/service/router/admin.go:41`
- `POST /snapshot-batches` at `server/service/router/admin.go:42`
- `GET /snapshots/overview` at `server/service/router/admin.go:57`
- `GET /snapshots` at `server/service/router/admin.go:58`
- `GET /snapshot-tasks/:id` at `server/service/router/admin.go:59`
- `DELETE /snapshots/:id` at `server/service/router/admin.go:60`
- `POST /snapshots/:id/restore` at `server/service/router/admin.go:61`
- `GET /snapshots/download/:id` at `server/service/router/admin.go:62`
- `GET /snapshot-schedules` at `server/service/router/admin.go:63`
- `POST /snapshot-schedules` at `server/service/router/admin.go:64`
- `PUT /snapshot-schedules/:id` at `server/service/router/admin.go:65`
- `DELETE /snapshot-schedules/:id` at `server/service/router/admin.go:66`
- `GET /providers/local/detect` at `server/service/router/admin.go:92`
- `POST /providers/import-csv` at `server/service/router/admin.go:93`
- `POST /providers/:id/cleanup-orphans` at `server/service/router/admin.go:110`
- `POST /configuration-tasks/:id/cancel` at `server/service/router/admin.go:143`
- `GET /providers/:id/monitoring/sync/latest` at `server/service/router/admin.go:210`
- `GET /providers/:id/monitoring/sync/:taskId` at `server/service/router/admin.go:211`
- `POST /domains/sync-proxies` at `server/service/router/admin.go:241`
- `POST /domains/:id/sync` at `server/service/router/admin.go:243`
- `POST /system-images/sync` at `server/service/router/admin.go:282`
- `PUT /users/:id/reset-password-notify` at `server/service/router/admin.go:297`
- `GET /monitoring/logs` at `server/service/router/admin.go:327`
- `GET /monitoring/provider` at `server/service/router/admin.go:328`
- `GET /logs/read` at `server/service/router/admin.go:340`
- `POST /logs/cleanup` at `server/service/router/admin.go:341`
- `POST /storage/init` at `server/service/router/admin.go:345`
- `POST /storage/cleanup` at `server/service/router/admin.go:346`
- `GET callback` at `server/service/router/oauth2.go:18`
- `POST /instances/:name/start` at `server/service/router/provider.go:46`
- `POST /instances/:name/stop` at `server/service/router/provider.go:47`
- `DELETE /images/:image` at `server/service/router/provider.go:51`
- `GET instance-shares/:token/snapshots` at `server/service/router/public.go:28`
- `GET instance-shares/:token/snapshots/:snapshotId/download` at `server/service/router/public.go:29`
- `GET instance-shares/:token/ssh` at `server/service/router/public.go:38`
- `GET instance-shares/:token/exec` at `server/service/router/public.go:39`
- `GET instance-shares/:token/sftp/list` at `server/service/router/public.go:40`
- `GET instance-shares/:token/sftp/download` at `server/service/router/public.go:41`
- `POST instance-shares/:token/sftp/upload` at `server/service/router/public.go:42`
- `GET instance-shares/:token/sftp/upload/status` at `server/service/router/public.go:43`
- `POST instance-shares/:token/sftp/upload/abort` at `server/service/router/public.go:44`
- `GET /swagger/*any` at `server/service/router/setup.go:310`
- `GET /swagger/*any` at `server/service/router/setup.go:312`
- `GET /v1/health` at `server/service/router/setup.go:320`
- `GET agent/releases/:filename` at `server/service/router/setup.go:339`
- `GET /v1/ws/agent` at `server/service/router/setup.go:390`
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
