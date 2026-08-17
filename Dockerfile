# OneClickVirt All-in-One Container

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
ARG SERVER_VERSION=dev
WORKDIR /app/server
ENV GOTOOLCHAIN=local
RUN apk add --no-cache git ca-certificates
COPY server/ ./
COPY scripts/install_agent.sh /app/install_agent.sh
RUN mkdir -p assets/agent && cp /app/install_agent.sh assets/agent/install_agent.sh
RUN go version
RUN go mod download
RUN sed -i "s/const ServerVersion = \".*\"/const ServerVersion = \"${SERVER_VERSION}\"/" constant/version.go && \
    sed -i "s/const CompatibleAgentVersion = \".*\"/const CompatibleAgentVersion = \"${SERVER_VERSION}\"/" constant/version.go
RUN BUILD_COMMIT=$(git rev-parse HEAD 2>/dev/null || echo "docker") && \
    BUILD_TIME=$(date -u +%Y-%m-%dT%H:%M:%SZ) && \
    CGO_ENABLED=0 GOOS=linux GOARCH=${TARGETARCH} go build -a -installsuffix cgo \
    -ldflags "-w -s -X oneclickvirt/constant.BuildCommit=${BUILD_COMMIT} -X oneclickvirt/constant.BuildTime=${BUILD_TIME} -X oneclickvirt/constant.BuildSignature=official-docker" \
    -o main .

FROM debian:12-slim
ARG TARGETARCH

# Install database and other services based on architecture
RUN apt-get update && \
    DEBIAN_FRONTEND=noninteractive apt-get install -y \
        gnupg2 wget lsb-release procps nginx supervisor ca-certificates && \
    if [ "$TARGETARCH" = "amd64" ]; then \
        echo "Installing MySQL for AMD64..." && \
        wget -qO /usr/share/keyrings/mysql.asc https://repo.mysql.com/RPM-GPG-KEY-mysql-2025 && \
        mysql_key_fingerprint="$(gpg --batch --show-keys --with-colons /usr/share/keyrings/mysql.asc | awk -F: '$1 == "fpr" { print $10; exit }')" && \
        test "$mysql_key_fingerprint" = "BCA43417C3B485DD128EC6D4B7B3B788A8D3785C" && \
        gpg --batch --yes --dearmor --output /usr/share/keyrings/mysql.gpg /usr/share/keyrings/mysql.asc && \
        echo "deb [signed-by=/usr/share/keyrings/mysql.gpg] http://repo.mysql.com/apt/debian bookworm mysql-8.0" > /etc/apt/sources.list.d/mysql.list && \
        apt-get update && \
        DEBIAN_FRONTEND=noninteractive apt-get install -y mysql-server mysql-client; \
    else \
        echo "Installing MariaDB for ARM64..." && \
        DEBIAN_FRONTEND=noninteractive apt-get install -y mariadb-server mariadb-client; \
    fi && \
    apt-get clean

ENV TZ=Asia/Shanghai \
    SERVER_PORT=8888
WORKDIR /app
RUN mkdir -p /var/lib/mysql /var/log/mysql /var/run/mysqld /var/log/supervisor \
    && mkdir -p /app/storage/{cache,certs,configs,exports,logs,temp,uploads} \
    && mkdir -p /etc/mysql/conf.d

COPY --from=backend-builder /app/server/main ./main
COPY --from=backend-builder /app/server/config.yaml ./config.yaml.default
RUN if [ ! -f /app/config.yaml ]; then mv /app/config.yaml.default /app/config.yaml; else rm /app/config.yaml.default; fi
COPY --from=frontend-builder /app/web/dist /var/www/html

