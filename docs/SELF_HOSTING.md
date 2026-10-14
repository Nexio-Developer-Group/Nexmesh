# Self-hosting Nexmesh

Nexmesh runs on one Linux host with Docker. `docker compose` starts PostgreSQL and the control plane (the web app is embedded in the same binary).

> Status: Phase 1 (MVP). The Docker driver, compose bundle and workspace image have unit tests and `docker compose config` validation but have **not yet been exercised against a live Docker daemon in CI** (see `docs/phase-1/ACCEPTANCE.md`, gap 1). Run the smoke test below on your host before relying on it.

## Requirements

- Linux host, Docker Engine 24+ with the Compose plugin
- 4 GB RAM or more (each workspace reserves what you configure; Kafka alone needs about 2 GB)
- Ports: `8080` (UI and API) published on the host

## Quick start

```sh
cp deploy/.env.example deploy/.env
deploy/gen-key.sh          # prints a key: paste it after NEXMESH_MASTER_KEY= in deploy/.env
docker compose -f deploy/docker-compose.yml --env-file deploy/.env up -d --build
open http://localhost:8080
```

Optional: build the workspace image so each workspace runs the agent (which reports liveness and receives connection variables):

```sh
docker compose -f deploy/docker-compose.yml --profile images build workspace-image
# then set NEXMESH_WORKSPACE_IMAGE=nexmesh/workspace:local in deploy/.env and re-run `up -d`
```

### Smoke test

1. Open the UI, choose **Create Code Space**, accept the defaults, and wait for **Ready**.
2. `docker ps --filter label=nexmesh.managed=true` shows the workspace container; `docker network ls` shows its private network.
3. On the canvas, drag **Redis** onto the canvas, choose **Install and start**, and confirm. `docker ps` shows a `nexmesh-<workspace>-redis` container.
4. In **Workspace settings → Resources**, raise CPU. The container's `--cpus` limit changes without a restart (`docker inspect`).

## Configuration

All settings are environment variables on the `nexmesh` service.

| Variable | Default | Purpose |
| :-- | :-- | :-- |
| `NEXMESH_MASTER_KEY` | required | 32 random bytes, base64. Encrypts stored credentials. **Back it up.** |
| `NEXMESH_DATABASE_URL` | set by compose | PostgreSQL connection string |
| `NEXMESH_DRIVER` | `docker` | `docker`, or `fake` (no containers; for demos and development) |
| `NEXMESH_WORKSPACE_IMAGE` | `ubuntu:24.04` | Workspace base image. `ubuntu:*` runs an idle container; a Nexmesh workspace image runs the agent |
| `NEXMESH_PUBLIC_URL` | `http://host.docker.internal:8080` | How workspace containers reach the control plane |
| `NEXMESH_MAX_CPU` / `_RAM_GB` / `_STORAGE_GB` | 12 / 24 / 200 | Per-workspace limits |
| `NEXMESH_ALLOW_PRIVATE_EGRESS` | `false` | Allow Test Connection and model tests to reach RFC 1918 addresses |
| `NEXMESH_DEV_ALLOW_LOOPBACK_EGRESS` | `false` | Development only: allow probes to reach `127.0.0.1` |

## Security notes

- **Docker socket.** The control plane needs the host's Docker socket to create workspaces, which is root-equivalent access to the host. Run Nexmesh on a dedicated host or VM. A rootless-Docker or socket-proxy setup is planned (PRD Section 20.3.2). Workspace containers never receive the socket, run without privileges, drop all but a small capability allow-list, and sit on a private per-workspace network.
- **Master key.** Losing `NEXMESH_MASTER_KEY` makes stored connection credentials and agent keys unrecoverable (users re-enter them). Rotating it requires re-saving secrets.
- **Outbound connections.** Test Connection, health checks and model tests go through an SSRF guard: cloud metadata addresses and link-local ranges are always blocked; loopback is blocked; private ranges are blocked unless `NEXMESH_ALLOW_PRIVATE_EGRESS=true`.
- **Authentication.** Phase 1 has no sign-in screen: every request acts as one local user. **Do not expose port 8080 to untrusted networks**; put it behind a VPN or an authenticating reverse proxy. Sign-in and teams arrive in later phases.
- **Agent keys are not injected into workspaces.** Connection variables are delivered only to processes started through the workspace agent.

## Upgrading

```sh
git pull
docker compose -f deploy/docker-compose.yml --env-file deploy/.env up -d --build
```

Database migrations run automatically at startup.

## Troubleshooting

| Symptom | Likely cause |
| :-- | :-- |
| `docker daemon unreachable` on create | The Docker socket is not mounted, or the user lacks access |
| Workspace stuck in *Preparing environment* | The host cannot pull `ubuntu:24.04` (network or registry limits) |
| Workspace creation fails with "host has N GB free" | Choose fewer resources, or free memory on the host |
| Test Connection: "Connections to … are not allowed" | The egress policy blocked the address; see *Outbound connections* |
| Agent never connects | `NEXMESH_PUBLIC_URL` is not reachable from workspace containers |

## Development without Docker

```sh
make dev-server   # in-memory store, fake runtime driver, no containers
make dev-web      # Vite dev server on :5173, proxies /api to :8080
make acceptance   # walks the Phase 1 acceptance criteria in-process
make it-services && make it   # probe integration tests against real services
```

## Accounts and sign-in

Nexmesh requires sign-in. On a fresh instance the first visit offers **Create owner account**; that account owns the instance. Additional accounts can register only when `NEXMESH_ALLOW_SIGNUP=true`. Sessions are 14-day HttpOnly, SameSite=Lax cookies (marked `Secure` when `NEXMESH_PUBLIC_URL` is https or a proxy sets `X-Forwarded-Proto: https`); state-changing requests with a foreign `Origin` are rejected. `NEXMESH_AUTH=disabled` skips sign-in and acts as one local user. Use it for local development and automated tests only, never on a network others can reach.

## Port previews

Servers you start inside a workspace appear in **Code → Ports** and open through the control plane (an encrypted tunnel over the agent link; no inbound ports are published, and servers bound to 127.0.0.1 work). Previews run untrusted code, so they never share an origin with the Nexmesh UI:

- **Default (path mode):** `/api/preview/<workspace>/<port>/…`, served with a `Content-Security-Policy: sandbox` that gives the page an opaque origin (no Nexmesh cookies, no local storage). Fine for static sites and many dev servers; apps that need cookies or `localStorage`, or that use root-absolute asset paths, need subdomain mode.
- **Subdomain mode:** set `NEXMESH_PREVIEW_DOMAIN` (e.g. `preview.example.com`) and point a wildcard DNS record `*.preview.example.com` at Nexmesh. Each port gets its own origin, `p<port>-ws-<id>.preview.example.com`, with full browser behaviour including hot-reload WebSockets. For local development use `NEXMESH_PREVIEW_DOMAIN=localhost` (browsers resolve `*.localhost` to loopback). Terminate TLS for the wildcard in your reverse proxy and forward the `Host` header.

### Share links

Public share links are **off by default**. Set `NEXMESH_ALLOW_SHARE_LINKS=true` to let owners create them from **Code → Ports → Share…**. A link opens one port for anyone who holds it (no Nexmesh sign-in), expires after the chosen time (5 minutes to 7 days), and can be revoked at any time; revocation applies on the visitor's next request. Only a hash of each link's secret is stored, so a link can't be recovered later, only replaced. Turning the setting back off disables every existing link immediately. Previews are sandboxed as described above.
