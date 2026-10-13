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
go build -o /tmp/nexmesh-e2e ./cmd/nexmesh
touch "$root/server/internal/webui/dist/.gitkeep"
exec env NEXMESH_STORE=memory NEXMESH_DRIVER=fake NEXMESH_FAKE_DELAY_MS=150 \
  NEXMESH_CATALOG_DIR="$root/catalog" NEXMESH_MASTER_KEY_FILE=/tmp/nx-e2e-master.key \
  NEXMESH_DEV_ALLOW_LOOPBACK_EGRESS=true NEXMESH_ADDR=:18080 /tmp/nexmesh-e2e