RUN mkdir -p /var/run/mysqld && \
    chown -R mysql:mysql /var/lib/mysql /var/log/mysql /var/run/mysqld && \
    chown -R www-data:www-data /var/www/html && \
    chmod -R 755 /var/www/html && \
    chmod 755 /app/main && \
    chmod 666 /app/config.yaml && \
    chmod 750 /app/storage && \
    chmod -R 750 /app/storage/*

# Create a MySQL/MariaDB-compatible database configuration.
RUN echo '[mysqld]' > /etc/mysql/conf.d/custom.cnf && \
    echo 'datadir=/var/lib/mysql' >> /etc/mysql/conf.d/custom.cnf && \
    echo 'socket=/var/run/mysqld/mysqld.sock' >> /etc/mysql/conf.d/custom.cnf && \
    echo 'user=mysql' >> /etc/mysql/conf.d/custom.cnf && \
    echo 'pid-file=/var/run/mysqld/mysqld.pid' >> /etc/mysql/conf.d/custom.cnf && \
    echo 'bind-address=0.0.0.0' >> /etc/mysql/conf.d/custom.cnf && \
    echo 'port=3306' >> /etc/mysql/conf.d/custom.cnf && \
    echo 'character-set-server=utf8mb4' >> /etc/mysql/conf.d/custom.cnf && \
    echo 'collation-server=utf8mb4_unicode_ci' >> /etc/mysql/conf.d/custom.cnf && \
    echo 'max_connections=200' >> /etc/mysql/conf.d/custom.cnf && \
    echo 'skip-name-resolve' >> /etc/mysql/conf.d/custom.cnf && \
    echo 'secure-file-priv=""' >> /etc/mysql/conf.d/custom.cnf && \
    echo 'innodb_buffer_pool_size=256M' >> /etc/mysql/conf.d/custom.cnf && \
    echo 'innodb_log_file_size=64M' >> /etc/mysql/conf.d/custom.cnf && \
    echo 'loose-binlog_expire_logs_seconds=259200' >> /etc/mysql/conf.d/custom.cnf && \
    echo 'loose-expire_logs_days=3' >> /etc/mysql/conf.d/custom.cnf && \
    echo 'max_binlog_size=256M' >> /etc/mysql/conf.d/custom.cnf && \
    echo 'innodb_force_recovery=0' >> /etc/mysql/conf.d/custom.cnf

RUN echo 'user www-data;' > /etc/nginx/nginx.conf && \
    echo 'worker_processes auto;' >> /etc/nginx/nginx.conf && \
    echo 'error_log /dev/stderr;' >> /etc/nginx/nginx.conf && \
    echo 'pid /run/nginx.pid;' >> /etc/nginx/nginx.conf && \
    echo 'events { worker_connections 1024; }' >> /etc/nginx/nginx.conf && \
    echo 'http {' >> /etc/nginx/nginx.conf && \
    echo '    include /etc/nginx/mime.types;' >> /etc/nginx/nginx.conf && \
    echo '    default_type application/octet-stream;' >> /etc/nginx/nginx.conf && \
    echo '    access_log /dev/stdout;' >> /etc/nginx/nginx.conf && \
    echo '    sendfile on;' >> /etc/nginx/nginx.conf && \
    echo '    keepalive_timeout 65;' >> /etc/nginx/nginx.conf && \
    echo '    gzip on;' >> /etc/nginx/nginx.conf && \
    echo '    ' >> /etc/nginx/nginx.conf && \
    echo '    # WebSocket upgrade mapping' >> /etc/nginx/nginx.conf && \
    echo '    map $http_upgrade $connection_upgrade {' >> /etc/nginx/nginx.conf && \
    echo '        default upgrade;' >> /etc/nginx/nginx.conf && \
    echo '        '"''"' close;' >> /etc/nginx/nginx.conf && \
    echo '    }' >> /etc/nginx/nginx.conf && \
    echo '    ' >> /etc/nginx/nginx.conf && \
    echo '    server {' >> /etc/nginx/nginx.conf && \
    echo '        listen 80;' >> /etc/nginx/nginx.conf && \
    echo '        server_name localhost;' >> /etc/nginx/nginx.conf && \
    echo '        root /var/www/html;' >> /etc/nginx/nginx.conf && \
    echo '        index index.html;' >> /etc/nginx/nginx.conf && \
    echo '        client_max_body_size 10M;' >> /etc/nginx/nginx.conf && \
    echo '        ' >> /etc/nginx/nginx.conf && \
    echo '        location /api/ {' >> /etc/nginx/nginx.conf && \
    echo '            proxy_pass http://127.0.0.1:8888;' >> /etc/nginx/nginx.conf && \
    echo '            proxy_set_header Host $http_host;' >> /etc/nginx/nginx.conf && \
    echo '            proxy_set_header X-Real-IP $remote_addr;' >> /etc/nginx/nginx.conf && \
    echo '            proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;' >> /etc/nginx/nginx.conf && \
    echo '            proxy_set_header REMOTE-HOST $remote_addr;' >> /etc/nginx/nginx.conf && \
    echo '            proxy_set_header X-Forwarded-Host $http_host;' >> /etc/nginx/nginx.conf && \
    echo '            proxy_set_header X-Forwarded-Proto $scheme;' >> /etc/nginx/nginx.conf && \
    echo '            proxy_set_header X-Forwarded-Port $server_port;' >> /etc/nginx/nginx.conf && \
    echo '            ' >> /etc/nginx/nginx.conf && \
    echo '            # WebSocket support' >> /etc/nginx/nginx.conf && \
    echo '            proxy_set_header Upgrade $http_upgrade;' >> /etc/nginx/nginx.conf && \
    echo '            proxy_set_header Connection $connection_upgrade;' >> /etc/nginx/nginx.conf && \
    echo '            proxy_http_version 1.1;' >> /etc/nginx/nginx.conf && \
    echo '            ' >> /etc/nginx/nginx.conf && \
    echo '            # SSL settings' >> /etc/nginx/nginx.conf && \
    echo '            proxy_ssl_server_name off;' >> /etc/nginx/nginx.conf && \
    echo '            proxy_ssl_name $proxy_host;' >> /etc/nginx/nginx.conf && \
    echo '            ' >> /etc/nginx/nginx.conf && \
    echo '            # Timeout settings for SSH connections' >> /etc/nginx/nginx.conf && \
    echo '            proxy_connect_timeout 60s;' >> /etc/nginx/nginx.conf && \
    echo '            proxy_send_timeout 600s;' >> /etc/nginx/nginx.conf && \
    echo '            proxy_read_timeout 600s;' >> /etc/nginx/nginx.conf && \
    echo '            ' >> /etc/nginx/nginx.conf && \
    echo '            # Disable buffering for real-time data' >> /etc/nginx/nginx.conf && \
    echo '            proxy_buffering off;' >> /etc/nginx/nginx.conf && \
    echo '            add_header X-Cache $upstream_cache_status;' >> /etc/nginx/nginx.conf && \
    echo '            add_header Cache-Control no-cache;' >> /etc/nginx/nginx.conf && \
    echo '        }' >> /etc/nginx/nginx.conf && \
    echo '        ' >> /etc/nginx/nginx.conf && \
    echo '        location /swagger/ {' >> /etc/nginx/nginx.conf && \
    echo '            proxy_pass http://127.0.0.1:8888;' >> /etc/nginx/nginx.conf && \
    echo '            proxy_set_header Host $http_host;' >> /etc/nginx/nginx.conf && \
    echo '            proxy_set_header X-Real-IP $remote_addr;' >> /etc/nginx/nginx.conf && \
    echo '            proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;' >> /etc/nginx/nginx.conf && \
    echo '            proxy_set_header X-Forwarded-Host $http_host;' >> /etc/nginx/nginx.conf && \
    echo '            proxy_set_header X-Forwarded-Proto $scheme;' >> /etc/nginx/nginx.conf && \
    echo '            proxy_set_header X-Forwarded-Port $server_port;' >> /etc/nginx/nginx.conf && \
    echo '        }' >> /etc/nginx/nginx.conf && \
    echo '        ' >> /etc/nginx/nginx.conf && \
    echo '        location ~* \.(js|css|png|jpg|jpeg|gif|ico|svg|woff|woff2|ttf|eot)$ {' >> /etc/nginx/nginx.conf && \
    echo '            try_files $uri =404;' >> /etc/nginx/nginx.conf && \
    echo '            add_header Cache-Control "public, max-age=604800, immutable";' >> /etc/nginx/nginx.conf && \
    echo '        }' >> /etc/nginx/nginx.conf && \
    echo '        ' >> /etc/nginx/nginx.conf && \
    echo '        location = /index.html {' >> /etc/nginx/nginx.conf && \
    echo '            try_files $uri =404;' >> /etc/nginx/nginx.conf && \
    echo '            add_header Cache-Control "no-cache, no-store, must-revalidate" always;' >> /etc/nginx/nginx.conf && \
    echo '        }' >> /etc/nginx/nginx.conf && \
    echo '        ' >> /etc/nginx/nginx.conf && \
    echo '        location / {' >> /etc/nginx/nginx.conf && \
    echo '            try_files $uri $uri/ /index.html;' >> /etc/nginx/nginx.conf && \
    echo '        }' >> /etc/nginx/nginx.conf && \
    echo '    }' >> /etc/nginx/nginx.conf && \
    echo '}' >> /etc/nginx/nginx.conf

# Create base supervisor directory
RUN mkdir -p /etc/supervisor/conf.d

# Install the testable architecture-aware runtime lifecycle script.
COPY deploy/all-in-one-entrypoint.sh /start.sh
RUN chmod 755 /start.sh

# Declare volumes for data persistence (optional)
VOLUME ["/var/lib/mysql", "/app/storage"]

EXPOSE 80

HEALTHCHECK --interval=30s --timeout=10s --start-period=60s --retries=3 \
    CMD wget --quiet --tries=1 --timeout=5 -O /dev/null http://127.0.0.1:8888/api/v1/health || exit 1

CMD ["/start.sh"]
