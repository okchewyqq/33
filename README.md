# 33

Single-port container for Scaleway Serverless Containers.

This test branch removes OpenList and replaces the root service with streaming proxy features. It does not cache or write proxied content to disk.

## Public routing on one external port

The container listens on `PORT` (default `8080`) and routes by path:

- `/` -> plain text usage page
- `/proxy?url=https://example.com/file` -> streaming direct-link relay for preview/download
- `/v2/...` -> streaming Docker Registry v2 relay, default upstream `https://registry-1.docker.io`
- `/ws` -> internal Xray WebSocket listener (`127.0.0.1:10000` by default)
- `/healthz` -> local health check, always returns `ok`
- `/readyz` -> local JSON status
- `/status` -> local JSON status

## Runtime environment variables

- `PORT` default `8080`
- `SERVICE_NAME` default `stream-proxy-xray-tm`
- `APP_VERSION` default `dev`
- `DOCKER_REGISTRY_BASE` default `https://registry-1.docker.io`
- `ALLOW_PRIVATE_PROXY_TARGETS` default `false`; set `true` only in a private lab if `/proxy` needs private IP targets
- `XRAY_ENABLED` default `true`; set `false` to disable
- `XRAY_PORT` default `10000`
- `XRAY_LISTEN` default `127.0.0.1`
- `VLESS_WS_PATH` default `/ws`
- `VLESS_UUID` default `10974d1a-cbd6-4b6f-db1d-38d78b3fb109`
- `TM_TOKEN` optional; when set, starts Traffmonetizer in background
- `TM_ARGS` default `start accept`

Do not bake production secrets into the image or repository. Store real tokens as deployment secret/environment variables.

## Test commands

Health check:

```bash
curl -i https://YOUR_DOMAIN/healthz
```

Direct-link relay:

```bash
curl -L "https://YOUR_DOMAIN/proxy?url=https://example.com/file.zip" -o file.zip
```

Docker registry protocol check:

```bash
curl -i https://YOUR_DOMAIN/v2/
```

Docker image pull, Docker Hub official image example:

```bash
docker pull YOUR_DOMAIN/library/alpine:latest
```

For official Docker Hub images, `/v2/alpine/...` is automatically rewritten to `/v2/library/alpine/...`.

## GitHub Actions image

GitHub Actions builds and pushes branch images to GHCR using metadata-action tags.

For this branch, check the workflow run for the exact tag. The commit SHA tag will look like:

- `ghcr.io/okchewyqq/33:sha-<commit>`

## Scaleway

Use:

- Container port: `8080`
- Health check path: `/healthz`
- Optional environment variable: `TM_TOKEN=<your token>`
