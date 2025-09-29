# OneClickVirt All-in-One Container
# Single container with MySQL + Application + Nginx

# Stage 1: Build frontend
FROM node:22-alpine AS frontend-builder
WORKDIR /app/web
COPY web/package*.json ./
RUN npm ci --only=production
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

# Create users and set permissions
RUN adduser -D -s /bin/sh mysql mysql \
    && chown -R mysql:mysql /var/lib/mysql /var/log/mysql /run/mysqld \
    && chown -R nginx:nginx /var/www/html \
    && chmod -R 755 /var/www/html

# Configure MySQL
RUN cat > /etc/my.cnf << 'EOF'
[mysqld]
datadir=/var/lib/mysql
socket=/run/mysqld/mysqld.sock
user=mysql
pid-file=/run/mysqld/mysqld.pid
bind-address=127.0.0.1
port=3306
character-set-server=utf8mb4
collation-server=utf8mb4_unicode_ci
default-authentication-plugin=mysql_native_password
max_connections=100
skip-networking=0
EOF

# Configure Nginx
RUN cat > /etc/nginx/nginx.conf << 'EOF'
user nginx;
worker_processes auto;
error_log /var/log/nginx/error.log;
pid /run/nginx.pid;
events { worker_connections 1024; }
http {
    include /etc/nginx/mime.types;
    default_type application/octet-stream;
    sendfile on;
    keepalive_timeout 65;
    gzip on;
    server {
        listen 80;
        server_name localhost;
        root /var/www/html;
        index index.html;
        client_max_body_size 10M;
        
        location /api/ {
            proxy_pass http://127.0.0.1:8888;
            proxy_set_header Host $host;
            proxy_set_header X-Real-IP $remote_addr;
            proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        }
        
        location /swagger/ {
            proxy_pass http://127.0.0.1:8888;
            proxy_set_header Host $host;
            proxy_set_header X-Real-IP $remote_addr;
        }
        
        location / {
            try_files $uri $uri/ /index.html;
        }
    }
}
EOF

# Configure Supervisor
RUN cat > /etc/supervisor/conf.d/supervisord.conf << 'EOF'
[supervisord]
nodaemon=true
user=root

[program:mysql]
command=/usr/bin/mysqld --user=mysql --console
autostart=true
autorestart=true
user=mysql
priority=1

[program:app]
command=/app/main
directory=/app
autostart=true
autorestart=true
user=root
priority=2
environment=DB_HOST="127.0.0.1",DB_PORT="3306"

[program:nginx]
command=/usr/sbin/nginx -g "daemon off;"
autostart=true
autorestart=true
user=root
priority=3
EOF

# Create startup script
RUN cat > /start.sh << 'EOF'
#!/bin/bash
set -e
echo "Starting OneClickVirt..."

# Set environment variables
export MYSQL_ROOT_PASSWORD=${MYSQL_ROOT_PASSWORD:-OneClickVirt123!}
export MYSQL_DATABASE=${MYSQL_DATABASE:-oneclickvirt}
export MYSQL_USER=${MYSQL_USER:-oneclickvirt}
export MYSQL_PASSWORD=${MYSQL_PASSWORD:-OneClickVirt123!}

# Initialize MySQL database if needed
if [ ! -d "/var/lib/mysql/mysql" ]; then
    echo "Initializing MySQL database..."
    mysql_install_db --user=mysql --datadir=/var/lib/mysql
    
    mysqld --user=mysql --skip-networking --socket=/tmp/mysql_init.sock &
    mysql_pid=$!
    
    for i in {1..30}; do
        if mysql --socket=/tmp/mysql_init.sock -e "SELECT 1" >/dev/null 2>&1; then break; fi
        echo "Waiting for MySQL to start... ($i/30)"
        sleep 1
    done
    
    mysql --socket=/tmp/mysql_init.sock <<EOSQL
ALTER USER 'root'@'localhost' IDENTIFIED BY '${MYSQL_ROOT_PASSWORD}';
CREATE DATABASE IF NOT EXISTS \`${MYSQL_DATABASE}\` CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci;
CREATE USER IF NOT EXISTS '${MYSQL_USER}'@'localhost' IDENTIFIED BY '${MYSQL_PASSWORD}';
GRANT ALL PRIVILEGES ON \`${MYSQL_DATABASE}\`.* TO '${MYSQL_USER}'@'localhost';
FLUSH PRIVILEGES;
EOSQL
    
    kill $mysql_pid
    wait $mysql_pid
    echo "MySQL initialization completed."
fi

# Set environment variables for application
export DB_HOST="127.0.0.1"
export DB_PORT="3306"
export DB_NAME="$MYSQL_DATABASE"
export DB_USER="$MYSQL_USER"
export DB_PASSWORD="$MYSQL_PASSWORD"

echo "Starting services..."
exec /usr/bin/supervisord -c /etc/supervisor/conf.d/supervisord.conf
EOF

RUN chmod +x /start.sh

EXPOSE 80

HEALTHCHECK --interval=30s --timeout=10s --start-period=60s --retries=3 \
    CMD wget --quiet --tries=1 --spider http://localhost/api/v1/health || exit 1

CMD ["/start.sh"]