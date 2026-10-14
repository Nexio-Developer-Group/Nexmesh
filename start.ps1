# Nexmesh quick start for Windows. Run from the repository root:
#   .\start.cmd            (or)   powershell -ExecutionPolicy Bypass -File .\start.ps1
# Builds the web app once, then runs everything on http://localhost:8080.
param([switch]$Rebuild, [int]$Port = 8080)
$ErrorActionPreference = 'Continue'
$root = $PSScriptRoot
Set-Location $root

function Need($cmd, $hint) {
  if (-not (Get-Command $cmd -ErrorAction SilentlyContinue)) { Write-Host "Missing '$cmd'. $hint" -ForegroundColor Red; exit 1 }
}
Need go   'Install Go from https://go.dev/dl/ (1.26 or newer), then reopen this window.'
Need node 'Install Node.js 22 LTS from https://nodejs.org/, then reopen this window.'

if (-not (Get-Command pnpm -ErrorAction SilentlyContinue)) {
  Write-Host 'Enabling pnpm...'
  corepack enable
  if (-not (Get-Command pnpm -ErrorAction SilentlyContinue)) { npm install -g pnpm }
}

$dist = Join-Path $root 'server\internal\webui\dist'
if ($Rebuild -or -not (Test-Path (Join-Path $dist 'index.html'))) {
  Write-Host 'Building the web app (first run takes a minute or two)...'
  Push-Location web
  pnpm install --frozen-lockfile; if ($LASTEXITCODE) { exit 1 }
  pnpm build;                     if ($LASTEXITCODE) { exit 1 }
  Pop-Location
  if (Test-Path $dist) { Get-ChildItem $dist -Force | Where-Object Name -ne '.gitkeep' | Remove-Item -Recurse -Force }
  New-Item -ItemType Directory -Force -Path $dist | Out-Null
  Copy-Item -Recurse -Force (Join-Path $root 'web\dist\*') $dist
}

$work = Join-Path $env:LOCALAPPDATA 'nexmesh-work'
New-Item -ItemType Directory -Force -Path $work | Out-Null
$env:NEXMESH_DRIVER = 'fake'
$env:NEXMESH_STORE = 'memory'
$env:NEXMESH_AUTH = 'disabled'
$env:NEXMESH_FAKE_WORKSPACE_DIR = $work
$env:NEXMESH_CATALOG_DIR = Join-Path $root 'catalog'
$env:NEXMESH_ADDR = ":$Port"
$env:NEXMESH_PUBLIC_URL = "http://localhost:$Port"
$env:NEXMESH_MASTER_KEY_FILE = Join-Path $root 'nexmesh-master.key'

Write-Host "Nexmesh is starting. Open http://localhost:${Port}  (Ctrl+C to stop)" -ForegroundColor Green
$url = "http://localhost:$Port"
Start-Job -ArgumentList $url -ScriptBlock { param($u) Start-Sleep 5; Start-Process $u } | Out-Null
Set-Location (Join-Path $root 'server')
go run ./cmd/nexmesh
