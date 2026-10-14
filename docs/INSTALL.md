# Download, install and set up Nexmesh

## Quickest start (one command)

Install **Go 1.26+** and **Node.js 22** first, then from the repository folder:

| System | Command |
| :-- | :-- |
| Windows | double-click `start.cmd`, or run `.\start.cmd` in PowerShell |
| macOS / Linux | `./start.sh` |

It installs pnpm if needed, builds the web app once, and starts everything on **http://localhost:8080** (one process, one URL; the browser opens by itself on Windows). Later runs start in seconds. Use `start.cmd -Rebuild` / `./start.sh --rebuild` after pulling new code.

This mode uses the fake runtime and keeps data in memory, so workspaces are lost when you stop it. On Windows the editor, files, Git and the canvas work; **terminals need WSL or Docker** (see path B). For real, persistent workspaces use Docker (path B).

The rest of this guide is the detailed version.

Three ways to run Nexmesh. Pick one:

| Path | Use it for | Needs Docker |
| :-- | :-- | :-- |
| **A. Try it locally** (fake runtime) | Looking around, UI work, trying the editor and terminal | No |
| **B. Self-host with Docker Compose** | Real workspaces and installed services | Yes |
| **C. Build binaries** | Running without Compose, packaging | Optional |

> Status: Phases 1, 2 and 3 are implemented. Path B has not yet been run against a live Docker daemon by the project (see `docs/phase-2/ACCEPTANCE.md`, known gaps). Do the smoke test at the end of this guide before you rely on it.

## 1. Download

```sh
git clone https://github.com/nexio-developer-group/nexmesh.git
cd nexmesh
```

## 2. Prerequisites

| Tool | Version | Needed for |
| :-- | :-- | :-- |
| Git | any recent | download |
| Go | 1.26 (see `server/go.mod`) | paths A and C |
| Node.js | 22 | paths A and C |
| pnpm | 9 or newer (`corepack enable`) | paths A and C |
| Docker Engine + Compose plugin | 24 or newer | path B |
| Linux host | 4 GB RAM or more | path B (Kafka alone needs about 2 GB) |
| `make` | any | shortcuts |

Check: `go version && node -v && pnpm -v && docker compose version`.

## Path A: try it locally (no Docker)

`start.cmd` / `start.sh` do all of the following for you; read on only if you want the manual steps (two terminals, hot reload).

Runs the control plane with an in-memory store and a fake runtime. With a workspace directory set, the editor, terminal, Git and port previews work on real files and processes on your machine, so use a throwaway directory.

```sh
cd web && pnpm install --frozen-lockfile && cd ..

mkdir -p /tmp/nexmesh-work
# terminal 1: control plane on :8080
cd server && \
  NEXMESH_DRIVER=fake NEXMESH_STORE=memory NEXMESH_AUTH=disabled \
  NEXMESH_FAKE_WORKSPACE_DIR=/tmp/nexmesh-work go run ./cmd/nexmesh
# terminal 2: web app on :5173 (proxies /api to :8080)
cd web && pnpm dev
```

Open http://localhost:5173. `make dev-server` and `make dev-web` do the same without the workspace directory or auth settings.

`NEXMESH_AUTH=disabled` skips sign-in and is for local use only. Leave it out to try sign-in: the first visit then offers **Create owner account**.

Data lives in memory and is gone when you stop the server.

## Path B: self-host with Docker Compose

1. Create the configuration and a master key:

   ```sh
   cp deploy/.env.example deploy/.env
   deploy/gen-key.sh        # prints a key
   ```

   Edit `deploy/.env`: paste the key after `NEXMESH_MASTER_KEY=`, and change `POSTGRES_PASSWORD`. **Back up the master key.** Without it stored credentials cannot be decrypted.

2. (Recommended) build the workspace image, so each workspace runs the Nexmesh agent. Without it the editor, terminal, Git and previews are unavailable because nothing in the container answers:

   ```sh
   docker compose -f deploy/docker-compose.yml --profile images build workspace-image
   ```

   then set `NEXMESH_WORKSPACE_IMAGE=nexmesh/workspace:local` in `deploy/.env`.

3. Start:

   ```sh
   docker compose -f deploy/docker-compose.yml --env-file deploy/.env up -d --build
   docker compose -f deploy/docker-compose.yml logs -f nexmesh
   ```

