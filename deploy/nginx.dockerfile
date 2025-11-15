# 构建前端
FROM node:22-slim AS builder
ARG TARGETARCH

WORKDIR /app

COPY web/package*.json ./

RUN if [ "$TARGETARCH" = "amd64" ]; then \
        npm ci --include=optional && \
        npm install --no-save @rollup/rollup-linux-x64-gnu; \
    elif [ "$TARGETARCH" = "arm64" ]; then \
        npm install --force && \
        npm install --no-save --force @rollup/rollup-linux-arm64-gnu; \
    else \
        npm ci --include=optional; \
    fi

COPY web/ ./

RUN npm run build

#  将打包的文件复制到 nginx 中
FROM nginx:alpine

COPY --from=builder /app/dist /usr/share/nginx/html

COPY deploy/default.conf /etc/nginx/conf.d/