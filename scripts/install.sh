#!/usr/bin/env bash
# install.sh — single-command setup for chatd (daemon + chat + chat-client).
#
# Three modes, autodetected:
#
#   1. tarball:   running from inside an extracted release tarball
#                 (chatd / chat / chat-client live next to this
#                 script). We just copy them into ~/.local/bin. No Go
#                 toolchain needed. THIS IS THE DEFAULT FOR USERS.
#
#   2. source:    running from a git checkout (../go.mod present). We
#                 build the three binaries with `go build`. Useful for
#                 contributors / dev installs.
#
#   3. download:  pass --download to fetch the latest GitHub Release
#                 tarball over the internet. Repo must be public, or
#                 set CHATD_TARBALL_URL=<direct-link> to a private
#                 asset URL you've already authenticated against.
#
# Runs as a regular user — DO NOT run with sudo. chatd is a
# systemd-USER service that lives entirely under your home directory.
#
# Environment overrides (used during initial chatd.env materialisation):
#
#   CHATD_USERNAME, CHATD_TOKEN, CHATD_RELAY_URL, CHATD_TERMINAL
#
# Other knobs:
#
#   PREFIX=$HOME/.local            install destination (default)
#   CHATD_REPO=alsoamit/chatd      GitHub slug for --download
#   CHATD_VERSION=vX.Y.Z           pin a release with --download
#   CHATD_NO_START=1               don't auto-start chatd.service
#   CHATD_TARBALL_URL=<url>        direct asset URL (private repos)

set -euo pipefail

c_red()    { printf '\033[1;31m%s\033[0m\n' "$*" >&2; }
c_green()  { printf '\033[1;32m%s\033[0m\n' "$*"; }
c_cyan()   { printf '\033[1;36m%s\033[0m\n' "$*"; }
c_yellow() { printf '\033[1;33m%s\033[0m\n' "$*"; }

if [[ "${OSTYPE:-}" != linux* ]]; then
  c_red "chatd is Linux-only (OSTYPE=$OSTYPE)"; exit 1
fi
if [[ "$(id -u)" == "0" ]]; then
  c_red "run install.sh as a normal user, not root."
  c_red "chatd is a systemd-user service that installs into your \$HOME."
  exit 1
fi

PREFIX="${PREFIX:-$HOME/.local}"
BIN_DIR="$PREFIX/bin"
CFG_DIR="${XDG_CONFIG_HOME:-$HOME/.config}/chatd"
DATA_DIR="${XDG_DATA_HOME:-$HOME/.local/share}/chatd"
SYSTEMD_DIR="${XDG_CONFIG_HOME:-$HOME/.config}/systemd/user"

REPO="${CHATD_REPO:-alsoamit/chatd}"
VERSION="${CHATD_VERSION:-}"
MODE="auto"

while [[ $# -gt 0 ]]; do
  case "$1" in
    --download) MODE="download"; shift ;;
    --source)   MODE="source"; shift ;;
    --tarball)  MODE="tarball"; shift ;;
    --version)  VERSION="$2"; shift 2 ;;
    --repo)     REPO="$2"; shift 2 ;;
    --prefix)   PREFIX="$2"; BIN_DIR="$PREFIX/bin"; shift 2 ;;
    --help|-h)
      sed -n '2,40p' "$0" | sed 's/^# \{0,1\}//'
      exit 0 ;;
    *) c_red "unknown arg: $1"; exit 2 ;;
  esac
done

mkdir -p "$BIN_DIR" "$CFG_DIR" "$DATA_DIR" "$SYSTEMD_DIR"

# Locate the script's own directory (and thus a colocated tarball, if any).
script_dir="$(cd "$(dirname "${BASH_SOURCE[0]:-$0}")" >/dev/null 2>&1 && pwd)"

# --- Resolve mode -----------------------------------------------------
if [[ "$MODE" == "auto" ]]; then
  if [[ -x "$script_dir/chatd" && -x "$script_dir/chat" && -x "$script_dir/chat-client" ]]; then
    MODE="tarball"
  elif [[ -f "$script_dir/../go.mod" ]]; then
    MODE="source"
  else
    c_red "no colocated binaries and no source checkout. Pass --download."
    exit 1
  fi
fi
c_cyan "mode: $MODE"

