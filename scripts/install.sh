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

# tty for interactive prompts. When the script is run via `curl ... | bash`
# stdin is the pipe (closed for input); we read straight from the
# controlling terminal instead so prompts work either way. If neither
# is available (unattended runs, CI, sandboxed shells), the function
# returns the empty string and the caller is expected to use defaults
# from the environment.
read_tty() {
  local prompt="$1" __out=""
  # Probe /dev/tty in a subshell so a failed open's error message goes
  # to /dev/null. Then do the real read in the main shell with stderr
  # untouched, so bash's `read -p` actually shows the prompt to the
  # user.
  if ( : </dev/tty ) 2>/dev/null; then
    read -r -p "$prompt" __out </dev/tty || __out=""
  elif [[ -t 0 ]]; then
    read -r -p "$prompt" __out || __out=""
  fi
  printf '%s' "$__out"
}

have_tty() {
  ( exec </dev/tty ) 2>/dev/null && return 0
  [[ -t 0 ]]
}

# Read a single key from a KEY=VAL .env file. Empty if missing.
env_value() {
  local file="$1" key="$2"
  [[ -f "$file" ]] || { printf ''; return 0; }
  awk -F= -v k="$key" '
    $0 ~ "^[[:space:]]*"k"=" {
      sub("^[[:space:]]*"k"=", "");
      gsub(/^["'\'']|["'\'']$/, "");
      print; exit
    }' "$file"
}

# Detect a currently-installed chatd version, if any.
existing_version() {
  local cand=""
  if [[ -x "$BIN_DIR/chatd" ]]; then
    cand="$BIN_DIR/chatd"
  elif command -v chatd >/dev/null 2>&1; then
    cand="$(command -v chatd)"
  fi
  if [[ -n "$cand" ]]; then
    "$cand" --version 2>/dev/null | awk 'NR==1 {print $2}'
  fi
}

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

# Tracks whether the user explicitly asked for a same-version reinstall,
# so we know to re-prompt for username/URL even when an env file exists.
reinstall=0

# --- Server-mode prompt -----------------------------------------------
# Headless servers can't pop GUI windows. CHATD_SERVER=1|0 in the env
# bypasses the prompt; otherwise we ask once and remember the answer.
if [[ -n "${CHATD_SERVER:-}" ]]; then
  is_server="$CHATD_SERVER"
else
  ans="$(read_tty 'Is this a headless server install (no popup windows)? [y/N]: ')"
  case "${ans:-N}" in
    [yY]*) is_server=1 ;;
    *)     is_server=0 ;;
  esac
fi
if [[ "$is_server" == "1" ]]; then
  c_cyan "server mode — terminal emulator setup will be skipped"
fi

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

    # If chatd is already installed, confirm the update before doing
    # anything destructive. The user can decline and we exit clean.
    current="$(existing_version || true)"
    if [[ -n "$current" ]]; then
      if [[ "$current" == "$VERSION" ]]; then
        c_cyan "chatd $current is already installed."
        ans="$(read_tty "Reinstall the same version? [y/N]: ")"
        case "${ans:-N}" in
          [yY]*) reinstall=1 ;;
          *) c_cyan "nothing to do."; exit 0 ;;
        esac
      else
        c_cyan "chatd $current is installed; latest is $VERSION."
        ans="$(read_tty "Update $current → $VERSION? [Y/n]: ")"
        case "${ans:-Y}" in
          [yY]*|"") ;;
          *) c_cyan "skipping update."; exit 0 ;;
        esac
      fi
      if command -v systemctl >/dev/null; then
        systemctl --user stop chatd.service 2>/dev/null || true
      fi
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

# 4. Terminal emulator handling.
#
# On a desktop machine we want a popup-capable emulator. Look for one of
# the supported list; if none are found, offer to apt-install kitty
# (modern, X11+Wayland, no fuss). On a server install (CHATD_SERVER=1
# or "yes" to the prompt above) we skip this step entirely — chatd
# falls back to the headless launcher and `chat dashboard` works inline.
detect_emulator() {
  for c in ghostty kitty alacritty foot xterm; do
    if command -v "$c" >/dev/null 2>&1; then
      printf '%s' "$c"
      return 0
    fi
  done
  return 1
}

if [[ "$is_server" == "1" ]]; then
  c_cyan "skipping terminal emulator setup (server install)"
