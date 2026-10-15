<#
Nexmesh launcher for Windows. Runs the REAL system: PostgreSQL and the Nexmesh
control plane in Docker Desktop containers, with every workspace a real Linux
container. Nothing here is simulated.

  nexmesh.ps1            start (default): set everything up on first run, then open the browser
  nexmesh.ps1 stop       stop Nexmesh (workspaces keep their data)
  nexmesh.ps1 status     show what is running
  nexmesh.ps1 logs       follow the control plane log
  nexmesh.ps1 reset      stop and DELETE all workspaces and data (asks first)
#>
param([string]$Command = 'start', [switch]$Yes)

$ErrorActionPreference = 'Stop'
$Root = Split-Path -Parent $MyInvocation.MyCommand.Path
$Stack = Join-Path $Root 'stack'
$Data = Join-Path $env:LOCALAPPDATA 'Nexmesh'
$EnvFile = Join-Path $Data '.env'
$Compose = Join-Path $Stack 'docker-compose.yml'

function Say($m) { Write-Host "==> $m" -ForegroundColor Cyan }
function Fail($m) {
  Write-Host ""
  Write-Host "Nexmesh could not start: $m" -ForegroundColor Red
  Write-Host ""
  if ($Host.Name -eq 'ConsoleHost') { Read-Host 'Press Enter to close' | Out-Null }
  exit 1
}

function Test-Docker {
  # docker info succeeds only when the engine is running.
  $old = $ErrorActionPreference; $ErrorActionPreference = 'Continue'
  & docker info *> $null
  $ok = ($LASTEXITCODE -eq 0)
  $ErrorActionPreference = $old
  return $ok
}

# Runs docker compose for the Nexmesh stack and returns its exit code. It runs
# as a child process that shares this console (no piping), because docker compose
# fails with "failed to get console" when its output is redirected. Plain progress
# output is used so it also works in a double-clicked window.
function Quote-Arg([string]$a) { if ($a -match '[\s"]') { return '"' + ($a -replace '"', '\"') + '"' } else { return $a } }
function Invoke-Compose([string[]]$ComposeArgs) {
  $all = @('compose', '--ansi', 'never', '--progress', 'plain', '--project-directory', $Stack, '-f', $Compose, '--env-file', $EnvFile) + $ComposeArgs
  $line = ($all | ForEach-Object { Quote-Arg $_ }) -join ' '
  $p = Start-Process -FilePath 'docker' -ArgumentList $line -NoNewWindow -Wait -PassThru
  return $p.ExitCode
}

function Ensure-Docker {
  if (-not (Get-Command docker -ErrorAction SilentlyContinue)) {
    Fail "Docker Desktop is not installed. Install it from https://www.docker.com/products/docker-desktop (or run: winget install -e --id Docker.DockerDesktop), restart Windows if it asks, then start Nexmesh again."
  }
  if (Test-Docker) { return }
  $exe = Join-Path $env:ProgramFiles 'Docker\Docker\Docker Desktop.exe'
  if (Test-Path $exe) {
    Say 'Starting Docker Desktop (this can take a minute)'
    Start-Process $exe
  } else {
    Fail 'Docker is installed but its engine is not running. Start Docker Desktop and try again.'
  }
  for ($i = 0; $i -lt 90; $i++) {
    Start-Sleep -Seconds 2
    if (Test-Docker) { return }
  }
  Fail 'Docker Desktop did not become ready in time. Open Docker Desktop, wait until it says it is running, then start Nexmesh again.'
}

function New-Secret($bytes) {
  $b = New-Object byte[] $bytes
  [System.Security.Cryptography.RandomNumberGenerator]::Create().GetBytes($b)
  return $b
}

function Ensure-Env {
  New-Item -ItemType Directory -Force -Path $Data | Out-Null
  if (Test-Path $EnvFile) { return }
  Say 'Generating your private keys (first run only)'
  $key = [Convert]::ToBase64String((New-Secret 32))
  $pg = -join ((New-Secret 12) | ForEach-Object { $_.ToString('x2') })
  $port = 8080
  foreach ($p in 8080..8099) {
    $busy = $false
    try {
      $l = New-Object System.Net.Sockets.TcpListener([System.Net.IPAddress]::Loopback, $p)
      $l.Start(); $l.Stop()
    } catch { $busy = $true }
    if (-not $busy) { $port = $p; break }
  }
  $lines = @(
    "NEXMESH_MASTER_KEY=$key",
    "POSTGRES_PASSWORD=$pg",
    "NEXMESH_PORT=$port",
    "NEXMESH_MAX_CPU=8",
    "NEXMESH_MAX_RAM_GB=16",
    "NEXMESH_MAX_STORAGE_GB=100"
  )
  # LF line endings, no BOM: Docker Compose reads this file.
  [System.IO.File]::WriteAllText($EnvFile, (($lines -join "`n") + "`n"), (New-Object System.Text.UTF8Encoding($false)))
}

