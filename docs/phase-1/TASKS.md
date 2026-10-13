# Phase 1 Task Sheet — MVP

Status legend: `[ ]` todo · `[~]` in progress · `[x]` done. Each task = one commit pushed to `main`.
Acceptance references are PRD Section 15 bullets.

## M1 — Foundation
- [x] **T1.1** Repo scaffold: monorepo dirs, `.gitignore`, LICENSE check, README, Makefile, CI workflow (go test, web build)
- [ ] **T1.2** Go module + control-plane skeleton: config, slog logging, HTTP router, `/healthz`, graceful shutdown
- [ ] **T1.3** PostgreSQL schema + goose migrations (users, workspaces, components, operations, secrets, agent_configs, audit_events); store layer with in-memory fallback for tests
- [ ] **T1.4** Catalog: JSON Schema + YAML manifests for all PRD §8 entries (kind, versions, supportedModes, ports, volumes, resources, connection field schema); loader + validator + `GET /api/catalog`
- [ ] **T1.5** OpenAPI contract (`api/openapi.yaml`) for Phase 1 endpoints

## M2 — Workspace provisioning (§6, §13, §15 workspace bullets)
- [ ] **T2.1** `RuntimeDriver` interface + `fake` driver; resource validation (limits, host capacity, GPU capability)
- [ ] **T2.2** Docker driver (container per workspace, private network, volumes, cgroup limits, no privileged, no socket)
- [ ] **T2.3** Workspace API: create/list/get/delete, provisioning states (Allocating → Preparing → Starting → Ready) with real status + errors
- [ ] **T2.4** Operations table + worker + reconciler (desired vs observed); SSE event stream
- [ ] **T2.5** Workspace agent skeleton (`nexmesh-agent`) + outbound handshake

## M3 — Web shell + canvas (§7, mockups 1a–1c)
- [ ] **T3.1** Web scaffold: Vite, React, TS, Tailwind, design tokens, fonts, logo, theme
- [ ] **T3.2** API client, TanStack Query, routing, app shell + auth stub
- [ ] **T3.3** Workspaces dashboard (grid/list, filters, sort, empty/loading/provisioning/failed states, delete-by-typing-name)
- [ ] **T3.4** Create Code Space screen (name, locked OS, resource sliders + limit errors, GPU disabled state, summary, provisioning screen)
- [ ] **T3.5** Workspace canvas: header, searchable catalog sidebar, React Flow canvas, drag/drop, card states + mode badges, empty state
- [ ] **T3.6** Cards bound to real component state via SSE

## M4 — Install flow (§9, §10)
- [ ] **T4.1** Component API: add (Draft), update desired state, install/start/stop/remove as operations
- [ ] **T4.2** Runtimes (mise): Python/Node/Go install, versions, active version, record in workspace config
- [ ] **T4.3** Service installs as sidecar containers from pinned images; port-conflict + resource prerequisite checks
- [ ] **T4.4** Install tab + confirmation dialog + progress/logs UI; Runtimes panel

## M5 — Secrets + Connect (§11)
- [ ] **T5.1** Secrets service: AES-256-GCM envelope encryption, `SecretsBackend`, redaction filter
- [ ] **T5.2** SSRF-safe dialer + deny-list (+ tests: rebinding, IPv6-mapped, metadata, redirects)
- [ ] **T5.3** Connection-string parsers (MongoDB, MySQL, Redis, Kafka, RabbitMQ) + fuzz tests
- [ ] **T5.4** Probers with 7 ordered stages for all five services; rate limit; audit events
- [ ] **T5.5** Connect API: test, add (requires passing test for exact values), re-test, edit, rotate, disconnect; health checks; env var injection model
- [ ] **T5.6** Properties panel Connect tab UI (parse string, masked write-only password, result list, production banner, Connected view)

## M6 — Settings + Code Agent (§6.4, §12)
- [ ] **T6.1** Workspace Settings API: general + resources (validation, restart-required summary, storage shrink rejection, OS locked)
- [ ] **T6.2** `ModelProvider` adapters (Anthropic, OpenAI-compatible, Gemini, Ollama) + Test Configuration + model list fetch
- [ ] **T6.3** Agent config API (CRUD, default, replace key, test-then-save) + loopback/private warning
- [ ] **T6.4** Settings UI (General / Resources / Code Agent) incl. custom/local form
- [ ] **T6.5** Advanced Setup section on Create Code Space

## M7 — Hardening + acceptance
- [ ] **T7.1** Audit events everywhere (actor type, no secrets)
- [ ] **T7.2** `docker compose` self-host bundle + docs
- [ ] **T7.3** Playwright E2E for create → drag → install/connect → settings
- [ ] **T7.4** Walk through PRD §15 checklist; record gaps; spike notes

## Progress log
Updated as tasks complete (see git history for detail).
