# 33

Plain HTTP container service template for GitHub Actions + GHCR + Scaleway Serverless Containers.

It can also supervise Traffmonetizer in the background when `TM_TOKEN` is provided at runtime.

## Endpoints

- `GET /` returns JSON service status, including Traffmonetizer state
- `GET /healthz` returns `ok`
- `GET /readyz` returns `ready`

## Runtime

Environment variables:

- `PORT` default `8080`
- `SERVICE_NAME` default `scaleway-http-template`
- `APP_VERSION` default `dev`
- `TM_TOKEN` optional; when set, starts Traffmonetizer in background
- `TM_ARGS` default `start accept`

Equivalent runtime behavior:

```bash
docker run -d --restart always --network host --name tm \
  -e TM_TOKEN='your-token' \
  ghcr.io/<owner>/33:latest
```

Do not bake tokens into the image or repository. Store `TM_TOKEN` as a deployment secret/environment variable.

## Image

GitHub Actions builds and pushes:

- `ghcr.io/<owner>/33:latest`
- `ghcr.io/<owner>/33:sha-<commit>`

## Scaleway

Use:

- Container port: `8080`
- Health check path: `/healthz`
- Environment variable: `TM_TOKEN=<your token>`