else
  if found="$(detect_emulator)"; then
    c_green "using terminal: $found"
  else
    c_yellow "no supported terminal emulator detected on \$PATH."
    c_yellow "without one, popup dashboard / conversation windows won't open."
    if command -v apt-get >/dev/null 2>&1; then
      ans="$(read_tty 'Install kitty via apt now? [Y/n]: ')"
      case "${ans:-Y}" in
        [yY]*|"")
          c_cyan "running: sudo apt-get install -y kitty"
          if sudo apt-get install -y kitty; then
            c_green "kitty installed: $(command -v kitty)"
          else
            c_red "kitty install failed — run 'sudo apt install kitty' yourself, or pick another emulator."
          fi
          ;;
        *)
          c_cyan "skipping. you can run 'chat dashboard' inline, or install one of:"
          c_cyan "  ghostty / kitty / alacritty / foot / xterm"
          ;;
      esac
    else
      c_yellow "no apt-get on this system. install one manually:"
      c_yellow "  ghostty / kitty / alacritty / foot / xterm"
    fi
  fi
fi

# 5. chatd.env
#
# Three flows:
#   - fresh install (no env file): prompt for everything, defaults to
#     $(whoami) for username, no default for URL.
#   - update (env file exists, reinstall=0): preserve as-is.
#   - reinstall (same version, user confirmed): re-prompt with the
#     CURRENT values as defaults, so pressing Enter keeps them.
#
# Env-var overrides (CHATD_USERNAME / CHATD_RELAY_URL / CHATD_TOKEN /
# CHATD_TERMINAL) skip the matching prompt at any time.
ENV_FILE="$CFG_DIR/chatd.env"

write_env=0
if [[ ! -f "$ENV_FILE" ]]; then
  write_env=1
elif [[ "$reinstall" == "1" ]]; then
  write_env=1
  c_cyan "reinstall: re-running config (press Enter to keep current values)"
fi

if [[ "$write_env" == "1" ]]; then
  cur_user="$(env_value "$ENV_FILE" CHATD_USERNAME)"
  cur_url="$(env_value  "$ENV_FILE" CHATD_RELAY_URL)"
  cur_tok="$(env_value  "$ENV_FILE" CHATD_TOKEN)"
  cur_term="$(env_value "$ENV_FILE" CHATD_TERMINAL)"

  # Username
  if [[ -n "${CHATD_USERNAME:-}" ]]; then
    user="$CHATD_USERNAME"
  else
    default_user="${cur_user:-$(whoami)}"
    user_in="$(read_tty "Username [$default_user]: ")"
    user="${user_in:-$default_user}"
  fi

  # Relay URL
  if [[ -n "${CHATD_RELAY_URL:-}" ]]; then
    url="$CHATD_RELAY_URL"
  elif have_tty; then
    while :; do
      if [[ -n "$cur_url" ]]; then
        url_in="$(read_tty "Relay URL [$cur_url]: ")"
        url="${url_in:-$cur_url}"
      else
        url="$(read_tty 'Relay URL (http://host:port or https://domain) [required]: ')"
      fi
      if [[ -z "$url" ]]; then
        c_red "relay URL is required — installation cancelled."
        exit 1
      fi
      case "$url" in
        http://*|https://*|ws://*|wss://*) break ;;
        *) c_yellow "URL must start with http://, https://, ws://, or wss://. try again." ;;
      esac
    done
  else
    c_red "relay URL is required and no terminal is available for the prompt."
    c_red "re-run with CHATD_RELAY_URL set, e.g.:"
    c_red "  CHATD_RELAY_URL=http://relay.example:7878 bash install.sh"
    exit 1
  fi

  # Translate http(s):// → ws(s):// and append /ws if no path was given.
  case "$url" in
    http://*)  url="ws://${url#http://}" ;;
    https://*) url="wss://${url#https://}" ;;
  esac
  no_proto="${url#*://}"
  if [[ "$no_proto" != *"/"* ]]; then
    url="${url}/ws"
  fi

  token="${CHATD_TOKEN:-${cur_tok:-open}}"
  term="${CHATD_TERMINAL:-$cur_term}"

  {
    printf 'CHATD_USERNAME=%s\n' "$user"
    printf 'CHATD_TOKEN=%s\n' "$token"
    printf 'CHATD_RELAY_URL=%s\n' "$url"
    [[ -n "$term" ]] && printf 'CHATD_TERMINAL=%s\n' "$term"
  } > "$ENV_FILE"
  chmod 0600 "$ENV_FILE"
  c_green "wrote $ENV_FILE  (user=$user  relay=$url)"
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
