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

On a fresh install the script asks for two things:

- **Username** — defaults to your shell `$(whoami)`.
- **Relay URL** — required, no default. Accepts
  `http://host:port`, `https://domain[:port]`, `ws://...`, or
  `wss://...`. The installer auto-translates `http→ws` / `https→wss`
  and appends `/ws` if you omit the path.

If the relay URL is empty, installation is cancelled.

On re-run with chatd already installed (the typical "update"
scenario), the script detects the existing version, prompts
`Update X → Y? [Y/n]`, and on confirmation stops the service, swaps
binaries, and restarts. Your `chatd.env` and local message history
are preserved.

To keep up to date without remembering the curl line, use:

```bash
chat update
```

It's a thin wrapper around the same install.sh download flow.

See [`DEPLOY.md`](DEPLOY.md) for the long-form walkthrough
(verification, PATH setup, systemd cheatsheet, troubleshooting).

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
