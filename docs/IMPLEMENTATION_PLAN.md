# Nexmesh Implementation Plan

Source documents (kept in `docs/reference/`):

- `Nexmesh_Project_Ideation_v0_6.md` — product requirements (PRD), roadmap (Section 18), design system (Section 19), architecture (Section 20).
- `Nexmesh_UI_Mockups.html` — Phase 1 screen designs (canvas + properties panel, Create Code Space, dashboard, Workspace Settings / Code Agent).

Working agreement: **every finished task is committed and pushed directly to `main`.** No feature branches.

## 1. Architecture (from PRD Section 20)

```
Browser (React, React Flow) --REST/SSE/WS--> Control plane (Go) --> PostgreSQL
                                               |-- Secrets service (AES-256-GCM envelope)
                                               |-- Connection prober (SSRF-safe dialer)
                                               |-- Code agent config/test service (ModelProvider)
                                               '-- RuntimeDriver --> Docker Engine
                                                                       |-- workspace container (Ubuntu 24.04 + workspace agent)
                                                                       '-- service sidecars (MongoDB, Redis, ...)
Workspace agent --outbound gRPC/stream--> Control plane
```

Monorepo layout:

| Path | Contents |
| :-- | :-- |
| `web/` | React + TS + Vite + Tailwind app (pnpm) |
| `server/` | Go module: `cmd/nexmesh` (control plane), `cmd/nexmesh-agent` (workspace agent), `internal/*` |
| `catalog/` | YAML manifests + JSON Schema for the technology catalog |
| `api/` | OpenAPI contract |
| `deploy/` | Docker Compose for self-hosting |
| `docs/` | Plan, task sheets, reference documents |

Key interfaces (every open PRD decision sits behind one): `RuntimeDriver`, `SecretsBackend`, `ModelProvider`, `Prober`.

## 2. Phases

Phases are ordered by dependency, not dates. Feature IDs refer to PRD Section 18.

### Phase 1 — MVP workspace and connectors (PRD Sections 5–15, 20.7)
Reliable provisioning, catalog + canvas, Install/Connect, secrets, Workspace Settings, Code Agent configuration. Detailed task sheet: [`docs/phase-1/TASKS.md`](phase-1/TASKS.md).

Milestones: M1 Foundation → M2 Workspace provisioning → M3 Web shell + canvas → M4 Install flow → M5 Secrets + Connect → M6 Settings + Code Agent → M7 Hardening + acceptance.

### Phase 2 — Daily-use workspace
F9 editor/terminal/file browser, F5 logs + health history, F8 ports + previews, F11 secrets/env manager, F10 Git integration, F7 idle stop + snapshots, F3 stack templates, F6 dev-container import/export, F12 workspace definition export/import, F14 connection import, F27 audit log viewer.
Entry: Phase 1 acceptance criteria met. Detailed task sheet: [`docs/phase-2/TASKS.md`](phase-2/TASKS.md). Implemented; review in [`docs/phase-2/ACCEPTANCE.md`](phase-2/ACCEPTANCE.md).

### Phase 3 — Structured AI development
F1 canvas connections + env wiring, F2 architecture view, F4 canvas environments, F13 DB/broker tools, F16 external API card, F17–F19 canvas-scoped agent with approvals/diffs/contextual help, F21 agent rules files, F23 quality gates, F25 Dockerfile/compose export, F5 metrics.
Entry: Phase 2 editor, terminal, Git, secrets available; agent location + permission model confirmed.

### Phase 4 — Ship and scale
F15 private network access, F20 Nexmesh MCP server, F22 model routing + usage, F24 CI + deploy targets, F26 vulnerability scanning, F28 teams/roles, F29 community catalog, Kubernetes `RuntimeDriver`.
Entry: Phase 3 stable; security review done.

Tasks sheets for Phases 2–4 are written when the previous phase is near completion.

## 3. Cross-cutting rules (PRD 18.4)

Real state only (desired vs observed); explicit confirmation for impact; secrets never in logs/audit/errors; every state change audited with actor type; catalog-driven behaviour; least privilege for agents; production-labelled resources protected.

## 4. Design system (PRD 19.2, mockups)

Cyan `#00B8FF` → blue `#1676FE` → indigo `#4B2BE8`; ink `#0B1530`; slate `#52607A`; surfaces `#F5F8FC` / `#070D1F` / `#0E1730`. Plus Jakarta Sans (UI) + JetBrains Mono (versions, ports, hosts). Canvas/settings dark, account/setup light. State = dot + text label, never colour alone. Two-letter monogram tiles stand in for tech logos. Connection lines labelled "Coming soon".

## 5. Spikes (PRD 20.6) tracked inside Phase 1 M7

Storage quotas, shared cgroup budget, SSRF prober tests (done as part of M5), Kafka footprint, terminal through the edge (Phase 2).

## 6. Environment caveat

Development here has Go, Node, pnpm, PostgreSQL client and Docker CLI, but a Docker daemon may not be available in CI-like sandboxes. The Docker `RuntimeDriver` is therefore built behind an interface with an in-memory `fake` driver used by tests and local UI development (`NEXMESH_DRIVER=fake`).
