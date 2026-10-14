# Phase 3 acceptance review

Walks the Phase 3 features (F1, F2, F4, F5 metrics, F13, F16, F17, F18, F19, F21, F23, F25, and the Phase 3 extension of F12) against what is built and tested. **Fake driver** means the workspace runtime was the in-process local agent against a real directory: real files, PTYs, processes, git and TCP, but no container. **Protocol fake** means an HTTP server speaking a provider's wire format; no hosted model API was called.

```sh
scripts/verify.sh                       # go vet, race tests, vitest, web build
cd web && CHROME_PATH=/path/to/chromium pnpm exec playwright test   # browser journeys against the embedded UI
source scripts/it/env.sh && scripts/it/start-services.sh            # real Redis, MariaDB, MongoDB, RabbitMQ, Kafka for the integration tests
NEXMESH_IT=1 go test ./...                                           # (from server/) runs those integration tests too
python3 scripts/smoke.py http://127.0.0.1:8080                       # drives every Phase 1 to 3 API against a running server
```

## Criteria

| Feature | Criterion | Status | Evidence |
| :-- | :-- | :-- | :-- |
| F1 | Draw, preview, confirm and remove a line; variables appear and disappear | Met | `wiring` tests; Playwright `wires.spec.ts` draws a line in the browser, reviews the variable names, confirms, removes it |
| F1 | Invalid pairs rejected with an explanation | Met | catalog `connectsTo` rules; the UI refuses to start an impossible line (Playwright) |
| F1 | Secrets never copied into the consumer's visible configuration | Met | wires store variable **names** only; values are resolved when the environment is built (`wiring` tests, e2e `TestEventStreamCarriesNoSecrets`) |
| F1 | Line state follows the provider within the health interval | Met | state is derived from the provider card's observed state; Playwright shows `waiting` then `healthy` after install |
| F2 | View always matches the canvas; exports contain no secrets; drift matches card state | Met | `architecture` tests; computed per request from cards and lines; Playwright compares the view with the canvas and checks the exports |
| F4 | Switching environment changes injected values; production protected; no cross-environment leakage | Met | `component/envprofile_test.go`; Playwright with two real Redis servers; production switch needs an explicit confirmation |
| F5 | CPU, memory, storage trends per card; latency for connected cards | Met (simulated on the fake driver) | `metrics` tests; Docker stats path written, not run live |
| F13 | Queries never expose credentials to the browser; read-only blocks writes; every query audited without results | Met (read-only) | real MariaDB, MongoDB, Redis, RabbitMQ and Kafka in `probe/inspect_it_test.go` (SQL write refusal, Mongo and Redis command allow-lists, panic recovery); Playwright write refusal is audited. **Gap 1** |
| F16 | External API card follows test-then-add and secret handling | Met | `component` tests and `externalapi.test.tsx`; the probe goes through the SSRF guard |
| F25 | Compose starts installed services only; no connected card becomes a container; no secrets in files | Met (validated, not run) | `deployexport` tests; `docker compose config` accepts the generated file; Playwright writes files and keeps existing ones. **Gap 2** |
| F17 | Agent context built from the canvas and never includes secret values | Met | `codeagent` `TestContextNeverCarriesSecretValues`; e2e `TestCodeAgentOverRealWorkspace` checks the provider's request bodies for a variable's secret value and a `.env` secret |
| F17 | Forbidden actions are rejected and recorded | Met | secret files, path escapes, `printenv`, `$SECRET` expansion, unknown tools and tools above the permission level all return `denied` and write `agent.action_denied` to the audit log (`codeagent` tests, Playwright) |
| F17 | Allowed actions follow F18's approval rules | Met | file edits only as change sets; risky commands wait for approval with the exact command; read-only and test commands run at once (`policy_test.go`, approval and decline flows in Go and Playwright) |
| F17 | The user can inspect the agent's context | Met | Settings, "Show what the agent can see"; `GET /agent/context` returns the exact text sent |
| F17 | Propose Draft cards and connections | Met | `propose_card` and `propose_connection` create proposals; accepting adds a Draft card or a confirmed line (`canvasagent_test.go`). The agent has no tool that installs, starts, deletes, or changes resources |
| F18 | No agent file change is written before acceptance unless auto-accept is on | Met | `TestProposeChangesStagesAndNeverWrites`; auto-accept is off by default (`TestAutoAcceptIsOptInAndRespectsGates`) |
| F18 | Diffs by file; accept all, part or reject | Met | hunk-level apply in `changeset/diff.go` (randomised round-trip test); the review dialog drives partial accept in Playwright |
| F18 | Checkpoint before each change set; one-click revert restores the pre-change state | Met | `TestAcceptAllAppliesAndRevertRestores`; revert refuses to overwrite later edits unless forced |
| F18 | Rejected changes leave no residue | Met | `TestRejectTouchesNothing`; a failed apply rolls back and leaves no checkpoint |
| F18 | Risky commands always need approval with the exact command | Met | classification in `codeagent/policy.go`; see the security review for what counts as risky |
| F18 | Summary lists files, components and variables, linked to highlight mode | Met | `impact` on every change set; "Highlight in architecture view" opens F2 with the components highlighted |
| F18 | Agent-authored commits are marked as such (F10) | Met | files written by accepted agent changes are tracked until committed; a commit that includes any gets an `Assisted-by: Nexmesh Code Agent` line, the Git panel says so beforehand, and the audit event records `agentAssisted` (e2e `TestAgentAuthoredCommitsAreMarked`, `git.test.tsx`). The agent itself has no commit tool |
| F19 | Explain and Diagnose on a card from logs, health and test results; failure-category explanation | Met | `assist.go` builds the request from the card's state, last connection test (with fixed per-stage hints from PRD 11.5), health history and scrubbed logs |
| F19 | The preview matches what is sent; no secret in sent context | Met | the preview is stored server side and sent verbatim as the user message (`TestAssistPreviewIsScrubbedAndSentExactly`, Playwright compares the text); `secretscan` plus exact-value masking |
| F19 | Suggested fixes never applied without approval | Met | fixes arrive as change sets |
| F21 | Rules in repository files, with an editor; global, component and path scopes | Met | `.nexmesh/rules/*.md` and a root `AGENTS.md`; `rules` tests; RulesTab |
| F21 | Rule changes take effect on the next request | Met | rules are read from the workspace on every model call (`TestEditsTakeEffectOnNextRead`; e2e edits a rule between two messages and inspects the second provider request) |
| F21 | The applied-rules list for a change set is accurate | Met | recorded at proposal time with the rule's content hash; shown in the review (`TestResolveRecordsVersionsAndWarnings`, e2e) |
| F23 | Gates for format, lint, typecheck, test, build, secret and dependency checks, detected and confirmed | Met | detection for Go, Node, Python, Rust and Makefiles (`gates/detect.go`); built-in secret scan and dependency checks; nothing counts until the user saves |
| F23 | Per-gate required or advisory; a failing required gate blocks acceptance unless overridden; overrides recorded | Met | `TestRequiredGatesBlockAgentSetsUntilPassedOrOverridden`; Playwright blocks, overrides with a reason, and finds `gates.overridden` in the audit log |
| F23 | Gates run in the workspace with timeouts and logs; on demand too | Met | gates run against the proposed change applied temporarily, then restore (also after errors and after a simulated crash); per-gate logs in the UI |
| F23 | Configuration stored with the workspace definition | Met | `definition` carries gates; imported command gates start switched off for review |
| F12 | Definition carries wires, environments, gates, rules, agent policy; drift covers them | Met | e2e `TestDefinitionCarriesPhase3State`: export has no credentials, import rebuilds, drift names each difference. Auto-accept is deliberately not exported |

