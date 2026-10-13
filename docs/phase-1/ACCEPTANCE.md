# Phase 1 acceptance review

Walks the MVP acceptance criteria in PRD Section 15 against what is built and tested. "Evidence" names the automated test or the manual check. **Verified live** means exercised against a real service in this repository's development environment; **fake driver** means the workspace runtime was the in-memory driver.

Run it yourself:

```sh
make test          # unit + component tests (server and web)
make acceptance    # the Section 15 walk-through, in-process (server/internal/e2e)
make it-services && make it     # probes against real Redis, MariaDB, MongoDB, RabbitMQ, Kafka
cd web && pnpm exec playwright test   # browser journey (embedded UI, fake driver) + accessibility checks
```

## Criteria

| # | Criterion | Status | Evidence |
| :-- | :-- | :-- | :-- |
| 1 | Create a workspace on Ubuntu 24.04 LTS with valid resources | Met (fake driver) | `TestPhase1Acceptance`; Playwright "create a code space"; Docker driver commands unit-tested (`runtime/docker`) |
| 2 | Unsupported or unavailable resource combinations are rejected clearly | Met | `runtime.ValidateResources` tests; acceptance (CPU, GPU); create-screen limit test; Docker driver reads host RAM and `nvidia` runtime |
| 3 | Canvas only after provisioning reports readiness | Met | Provisioning page redirects only on `ready`; canvas redirects back if not ready; Playwright |
| 4 | Sidebar shows the defined categories and entries | Met | Catalog loader test (18 entries, 6 categories, PRD 11.2 mode matrix); `CatalogSidebar` tests |
| 5 | Dragging an entry opens a configuration/confirmation flow | Met | Drop creates a Draft card and opens the properties panel; Install shows a confirmation dialog; Playwright drag-and-drop |
| 6 | Version and settings persisted before an install begins | Met | Acceptance test reads the card immediately after the install call |
| 7 | UI reports progress and distinguishes success, failure, running | Met, coarse | Operation progress, log, failed state with reason and Retry. Progress is milestone-based, not byte-level |
| 8 | A card maps to real configuration and observed state | Met | Desired vs observed stored separately; SSE pushes observed state; reconcilers compare workspaces and installed services with the backend (`TestReconcileInstalledShowsRealState`) |
| 9 | Python, Node.js, Go install through the global runtime workflow | Met | Runtimes API + panel; `mise` install script **verified live** on an Ubuntu 24.04 host (Node 22.11.0 via mise, then `npm install` of pinned React). Python and Go use the same path but were not run |
| 10 | Workspace files and service data follow defined persistence | Met by design | Workspace volume `/workspace`; service data in named volumes, kept on remove unless "delete data" is chosen; workspace delete removes everything. Not verified on a live Docker daemon |
| 11 | Service components show Install and Connect tabs; others Install only | Met | `PropertiesPanel` tests; catalog `supportedModes`; Playwright |
| 12 | Connect tab shows component-specific fields and accepts a connection string | Met | Form is generated from the catalog; parsers for MongoDB, MySQL, Redis, RabbitMQ fuzz-tested (1.5M executions, no leaks); Kafka has no standard string by design |
| 13 | Test Connection runs from the backend, reports a specific category, performs no writes | Met, **verified live** | Seven stages against real Redis 7.0, MariaDB 10.11, MongoDB 7.0, RabbitMQ 3.12, Kafka 3.8 (`probe/it_test.go`): wrong password, missing permission, refused port, DNS failure, TLS (untrusted CA, hostname mismatch, plaintext port, opt-out), blocked address. Operations are ping, `INFO`, `listCollections`, `SELECT 1`, passive exchange declare, metadata |
| 14 | Add is disabled until a test passes for the current values; edits invalidate | Met | HMAC fingerprint binds the result to exact values (server) and `passedKey === key` (UI); acceptance, `ConnectTab` and Playwright tests |
| 15 | Credentials encrypted, masked, absent from logs and audit | Met | AES-256-GCM envelope with record-id binding; acceptance test scans every API response, SSE stream and audit record for secret values; stored ciphertext checked for plaintext |
| 16 | Connected card shows Connected and health; details reach projects | Met with a gap | Badge, environment chip, 30 s health checks with Degraded transitions. Variables are served to the workspace agent (`GET /agent/v1/workspaces/{id}/env`); **the agent does not yet start processes with them** (arrives with the terminal in Phase 2) |
| 17 | Disconnect removes connection and secrets, not remote data | Met | `TestDisconnectRemovesSecretsAndKeepsCard`; acceptance test checks the secret is gone |
| 18 | Settings: name, description, RAM, CPU, storage, GPU with validation and restart indication | Met (fake driver) | `workspace/settings.go`; preview vs apply; CPU/RAM live, storage/GPU confirmed restart. Docker `update`/recreate unit-tested only |
| 19 | OS shown read-only and cannot be changed | Met | API returns `os_locked`; UI has no control; acceptance and component tests |
| 20 | Reducing storage below current usage is rejected | Met | Usage = installed component reservations; Docker backend rejects any shrink; `TestSettingsStorageReduction` |
| 21 | Code Agent tab: provider, model, key, provider fields | Met | Anthropic, OpenAI, Google (Gemini API), Custom or local. Vertex AI is shown as "Coming soon" |
| 22 | Save only after Test Configuration passes; edits invalidate | Met | Same fingerprint mechanism; `agentcfg` and UI tests; Playwright |
| 23 | Custom or local model by endpoint, protocol, model ID, optional key and headers | Met | OpenAI-compatible, Anthropic-compatible, Ollama adapters, tested against protocol fakes; **hosted providers were not called with real keys** |
| 24 | Agent keys encrypted, masked, write-only, absent from logs, not injected | Met | `hasKey` only in responses; acceptance scan; the workspace environment endpoint never includes agent configuration |
| 25 | Advanced Setup is collapsible; a workspace can be created without it | Met | Untouched section never blocks; started-but-untested blocks; API enforces it too (`test_required`) |
| 26 | Loopback or private endpoint shows a warning | Met | Client-side hint plus server warning in the test result |

