# 构建前端
FROM node:22-slim AS builder

WORKDIR /app

COPY web/package*.json ./

RUN npm ci --include=optional && npm cache clean --force

COPY web/ ./

RUN npm run build

#  将打包的文件复制到 nginx 中
FROM nginx:alpine

COPY --from=builder /app/dist /usr/share/nginx/html

COPY deploy/default.conf /etc/nginx/conf.d/