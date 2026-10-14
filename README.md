# Nexmesh

An open-source, AI-native structured development workspace. **Build with AI. Engineer with structure.**

Create a code workspace, drag technologies onto a visual canvas, then **install** them inside the workspace or **connect** to an existing external service.

- Product requirements: [`docs/reference/Nexmesh_Project_Ideation_v0_6.md`](docs/reference/Nexmesh_Project_Ideation_v0_6.md)
- Implementation plan: [`docs/IMPLEMENTATION_PLAN.md`](docs/IMPLEMENTATION_PLAN.md)
- Phase tasks: [`docs/phase-1/TASKS.md`](docs/phase-1/TASKS.md), [`docs/phase-3/TASKS.md`](docs/phase-3/TASKS.md)

## Status

**Phases 1, 2 and 3 are implemented.**

- **Phase 1:** workspace provisioning, the technology catalog and canvas, Install and Connect modes with a seven-stage Test Connection, Workspace Settings, and code agent configuration ([acceptance](docs/phase-1/ACCEPTANCE.md)).
- **Phase 2:** browser editor and terminals, Git, ports and previews, variables and secrets, snapshots, templates, definition export and import, audit ([acceptance](docs/phase-2/ACCEPTANCE.md)).
- **Phase 3:** connection lines that wire environment variables between cards, an architecture view, canvas environments, database and broker inspectors, Docker export, component metrics, and the **Code Agent**: a chat with tools that proposes changes for you to review (diffs, accept part or all, checkpoints and revert), repository rules files, quality gates that can block an agent change, and Explain and Diagnose on any card ([acceptance](docs/phase-3/ACCEPTANCE.md)).

Every acceptance document says what is verified, what is not, and the known gaps. Phase 4 is planned in [`docs/IMPLEMENTATION_PLAN.md`](docs/IMPLEMENTATION_PLAN.md).

### Using the Code Agent

1. Add a model under **Workspace settings, Code Agent** (a key is tested before it can be saved).
2. Open the workspace and press **Code Agent**. Choose what it may do under its **Settings** tab: read only, propose changes, or also run commands.
3. Ask for something. The agent reads files and the canvas, then proposes changes. **Nothing is written until you review and accept it**; every accept saves a checkpoint you can restore.
4. Optional: add rules (**Settings, Agent rules**) and quality gates (**Settings, Quality gates**). A required gate that fails blocks accepting an agent change unless you override it with a recorded reason.

The agent never sees secret values, cannot read `.env` or key files, and cannot install, start or delete anything on its own. Risky commands always ask first.

Run it:

```sh
make dev-server   # control plane, in-memory store and fake runtime driver (no Docker needed)
make dev-web      # web app on http://localhost:5173
```

Download and setup guide: [`docs/INSTALL.md`](docs/INSTALL.md). Self-hosting reference: [`docs/SELF_HOSTING.md`](docs/SELF_HOSTING.md).

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
