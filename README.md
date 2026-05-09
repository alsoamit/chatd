# chatd — terminal-native realtime chat for Linux

A persistent local daemon plus two BubbleTea TUIs that open as native
Ghostty windows. No tmux, no shell exposure, no SSH, no browser.
Spawned conversation windows are dedicated text-message UIs only —
typing `rm -rf /` just sends a string to your peer.

```
~/.config/chatd/chatd.env
        │
   ┌────┴────┐
   │  chatd  │  ← systemd-user service (this repo's daemon)
   └────┬────┘
        │ unix socket  (~/.config/chatd/ipc.sock)
        ├──── chat (dashboard)            ← `chat`
        └──── chat-client (per peer)      ← `ghostty -e chat-client --peer alice`
        │
        │  outbound websocket
        │
   ┌────┴─────────┐
   │  chatrelay   │   ← see ../chatrelay
   └──────────────┘
```

## Install

See [`DEPLOY.md`](DEPLOY.md) for the full user-side recipe (cut a
release, install from a release tarball or a public curl one-liner,
configure, verify, upgrade, troubleshoot).

The short version:

```bash
# from a published public release
curl -fsSL https://raw.githubusercontent.com/alsoamit/chatd/main/scripts/install.sh \
  | bash -s -- --download

# from a downloaded tarball (works for private repos)
tar xzf chatd-vX.Y.Z-linux-amd64.tar.gz
bash chatd-vX.Y.Z-linux-amd64/install.sh

# from a source checkout (developer install)
bash scripts/install.sh
```

**Do not run with sudo.** chatd is a systemd-user service; it lives
entirely under your `$HOME`.

The installer drops a starter `~/.config/chatd/chatd.env` you'll need
to edit (`CHATD_TOKEN`, `CHATD_RELAY_URL`); env knobs at install time
pre-fill it: `CHATD_USERNAME`, `CHATD_TOKEN`, `CHATD_RELAY_URL`,
`CHATD_TERMINAL`.

## CLI

```
chat                        open dashboard in a Ghostty window
chat dashboard              render dashboard inline
chat open <peer>            open a conversation window
chat users                  print online users + unread counts
chat status                 print daemon status
chat send <peer> <body>...  one-shot send (no UI)
chat logs                   follow journald output for chatd.service
```

## Configuration

`~/.config/chatd/chatd.env`:

```ini
CHATD_USERNAME=alice
CHATD_TOKEN=open
CHATD_RELAY_URL=ws://relay.example:7878/ws
# CHATD_TERMINAL=ghostty
```

systemd's `Environment=` directives override individual values.

## Storage

Persistent state lives at `~/.local/share/chatd/data.db` (bbolt). It
holds per-peer message logs, unread counters, and merge-history
breadcrumbs. The relay holds the canonical history; local storage is
a cache that gets reconciled on every conversation open.

## Why no shell?

The daemon spawns each conversation window as
`ghostty --title="CHAT — alice" -e chat-client --peer alice`. The
`chat-client` binary owns the terminal — there is no `bash`, no
`PATH`, no `exec`. Keystrokes flow into a BubbleTea textarea and out
over the local IPC socket as plain text frames. There is no path
inside the program by which user input becomes a system call.

## Development

```bash
make build   # build all three binaries into ./bin
make test    # run the test suite
make vet     # go vet ./...
```

Tests cover protocol validation, the bbolt storage layer, the
config/env loader, the websocket reconnect path, the IPC server
handshake + broadcast filtering, and the conversation-manager state
machine (incoming messages, presence, self-echo dedup, unread
bookkeeping).
