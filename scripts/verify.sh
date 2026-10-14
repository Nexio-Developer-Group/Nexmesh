#!/usr/bin/env bash
# Runs every fast check a change must pass: Go vet + race tests, web unit
# tests, and the production web build. Set NEXMESH_TEST_DATABASE_URL to also
# run the store contract against PostgreSQL. Exits non-zero on the first failure.
set -euo pipefail
root="$(cd "$(dirname "$0")/.." && pwd)"

echo "== go vet + test =="
(cd "$root/server" && go vet ./... && go test -race -count=1 ./... 2>&1 | grep -v "no test files")

echo "== web unit tests =="
(cd "$root/web" && pnpm vitest run 2>&1 | tail -6)

echo "== web build =="
(cd "$root/web" && pnpm build >/tmp/nexmesh-verify-build.log 2>&1 || { tail -30 /tmp/nexmesh-verify-build.log; exit 1; })
if grep -q "error TS" /tmp/nexmesh-verify-build.log; then grep "error TS" /tmp/nexmesh-verify-build.log; exit 1; fi
echo "ok"
