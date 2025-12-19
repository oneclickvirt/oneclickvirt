# OneClickVirt All-in-One Container

FROM node:22-slim AS frontend-builder
ARG TARGETARCH
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


FROM golang:1.24-alpine AS backend-builder
ARG TARGETARCH
WORKDIR /app/server
RUN apk add --no-cache git ca-certificates
COPY server/ ./
RUN go mod download
RUN CGO_ENABLED=0 GOOS=linux GOARCH=${TARGETARCH} go build -a -installsuffix cgo -ldflags "-w -s" -o main .

FROM debian:12-slim
ARG TARGETARCH

# Install database and other services based on architecture
RUN apt-get update && \
    DEBIAN_FRONTEND=noninteractive apt-get install -y \
        gnupg2 wget lsb-release procps nginx supervisor ca-certificates && \
    if [ "$TARGETARCH" = "amd64" ]; then \
        echo "Installing MySQL for AMD64..." && \
        gpg --keyserver keyserver.ubuntu.com --recv-keys B7B3B788A8D3785C && \
        gpg --export B7B3B788A8D3785C > /usr/share/keyrings/mysql.gpg && \
        echo "deb [signed-by=/usr/share/keyrings/mysql.gpg] http://repo.mysql.com/apt/debian bookworm mysql-8.0" > /etc/apt/sources.list.d/mysql.list && \
        apt-get update && \
        DEBIAN_FRONTEND=noninteractive apt-get install -y mysql-server mysql-client; \
    else \
        echo "Installing MariaDB for ARM64..." && \
        DEBIAN_FRONTEND=noninteractive apt-get install -y mariadb-server mariadb-client; \
    fi && \
    apt-get clean

ENV TZ=Asia/Shanghai
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

# Create database configuration based on architecture
RUN if [ "$TARGETARCH" = "amd64" ]; then \
        echo '[mysqld]' > /etc/mysql/conf.d/custom.cnf && \
        echo 'datadir=/var/lib/mysql' >> /etc/mysql/conf.d/custom.cnf && \
        echo 'socket=/var/run/mysqld/mysqld.sock' >> /etc/mysql/conf.d/custom.cnf && \
        echo 'user=mysql' >> /etc/mysql/conf.d/custom.cnf && \
        echo 'pid-file=/var/run/mysqld/mysqld.pid' >> /etc/mysql/conf.d/custom.cnf && \
        echo 'bind-address=0.0.0.0' >> /etc/mysql/conf.d/custom.cnf && \
        echo 'port=3306' >> /etc/mysql/conf.d/custom.cnf && \
        echo 'character-set-server=utf8mb4' >> /etc/mysql/conf.d/custom.cnf && \
        echo 'collation-server=utf8mb4_unicode_ci' >> /etc/mysql/conf.d/custom.cnf && \
        echo 'authentication_policy=mysql_native_password' >> /etc/mysql/conf.d/custom.cnf && \
        echo 'max_connections=200' >> /etc/mysql/conf.d/custom.cnf && \
        echo 'skip-name-resolve' >> /etc/mysql/conf.d/custom.cnf && \
        echo 'secure-file-priv=""' >> /etc/mysql/conf.d/custom.cnf && \
        echo 'innodb_buffer_pool_size=256M' >> /etc/mysql/conf.d/custom.cnf && \
        echo 'innodb_redo_log_capacity=67108864' >> /etc/mysql/conf.d/custom.cnf && \
        echo 'innodb_force_recovery=0' >> /etc/mysql/conf.d/custom.cnf; \
    else \
        echo '[mysqld]' > /etc/mysql/conf.d/custom.cnf && \
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
        echo 'innodb_force_recovery=0' >> /etc/mysql/conf.d/custom.cnf; \
    fi

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
    echo '            proxy_set_header Host $host;' >> /etc/nginx/nginx.conf && \
    echo '            proxy_set_header X-Real-IP $remote_addr;' >> /etc/nginx/nginx.conf && \
    echo '            proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;' >> /etc/nginx/nginx.conf && \
    echo '            proxy_set_header REMOTE-HOST $remote_addr;' >> /etc/nginx/nginx.conf && \
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
    echo '            proxy_set_header Host $host;' >> /etc/nginx/nginx.conf && \
    echo '            proxy_set_header X-Real-IP $remote_addr;' >> /etc/nginx/nginx.conf && \
    echo '            proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;' >> /etc/nginx/nginx.conf && \
    echo '        }' >> /etc/nginx/nginx.conf && \
    echo '        ' >> /etc/nginx/nginx.conf && \
    echo '        location / {' >> /etc/nginx/nginx.conf && \
    echo '            try_files $uri $uri/ /index.html;' >> /etc/nginx/nginx.conf && \
    echo '        }' >> /etc/nginx/nginx.conf && \
    echo '    }' >> /etc/nginx/nginx.conf && \
    echo '}' >> /etc/nginx/nginx.conf

