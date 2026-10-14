# Phase 2 acceptance review

Walks the Phase 2 features (F3, F5, F6, F7, F8, F9, F10, F11, F12, F14, F27) against what is built and tested. **Fake driver** means the workspace runtime was the in-process local agent against a real directory (`NEXMESH_DRIVER=fake` with `NEXMESH_FAKE_WORKSPACE_DIR`): real files, PTYs, processes, git and TCP, but no container.

```sh
scripts/verify.sh                       # go vet, race tests, vitest, web build
cd web && pnpm exec playwright test     # browser journeys against the embedded UI (two servers: auth on/off)
make acceptance                         # in-process walk-throughs (server/internal/e2e)
```

## Criteria

| Feature | Criterion | Status | Evidence |
| :-- | :-- | :-- | :-- |
| F9 | Edit files in a browser editor, with a persistent terminal | Met (fake driver) | Monaco edit/save and xterm reattach with offset replay driven in Playwright (`code.spec.ts`); `agent/{workfs,terminal}` tests; files confined with `os.Root` (traversal and symlink-escape tests) |
| F11 | Plain variables and write-only secrets, with precedence | Met | `envvars` tests; precedence generated < workspace < component; secrets never returned, scrubbed from logs and audit; agent fetches env per process start |
| F10 | Clone, status, commit, push, pull with credentials that are never persisted | Met | Real smart-HTTP git server in Go tests: token injected through env, scrubbed from output, absent from `.git/config`; Git panel in Playwright (`tools.spec.ts`) |
| F8 | Detected and declared ports, authenticated preview | Met | `/proc` detection tests; path-mode preview is sandboxed (CSP `sandbox`, no `allow-same-origin`); subdomain mode with HMAC bootstrap, host-only cookie, WebSocket proxying and Nexmesh cookie stripping (`preview` tests) |
| F8 | Share links off by default, expiring, revocable | Met | Hashed secrets, 5 min to 7 days, limit 20, revocation effective on the next request, disabled flag kills all links; Playwright covers create, visit and revoke |
| F7 | Idle auto-stop with a reason and an audit event; busy agent blocks it | Met | `idle`, `workspace/idle_test.go` (timeout, activity reset, busy block, disabled) |
| F7 | Snapshots (max 5), restore only when stopped and confirmed | Met (files) | `snapfs` real tar.gz with safe extraction; Playwright restore flow. **Gap 1** |
| F7 | Connected components reconnect on resume and report observed state | Met | `workspace.WithOnStart` runs `component.HealthForWorkspace` after a start; `TestOnStartHookRunsWhenWorkspaceResumes` |
| F5 | Card logs (scrubbed) and health history | Met | `Driver.ServiceLogs` through the secrets scrubber; 500-sample health retention in the store contract |
| F3 | Curated and personal stack templates; applying only adds Draft cards | Met | YAML schema validation of curated templates; `stacks` tests; Playwright |
| F12 | Secret-free definition export, import, drift report | Met | `definition` tests (no secret values in output, round trip, drift), e2e `definition_test.go` |
| F14 | Import connections from `.env`, a connection string or compose | Met | `connimport` tests; secrets held only in browser memory (zustand), never persisted |
| F6 | Dev container import with a per-field report; export | Met | `devcontainer` tests; export validated against the official `devContainer.base.schema.json` (with a negative control); post-create commands opt-in. The `devcontainer` CLI itself was **not** run |
| F27 | Audit viewer with filters, paging, CSV export | Met | `audit/query_test.go`; CSV formula neutralisation tested; Playwright |
| F27 | Account actions are audited | Met | `auth.register`, `auth.login`, `auth.login_failed`, `auth.logout`, never with passwords (`TestAccountEventsAreAudited`). These have an empty workspace id, so **Gap 3** |
| Auth | Sign-in required, first user owns, CSRF-safe | Met | `httpapi/auth_test.go`; Playwright `auth.spec.ts`; accessibility checks with axe (`a11y.spec.ts`) |

## Known gaps

1. **Snapshots restore files only.** The PRD also asks that "installed-component configuration" return to the snapshot state. Component desired state, versions and variables are not captured. Planned approach: store a secret-free definition beside each snapshot and apply it on restore (re-adding missing cards as Draft, reporting differences).
2. **No live Docker run** (carried over from Phase 1). The Docker driver, the snapshot helper container, the workspace image and the compose bundle have not run against a real daemon. Everything above was verified with the in-process agent.
3. **Account audit events have no UI.** The viewer is per workspace; they are stored with an empty workspace id.
4. **Postgres-backed full app run.** Store contract tests run against PostgreSQL when `NEXMESH_TEST_DATABASE_URL` is set; a whole-app run on Postgres was not done.
5. Hosted model providers, CI on GitHub Actions: unchanged from Phase 1.

## Security review of the new attack surfaces

| Surface | Threat | Mitigation |
| :-- | :-- | :-- |
| Sessions | Token theft, CSRF | SHA-256-hashed tokens, HttpOnly SameSite=Lax cookie, Origin check on unsafe methods and upgrades, argon2id, login rate limit |
| `NEXMESH_AUTH=disabled` | Accidental exposure | Loud startup warning; documented as development and test only |
| Files API | Path traversal, symlink escape | Confined with `os.Root` inside the agent |
| Preview (path mode) | Workspace app attacking Nexmesh | CSP `sandbox` without `allow-same-origin`: previewed pages get an opaque origin and cannot use Nexmesh cookies |
| Preview (subdomain) | Cookie leakage, token replay | Separate origin per port, host-only `nx_pv` cookie, Nexmesh cookies stripped upstream |
| Share links | Guessing, lingering access | High-entropy secrets stored hashed, TTL, revocation, off by default, count limit |
| Tunnel | Access to arbitrary host ports | Only the workspace's own loopback, through the authenticated agent link |
| Git credentials | Token leakage | Env-injected per command, scrubbed from output, never written to `.git/config` |
| Snapshot restore | Tar path traversal, accidental data loss | Safe extraction (no absolute paths, `..`, or links escaping the root), stopped-only, explicit confirm |
| Import (definition, dev container, compose) | Hidden execution, secret smuggling | Imports only add Draft cards; post-create commands are opt-in and shown first; secrets from imports stay in browser memory |
| CSV export | Formula injection | Cells starting with `= + - @` are neutralised |
| Audit and logs | Secrets in output | Central scrubbing through the secrets service |

## Test inventory (at the time of writing)

- Go: about 237 top-level tests across 46 packages, all under `-race`.
- Web unit: 149 vitest tests in 25 files.
- Playwright: 20 tests in 5 spec files (journey, code, tools, auth, a11y).
