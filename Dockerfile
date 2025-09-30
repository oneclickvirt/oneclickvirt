# OneClickVirt All-in-One Container

FROM node:22-slim AS frontend-builder
WORKDIR /app/web
COPY web/package*.json ./
RUN rm -f package-lock.json && npm install
COPY web/ ./
RUN npm run build


FROM golang:1.24-alpine AS backend-builder
ARG TARGETARCH
WORKDIR /app/server
RUN apk add --no-cache git ca-certificates
COPY server/go.mod server/go.sum ./
RUN go mod download
COPY server/ ./
RUN CGO_ENABLED=0 GOOS=linux GOARCH=${TARGETARCH} go build -a -installsuffix cgo -ldflags "-w -s" -o main .

FROM debian:12-slim

RUN apt-get update && \
    DEBIAN_FRONTEND=noninteractive apt-get install -y \
    gnupg2 wget lsb-release && \
    wget https://dev.mysql.com/get/mysql-apt-config_0.8.29-1_all.deb && \
    DEBIAN_FRONTEND=noninteractive dpkg -i mysql-apt-config_0.8.29-1_all.deb && \
    apt-get update && \
    DEBIAN_FRONTEND=noninteractive apt-get install -y \
    mysql-server mysql-client nginx supervisor bash curl ca-certificates tzdata && \
    rm -f mysql-apt-config_0.8.29-1_all.deb && \
    apt-get clean && \
    rm -rf /var/lib/apt/lists/*

ENV TZ=Asia/Shanghai
WORKDIR /app
RUN mkdir -p /var/lib/mysql /var/log/mysql /var/run/mysqld /var/log/supervisor \
    && mkdir -p /app/storage/{cache,certs,configs,exports,logs,temp,uploads} \
    && mkdir -p /etc/mysql/conf.d

COPY --from=backend-builder /app/server/main ./main
COPY --from=backend-builder /app/server/config.yaml ./config.yaml
COPY --from=frontend-builder /app/web/dist /var/www/html

RUN mkdir -p /var/run/mysqld && \
    chown -R mysql:mysql /var/lib/mysql /var/log/mysql /var/run/mysqld && \
    chown -R www-data:www-data /var/www/html && \
    chmod -R 755 /var/www/html

RUN echo '[mysqld]' > /etc/mysql/conf.d/custom.cnf && \
    echo 'datadir=/var/lib/mysql' >> /etc/mysql/conf.d/custom.cnf && \
    echo 'socket=/var/run/mysqld/mysqld.sock' >> /etc/mysql/conf.d/custom.cnf && \
    echo 'user=mysql' >> /etc/mysql/conf.d/custom.cnf && \
    echo 'pid-file=/var/run/mysqld/mysqld.pid' >> /etc/mysql/conf.d/custom.cnf && \
    echo 'bind-address=0.0.0.0' >> /etc/mysql/conf.d/custom.cnf && \
    echo 'port=3306' >> /etc/mysql/conf.d/custom.cnf && \
    echo 'character-set-server=utf8mb4' >> /etc/mysql/conf.d/custom.cnf && \
    echo 'collation-server=utf8mb4_unicode_ci' >> /etc/mysql/conf.d/custom.cnf && \
    echo 'authentication_policy=mysql_native_password' >> /etc/mysql/conf.d/custom.cnf && \
    echo 'max_connections=100' >> /etc/mysql/conf.d/custom.cnf && \
    echo 'skip-name-resolve' >> /etc/mysql/conf.d/custom.cnf && \
    echo 'secure-file-priv=""' >> /etc/mysql/conf.d/custom.cnf && \
    echo 'innodb_buffer_pool_size=128M' >> /etc/mysql/conf.d/custom.cnf && \
    echo 'innodb_redo_log_capacity=67108864' >> /etc/mysql/conf.d/custom.cnf && \
    echo 'innodb_force_recovery=0' >> /etc/mysql/conf.d/custom.cnf

RUN echo 'user www-data;' > /etc/nginx/nginx.conf && \
    echo 'worker_processes auto;' >> /etc/nginx/nginx.conf && \
    echo 'error_log /var/log/nginx/error.log;' >> /etc/nginx/nginx.conf && \
    echo 'pid /run/nginx.pid;' >> /etc/nginx/nginx.conf && \
    echo 'events { worker_connections 1024; }' >> /etc/nginx/nginx.conf && \
    echo 'http {' >> /etc/nginx/nginx.conf && \
    echo '    include /etc/nginx/mime.types;' >> /etc/nginx/nginx.conf && \
    echo '    default_type application/octet-stream;' >> /etc/nginx/nginx.conf && \
    echo '    sendfile on;' >> /etc/nginx/nginx.conf && \
    echo '    keepalive_timeout 65;' >> /etc/nginx/nginx.conf && \
    echo '    gzip on;' >> /etc/nginx/nginx.conf && \
    echo '    server {' >> /etc/nginx/nginx.conf && \
    echo '        listen 80;' >> /etc/nginx/nginx.conf && \
    echo '        server_name localhost;' >> /etc/nginx/nginx.conf && \
    echo '        root /var/www/html;' >> /etc/nginx/nginx.conf && \
    echo '        index index.html;' >> /etc/nginx/nginx.conf && \
    echo '        client_max_body_size 10M;' >> /etc/nginx/nginx.conf && \
    echo '        ' >> /etc/nginx/nginx.conf && \
    echo '        location /api/ {' >> /etc/nginx/nginx.conf && \
    echo '            proxy_pass http://127.0.0.1:8888;' >> /etc/nginx/nginx.conf && \
    echo '            proxy_set_header Host $host;' >> /etc/nginx/nginx.conf && \
    echo '            proxy_set_header X-Real-IP $remote_addr;' >> /etc/nginx/nginx.conf && \
    echo '            proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;' >> /etc/nginx/nginx.conf && \
    echo '        }' >> /etc/nginx/nginx.conf && \
    echo '        ' >> /etc/nginx/nginx.conf && \
    echo '        location /swagger/ {' >> /etc/nginx/nginx.conf && \
    echo '            proxy_pass http://127.0.0.1:8888;' >> /etc/nginx/nginx.conf && \
    echo '            proxy_set_header Host $host;' >> /etc/nginx/nginx.conf && \
    echo '            proxy_set_header X-Real-IP $remote_addr;' >> /etc/nginx/nginx.conf && \
    echo '        }' >> /etc/nginx/nginx.conf && \
    echo '        ' >> /etc/nginx/nginx.conf && \
    echo '        location / {' >> /etc/nginx/nginx.conf && \
    echo '            try_files $uri $uri/ /index.html;' >> /etc/nginx/nginx.conf && \
    echo '        }' >> /etc/nginx/nginx.conf && \
    echo '    }' >> /etc/nginx/nginx.conf && \
    echo '}' >> /etc/nginx/nginx.conf

RUN mkdir -p /etc/supervisor/conf.d && \
    echo '[supervisord]' > /etc/supervisor/conf.d/supervisord.conf && \
    echo 'nodaemon=true' >> /etc/supervisor/conf.d/supervisord.conf && \
    echo 'user=root' >> /etc/supervisor/conf.d/supervisord.conf && \
    echo '' >> /etc/supervisor/conf.d/supervisord.conf && \
    echo '[program:mysql]' >> /etc/supervisor/conf.d/supervisord.conf && \
    echo 'command=/usr/sbin/mysqld --defaults-file=/etc/mysql/conf.d/custom.cnf' >> /etc/supervisor/conf.d/supervisord.conf && \
    echo 'autostart=true' >> /etc/supervisor/conf.d/supervisord.conf && \
    echo 'autorestart=true' >> /etc/supervisor/conf.d/supervisord.conf && \
    echo 'user=mysql' >> /etc/supervisor/conf.d/supervisord.conf && \
    echo 'priority=1' >> /etc/supervisor/conf.d/supervisord.conf && \
    echo 'stdout_logfile=/var/log/supervisor/mysql.log' >> /etc/supervisor/conf.d/supervisord.conf && \
    echo 'stderr_logfile=/var/log/supervisor/mysql_error.log' >> /etc/supervisor/conf.d/supervisord.conf && \
    echo 'stdout_logfile_maxbytes=10MB' >> /etc/supervisor/conf.d/supervisord.conf && \
    echo 'stderr_logfile_maxbytes=10MB' >> /etc/supervisor/conf.d/supervisord.conf && \
    echo 'startsecs=10' >> /etc/supervisor/conf.d/supervisord.conf && \
    echo 'startretries=3' >> /etc/supervisor/conf.d/supervisord.conf && \
    echo '' >> /etc/supervisor/conf.d/supervisord.conf && \
    echo '[program:app]' >> /etc/supervisor/conf.d/supervisord.conf && \
    echo 'command=/app/main' >> /etc/supervisor/conf.d/supervisord.conf && \
    echo 'directory=/app' >> /etc/supervisor/conf.d/supervisord.conf && \
    echo 'autostart=true' >> /etc/supervisor/conf.d/supervisord.conf && \
    echo 'autorestart=true' >> /etc/supervisor/conf.d/supervisord.conf && \
    echo 'user=root' >> /etc/supervisor/conf.d/supervisord.conf && \
    echo 'priority=2' >> /etc/supervisor/conf.d/supervisord.conf && \
    echo 'environment=DB_HOST="127.0.0.1",DB_PORT="3306"' >> /etc/supervisor/conf.d/supervisord.conf && \
    echo '' >> /etc/supervisor/conf.d/supervisord.conf && \
    echo '[program:nginx]' >> /etc/supervisor/conf.d/supervisord.conf && \
    echo 'command=/usr/sbin/nginx -g "daemon off;"' >> /etc/supervisor/conf.d/supervisord.conf && \
    echo 'autostart=true' >> /etc/supervisor/conf.d/supervisord.conf && \
    echo 'autorestart=true' >> /etc/supervisor/conf.d/supervisord.conf && \
    echo 'user=root' >> /etc/supervisor/conf.d/supervisord.conf && \
    echo 'priority=3' >> /etc/supervisor/conf.d/supervisord.conf

RUN echo '#!/bin/bash' > /start.sh && \
    echo 'set -e' >> /start.sh && \
    echo 'echo "Starting OneClickVirt..."' >> /start.sh && \
    echo '' >> /start.sh && \
    echo 'export MYSQL_DATABASE=${MYSQL_DATABASE:-oneclickvirt}' >> /start.sh && \
    echo '' >> /start.sh && \
    echo 'chown -R mysql:mysql /var/lib/mysql /var/run/mysqld /var/log/mysql' >> /start.sh && \
    echo 'chmod 755 /var/run/mysqld' >> /start.sh && \
    echo '' >> /start.sh && \
    echo 'INIT_NEEDED=false' >> /start.sh && \
    echo 'if [ ! -d "/var/lib/mysql/mysql" ] || [ -f "/var/lib/mysql/mariadb_upgrade_info" ] || [ -f "/var/lib/mysql/aria_log.00000001" ]; then' >> /start.sh && \
    echo '    echo "Cleaning and initializing MySQL database..."' >> /start.sh && \
    echo '    INIT_NEEDED=true' >> /start.sh && \
    echo '    # Stop any running MySQL processes' >> /start.sh && \
    echo '    pkill -f mysqld || true' >> /start.sh && \
    echo '    sleep 2' >> /start.sh && \
    echo '    # Remove old/corrupted data' >> /start.sh && \
    echo '    rm -rf /var/lib/mysql/*' >> /start.sh && \
    echo '    # Initialize fresh MySQL 8.0 database' >> /start.sh && \
    echo '    mysqld --initialize-insecure --user=mysql --datadir=/var/lib/mysql --skip-name-resolve' >> /start.sh && \
    echo '    if [ $? -ne 0 ]; then' >> /start.sh && \
    echo '        echo "MySQL initialization failed"' >> /start.sh && \
    echo '        exit 1' >> /start.sh && \
    echo '    fi' >> /start.sh && \
    echo 'else' >> /start.sh && \
    echo '    echo "MySQL database exists, checking configuration..."' >> /start.sh && \
    echo 'fi' >> /start.sh && \
    echo '' >> /start.sh && \
    echo 'echo "Configuring MySQL users and permissions..."' >> /start.sh && \
    echo 'pkill -f mysqld || true' >> /start.sh && \
    echo 'sleep 2' >> /start.sh && \
    echo '' >> /start.sh && \
    echo '# Start temporary MySQL server for configuration' >> /start.sh && \
    echo 'echo "Starting temporary MySQL server for configuration..."' >> /start.sh && \
    echo 'mysqld --user=mysql --skip-networking --skip-grant-tables --socket=/var/run/mysqld/mysqld.sock --pid-file=/var/run/mysqld/mysqld.pid --log-error=/var/log/mysql/error.log &' >> /start.sh && \
    echo 'mysql_pid=$!' >> /start.sh && \
    echo '' >> /start.sh && \
    echo 'for i in {1..30}; do' >> /start.sh && \
    echo '    if mysql --socket=/var/run/mysqld/mysqld.sock -e "SELECT 1" >/dev/null 2>&1; then' >> /start.sh && \
    echo '        echo "MySQL started successfully"' >> /start.sh && \
    echo '        break' >> /start.sh && \
    echo '    fi' >> /start.sh && \
    echo '    echo "Waiting for MySQL to start... ($i/30)"' >> /start.sh && \
    echo '    if [ $i -eq 30 ]; then' >> /start.sh && \
    echo '        echo "MySQL failed to start"' >> /start.sh && \
    echo '        kill $mysql_pid 2>/dev/null || true' >> /start.sh && \
    echo '        exit 1' >> /start.sh && \
    echo '    fi' >> /start.sh && \
    echo '    sleep 1' >> /start.sh && \
    echo 'done' >> /start.sh && \
    echo '' >> /start.sh && \
    echo 'echo "Configuring MySQL users and database..."' >> /start.sh && \
    echo 'mysql --socket=/var/run/mysqld/mysqld.sock <<SQLEND' >> /start.sh && \
    echo 'FLUSH PRIVILEGES;' >> /start.sh && \
    echo "ALTER USER 'root'@'localhost' IDENTIFIED WITH mysql_native_password BY '';" >> /start.sh && \
    echo "DROP USER IF EXISTS 'root'@'127.0.0.1';" >> /start.sh && \
    echo "DROP USER IF EXISTS 'root'@'%';" >> /start.sh && \
    echo "CREATE USER 'root'@'127.0.0.1' IDENTIFIED WITH mysql_native_password BY '';" >> /start.sh && \
    echo "CREATE USER 'root'@'%' IDENTIFIED WITH mysql_native_password BY '';" >> /start.sh && \
    echo "GRANT ALL PRIVILEGES ON *.* TO 'root'@'localhost' WITH GRANT OPTION;" >> /start.sh && \
    echo "GRANT ALL PRIVILEGES ON *.* TO 'root'@'127.0.0.1' WITH GRANT OPTION;" >> /start.sh && \
    echo "GRANT ALL PRIVILEGES ON *.* TO 'root'@'%' WITH GRANT OPTION;" >> /start.sh && \
    echo "CREATE DATABASE IF NOT EXISTS \\\`\${MYSQL_DATABASE}\\\` CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci;" >> /start.sh && \
    echo 'FLUSH PRIVILEGES;' >> /start.sh && \
    echo 'SQLEND' >> /start.sh && \
    echo '' >> /start.sh && \
    echo 'kill $mysql_pid' >> /start.sh && \
    echo 'wait $mysql_pid 2>/dev/null || true' >> /start.sh && \
    echo 'echo "MySQL configuration completed."' >> /start.sh && \
    echo '' >> /start.sh && \
    echo 'export DB_HOST="127.0.0.1"' >> /start.sh && \
    echo 'export DB_PORT="3306"' >> /start.sh && \
    echo 'export DB_NAME="$MYSQL_DATABASE"' >> /start.sh && \
    echo 'export DB_USER="root"' >> /start.sh && \
    echo 'export DB_PASSWORD=""' >> /start.sh && \
    echo '' >> /start.sh && \
    echo 'echo "Starting services..."' >> /start.sh && \
    echo 'exec supervisord -c /etc/supervisor/conf.d/supervisord.conf' >> /start.sh && \
    chmod +x /start.sh

EXPOSE 80

HEALTHCHECK --interval=30s --timeout=10s --start-period=60s --retries=3 \
    CMD wget --quiet --tries=1 --spider http://localhost/api/v1/health || exit 1

CMD ["/start.sh"]