# Create base supervisor directory
RUN mkdir -p /etc/supervisor/conf.d

# Create architecture-aware startup script
RUN echo '#!/bin/bash' > /start.sh && \
    echo 'set -e' >> /start.sh && \
    echo 'echo "Starting OneClickVirt..."' >> /start.sh && \
    echo '' >> /start.sh && \
    echo 'export MYSQL_DATABASE=${MYSQL_DATABASE:-oneclickvirt}' >> /start.sh && \
    echo '' >> /start.sh && \
    echo '# Update config.yaml with FRONTEND_URL if provided' >> /start.sh && \
    echo 'if [ ! -z "$FRONTEND_URL" ]; then' >> /start.sh && \
    echo '    echo "Configuring frontend-url: $FRONTEND_URL"' >> /start.sh && \
    echo '    sed -i "s|frontend-url:.*|frontend-url: \"$FRONTEND_URL\"|g" /app/config.yaml' >> /start.sh && \
    echo '    ' >> /start.sh && \
    echo '    # Detect if URL is HTTPS and update nginx config accordingly' >> /start.sh && \
    echo '    if echo "$FRONTEND_URL" | grep -q "^https://"; then' >> /start.sh && \
    echo '        echo "Detected HTTPS frontend, updating nginx proxy headers..."' >> /start.sh && \
    echo '        sed -i "/proxy_set_header X-Forwarded-For/a\            proxy_set_header X-Forwarded-Proto https;" /etc/nginx/nginx.conf' >> /start.sh && \
    echo '        sed -i "/proxy_set_header X-Forwarded-For/a\            proxy_set_header X-Forwarded-Ssl on;" /etc/nginx/nginx.conf' >> /start.sh && \
    echo '    else' >> /start.sh && \
    echo '        echo "Detected HTTP frontend, using default nginx config"' >> /start.sh && \
    echo '    fi' >> /start.sh && \
    echo 'fi' >> /start.sh && \
    echo '' >> /start.sh && \
    echo '# Detect architecture and set database type' >> /start.sh && \
    echo 'ARCH=$(uname -m)' >> /start.sh && \
    echo 'if [ "$ARCH" = "x86_64" ]; then' >> /start.sh && \
    echo '    DB_TYPE="mysql"' >> /start.sh && \
    echo '    DB_DAEMON="mysqld"' >> /start.sh && \
    echo 'else' >> /start.sh && \
    echo '    DB_TYPE="mariadb"' >> /start.sh && \
    echo '    DB_DAEMON="mariadbd"' >> /start.sh && \
    echo 'fi' >> /start.sh && \
    echo 'echo "Detected architecture: $ARCH, using database: $DB_TYPE"' >> /start.sh && \
    echo '' >> /start.sh && \
    echo 'chown -R mysql:mysql /var/lib/mysql /var/run/mysqld /var/log/mysql' >> /start.sh && \
    echo 'chmod 755 /var/run/mysqld' >> /start.sh && \
    echo '' >> /start.sh && \
    echo '# Check if database needs initialization' >> /start.sh && \
    echo 'INIT_NEEDED=false' >> /start.sh && \
    echo '# Create database initialization flag file path (different from business init)' >> /start.sh && \
    echo 'DB_INIT_FLAG="/var/lib/mysql/.mysql_initialized"' >> /start.sh && \
    echo '' >> /start.sh && \
    echo '# Check various conditions for initialization' >> /start.sh && \
    echo 'if [ ! -f "$DB_INIT_FLAG" ]; then' >> /start.sh && \
    echo '    echo "Database initialization flag not found - database needs initialization"' >> /start.sh && \
    echo '    INIT_NEEDED=true' >> /start.sh && \
    echo 'elif [ ! -d "/var/lib/mysql/mysql" ]; then' >> /start.sh && \
    echo '    echo "Database system directory not found - reinitializing database..."' >> /start.sh && \
    echo '    INIT_NEEDED=true' >> /start.sh && \
    echo 'elif [ "$(ls -A /var/lib/mysql 2>/dev/null | wc -l)" -eq 0 ]; then' >> /start.sh && \
    echo '    echo "Database directory is empty - reinitializing database..."' >> /start.sh && \
    echo '    INIT_NEEDED=true' >> /start.sh && \
    echo 'else' >> /start.sh && \
    echo '    echo "Database already initialized (flag exists and data present), skipping initialization..."' >> /start.sh && \
    echo 'fi' >> /start.sh && \
    echo '' >> /start.sh && \
    echo 'if [ "$INIT_NEEDED" = "true" ]; then' >> /start.sh && \
    echo '    # Stop any running database processes' >> /start.sh && \
    echo '    pkill -f "$DB_DAEMON" || true' >> /start.sh && \
    echo '    sleep 2' >> /start.sh && \
    echo '    # Remove old/corrupted data only when needed' >> /start.sh && \
    echo '    rm -rf /var/lib/mysql/*' >> /start.sh && \
    echo '    # Initialize database based on type' >> /start.sh && \
    echo '    if [ "$DB_TYPE" = "mysql" ]; then' >> /start.sh && \
    echo '        mysqld --initialize-insecure --user=mysql --datadir=/var/lib/mysql --skip-name-resolve' >> /start.sh && \
    echo '    else' >> /start.sh && \
    echo '        mariadb-install-db --user=mysql --datadir=/var/lib/mysql --skip-name-resolve' >> /start.sh && \
    echo '    fi' >> /start.sh && \
    echo '    if [ $? -ne 0 ]; then' >> /start.sh && \
    echo '        echo "$DB_TYPE initialization failed"' >> /start.sh && \
    echo '        exit 1' >> /start.sh && \
    echo '    fi' >> /start.sh && \
    echo 'fi' >> /start.sh && \
    echo '' >> /start.sh && \
    echo '# Configure database users and permissions only if initialization was needed' >> /start.sh && \
    echo 'if [ "$INIT_NEEDED" = "true" ]; then' >> /start.sh && \
    echo '    echo "Configuring $DB_TYPE users and permissions..."' >> /start.sh && \
    echo '    pkill -f "$DB_DAEMON" || true' >> /start.sh && \
    echo '    sleep 2' >> /start.sh && \
    echo '    ' >> /start.sh && \
    echo '    # Start temporary database server for configuration' >> /start.sh && \
    echo '    echo "Starting temporary $DB_TYPE server for configuration..."' >> /start.sh && \
    echo '    $DB_DAEMON --user=mysql --skip-networking --skip-grant-tables --socket=/var/run/mysqld/mysqld.sock --pid-file=/var/run/mysqld/mysqld.pid --log-error=/var/log/mysql/error.log &' >> /start.sh && \
    echo '    mysql_pid=$!' >> /start.sh && \
    echo '' >> /start.sh && \
    echo 'for i in {1..30}; do' >> /start.sh && \
    echo '    if mysql --socket=/var/run/mysqld/mysqld.sock -e "SELECT 1" >/dev/null 2>&1; then' >> /start.sh && \
    echo '        echo "$DB_TYPE started successfully"' >> /start.sh && \
    echo '        break' >> /start.sh && \
    echo '    fi' >> /start.sh && \
    echo '    echo "Waiting for $DB_TYPE to start... ($i/30)"' >> /start.sh && \
    echo '    if [ $i -eq 30 ]; then' >> /start.sh && \
    echo '        echo "$DB_TYPE failed to start"' >> /start.sh && \
    echo '        kill $mysql_pid 2>/dev/null || true' >> /start.sh && \
    echo '        exit 1' >> /start.sh && \
    echo '    fi' >> /start.sh && \
    echo '    sleep 1' >> /start.sh && \
    echo '    done' >> /start.sh && \
    echo '    ' >> /start.sh && \
    echo '    echo "Configuring $DB_TYPE users and database..."' >> /start.sh && \
    echo '    if [ "$DB_TYPE" = "mysql" ]; then' >> /start.sh && \
    echo '        mysql --socket=/var/run/mysqld/mysqld.sock <<SQLEND' >> /start.sh && \
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
    echo '    else' >> /start.sh && \
    echo '        mysql --socket=/var/run/mysqld/mysqld.sock <<SQLEND' >> /start.sh && \
    echo 'FLUSH PRIVILEGES;' >> /start.sh && \
    echo "SET PASSWORD FOR 'root'@'localhost' = PASSWORD('');" >> /start.sh && \
    echo "GRANT ALL PRIVILEGES ON *.* TO 'root'@'localhost' WITH GRANT OPTION;" >> /start.sh && \
    echo "CREATE USER IF NOT EXISTS 'root'@'127.0.0.1' IDENTIFIED BY '';" >> /start.sh && \
    echo "GRANT ALL PRIVILEGES ON *.* TO 'root'@'127.0.0.1' WITH GRANT OPTION;" >> /start.sh && \
    echo "CREATE USER IF NOT EXISTS 'root'@'%' IDENTIFIED BY '';" >> /start.sh && \
    echo "GRANT ALL PRIVILEGES ON *.* TO 'root'@'%' WITH GRANT OPTION;" >> /start.sh && \
    echo "CREATE DATABASE IF NOT EXISTS \\\`\${MYSQL_DATABASE}\\\` CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci;" >> /start.sh && \
    echo 'FLUSH PRIVILEGES;' >> /start.sh && \
    echo 'SQLEND' >> /start.sh && \
    echo '    fi' >> /start.sh && \
    echo '' >> /start.sh && \
    echo '    kill $mysql_pid' >> /start.sh && \
    echo '    wait $mysql_pid 2>/dev/null || true' >> /start.sh && \
    echo '    echo "$DB_TYPE configuration completed."' >> /start.sh && \
    echo '    # Create database initialization flag to prevent re-initialization' >> /start.sh && \
    echo '    echo "$(date): Database initialized successfully with $DB_TYPE" > "$DB_INIT_FLAG"' >> /start.sh && \
    echo '    echo "Created database initialization flag at $DB_INIT_FLAG"' >> /start.sh && \
    echo 'else' >> /start.sh && \
    echo '    echo "Database already configured, skipping user configuration..."' >> /start.sh && \
    echo 'fi' >> /start.sh && \
    echo '' >> /start.sh && \
    echo '# Create supervisor configuration dynamically' >> /start.sh && \
    echo 'echo "Creating supervisor configuration for $DB_TYPE..."' >> /start.sh && \
    echo 'cat > /etc/supervisor/conf.d/supervisord.conf <<SUPEREND' >> /start.sh && \
    echo '[unix_http_server]' >> /start.sh && \
    echo 'file=/var/run/supervisor.sock' >> /start.sh && \
    echo 'chmod=0700' >> /start.sh && \
    echo '' >> /start.sh && \
    echo '[supervisorctl]' >> /start.sh && \
    echo 'serverurl=unix:///var/run/supervisor.sock' >> /start.sh && \
    echo '' >> /start.sh && \
    echo '[rpcinterface:supervisor]' >> /start.sh && \
    echo 'supervisor.rpcinterface_factory = supervisor.rpcinterface:make_main_rpcinterface' >> /start.sh && \
    echo '' >> /start.sh && \
    echo '[supervisord]' >> /start.sh && \
    echo 'nodaemon=true' >> /start.sh && \
    echo 'user=root' >> /start.sh && \
    echo '' >> /start.sh && \
    echo '[program:mysql]' >> /start.sh && \
    echo 'SUPEREND' >> /start.sh && \
    echo '' >> /start.sh && \
    echo 'if [ "$DB_TYPE" = "mysql" ]; then' >> /start.sh && \
    echo '    echo "command=/usr/sbin/mysqld --defaults-file=/etc/mysql/conf.d/custom.cnf --lc-messages=en_US" >> /etc/supervisor/conf.d/supervisord.conf' >> /start.sh && \
    echo 'else' >> /start.sh && \
    echo '    echo "command=/usr/sbin/mariadbd --defaults-file=/etc/mysql/conf.d/custom.cnf" >> /etc/supervisor/conf.d/supervisord.conf' >> /start.sh && \
    echo 'fi' >> /start.sh && \
    echo '' >> /start.sh && \
    echo 'cat >> /etc/supervisor/conf.d/supervisord.conf <<SUPEREND2' >> /start.sh && \
    echo 'autostart=true' >> /start.sh && \
    echo 'autorestart=true' >> /start.sh && \
    echo 'user=mysql' >> /start.sh && \
    echo 'priority=1' >> /start.sh && \
    echo 'stdout_logfile=/var/log/supervisor/mysql.log' >> /start.sh && \
    echo 'stderr_logfile=/var/log/supervisor/mysql_error.log' >> /start.sh && \
    echo 'stdout_logfile_maxbytes=10MB' >> /start.sh && \
    echo 'stderr_logfile_maxbytes=10MB' >> /start.sh && \
    echo 'startsecs=10' >> /start.sh && \
    echo 'startretries=3' >> /start.sh && \
    echo '' >> /start.sh && \
    echo '[program:app]' >> /start.sh && \
    echo 'command=/bin/bash -c "sleep 12 && /app/main"' >> /start.sh && \
    echo 'directory=/app' >> /start.sh && \
    echo 'autostart=true' >> /start.sh && \
    echo 'autorestart=true' >> /start.sh && \
    echo 'user=root' >> /start.sh && \
    echo 'priority=2' >> /start.sh && \
    echo 'environment=DB_HOST="127.0.0.1",DB_PORT="3306",DB_USER="root",DB_PASSWORD="",DB_NAME="oneclickvirt"' >> /start.sh && \
    echo 'startsecs=1' >> /start.sh && \
    echo '' >> /start.sh && \
    echo '[program:nginx]' >> /start.sh && \
    echo 'command=/usr/sbin/nginx -g "daemon off;"' >> /start.sh && \
    echo 'autostart=true' >> /start.sh && \
    echo 'autorestart=true' >> /start.sh && \
    echo 'user=root' >> /start.sh && \
    echo 'priority=3' >> /start.sh && \
    echo 'SUPEREND2' >> /start.sh && \
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

# Declare volumes for data persistence (optional)
VOLUME ["/var/lib/mysql", "/app/storage"]

EXPOSE 80

HEALTHCHECK --interval=30s --timeout=10s --start-period=60s --retries=3 \
    CMD wget --quiet --tries=1 --spider http://localhost/api/v1/health || exit 1

CMD ["/start.sh"]