## Known gaps

1. **Inspectors are read-only.** There is no opt-in write mode, and installed (container) services cannot be inspected yet.
2. **Docker is still unverified live.** The Docker driver, its stats, the generated compose bundle, the workspace image and the Windows installer have not run against a real daemon or a real Windows machine. The installer builds with `makensis` on Linux and is smoke-checked only structurally.
3. **No hosted model API was called.** Chat with tools is tested with protocol fakes for Anthropic, OpenAI-compatible, Gemini and Ollama request and response shapes. Differences in a real provider's tool-calling behaviour will only show against the real service.
4. **Snapshot restore still covers files only** (Phase 2 gap 1). A restored workspace keeps its current canvas configuration.
5. **Agent runs live in server memory.** A restart interrupts a running conversation (it is marked interrupted and can be continued by sending another message). A pending approval does not survive a restart.
6. **Gate trial runs touch the working tree.** While gates run on a proposed change, the files on disk temporarily hold the proposal and Nexmesh refuses other change-set work in that workspace. Edits you make by hand during that window can conflict. The review dialog says so, and a crash during a trial is repaired the next time the workspace's change sets are used.
7. **Static analysis of commands is heuristic.** Command classification, the path guard for secret files and credential-shaped scrubbing reduce risk but are not a sandbox. Tests and build scripts the project already contains run arbitrary project code, the same as when a person runs them.
8. **Account audit events have no UI** (Phase 2 gap 3) and a full run on PostgreSQL was not done (Phase 2 gap 4).

