#!/usr/bin/env bash
# uninstall.sh — remove chatd from the current user's machine.

set -euo pipefail

BIN_DIR="${PREFIX:-$HOME/.local}/bin"
CFG_DIR="${XDG_CONFIG_HOME:-$HOME/.config}/chatd"
DATA_DIR="${XDG_DATA_HOME:-$HOME/.local/share}/chatd"
SYSTEMD_DIR="${XDG_CONFIG_HOME:-$HOME/.config}/systemd/user"

if command -v systemctl >/dev/null; then
  systemctl --user disable --now chatd.service 2>/dev/null || true
fi

rm -f "$SYSTEMD_DIR/chatd.service"
systemctl --user daemon-reload 2>/dev/null || true

rm -f "$BIN_DIR/chatd" "$BIN_DIR/chat" "$BIN_DIR/chat-client"

read -r -p "remove $CFG_DIR (env file, socket)? [y/N] " a
[[ "$a" == [yY]* ]] && rm -rf "$CFG_DIR"

read -r -p "remove $DATA_DIR (database, logs)? [y/N] " a
[[ "$a" == [yY]* ]] && rm -rf "$DATA_DIR"

echo "done."
