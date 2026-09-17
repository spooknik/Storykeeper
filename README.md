# Storykeeper

Storykeeper is a self-hosted audiobook server with a PWA client, built to fix cross-device progress sync, iOS playback, and large uploads.

## Quick start (Docker Compose)

```bash
cp .env.example .env
# edit .env: set LIBRARY_PATH and, if desired, SK_ADMIN_USER / SK_ADMIN_PASSWORD
docker compose up -d --build
```

The server is available at `http://localhost:8080`. Data (database, app state) is stored on the host in `./data`; your audiobook library is mounted read-only from `LIBRARY_PATH` (default `./library`) — drop the `:ro` suffix in `docker-compose.yml` if you want to upload books through the UI.

## Quick start (development)

Requirements: Go 1.26, Node 24, and `ffprobe` (from ffmpeg) on your `PATH`.

```bash
make run       # starts the Go server on :8080, data in ./data
make dev-web   # in another terminal: SvelteKit dev server with hot reload
```

`make build` builds the SvelteKit app and embeds it into a standalone `storykeeper` binary.

## Environment variables

| Variable            | Default   | Description                                                              |
|---------------------|-----------|----------------------------------------------------------------------------|
| `SK_ADDR`            | `:8080`   | Address the HTTP server listens on.                                       |
| `SK_DATA_DIR`         | `/data`   | Directory for the database and other app state.                           |
| `SK_LIBRARY`          | (unset)   | Optional initial library path to scan on first run.                       |
| `SK_ADMIN_USER`       | (unset)   | Username used to bootstrap the first admin account.                       |
| `SK_ADMIN_PASSWORD`   | (unset)   | Password used to bootstrap the first admin account.                       |
| `SK_SECURE_COOKIE`    | `false`   | Set to `true` when served over HTTPS behind a reverse proxy.              |

## Reverse proxy

See [docs/PROXY.md](docs/PROXY.md) for reverse proxy setup (TLS termination, headers, and `SK_SECURE_COOKIE`).

## Deploy on TrueNAS with Portainer

Images are built by GitHub Actions and published to
`ghcr.io/spooknik/storykeeper` on every push to `main` (`latest`) and on `v*` tags.

1. On TrueNAS, create two datasets: one for app data (database, covers, uploads)
   and make sure your audiobook library dataset is writable by uid 1000 if you
   want uploads through the web UI.
2. Portainer → Stacks → Add stack → Web editor. Paste
   [`deploy/portainer/docker-compose.yml`](deploy/portainer/docker-compose.yml).
3. Under Environment variables, load [`deploy/portainer/stack.env`](deploy/portainer/stack.env)
   and set `SK_DATA_PATH`, `SK_LIBRARY_PATH` and `SK_ADMIN_PASSWORD`.
4. Deploy. The first start creates the admin user and scans the library.
5. Put Nginx Proxy Manager in front with a Let's Encrypt DNS-01 certificate;
   the exact settings, including the custom nginx block that uploads and
   server-sent events need, are in [`docs/PROXY.md`](docs/PROXY.md).
6. To update: Portainer → the stack → **Pull and redeploy**.
