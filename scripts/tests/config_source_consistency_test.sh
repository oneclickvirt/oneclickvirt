#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
CONFIG="${ROOT_DIR}/server/config.yaml"

test -s "${CONFIG}"

# Keep the tracked template as the only default-config source.  A byte-level
# check catches accidental same-size/stale copies that timestamp checks miss.
CONFIG_HASH="$(sha256sum "${CONFIG}" | awk '{print $1}')"
test "${#CONFIG_HASH}" -eq 64
if [ -f "${ROOT_DIR}/server/config/config.yaml" ] && ! cmp -s "${CONFIG}" "${ROOT_DIR}/server/config/config.yaml"; then
    echo "server/config/config.yaml diverges from server/config.yaml" >&2
    exit 1
fi

# Every image/build entry point must consume the one tracked template. A
# missing path here otherwise fails only on a clean Docker build.
grep -Fqx 'COPY config.yaml /app/config.yaml' "${ROOT_DIR}/server/Dockerfile"
grep -Fqx 'COPY server/config.yaml /app/config.yaml' "${ROOT_DIR}/deploy/server.dockerfile"
grep -Fq 'COPY --from=backend-builder /app/server/config.yaml ./config.yaml.default' "${ROOT_DIR}/Dockerfile"
grep -Fq 'COPY --from=backend-builder /app/server/config.yaml ./config.yaml.default' "${ROOT_DIR}/Dockerfile.no-db"
grep -Fq './server/config.yaml:/app/config.yaml:rw' "${ROOT_DIR}/docker-compose.yaml"
grep -Fq 'OCV_CONTROLLER_PORT_RANGE_START: "${OCV_CONTROLLER_PORT_RANGE_START:-10000}"' "${ROOT_DIR}/docker-compose.yaml"
grep -Fq 'OCV_CONTROLLER_PORT_RANGE_END: "${OCV_CONTROLLER_PORT_RANGE_END:-10099}"' "${ROOT_DIR}/docker-compose.yaml"
grep -Fq -- '${OCV_CONTROLLER_PORT_RANGE_START:-10000}-${OCV_CONTROLLER_PORT_RANGE_END:-10099}:${OCV_CONTROLLER_PORT_RANGE_START:-10000}-${OCV_CONTROLLER_PORT_RANGE_END:-10099}' "${ROOT_DIR}/docker-compose.yaml"
grep -Fqx 'agent/target/' "${ROOT_DIR}/server/.dockerignore"

echo "config source consistency tests passed"
