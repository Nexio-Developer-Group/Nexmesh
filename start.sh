#!/usr/bin/env bash
# Nexmesh quick start for macOS and Linux: ./start.sh [--rebuild]
# Builds the web app once, then runs everything on http://localhost:8080.
set -euo pipefail
root="$(cd "$(dirname "$0")" && pwd)"; cd "$root"
port="${PORT:-8080}"
need() { command -v "$1" >/dev/null || { echo "Missing '$1'. $2" >&2; exit 1; }; }
need go "Install Go 1.26+ from https://go.dev/dl/"
need node "Install Node.js 22 LTS from https://nodejs.org/"
command -v pnpm >/dev/null || { corepack enable 2>/dev/null || true; command -v pnpm >/dev/null || npm install -g pnpm; }

dist="$root/server/internal/webui/dist"
if [ "${1:-}" = "--rebuild" ] || [ ! -f "$dist/index.html" ]; then
  echo "Building the web app (first run takes a minute or two)..."
  (cd web && pnpm install --frozen-lockfile && pnpm build)
  mkdir -p "$dist" && find "$dist" -mindepth 1 ! -name .gitkeep -delete
  cp -r web/dist/. "$dist/"
fi

work="${XDG_DATA_HOME:-$HOME/.local/share}/nexmesh-work"; mkdir -p "$work"
export NEXMESH_DRIVER=fake NEXMESH_STORE=memory NEXMESH_AUTH=disabled
export NEXMESH_FAKE_WORKSPACE_DIR="$work" NEXMESH_CATALOG_DIR="$root/catalog"
export NEXMESH_ADDR=":$port" NEXMESH_PUBLIC_URL="http://localhost:$port"
export NEXMESH_MASTER_KEY_FILE="$root/nexmesh-master.key"
echo "Nexmesh is starting. Open http://localhost:$port  (Ctrl+C to stop)"
cd server && exec go run ./cmd/nexmesh
