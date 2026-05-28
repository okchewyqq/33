# 33

Plain HTTP container service template for GitHub Actions + GHCR + Scaleway Serverless Containers.

## Endpoints

- `GET /` returns JSON service status
- `GET /healthz` returns `ok`
- `GET /readyz` returns `ready`

## Runtime

Environment variables:

- `PORT` default `8080`
- `SERVICE_NAME` default `scaleway-http-template`
- `APP_VERSION` default `dev`

## Image

GitHub Actions builds and pushes:

- `ghcr.io/<owner>/33:latest`
- `ghcr.io/<owner>/33:sha-<commit>`

## Scaleway

Use:

- Container port: `8080`
- Health check path: `/healthz`
