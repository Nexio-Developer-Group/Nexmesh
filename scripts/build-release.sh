#!/usr/bin/env bash
# Builds downloadable release artifacts into dist/:
#   nexmesh-<version>-linux-amd64.tar.gz, -linux-arm64.tar.gz, -darwin-*.tar.gz,
#   nexmesh-<version>-windows-amd64.zip, Nexmesh-Setup-<version>.exe (needs makensis),
#   SHA256SUMS
# Usage: scripts/build-release.sh [version]      (default: git describe)
#        ONLY_WINDOWS=1 scripts/build-release.sh   (just the Windows zip and installer)
set -euo pipefail
root="$(cd "$(dirname "$0")/.." && pwd)"; cd "$root"
version="${1:-$(git describe --tags --always --dirty 2>/dev/null || echo dev)}"
version="${version#v}"
out="$root/dist"; rm -rf "$out"; mkdir -p "$out"

echo "== web app =="
(cd web && pnpm install --frozen-lockfile >/dev/null && pnpm build >/dev/null)
rm -rf server/internal/webui/dist && mkdir -p server/internal/webui/dist
cp -r web/dist/. server/internal/webui/dist/ && touch server/internal/webui/dist/.gitkeep
make -s embed-catalog

build() { # os arch
  local os="$1" arch="$2" ext="" name="nexmesh-$version-$1-$2"
  [ "$os" = windows ] && ext=".exe"
  local stage="$out/stage/$name"; mkdir -p "$stage/docs"
  echo "== $os/$arch =="
  (cd server && CGO_ENABLED=0 GOOS="$os" GOARCH="$arch" go build -trimpath -ldflags "-s -w -X main.version=$version" -o "$stage/nexmesh$ext" ./cmd/nexmesh)
  if [ "$os" = linux ]; then
    (cd server && CGO_ENABLED=0 GOOS=linux GOARCH="$arch" go build -trimpath -ldflags "-s -w" -o "$stage/nexmesh-agent" ./cmd/nexmesh-agent)
  fi
  cp LICENSE README.md "$stage/"; cp docs/INSTALL.md docs/SELF_HOSTING.md "$stage/docs/"
  if [ "$os" = linux ]; then cp scripts/install-ubuntu.sh "$stage/" 2>/dev/null || true; fi
  if [ "$os" = windows ]; then
    (cd "$out/stage" && zip -qr "$out/$name.zip" "$name")
  else
    tar -C "$out/stage" -czf "$out/$name.tar.gz" "$name"
  fi
}
if [ "${ONLY_WINDOWS:-}" = 1 ]; then
  build windows amd64
else
  build linux amd64; build linux arm64; build darwin arm64; build darwin amd64; build windows amd64
fi

if command -v makensis >/dev/null; then
  echo "== Windows installer =="
  win="$out/stage/nexmesh-$version-windows-amd64"
  mkdir -p "$out/windows"; cp -r "$win"/. "$out/windows/"
  makensis -V2 -DVERSION="$version" -DSRCDIR="$out/windows" -DOUTFILE="$out/Nexmesh-Setup-$version.exe" installer/windows/nexmesh.nsi
else
  echo "makensis not found: skipping the Windows installer (apt install nsis)"
fi

rm -rf "$out/stage" "$out/windows"
(cd "$out" && sha256sum * > SHA256SUMS)
ls -lh "$out"