4. Open http://localhost:8080 (or the host's address) and **Create owner account**. That first account owns the instance.

5. Set `NEXMESH_PUBLIC_URL` in `deploy/.env` to an address workspace containers can reach (the default is `http://host.docker.internal:8080`). If a workspace never reports its agent, this is the usual cause.

### Putting it on a network

- Terminate TLS in a reverse proxy (Caddy, nginx, Traefik) and forward `Host` and `X-Forwarded-Proto`. Set `NEXMESH_PUBLIC_URL` to the https address so cookies are marked `Secure`.
- WebSockets must be forwarded (terminal, editor link, previews).
- The control plane holds the Docker socket, which is root-equivalent on the host. Use a dedicated host or VM.
- Optional features: `NEXMESH_ALLOW_SIGNUP=true` (more accounts), `NEXMESH_PREVIEW_DOMAIN=preview.example.com` plus a wildcard DNS record and wildcard certificate (per-port preview origins), `NEXMESH_ALLOW_SHARE_LINKS=true` (public links), `NEXMESH_DEFAULT_IDLE_MINUTES=60` (auto-stop). Full list: [`SELF_HOSTING.md`](SELF_HOSTING.md).

### Smoke test

1. Create a Code Space with the defaults and wait for **Ready**.
2. `docker ps --filter label=nexmesh.managed=true` shows the workspace container.
3. Open the **Code** tab, create a file, save it, and run `echo hello` in the terminal.
4. In the terminal run `python3 -m http.server 3000`; the **Ports** panel lists 3000 and **Open** shows the page.
5. Back on the canvas, drag **Redis**, **Install and start**, and confirm; `docker ps` shows a `nexmesh-<workspace>-redis` container.
6. Stop and start the workspace; the file is still there.

If a step fails, record which one and see Troubleshooting.

## Path C: build binaries

```sh
make build        # web build, embedded in bin/nexmesh; also bin/nexmesh-agent
```

Run with PostgreSQL:

```sh
export NEXMESH_MASTER_KEY="$(deploy/gen-key.sh)"
export NEXMESH_DATABASE_URL="postgres://user:pass@localhost:5432/nexmesh?sslmode=disable"
export NEXMESH_DRIVER=docker        # or fake
./bin/nexmesh
```

Migrations run at startup. The agent binary is what the workspace image runs; you only need it directly when building your own image.

## Verify your checkout

```sh
scripts/verify.sh                          # vet, race tests, unit tests, build (or: make verify)
cd web && pnpm exec playwright install chromium && pnpm exec playwright test   # browser tests
```

Set `NEXMESH_TEST_DATABASE_URL` to also run the store tests against PostgreSQL.

## Upgrade, back up, uninstall

- **Upgrade:** `git pull`, then rerun the `up -d --build` command. Migrations apply automatically.
- **Back up:** the `postgres` volume (`pg_dump`), `NEXMESH_MASTER_KEY`, and the workspace volumes. Snapshots inside Nexmesh are for rollback, not a backup.
- **Uninstall:** `docker compose -f deploy/docker-compose.yml down` keeps data. Add `-v` to delete volumes, which deletes the database. Remove leftover workspaces with `docker ps -a --filter label=nexmesh.managed=true` and `docker rm -f`.

## Troubleshooting

| Symptom | Likely cause |
| :-- | :-- |
| `docker daemon unreachable` on create | Docker socket not mounted, or the user lacks access to it |
| Workspace stuck in *Preparing environment* | The host cannot pull `ubuntu:24.04` |
| Workspace Ready but Code tab says offline | Using the plain `ubuntu:24.04` image, or `NEXMESH_PUBLIC_URL` is unreachable from containers |
| "Cross-site request blocked" after sign-in | `NEXMESH_PUBLIC_URL` does not match the address in your browser |
| Sign-in loops behind a proxy | Proxy does not forward `Host` / `X-Forwarded-Proto` |
| Terminal or preview disconnects | Reverse proxy not forwarding WebSocket upgrades, or timing out idle connections |
| "host has N GB free" on create | Choose fewer resources or free host memory |
| Test Connection: "Connections to … are not allowed" | Egress policy; see `NEXMESH_ALLOW_PRIVATE_EGRESS` |
| Lost the master key | Stored credentials cannot be recovered; users re-enter them |
