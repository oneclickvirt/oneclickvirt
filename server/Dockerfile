# Backend Dockerfile
ARG GO_VERSION=1.25.0
FROM golang:${GO_VERSION}-alpine AS build
WORKDIR /app
COPY . .
ENV GOTOOLCHAIN=local
RUN apk add --no-cache build-base git && go build -o oneclickvirt .

FROM alpine:3.20
WORKDIR /app
COPY --from=build /app/oneclickvirt /app/
COPY config/config.yaml /app/config.yaml
EXPOSE 8080
ENTRYPOINT ["/app/oneclickvirt"]
