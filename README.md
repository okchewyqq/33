# 33

Single-port container for Scaleway Serverless Containers.

## Public routing on one external port

The container listens on `PORT` (default `8080`) and routes by path:

- `/` -> local plain text `ok`
- `/healthz` -> local health check, always returns `ok`
- `/readyz` -> local JSON status
- `/status` -> local JSON status
- `/op` -> internal OpenList service (`127.0.0.1:5244` by default)
- `/ws` -> internal Xray VLESS WebSocket listener (`127.0.0.1:10000` by default)

## Runtime environment variables

- `PORT` default `8080`
- `SERVICE_NAME` default `openlist-xray-tm`
- `APP_VERSION` default `dev`
- `OPENLIST_ENABLED` default `true`; set `false` to disable
- `OPENLIST_PATH` default `/op`
- `OPENLIST_PORT` default `5244`
- `OPENLIST_LISTEN` default `127.0.0.1`
- `OPENLIST_DATA_DIR` default `/opt/openlist/data`
- `OPENLIST_SITE_URL` default `/op`
- `OPENLIST_ADMIN_USERNAME` default `Neu` as deployment metadata
- `OPENLIST_ADMIN_PASSWORD` default `114514`
- `OPENLIST_ARGS` optional override for OpenList arguments, default `server`
- `XRAY_ENABLED` default `true`; set `false` to disable
- `XRAY_PORT` default `10000`
- `XRAY_LISTEN` default `127.0.0.1`
- `VLESS_WS_PATH` default `/ws`
- `VLESS_UUID` default `10974d1a-cbd6-4b6f-db1d-38d78b3fb109`
- `TM_TOKEN` optional; when set, starts Traffmonetizer in background
- `TM_ARGS` default `start accept`

Do not bake production secrets into the image or repository. Store real tokens/passwords as deployment secret/environment variables.

## OpenList login

OpenList documents `OPENLIST_ADMIN_PASSWORD` for setting the admin password by environment variable.

The image defaults to:

- username metadata: `Neu`
- password: `114514`

If upstream OpenList still creates the default admin username internally, check `/status` or container logs for the actual username, then use password `114514`.

## GitHub Actions image

GitHub Actions builds and pushes:

- `ghcr.io/okchewyqq/33:latest`
- `ghcr.io/okchewyqq/33:sha-<commit>`

## Scaleway

Use:

- Container port: `8080`
- Health check path: `/healthz`
- Optional environment variable: `TM_TOKEN=<your token>`
