# 33

Single-port container for Scaleway Serverless Containers.

## Public routing on one external port

The container listens on `PORT` (default `8080`) and routes by path:

- `/healthz` -> local health check, always returns `ok`
- `/readyz` -> local JSON status
- `/status` -> local JSON status
- `/ws` -> internal Xray VLESS WebSocket listener (`127.0.0.1:10000` by default)
- `/` and every other path -> internal EasyTier Web service (`127.0.0.1:11211` by default)

This matches platforms that expose only one container port.

## Runtime environment variables

- `PORT` default `8080`
- `SERVICE_NAME` default `easytier-xray-tm`
- `APP_VERSION` default `dev`
- `EASYTIER_WEB_ENABLED` default `true`; set `false` to disable
- `EASYTIER_API_PORT` default `11211`
- `EASYTIER_CONFIG_PORT` default `22020`
- `EASYTIER_CONFIG_PROTOCOL` default `udp`
- `EASYTIER_WEB_ARGS` optional override for all EasyTier Web arguments
- `XRAY_ENABLED` default `true`; set `false` to disable
- `XRAY_PORT` default `10000`
- `XRAY_LISTEN` default `127.0.0.1`
- `VLESS_WS_PATH` default `/ws`
- `VLESS_UUID` default `10974d1a-cbd6-4b6f-db1d-38d78b3fb109`
- `TM_TOKEN` optional; when set, starts Traffmonetizer in background
- `TM_ARGS` default `start accept`

Do not bake secrets into the image or repository. Store `TM_TOKEN` as a deployment secret/environment variable.

## GitHub Actions image

GitHub Actions builds and pushes:

- `ghcr.io/okchewyqq/33:latest`
- `ghcr.io/okchewyqq/33:sha-<commit>`

## Scaleway

Use:

- Container port: `8080`
- Health check path: `/healthz`
- Optional environment variable: `TM_TOKEN=<your token>`