function Get-Port {
  $m = Select-String -Path $EnvFile -Pattern '^NEXMESH_PORT=(\d+)' | Select-Object -First 1
  if ($m) { return $m.Matches[0].Groups[1].Value }
  return '8080'
}

switch ($Command.ToLower()) {
  'start' {
    Ensure-Docker
    Ensure-Env
    $port = Get-Port
    Say 'Preparing Nexmesh (the first start builds the images and takes a few minutes)'
    if ((Invoke-Compose @('up', '-d', '--build')) -ne 0) { Fail 'docker compose could not start Nexmesh. The messages above say why.' }
    Say 'Waiting for Nexmesh to answer'
    $url = "http://localhost:$port"
    $ready = $false
    for ($i = 0; $i -lt 90; $i++) {
      try {
        $r = Invoke-WebRequest -UseBasicParsing -Uri "$url/healthz" -TimeoutSec 3
        if ($r.StatusCode -eq 200) { $ready = $true; break }
      } catch { }
      Start-Sleep -Seconds 2
    }
    if (-not $ready) {
      $null = Invoke-Compose @('logs', '--tail', '40', 'nexmesh')
      Fail 'Nexmesh did not become healthy. The log above shows why.'
    }
    Write-Host ""
    Write-Host "  Nexmesh is running at $url" -ForegroundColor Green
    Write-Host "  First visit: create the owner account on the sign-in page."
    Write-Host "  Your data lives in Docker volumes and in $Data."
    Write-Host ""
    Start-Process $url
  }
  'stop' {
    if (-not (Get-Command docker -ErrorAction SilentlyContinue) -or -not (Test-Docker)) { Write-Host 'Docker is not running, so Nexmesh is not either.'; break }
    if (-not (Test-Path $EnvFile)) { Write-Host 'Nexmesh has not been started yet.'; break }
    Say 'Stopping Nexmesh (workspaces keep their data)'
    $old = $ErrorActionPreference; $ErrorActionPreference = 'Continue'
    $ws = & docker ps -q --filter label=nexmesh.managed=true
    if ($ws) { & docker stop $ws | Out-Null }
    $ErrorActionPreference = $old
    $null = Invoke-Compose @('stop')
    Write-Host 'Stopped. Start it again from the Start menu.'
  }
  'status' {
    Ensure-Docker
    if (Test-Path $EnvFile) { $null = Invoke-Compose @('ps') }
  }
  'logs' {
    Ensure-Docker
    $null = Invoke-Compose @('logs', '-f', '--tail', '100', 'nexmesh')
  }
  'reset' {
    Ensure-Docker
    $a = 'DELETE'
    if (-not $Yes) { $a = Read-Host 'This DELETES every workspace, its files and all Nexmesh data. Type DELETE to continue' }
    if ($a -ne 'DELETE') { Write-Host 'Nothing was deleted.'; break }
    if (Test-Path $EnvFile) { $null = Invoke-Compose @('down', '-v', '--remove-orphans') }
    $old = $ErrorActionPreference; $ErrorActionPreference = 'Continue'
    $ids = & docker ps -aq --filter label=nexmesh.managed=true
    if ($ids) { & docker rm -f $ids *> $null }
    $nets = & docker network ls -q --filter label=nexmesh.managed=true
    if ($nets) { & docker network rm $nets *> $null }
    $vols = & docker volume ls -q --filter label=nexmesh.managed=true
    if ($vols) { & docker volume rm -f $vols *> $null }
    $ErrorActionPreference = $old
    Remove-Item -Recurse -Force $Data -ErrorAction SilentlyContinue
    Write-Host 'Everything was deleted.'
  }
  default { Write-Host "Unknown command '$Command'. Use: start, stop, status, logs, reset." }
}
