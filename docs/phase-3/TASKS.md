# Phase 3 task sheet: structured AI development

Status legend: `[ ]` todo, `[~]` in progress, `[x]` done. Each task is one commit pushed to `main`.
Feature IDs refer to PRD Section 18. Rules from 18.4 apply to every task (real state only, confirmation for impact, no secrets in output, everything audited, catalog-driven, agents have less privilege than users, production is protected).

Decisions taken here for the open items in PRD Section 17:
- **Agent location:** the agent loop runs in the control plane. It acts on the workspace only through the existing workspace-agent link (files, `exec.run`, git), so every action is mediated, audited and permission-checked server-side.
- **Model access:** the existing `ModelProvider` configuration (Phase 1) is used. Chat with tool calls is added for Anthropic-compatible and OpenAI-compatible endpoints (Ollama and Gemini through their OpenAI-compatible endpoints), plus a deterministic scripted provider used only by tests.
- **Checkpoints:** a Git-independent checkpoint of the files an agent change set touches (so it works in folders that are not repositories); full snapshots (F7) remain available.

## M0: Verify what exists, and make it easy to install
- [x] **T0.1** Smoke test script that drives every Phase 1 and 2 feature through the HTTP API of a running server; run on memory and PostgreSQL; fix defects found
- [x] **T0.2** `nexmesh --local` mode: embedded catalog, persistent file store, per-user data dir, opens the browser (the double-click experience)
- [ ] **T0.3** Windows installer (.exe, NSIS): bundles the prebuilt app, Start Menu and desktop shortcuts, optional developer tools (Git, Go, Node, Docker via winget), uninstaller, docs
- [ ] **T0.4** Ubuntu one-command installer: installs prerequisites and Docker, sets up the stack with PostgreSQL, systemd unit, firewall note, uninstall
- [ ] **T0.5** Release workflow that builds Linux and Windows artifacts and the installer

## M1: Structure (F1, F2, F4, F16, F25, F5 metrics)
- [x] **T1.1** Connections: model, catalog `connectsTo` metadata, validation, variable preview, apply and delete, line health (F1 backend)
- [x] **T1.2** Canvas lines UI: draw, preview dialog, status colours, delete (F1 UI)
- [x] **T1.3** Architecture view: layers, edges, drift, Mermaid, Markdown, SVG export, highlight mode (F2)
- [x] **T1.4** Canvas environments: labels, per-environment values, active environment, production protection (F4)
- [x] **T1.5** External API card: catalog entry, probe, test-then-add (F16)
- [ ] **T1.6** Dockerfile and docker-compose export without secrets (F25)
- [ ] **T1.7** Component metrics: CPU, memory, storage; latency trends for connected (F5)

## M2: Tools on cards (F13)
- [ ] **T2.1** Inspector API: read-only browse and query for MySQL, MongoDB, Redis, Kafka, RabbitMQ, with limits, timeouts and audit
- [ ] **T2.2** Inspector UI on the card

## M3: Agent (F17, F18, F19, F21, F23)
- [ ] **T3.1** Chat with tool calls in the model provider layer (Anthropic, OpenAI-compatible, scripted test provider)
- [ ] **T3.2** Change sets: staged diffs, accept all, accept part, reject, checkpoints and revert (F18 core)
- [ ] **T3.3** Agent context and actions: canvas context without secrets, action permissions, forbidden actions recorded, approval for risky commands (F17)
- [ ] **T3.4** Rules files: repository files, scopes, applied-rules list (F21)
- [ ] **T3.5** Quality gates: detection, required or advisory, run in workspace, override recorded, stored in the definition (F23)
- [ ] **T3.6** Agent chat panel and change-set review UI
- [ ] **T3.7** Per-card Explain and Diagnose with context preview (F19)

## M4: Acceptance
- [ ] **T4.0** Workspace definition carries wires, environments, gates and rules (F12 with F1, F4, F21, F23); drift covers them
- [ ] **T4.1** Phase 3 acceptance review, Playwright journeys, docs, security review
