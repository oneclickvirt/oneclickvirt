# Rebuild only the application layers on an existing all-in-one runtime.
# This avoids reinstalling the embedded database when an upstream database
# package is temporarily unavailable during a production image build.

ARG BASE_IMAGE=oneclickvirt/oneclickvirt:latest
ARG GO_VERSION=1.25.0

FROM node:22-slim AS frontend-builder
ARG TARGETARCH
ARG NODE_OPTIONS=--max-old-space-size=1024
ENV NODE_OPTIONS=${NODE_OPTIONS}
WORKDIR /app/web
COPY web/package*.json ./
RUN npm ci --include=optional
RUN if [ "$TARGETARCH" = "amd64" ]; then \
        npm install --no-save @rollup/rollup-linux-x64-gnu; \
    elif [ "$TARGETARCH" = "arm64" ]; then \
        npm install --no-save @rollup/rollup-linux-arm64-gnu; \
    fi
COPY web/ ./
RUN npm run build

FROM golang:${GO_VERSION}-alpine AS backend-builder
ARG TARGETARCH
ARG BUILD_COMMIT=runtime-overlay
ARG BUILD_TIME
WORKDIR /app/server
ENV GOTOOLCHAIN=local
RUN apk add --no-cache git ca-certificates
COPY server/ ./
COPY scripts/install_agent.sh /app/install_agent.sh
RUN mkdir -p assets/agent && cp /app/install_agent.sh assets/agent/install_agent.sh
RUN go mod download
RUN build_time="${BUILD_TIME}" && \
    if [ -z "$build_time" ]; then build_time=$(date -u +%Y-%m-%dT%H:%M:%SZ); fi && \
    CGO_ENABLED=0 GOOS=linux GOARCH=${TARGETARCH} go build -a -installsuffix cgo \
    -ldflags "-w -s -X oneclickvirt/constant.BuildCommit=${BUILD_COMMIT} -X oneclickvirt/constant.BuildTime=${build_time} -X oneclickvirt/constant.BuildSignature=runtime-overlay" \
    -o main .

FROM ${BASE_IMAGE} AS runtime
COPY --from=backend-builder /app/server/main /app/main
COPY --from=frontend-builder /app/web/dist/ /var/www/html/
COPY deploy/all-in-one-nginx.conf /etc/nginx/nginx.conf
RUN chown -R www-data:www-data /var/www/html && \
    chmod -R 755 /var/www/html && \
    chmod 755 /app/main
