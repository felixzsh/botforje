#!/usr/bin/env bash
# Install Botflota as a systemd service (user by default).
#
# The binary never invokes systemd; this script is packaging: it writes a unit
# file and a secrets file, then enables and starts the service. Secrets come
# from an EnvironmentFile, never from the unit.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
UNIT_NAME="botflota.service"
MODE="user"
BIN=""
CONFIG=""
DATA_DIR=""
START=1
FORCE=0

usage() {
  cat <<'EOF'
Install Botflota as a systemd service.

usage: install-service.sh [options]

options:
  --user            install a user service (default)
  --system          install a system-wide service; needs root
  --bin PATH        binary to run (default: <repo>/bin/botflota)
  --config PATH     config file to run with
  --data-dir PATH   data directory of the system service (default: /var/lib/botflota)
  --no-start        install files only; do not enable or start the service
  --force           rewrite an existing unit and secrets file
  -h, --help        show this help
EOF
}

die() {
  printf 'install-service: %s\n' "$1" >&2
  exit 1
}

while [ $# -gt 0 ]; do
  case "$1" in
    --user) MODE="user" ;;
    --system) MODE="system" ;;
    --bin) BIN="${2:?--bin needs a path}"; shift ;;
    --config) CONFIG="${2:?--config needs a path}"; shift ;;
    --data-dir) DATA_DIR="${2:?--data-dir needs a path}"; shift ;;
    --no-start) START=0 ;;
    --force) FORCE=1 ;;
    -h|--help) usage; exit 0 ;;
    *) die "unknown option $1 (try --help)" ;;
  esac
  shift
done

# render TEMPLATE DEST BIN CONFIG writes one unit with the placeholders
# replaced.
render() {
  local template="$1" dest="$2" bin="$3" config="$4"
  sed -e "s|@BOTFLOTA_BIN@|${bin}|g" \
      -e "s|@BOTFLOTA_CONFIG@|${config}|g" \
      "$template" > "$dest"
}

# seed_secrets PATH creates a 0600 placeholder when none exists, unless --force
# asked to rewrite it.
seed_secrets() {
  local path="$1"
  if [ -e "$path" ] && [ "$FORCE" -eq 0 ]; then
    echo "kept $path"
    return
  fi
  local old_umask
  old_umask="$(umask)"
  umask 077
  cat > "$path" <<'EOF'
# Environment variables for ${VAR} references in the config.
# Keep this file 0600 and out of version control.
# Examples:
# OPENAI_API_KEY=sk-...
# BOTFLOTA_WEBHOOK_SECRET=...
EOF
  umask "$old_umask"
  chmod 600 "$path"
  echo "created $path"
}

install_user() {
  if [ "$(id -u)" -eq 0 ]; then
    echo "warning: running as root installs a service for root, not for $SUDO_USER"
  fi
  local config="${CONFIG:-${XDG_CONFIG_HOME:-$HOME/.config}/botflota/config.yml}"
  local bin="${BIN:-$ROOT/bin/botflota}"
  local unit_dir="${XDG_CONFIG_HOME:-$HOME/.config}/systemd/user"
  [ -x "$bin" ] || die "no executable at $bin; run make build first"

  mkdir -p "$unit_dir" "$(dirname "$config")"
  if [ ! -f "$config" ]; then
    "$bin" config init --config "$config"
  fi
  seed_secrets "$(dirname "$config")/secrets.env"
  render "$ROOT/packaging/systemd/botflota.user.service" \
    "$unit_dir/$UNIT_NAME" "$bin" "$config"
  echo "wrote $unit_dir/$UNIT_NAME"

  systemctl --user daemon-reload
  if [ "$START" -eq 1 ]; then
    systemctl --user enable --now "$UNIT_NAME"
    loginctl enable-linger "$USER" 2>/dev/null || \
      echo "note: run 'loginctl enable-linger $USER' to keep it running after logout"
    echo "started; check it with: systemctl --user status $UNIT_NAME"
  fi
  echo "fill $(dirname "$config")/secrets.env if the config uses \${VAR}"
}

install_system() {
  [ "$(id -u)" -eq 0 ] || die "--system needs root; run it with sudo"
  local data_dir="${DATA_DIR:-/var/lib/botflota}"
  local src="${BIN:-$ROOT/bin/botflota}"
  local bin="/usr/local/bin/botflota"
  local config="${CONFIG:-/etc/botflota/config.yml}"
  [ -x "$src" ] || die "no executable at $src; run make build first"

  if ! id -u botflota >/dev/null 2>&1; then
    useradd --system --user-group --home-dir "$data_dir" \
      --shell /usr/sbin/nologin botflota
  fi
  install -m 0755 "$src" "$bin"
  install -d -m 0750 -o botflota -g botflota "$data_dir"
  install -d -m 0750 -o botflota -g botflota "$(dirname "$config")"

  if [ ! -f "$config" ]; then
    "$bin" config init --config "$config"
    printf 'data_dir: "%s"\n' "$data_dir" >> "$config"
  fi
  seed_secrets "$(dirname "$config")/secrets.env"
  chown -R botflota:botflota "$(dirname "$config")"

  render "$ROOT/packaging/systemd/botflota.system.service" \
    "/etc/systemd/system/$UNIT_NAME" "$bin" "$config"
  echo "wrote /etc/systemd/system/$UNIT_NAME"

  systemctl daemon-reload
  if [ "$START" -eq 1 ]; then
    systemctl enable --now "$UNIT_NAME"
    echo "started; check it with: systemctl status $UNIT_NAME"
  fi
  echo "fill $(dirname "$config")/secrets.env if the config uses \${VAR}"
}

case "$MODE" in
  user) install_user ;;
  system) install_system ;;
esac
