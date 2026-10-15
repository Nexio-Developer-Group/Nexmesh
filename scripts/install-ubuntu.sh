#!/usr/bin/env bash
# Nexmesh installer for Ubuntu / Debian. One command, run as root (or with sudo):
#
#   curl -fsSL https://raw.githubusercontent.com/nexio-developer-group/nexmesh/main/scripts/install-ubuntu.sh | sudo bash
#   # or, from a checkout:   sudo scripts/install-ubuntu.sh
#
# Default ("docker" mode): installs Docker if missing, fetches Nexmesh, generates secrets,
# builds and starts PostgreSQL + Nexmesh in containers, and waits until it answers.
# Workspaces run in real containers. Re-running upgrades in place.
#
# Options:
#   --dir PATH      install location                       (default /opt/nexmesh)
#   --port N        port to publish                        (default 8080)
#   --ref NAME      git branch or tag to install           (default main)
#   --repo URL      git repository                         (default the official one)
#   --uninstall     stop and remove the service; add --purge to delete data (database, keys)
#   -h, --help
set -euo pipefail

REPO="https://github.com/nexio-developer-group/nexmesh.git"
REF="main"; DIR="/opt/nexmesh"; PORT="8080"; UNINSTALL=0; PURGE=0

say()  { printf '\033[1;36m==>\033[0m %s\n' "$*"; }
warn() { printf '\033[1;33m!!\033[0m  %s\n' "$*" >&2; }
die()  { printf '\033[1;31mxx\033[0m  %s\n' "$*" >&2; exit 1; }

while [ $# -gt 0 ]; do
  case "$1" in
    --dir) DIR="${2:?--dir needs a path}"; shift ;;
    --port) PORT="${2:?--port needs a number}"; shift ;;
    --ref) REF="${2:?--ref needs a name}"; shift ;;
    --repo) REPO="${2:?--repo needs a URL}"; shift ;;
    --uninstall) UNINSTALL=1 ;;
    --purge) PURGE=1 ;;
    -h|--help) sed -n '2,22p' "$0" | sed 's/^# \{0,1\}//'; exit 0 ;;
    *) die "unknown option: $1 (try --help)" ;;
  esac
  shift
done
case "$PORT" in ''|*[!0-9]*) die "--port must be a number" ;; esac

# --- root -------------------------------------------------------------------
if [ "$(id -u)" -ne 0 ]; then
  if [ -f "$0" ] && command -v sudo >/dev/null; then
    say "Re-running with sudo"; exec sudo -E bash "$0" "$@"
  fi
  die "run as root:  curl -fsSL <url> | sudo bash"
fi

. /etc/os-release 2>/dev/null || true
case "${ID:-}${ID_LIKE:-}" in
  *ubuntu*|*debian*) ;;
  *) warn "this script is written for Ubuntu/Debian (found: ${PRETTY_NAME:-unknown}); continuing anyway" ;;
esac
export DEBIAN_FRONTEND=noninteractive
have() { command -v "$1" >/dev/null 2>&1; }
compose() { docker compose -f "$DIR/deploy/docker-compose.yml" --env-file "$DIR/deploy/.env" "$@"; }

# --- uninstall --------------------------------------------------------------
if [ "$UNINSTALL" = 1 ]; then
  say "Stopping Nexmesh"
  if [ -f "$DIR/deploy/docker-compose.yml" ] && have docker; then
    if [ "$PURGE" = 1 ]; then compose down -v --remove-orphans || true; else compose down --remove-orphans || true; fi
  fi
  if have systemctl && [ -f /etc/systemd/system/nexmesh.service ]; then
    systemctl disable --now nexmesh 2>/dev/null || true; rm -f /etc/systemd/system/nexmesh.service; systemctl daemon-reload
  fi
  if have docker; then
    ids="$(docker ps -aq --filter label=nexmesh.managed=true || true)"
    [ -n "$ids" ] && warn "workspace containers still exist (docker ps -a --filter label=nexmesh.managed=true); remove with: docker rm -f \$(docker ps -aq --filter label=nexmesh.managed=true)"
  fi
  if [ "$PURGE" = 1 ]; then rm -rf "$DIR" /var/lib/nexmesh; say "Removed $DIR and all data"; else say "Removed the service. Files and data kept in $DIR (add --purge to delete)"; fi
  exit 0
fi