## Gaps and risks, in priority order

1. **No live Docker run.** The development environment had the Docker CLI but no daemon. The Docker driver is tested through a recording runner (exact `docker` arguments, hardening flags, resource limits) and `docker compose config` validates the bundle, but images, networking, `docker update`, container recreation and the compose stack have not run against a real daemon. The first task on a Docker host is the smoke test in `docs/SELF_HOSTING.md`.
2. **Service image assumptions.** Install-mode services rely on official image behaviour that could not be exercised: Kafka KRaft environment (advertised as `kafka:9092`), MySQL started with an empty root password, RabbitMQ with a default `nexmesh` user, Redis with protected mode off. These are acceptable only on the private per-workspace network. Generated per-install credentials arrive with the secrets manager (F11).
3. **Authentication is a stub.** Every request acts as one local user. Do not expose the port to untrusted networks.
4. **Agent injection is not consumed.** See criterion 16.
5. **Hosted model providers were tested against protocol fakes**, not live APIs, because no keys were available.
6. **CI is untested.** The workflows were written and the same commands pass locally, but GitHub Actions has not run them. The integration job is `continue-on-error` until it has.
7. **Spike follow-ups** (PRD 20.6): see below.
8. **UI polish.** The provisioning page works but is simpler than mockup 1e (no per-step timings). Sign-in, landing page, docs and system pages (design prompts P1, P8, P9) are outside the Phase 1 task sheet.

## Spike notes (PRD Section 20.6)

| Spike | Result so far |
| :-- | :-- |
| 1. Storage quotas and resizing | Not run. Quotas are behind `StorageQuota` (needs overlay2 on XFS with project quotas). The Docker backend reports it cannot shrink; growth recreates the container and keeps the volume |
| 2. Shared resource budget | Not run. CPU and RAM limits apply to the workspace container; services have per-service caps from the catalog. A shared parent cgroup and OOM ordering are open |
| 3. Docker inside a workspace | Not started (Phase 2/3) |
| 4. SSRF-safe prober | Done: table tests for loopback, private, link-local, CGNAT, metadata (IPv4, IPv6, hostnames), IPv4-mapped and NAT64 forms, mixed DNS answers, a single resolution per dial (rebinding), and redirect-to-metadata through the HTTP client |
| 5. Kafka footprint | Measured on this host: single-node KRaft broker with `-Xmx256m` idles at about 395 MB RSS and 2% CPU. The catalog now requests 1 GB with a 512 MB heap |
| 6. Agent portability | Partly: four protocol adapters verified against fakes. Tool-calling behaviour is a Phase 3 question |
| 7. Terminal through the edge | Phase 2 |

## Test inventory (at the time of writing)

- Go: 18 packages with tests, `go test -race ./...`; fuzz target for connection strings; real-service integration tests behind `NEXMESH_IT=1`.
- Web: 77 component and unit tests (Vitest, Testing Library).
- Browser: 10 Playwright tests including an axe WCAG 2 A/AA scan of dashboard, create, canvas and settings screens.
