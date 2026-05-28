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
FROM easytier/easytier:latest AS easytier_src

FROM alpine:3.21 AS easytier_bin
COPY --from=easytier_src / /srcroot
RUN set -eux; \
    mkdir -p /out; \
    for b in easytier-core easytier-cli easytier-web-embed; do \
      p="$(find /srcroot -type f -name "$b" | head -n 1)"; \
      test -n "$p"; \
      cp "$p" "/out/$b"; \
      chmod +x "/out/$b"; \
    done

FROM alpine:3.21
RUN apk add --no-cache ca-certificates tzdata tini

LABEL org.opencontainers.image.description="EasyTier web at /, Xray VLESS WS at /ws, health checks, optional Traffmonetizer supervisor"

COPY --from=builder /out/app /usr/local/bin/app
COPY --from=xray /usr/local/bin/xray /usr/local/bin/xray
COPY --from=xray /usr/local/share/xray /usr/local/share/xray
COPY --from=easytier_bin /out/easytier-core /usr/local/bin/easytier-core
COPY --from=easytier_bin /out/easytier-cli /usr/local/bin/easytier-cli
COPY --from=easytier_bin /out/easytier-web-embed /usr/local/bin/easytier-web-embed
COPY --from=traffmonetizer/cli_v2 / /tmroot/

ENV PORT=8080 \
    SERVICE_NAME=easytier-xray-tm \
    EASYTIER_API_PORT=11211 \
    EASYTIER_CONFIG_PORT=22020 \
    EASYTIER_CONFIG_PROTOCOL=udp \
    XRAY_PORT=10000 \
    VLESS_WS_PATH=/ws \
    VLESS_UUID=10974d1a-cbd6-4b6f-db1d-38d78b3fb109 \
    TM_ARGS="start accept"

EXPOSE 8080 22020/udp
ENTRYPOINT ["/sbin/tini", "--", "/usr/local/bin/app"]