# --- base packages ----------------------------------------------------------
say "Installing base packages"
apt-get update -qq
apt-get install -y -qq --no-install-recommends curl ca-certificates git openssl tar xz-utils make >/dev/null

# --- get the source ---------------------------------------------------------
script_dir="$(cd "$(dirname "${BASH_SOURCE[0]:-$0}")" 2>/dev/null && pwd || true)"
if [ -n "$script_dir" ] && [ -f "$script_dir/../deploy/docker-compose.yml" ] && [ "$DIR" = "/opt/nexmesh" ]; then
  DIR="$(cd "$script_dir/.." && pwd)"; say "Using the checkout at $DIR"
elif [ -d "$DIR/.git" ]; then
  say "Updating $DIR to $REF"; git -C "$DIR" fetch -q --depth 1 origin "$REF" && git -C "$DIR" checkout -q -B "$REF" FETCH_HEAD
else
  say "Downloading Nexmesh ($REF) to $DIR"
  mkdir -p "$(dirname "$DIR")"; git clone -q --depth 1 --branch "$REF" "$REPO" "$DIR"
fi

# --- helpers ----------------------------------------------------------------
host_ip() { hostname -I 2>/dev/null | awk '{print $1}' || true; }
wait_healthy() {
  local url="$1" i
  for i in $(seq 1 90); do
    curl -fsS "$url/healthz" >/dev/null 2>&1 && return 0
    sleep 2
  done
  return 1
}
set_env() { # file key value
  if grep -q "^$2=" "$1"; then sed -i "s|^$2=.*|$2=$3|" "$1"; else echo "$2=$3" >> "$1"; fi
}

# =============================================================================
if true; then
  if ! have docker; then
    say "Installing Docker Engine (get.docker.com)"
    curl -fsSL https://get.docker.com | sh
  fi
  have systemctl && systemctl enable --now docker >/dev/null 2>&1 || true
  docker info >/dev/null 2>&1 || die "the Docker daemon is not running (try: systemctl start docker)"
  if ! docker compose version >/dev/null 2>&1; then
    say "Installing the Docker Compose plugin"; apt-get install -y -qq docker-compose-plugin >/dev/null || die "could not install docker-compose-plugin"
  fi

  env_file="$DIR/deploy/.env"
  if [ ! -f "$env_file" ]; then
    say "Generating secrets"
    cp "$DIR/deploy/.env.example" "$env_file"
    set_env "$env_file" NEXMESH_MASTER_KEY "$(openssl rand -base64 32)"
    set_env "$env_file" POSTGRES_PASSWORD "$(openssl rand -hex 16)"
    chmod 600 "$env_file"
  else
    say "Keeping existing configuration in $env_file"
  fi
  set_env "$env_file" NEXMESH_PORT "$PORT"
  set_env "$env_file" NEXMESH_PUBLIC_URL "http://host.docker.internal:$PORT"

  if [ -f "$DIR/deploy/workspace/Dockerfile" ]; then
    say "Building the workspace image (first time takes a few minutes)"
    compose --profile images build workspace-image
    set_env "$env_file" NEXMESH_WORKSPACE_IMAGE "nexmesh/workspace:local"
  fi

  say "Building and starting Nexmesh (first time takes a few minutes)"
  compose up -d --build
  say "Waiting for Nexmesh to answer"
  if ! wait_healthy "http://127.0.0.1:$PORT"; then
    compose ps || true; compose logs --tail 40 nexmesh || true
    die "Nexmesh did not become healthy; see the log above"
  fi
  ip="$(host_ip)"
  cat <<EOF

  Nexmesh is running.

    Open:        http://${ip:-localhost}:$PORT        (or http://localhost:$PORT on this machine)
    First step:  create the owner account on the sign-in page. That account owns the instance.
    Config:      $env_file      (back up NEXMESH_MASTER_KEY: stored credentials need it)
    Logs:        docker compose -f $DIR/deploy/docker-compose.yml logs -f nexmesh
    Upgrade:     sudo $DIR/scripts/install-ubuntu.sh
    Uninstall:   sudo $DIR/scripts/install-ubuntu.sh --uninstall   (--purge also deletes data)

  Reaching it from other machines: open port $PORT in your firewall (ufw allow $PORT/tcp)
  and put TLS in front of it before exposing it to the internet (see docs/SELF_HOSTING.md).
  The control plane holds the Docker socket, which is root-equivalent on this host.

EOF
  exit 0
fi
