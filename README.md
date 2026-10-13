# Nexmesh

An open-source, AI-native structured development workspace. **Build with AI. Engineer with structure.**

Create a code workspace, drag technologies onto a visual canvas, then **install** them inside the workspace or **connect** to an existing external service.

- Product requirements: [`docs/reference/Nexmesh_Project_Ideation_v0_6.md`](docs/reference/Nexmesh_Project_Ideation_v0_6.md)
- Implementation plan: [`docs/IMPLEMENTATION_PLAN.md`](docs/IMPLEMENTATION_PLAN.md)
- Phase 1 tasks: [`docs/phase-1/TASKS.md`](docs/phase-1/TASKS.md)

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
