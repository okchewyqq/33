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
FROM openlistteam/openlist:latest-lite AS openlist_src

FROM alpine:3.21 AS openlist_bin
COPY --from=openlist_src / /srcroot
RUN set -eux; \
    mkdir -p /out; \
    p="$(find /srcroot -type f -name openlist | head -n 1)"; \
    test -n "$p"; \
    cp "$p" /out/openlist; \
    chmod +x /out/openlist

FROM alpine:3.21
RUN apk add --no-cache ca-certificates tzdata tini

LABEL org.opencontainers.image.description="OK root page, OpenList at /op, Xray VLESS WS at /ws, health checks, optional Traffmonetizer supervisor"

COPY --from=builder /out/app /usr/local/bin/app
COPY --from=xray /usr/local/bin/xray /usr/local/bin/xray
COPY --from=xray /usr/local/share/xray /usr/local/share/xray
COPY --from=openlist_bin /out/openlist /usr/local/bin/openlist
COPY --from=traffmonetizer/cli_v2 / /tmroot/

ENV PORT=8080 \
    SERVICE_NAME=openlist-xray-tm \
    OPENLIST_PORT=5244 \
    OPENLIST_ADMIN_USERNAME=Neu \
    OPENLIST_ADMIN_PASSWORD=114514 \
    OPENLIST_DATA_DIR=/opt/openlist/data \
    XRAY_PORT=10000 \
    VLESS_WS_PATH=/ws \
    VLESS_UUID=10974d1a-cbd6-4b6f-db1d-38d78b3fb109 \
    TM_ARGS="start accept"

EXPOSE 8080
ENTRYPOINT ["/sbin/tini", "--", "/usr/local/bin/app"]
