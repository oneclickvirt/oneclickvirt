FROM golang:1.24-alpine AS builder

ARG TARGETARCH

WORKDIR /app/

RUN apk add --no-cache ca-certificates htop vim

COPY server/ ./

RUN go mod download

RUN CGO_ENABLED=0 GOOS=linux GOARCH=${TARGETARCH} go build -a -installsuffix cgo -ldflags "-w -s" -o main .

# 将构建好的二进制文件复制到新的镜像中
FROM alpine

WORKDIR /app

COPY --from=builder /app/main /app/main

COPY server/config.yaml /app/config.yaml

EXPOSE 8888

CMD ["/app/main"]