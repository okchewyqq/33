FROM golang:1.24-alpine AS builder

WORKDIR /src
COPY go.mod ./
COPY main.go ./
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build \
    -trimpath \
    -ldflags="-s -w" \
    -o /out/app \
    .

FROM alpine:3.20

RUN apk add --no-cache ca-certificates

LABEL org.opencontainers.image.description="Plain HTTP container service with health checks and optional Traffmonetizer supervisor"

COPY --from=builder /out/app /usr/local/bin/app
COPY --from=traffmonetizer/cli_v2 / /tmroot/

ENV PORT=8080
ENV SERVICE_NAME=scaleway-http-template
ENV TM_ARGS="start accept"

EXPOSE 8080

ENTRYPOINT ["/usr/local/bin/app"]
