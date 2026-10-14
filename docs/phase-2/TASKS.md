# Phase 2 Task Sheet — Daily-use workspace

Goal (PRD 18.2): a workspace a developer can work in every day: edit, run, preview, commit, manage secrets, resume. Entry condition: Phase 1 met (see `../phase-1/ACCEPTANCE.md`), plus the three Phase 1 gaps that block daily use: **no authentication**, **the agent cannot run anything**, and **connection variables are not consumed**.

Status legend: `[ ]` todo · `[~]` in progress · `[x]` done. Each task = one commit pushed to `main`.

## M0 — Foundations
- [x] **T0.1** Accounts and sessions: argon2id passwords, HttpOnly session cookie, Origin check on unsafe methods, first user = owner, `NEXMESH_AUTH=disabled` dev mode, per-user ownership everywhere
- [x] **T0.2** Sign-in / sign-up screens (design prompt P1), auth guard, user menu with sign-out
- [x] **T0.3** Agent transport: outbound WebSocket (request/response + events) replacing HTTP heartbeats; presence, reconnect with backoff

## M1 — Workspace agent capabilities (F9 backend, F8 detection)
- [x] **T1.1** Agent file operations confined to the workspace root: list, read, write, mkdir, rename, delete, stat, search (symlink and `..` escapes rejected)
- [x] **T1.2** Agent terminals: PTY sessions that outlive the browser, scrollback buffer, resize, attach
- [x] **T1.3** Agent command execution with limits, and listening-port detection
- [x] **T1.4** Control-plane file/exec API with ownership checks and audit
- [x] **T1.5** Browser terminal WebSocket bridge (reconnectable)
- [x] **T1.6** Local agent for the fake driver so development and E2E use real files and shells

## M2 — Secrets and environment variables (F11)
- [x] **T2.1** Variable store: workspace and component scopes, secret vs plain, write-only secrets, precedence
- [x] **T2.2** Effective environment (variables + connection variables) delivered to the agent; every process the agent starts receives it
- [x] **T2.3** `.env` import and names-only export; API
- [x] **T2.4** Variables UI (read-only generated rows with their source)

## M3 — Editor, terminal and files UI (F9)
- [x] **T3.1** Workspace shell: Code view with file tree (create, rename, move, delete, search, upload, download)
- [x] **T3.2** Tabbed Monaco editor with save, dirty state, conflict detection
- [x] **T3.3** xterm.js terminals with tabs and reconnect

## M4 — Git (F10)
- [x] **T4.1** Git provider credentials as secrets (token); revoke removes them
- [x] **T4.2** Clone, status, branch, commit, pull, push, diff through the agent with credentials injected per command (never written to disk or logs)
- [x] **T4.3** Git panel UI and "open pull request" link

## M5 — Ports and previews (F8)
- [x] **T5.1** Ports panel from detection plus declared ports; per-port authenticated preview proxy
- [x] **T5.2** Expiring, revocable share links (off by default)

## M6 — Resume, reproducibility, visibility
- [ ] **T6.1** Audit log viewer with filters and CSV/JSON export (F27)
- [ ] **T6.2** Card logs and health history (F5)
- [ ] **T6.3** Idle auto-stop, snapshots and resume (F7)
- [ ] **T6.4** Stack templates (F3)
- [ ] **T6.5** Workspace definition export, import and drift report (F12)
- [ ] **T6.6** Connection import from `.env`, connection string or compose service (F14)
- [ ] **T6.7** Dev container import and export (F6)
- [ ] **T6.8** Phase 2 acceptance review, E2E additions, docs

## Notes
- Agent-side work is tested against a real local agent process (real PTY, real files, real git) in addition to the fake runtime driver.
- Anything that needs a live Docker daemon is called out in `ACCEPTANCE.md` rather than claimed.
