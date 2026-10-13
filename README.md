# Nexmesh

An open-source, AI-native structured development workspace. **Build with AI. Engineer with structure.**

Create a code workspace, drag technologies onto a visual canvas, then **install** them inside the workspace or **connect** to an existing external service.

- Product requirements: [`docs/reference/Nexmesh_Project_Ideation_v0_6.md`](docs/reference/Nexmesh_Project_Ideation_v0_6.md)
- Implementation plan: [`docs/IMPLEMENTATION_PLAN.md`](docs/IMPLEMENTATION_PLAN.md)
- Phase 1 tasks: [`docs/phase-1/TASKS.md`](docs/phase-1/TASKS.md)

## Status

**Phase 1 (MVP) is implemented**: workspace provisioning, the technology catalog and canvas, Install and Connect modes with a seven-stage Test Connection, Workspace Settings, and code agent configuration. See [`docs/phase-1/ACCEPTANCE.md`](docs/phase-1/ACCEPTANCE.md) for what is verified, what is not, and the known gaps. Phases 2 to 4 are planned in [`docs/IMPLEMENTATION_PLAN.md`](docs/IMPLEMENTATION_PLAN.md).

Run it:

```sh
make dev-server   # control plane, in-memory store and fake runtime driver (no Docker needed)
make dev-web      # web app on http://localhost:5173
```

Self-hosting with Docker: [`docs/SELF_HOSTING.md`](docs/SELF_HOSTING.md).

## Layout

| Path | Purpose |
| :-- | :-- |
| `server/` | Go control plane (`cmd/nexmesh`) and workspace agent (`cmd/nexmesh-agent`) |
| `web/` | React + TypeScript web app |
| `catalog/` | Technology catalog manifests |
| `api/` | OpenAPI contract |
| `deploy/` | Self-hosting (Docker Compose) |

## Develop

```sh
make dev-server   # control plane with in-memory store + fake runtime driver
make dev-web      # Vite dev server
make test
```