## Security review of the new attack surfaces

| Surface | Threat | Mitigation |
| :-- | :-- | :-- |
| Agent tool set | Agent doing more than the user allowed | Tools are fixed per permission level; unknown or above-level tools return `denied` and are audited; there is no tool for secrets, resources, install, start or delete |
| Secret files | Agent reads `.env`, keys or credentials | Path guard on read, list, search and propose (and on command arguments); hidden from listings; `.env.example` style files allowed |
| Secret values in context | Credentials leaving for the model provider | Context lists names only; tool output is masked with the workspace's secret-named variable values and scrubbed with credential patterns; command output is scrubbed too; verified by inspecting provider request bodies |
| Commands | Destructive or exfiltrating commands | Forbidden list never runs (env dumps, secret expansion, `rm -rf /`, disk tools); an allow-list of read-only, build and test commands runs at once; everything else, including redirections, substitutions, wildcards and recursive search, waits for approval showing the exact command |
| Prompt injection in files | Instructions hidden in repository text | The agent's reach is limited by the tool set, the approval rule and review of every change; nothing it proposes is applied without acceptance (unless the user turned on auto-accept, which still checkpoints and honours required gates) |
| Change sets | Path traversal, protected paths, overwriting newer edits | Paths cleaned and confined (no `..`, absolute or `.git` paths); apply refuses if a file changed since the proposal; all-or-nothing writes with rollback; checkpoint before every write; revert refuses to overwrite later edits unless forced |
| Gates | Weakening checks, running hostile commands | Configuration lives in the store, not in files the agent can edit; commands from an imported definition start disabled; builtin scans never print a matched secret |
| Rules | Rule files used to smuggle instructions | Rules are guidance only, applied by the user's own files; a rule containing credential-like text is rejected in definitions; a malformed rule is shown and not applied |
| Definition import | Hidden execution and secret smuggling | Command gates start switched off; credentials in profiles and gate commands are rejected; auto-accept is not part of the schema |
| Per-card assist | Sending more than the user saw | The preview is held server side and sent as is; single use, bound to user, workspace and card, expires after 10 minutes |
| Run limits | Cost and load | 16 steps and 10 minutes per message, 3 concurrent runs per workspace, 200 turns per conversation; runs are cancelled when a workspace is deleted |
| Inspectors | SSRF, writes, data exposure | All connections through the SSRF guard; read-only by layered checks; limits, timeouts and audit of queries without results |
| Model keys | Leakage in errors and logs | Key held in memory only for a call, registered with the scrubber; provider errors are classified and never echo the key or the conversation |

## Test inventory (at the time of writing)

- Go: about 360 top-level tests across 45 packages, all under `-race`. Real-service integration tests run with `NEXMESH_IT=1` against Redis, MariaDB, MongoDB, RabbitMQ and Kafka.
- Web unit: 189 vitest tests in 30 files.
- Playwright: 30 tests in 7 spec files (journey, code, tools, auth, wires, agent, a11y); one needs a real Redis (`NX_IT_REDIS_PORT`).
