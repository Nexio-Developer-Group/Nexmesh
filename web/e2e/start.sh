#!/usr/bin/env bash
# Starts the full product for E2E tests: embedded UI, fake runtime driver,
# in-memory store, loopback egress allowed so tests can reach local fakes.
set -euo pipefail
root="$(cd "$(dirname "$0")/../.." && pwd)"
if [ ! -f "$root/server/internal/webui/dist/index.html" ] || [ "${E2E_REBUILD:-}" = "1" ]; then
  (cd "$root/web" && pnpm build)
  rm -rf "$root/server/internal/webui/dist" && mkdir -p "$root/server/internal/webui/dist"
  cp -r "$root/web/dist/." "$root/server/internal/webui/dist/"
fi
cd "$root/server"
go build -o /tmp/nexmesh-e2e${E2E_PORT:+-$E2E_PORT} ./cmd/nexmesh
touch "$root/server/internal/webui/dist/.gitkeep"
# E2E_PORT / E2E_AUTH let a second instance run with sign-in enabled (auth.spec.ts).
auth="disabled"
[ "${E2E_AUTH:-}" = "enabled" ] && auth=""
exec env NEXMESH_ALLOW_SHARE_LINKS=true NEXMESH_AUTH="$auth" NEXMESH_STORE=memory NEXMESH_DRIVER=fake NEXMESH_FAKE_DELAY_MS=150 \
  NEXMESH_CATALOG_DIR="$root/catalog" NEXMESH_MASTER_KEY_FILE=/tmp/nx-e2e-master${E2E_PORT:+-$E2E_PORT}.key NEXMESH_FAKE_WORKSPACE_DIR=/tmp/nx-e2e-ws${E2E_PORT:+-$E2E_PORT} \
  NEXMESH_DEV_ALLOW_LOOPBACK_EGRESS=true NEXMESH_ADDR=:${E2E_PORT:-18080} /tmp/nexmesh-e2e${E2E_PORT:+-$E2E_PORT}