# --- Acquire binaries -------------------------------------------------
case "$MODE" in
  tarball)
    src_dir="$script_dir"
    ;;
  source)
    if ! command -v go >/dev/null; then
      c_red "go toolchain required for --source mode (https://go.dev/dl/)"
      exit 1
    fi
    src_root="$(cd "$script_dir/.." && pwd)"
    c_cyan "building binaries from $src_root..."
    src_dir="$(mktemp -d)"
    trap 'rm -rf "$src_dir"' EXIT
    ( cd "$src_root" && \
      CGO_ENABLED=0 go build -trimpath -o "$src_dir/chatd"       ./cmd/daemon && \
      CGO_ENABLED=0 go build -trimpath -o "$src_dir/chat"        ./cmd/chat && \
      CGO_ENABLED=0 go build -trimpath -o "$src_dir/chat-client" ./cmd/chat-client )
    cp "$src_root/systemd/chatd.service" "$src_dir/chatd.service"
    [[ -f "$src_root/.env.example" ]] && cp "$src_root/.env.example" "$src_dir/.env.example"
    ;;
  download)
    arch="$(uname -m)"
    case "$arch" in
      x86_64|amd64)  goarch="amd64" ;;
      aarch64|arm64) goarch="arm64" ;;
      *) c_red "unsupported arch: $arch"; exit 1 ;;
    esac
    work="$(mktemp -d)"
    trap 'rm -rf "$work"' EXIT

    if [[ -n "${CHATD_TARBALL_URL:-}" ]]; then
      url="$CHATD_TARBALL_URL"
      asset="$(basename "$url")"
    else
      if [[ -z "$VERSION" ]]; then
        api="https://api.github.com/repos/$REPO/releases/latest"
        c_cyan "resolving latest release from $api"
        VERSION="$(curl -fsSL "$api" \
          | grep -oE '"tag_name":[[:space:]]*"[^"]+"' \
          | head -1 | sed -E 's/.*"([^"]+)".*/\1/')"
        [[ -n "$VERSION" ]] || { c_red "could not resolve version (private repo? set CHATD_TARBALL_URL)"; exit 1; }
      fi
      asset="chatd-${VERSION}-linux-${goarch}.tar.gz"
      url="https://github.com/$REPO/releases/download/$VERSION/$asset"
    fi
    c_cyan "downloading $url"
    curl -fsSL --retry 3 -o "$work/$asset" "$url"

    sums_url="${url%/*}/SHA256SUMS"
    if curl -fsSL -o "$work/SHA256SUMS" "$sums_url" 2>/dev/null; then
      c_cyan "verifying checksum"
      ( cd "$work" && grep "  $asset\$" SHA256SUMS | sha256sum -c - )
    else
      c_yellow "no SHA256SUMS published — skipping checksum verification"
    fi

    tar -C "$work" -xzf "$work/$asset"
    src_dir="$work/$(basename "$asset" .tar.gz)"
    ;;
esac

bin_chatd="$src_dir/chatd"
bin_chat="$src_dir/chat"
bin_client="$src_dir/chat-client"
unit_src="$src_dir/chatd.service"
env_src="$src_dir/.env.example"

for f in "$bin_chatd" "$bin_chat" "$bin_client"; do
  [[ -x "$f" ]] || { c_red "missing or non-executable: $f"; exit 1; }
done
[[ -f "$unit_src" ]] || { c_red "missing systemd unit: $unit_src"; exit 1; }

# --- Install ----------------------------------------------------------
install -m 0755 "$bin_chatd"  "$BIN_DIR/chatd"
install -m 0755 "$bin_chat"   "$BIN_DIR/chat"
install -m 0755 "$bin_client" "$BIN_DIR/chat-client"
c_green "installed binaries to $BIN_DIR"

if "$BIN_DIR/chatd" --version >/dev/null 2>&1; then
  c_cyan "$($BIN_DIR/chatd --version)"
fi

# 4. Ghostty / terminal emulator (best-effort)
if ! command -v ghostty >/dev/null; then
  if command -v kitty >/dev/null \
    || command -v alacritty >/dev/null \
    || command -v foot >/dev/null \
    || command -v xterm >/dev/null; then
    c_yellow "ghostty not found; chatd will use the next available terminal."
  else
    c_yellow "no terminal emulator detected. install ghostty (https://ghostty.org/download)"
    c_yellow "or 'sudo apt install xterm' for popup conversation windows."
    c_yellow "without one, chatd still works — use 'chat dashboard' inline."
  fi
fi

# 5. chatd.env (only if missing)
ENV_FILE="$CFG_DIR/chatd.env"
if [[ ! -f "$ENV_FILE" ]]; then
  cat > "$ENV_FILE" <<EOF
CHATD_USERNAME=${CHATD_USERNAME:-$(whoami)}
CHATD_TOKEN=${CHATD_TOKEN:-CHANGE_ME}
CHATD_RELAY_URL=${CHATD_RELAY_URL:-ws://127.0.0.1:7878/ws}
${CHATD_TERMINAL:+CHATD_TERMINAL=$CHATD_TERMINAL}
EOF
  chmod 0600 "$ENV_FILE"
  c_green "wrote $ENV_FILE"
  if [[ "${CHATD_TOKEN:-}" == "" ]]; then
    c_yellow "edit $ENV_FILE — set CHATD_TOKEN and CHATD_RELAY_URL before chatd will be useful."
  fi
else
  c_cyan "keeping existing $ENV_FILE"
fi

# 6. systemd-user unit
UNIT_DST="$SYSTEMD_DIR/chatd.service"
sed "s|%h/.local/bin/chatd|$BIN_DIR/chatd|" "$unit_src" > "$UNIT_DST"
c_green "installed systemd unit $UNIT_DST"

# 7. Enable + start
if command -v systemctl >/dev/null && [[ "${CHATD_NO_START:-0}" != "1" ]]; then
  systemctl --user daemon-reload || true
  systemctl --user enable chatd.service >/dev/null 2>&1 || true
  systemctl --user restart chatd.service || true
  c_green "chatd.service started"
  c_cyan  "  status: systemctl --user status chatd"
  c_cyan  "  logs:   journalctl --user -u chatd.service -f"
fi

# 8. PATH check
case ":$PATH:" in
  *":$BIN_DIR:"*) ;;
  *) c_yellow "$BIN_DIR is not in PATH. add to ~/.bashrc:"
     c_yellow "    export PATH=\"$BIN_DIR:\$PATH\"" ;;
esac

c_green "all set. open the dashboard with: chat"
