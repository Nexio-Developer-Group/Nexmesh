#!/usr/bin/env bash
# Builds downloadable release artifacts into dist/:
#   nexmesh-<version>-linux-amd64.tar.gz, -linux-arm64.tar.gz, -darwin-*.tar.gz,
#   nexmesh-<version>-windows.zip (real Docker stack bundle), Nexmesh-Setup-<version>.exe (needs makensis),
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
  local os="$1" arch="$2" name="nexmesh-$version-$1-$2"
  local stage="$out/stage/$name"; mkdir -p "$stage/docs"
  echo "== $os/$arch =="
  (cd server && CGO_ENABLED=0 GOOS="$os" GOARCH="$arch" go build -trimpath -ldflags "-s -w -X main.version=$version" -o "$stage/nexmesh" ./cmd/nexmesh)
  if [ "$os" = linux ]; then
    (cd server && CGO_ENABLED=0 GOOS=linux GOARCH="$arch" go build -trimpath -ldflags "-s -w" -o "$stage/nexmesh-agent" ./cmd/nexmesh-agent)
  fi
  cp LICENSE README.md "$stage/"; cp docs/INSTALL.md docs/SELF_HOSTING.md "$stage/docs/"
  if [ "$os" = linux ]; then cp scripts/install-ubuntu.sh "$stage/" 2>/dev/null || true; fi
  tar -C "$out/stage" -czf "$out/$name.tar.gz" "$name"
}
# The Linux amd64 binaries are also what the Windows bundle runs, inside Docker.
build linux amd64
if [ "${ONLY_WINDOWS:-}" = 1 ]; then
  rm -f "$out/nexmesh-$version-linux-amd64.tar.gz"
else
  build linux arm64; build darwin arm64; build darwin amd64
fi

echo "== Windows bundle (real Docker stack) =="
lin="$out/stage/nexmesh-$version-linux-amd64"
win="$out/windows"; mkdir -p "$win/stack/bin" "$win/docs"
cp "$lin/nexmesh" "$lin/nexmesh-agent" "$win/stack/bin/"
cp -r catalog "$win/stack/catalog"
cp deploy/prebuilt/Dockerfile.control deploy/prebuilt/Dockerfile.workspace deploy/prebuilt/docker-compose.yml "$win/stack/"
cp installer/windows/nexmesh.ps1 "installer/windows/Start Nexmesh.cmd" "installer/windows/Stop Nexmesh.cmd" "installer/windows/Nexmesh logs.cmd" "$win/"
cp LICENSE README.md "$win/"; cp docs/INSTALL.md docs/SELF_HOSTING.md "$win/docs/"
(cd "$out" && zip -qr "nexmesh-$version-windows.zip" windows && true)

if command -v makensis >/dev/null; then
  echo "== Windows installer =="
  makensis -V2 -DVERSION="$version" -DSRCDIR="$win" -DOUTFILE="$out/Nexmesh-Setup-$version.exe" installer/windows/nexmesh.nsi
else
  echo "makensis not found: skipping the Windows installer (apt install nsis)"
fi

rm -rf "$out/stage" "$out/windows"
(cd "$out" && sha256sum * > SHA256SUMS)
ls -lh "$out"
