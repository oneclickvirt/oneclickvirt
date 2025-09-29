# OneClickVirt All-in-One Container
# Single container with MySQL + Application + Nginx

# Stage 1: Build frontend
FROM node:22-slim AS frontend-builder
WORKDIR /app/web
COPY web/package*.json ./
RUN rm -f package-lock.json && npm install
COPY web/ ./
RUN npm run build

# Stage 2: Build backend  
FROM golang:1.24-alpine AS backend-builder
WORKDIR /app/server
RUN apk add --no-cache git ca-certificates
COPY server/go.mod server/go.sum ./
RUN go mod download
COPY server/ ./
RUN CGO_ENABLED=0 GOOS=linux go build -a -installsuffix cgo -o main .

# Stage 3: Final runtime image
FROM alpine:latest

# Install packages
RUN apk --no-cache add mysql mysql-client nginx supervisor bash curl wget ca-certificates tzdata

# Set timezone and create directories
ENV TZ=Asia/Shanghai
WORKDIR /app
RUN mkdir -p /var/lib/mysql /var/log/mysql /run/mysqld /var/log/supervisor \
    && mkdir -p /app/storage/{cache,certs,configs,exports,logs,temp,uploads}

# Copy application files - ensure config.yaml is in same directory as binary
COPY --from=backend-builder /app/server/main ./main
COPY --from=backend-builder /app/server/config.yaml ./config.yaml
COPY --from=frontend-builder /app/web/dist /var/www/html

# Set permissions (mysql user already exists from package installation)
RUN chown -R mysql:mysql /var/lib/mysql /var/log/mysql /run/mysqld \
    && chown -R nginx:nginx /var/www/html \
    && chmod -R 755 /var/www/html

# Configure MySQL
RUN echo '[mysqld]' > /etc/my.cnf && \
    echo 'datadir=/var/lib/mysql' >> /etc/my.cnf && \
    echo 'socket=/run/mysqld/mysqld.sock' >> /etc/my.cnf && \
    echo 'user=mysql' >> /etc/my.cnf && \
    echo 'pid-file=/run/mysqld/mysqld.pid' >> /etc/my.cnf && \
    echo 'bind-address=127.0.0.1' >> /etc/my.cnf && \
    echo 'port=3306' >> /etc/my.cnf && \
    echo 'character-set-server=utf8mb4' >> /etc/my.cnf && \
    echo 'collation-server=utf8mb4_unicode_ci' >> /etc/my.cnf && \
    echo 'default-authentication-plugin=mysql_native_password' >> /etc/my.cnf && \
    echo 'max_connections=100' >> /etc/my.cnf && \
    echo 'skip-networking=0' >> /etc/my.cnf

# Configure Nginx
RUN echo 'user nginx;' > /etc/nginx/nginx.conf && \
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

# Configure Supervisor
RUN mkdir -p /etc/supervisor/conf.d && \
    echo '[supervisord]' > /etc/supervisor/conf.d/supervisord.conf && \
    echo 'nodaemon=true' >> /etc/supervisor/conf.d/supervisord.conf && \
    echo 'user=root' >> /etc/supervisor/conf.d/supervisord.conf && \
    echo '' >> /etc/supervisor/conf.d/supervisord.conf && \
    echo '[program:mysql]' >> /etc/supervisor/conf.d/supervisord.conf && \
    echo 'command=/usr/bin/mysqld --user=mysql --console' >> /etc/supervisor/conf.d/supervisord.conf && \
    echo 'autostart=true' >> /etc/supervisor/conf.d/supervisord.conf && \
    echo 'autorestart=true' >> /etc/supervisor/conf.d/supervisord.conf && \
    echo 'user=mysql' >> /etc/supervisor/conf.d/supervisord.conf && \
    echo 'priority=1' >> /etc/supervisor/conf.d/supervisord.conf && \
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

# Create startup script
RUN echo '#!/bin/bash' > /start.sh && \
    echo 'set -e' >> /start.sh && \
    echo 'echo "Starting OneClickVirt..."' >> /start.sh && \
    echo '' >> /start.sh && \
    echo '# Set environment variables (无密码配置)' >> /start.sh && \
    echo 'export MYSQL_DATABASE=${MYSQL_DATABASE:-oneclickvirt}' >> /start.sh && \
    echo '' >> /start.sh && \
    echo '# Initialize MySQL database if needed' >> /start.sh && \
    echo 'if [ ! -d "/var/lib/mysql/mysql" ]; then' >> /start.sh && \
    echo '    echo "Initializing MySQL database..."' >> /start.sh && \
    echo '    mysql_install_db --user=mysql --datadir=/var/lib/mysql' >> /start.sh && \
    echo '    ' >> /start.sh && \
    echo '    mysqld --user=mysql --skip-networking --socket=/tmp/mysql_init.sock &' >> /start.sh && \
    echo '    mysql_pid=$!' >> /start.sh && \
    echo '    ' >> /start.sh && \
    echo '    for i in {1..30}; do' >> /start.sh && \
    echo '        if mysql --socket=/tmp/mysql_init.sock -e "SELECT 1" >/dev/null 2>&1; then break; fi' >> /start.sh && \
    echo '        echo "Waiting for MySQL to start... ($i/30)"' >> /start.sh && \
    echo '        sleep 1' >> /start.sh && \
    echo '    done' >> /start.sh && \
    echo '    ' >> /start.sh && \
    echo '    # 设置root用户无密码，并创建数据库' >> /start.sh && \
    echo '    mysql --socket=/tmp/mysql_init.sock <<SQLEND' >> /start.sh && \
    echo "DELETE FROM mysql.user WHERE User='root' AND Host NOT IN ('localhost', '127.0.0.1', '::1');" >> /start.sh && \
    echo "UPDATE mysql.user SET authentication_string='' WHERE User='root';" >> /start.sh && \
    echo "UPDATE mysql.user SET plugin='mysql_native_password' WHERE User='root';" >> /start.sh && \
    echo "CREATE DATABASE IF NOT EXISTS \\\`\${MYSQL_DATABASE}\\\` CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci;" >> /start.sh && \
    echo 'FLUSH PRIVILEGES;' >> /start.sh && \
    echo 'SQLEND' >> /start.sh && \
    echo '    ' >> /start.sh && \
    echo '    kill $mysql_pid' >> /start.sh && \
    echo '    wait $mysql_pid' >> /start.sh && \
    echo '    echo "MySQL initialization completed (root user with no password)."' >> /start.sh && \
    echo 'fi' >> /start.sh && \
    echo '' >> /start.sh && \
    echo '# Set environment variables for application' >> /start.sh && \
    echo 'export DB_HOST="127.0.0.1"' >> /start.sh && \
    echo 'export DB_PORT="3306"' >> /start.sh && \
    echo 'export DB_NAME="$MYSQL_DATABASE"' >> /start.sh && \
    echo 'export DB_USER="root"' >> /start.sh && \
    echo 'export DB_PASSWORD=""' >> /start.sh && \
    echo '' >> /start.sh && \
    echo 'echo "Starting services..."' >> /start.sh && \
    echo 'exec /usr/bin/supervisord -c /etc/supervisor/conf.d/supervisord.conf' >> /start.sh && \
    chmod +x /start.sh

EXPOSE 80

HEALTHCHECK --interval=30s --timeout=10s --start-period=60s --retries=3 \
    CMD wget --quiet --tries=1 --spider http://localhost/api/v1/health || exit 1

CMD ["/start.sh"]
