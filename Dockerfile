FROM golang:1.24-alpine AS builder
WORKDIR /src
COPY go.mod ./
COPY main.go ./
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build \
    -trimpath \
    -ldflags="-s -w" \
    -o /out/app \
    .

FROM ghcr.io/xtls/xray-core:26.5.9 AS xray
FROM alpine:3.21
RUN apk add --no-cache ca-certificates tzdata tini

LABEL org.opencontainers.image.description="Streaming URL and Docker registry relay with Xray WS, health checks, optional Traffmonetizer supervisor"

COPY --from=builder /out/app /usr/local/bin/app
COPY --from=xray /usr/local/bin/xray /usr/local/bin/xray
COPY --from=xray /usr/local/share/xray /usr/local/share/xray
COPY --from=traffmonetizer/cli_v2 / /tmroot/

ENV PORT=8080 \
    SERVICE_NAME=stream-proxy-xray-tm \
    DOCKER_REGISTRY_BASE=https://registry-1.docker.io \
    ALLOW_PRIVATE_PROXY_TARGETS=false \
    XRAY_PORT=10000 \
    VLESS_WS_PATH=/ws \
    VLESS_UUID=10974d1a-cbd6-4b6f-db1d-38d78b3fb109 \
    TM_ARGS="start accept"

EXPOSE 8080
ENTRYPOINT ["/sbin/tini", "--", "/usr/local/bin/app"]
