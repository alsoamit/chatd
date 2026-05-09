# Installing chatd on a user machine

`chatd` is the local daemon plus the two CLIs (`chat`, `chat-client`).
It runs as a **systemd-user service** under your home directory — no
root, no system-wide changes. Each user installs a copy on their own
laptop / desktop.

This doc walks through getting it onto a Linux user's box, given that
a `v*` tag has been published to
`https://github.com/alsoamit/chatd/releases`.

---

## 1. Cut a release

```bash
cd ~/projects/lab/chatd
git init                                  # if not already
git remote add origin git@github.com:alsoamit/chatd.git
git add . && git commit -m "init chatd"
git push -u origin main

git tag v0.1.0
git push origin v0.1.0
```

The `release.yml` workflow runs `go test ./...`, cross-compiles
`linux/amd64` and `linux/arm64`, bundles `chatd` + `chat` +
`chat-client` + `chatd.service` + `install.sh` + docs into a tarball,
and attaches it to the release.

---

## 2. Install on a user's box

### From a public release (curl one-liner)

```bash
curl -fsSL https://raw.githubusercontent.com/alsoamit/chatd/main/scripts/install.sh \
  | bash -s -- --download
```

Pin to a specific tag with `--version v0.1.0`. **Do not run with sudo
— chatd is a per-user service.**

### From a downloaded tarball (works for private repos)

On a machine that already has GitHub auth (your laptop, browser or
`gh release download`):

```bash
gh release download v0.1.0 --repo alsoamit/chatd \
  -p '*-linux-amd64.tar.gz' -p 'SHA256SUMS'
scp chatd-v0.1.0-linux-amd64.tar.gz user@target:/tmp/
```

On the target box:

```bash
cd /tmp
tar xzf chatd-v0.1.0-linux-amd64.tar.gz
bash chatd-v0.1.0-linux-amd64/install.sh
```

The bundled `install.sh` autodetects "tarball mode" because the
binaries sit next to it, copies them into `~/.local/bin`, drops a
starter `~/.config/chatd/chatd.env`, registers the systemd-user unit,
and starts the service.

### From a checkout (developer / source install)

```bash
cd ~/projects/lab/chatd
bash scripts/install.sh
```

Detects "source mode" (sees `../go.mod`), builds the three binaries,
and proceeds with the same install path. Requires the Go toolchain.

---

## 3. Configure

The installer writes a starter file at
`~/.config/chatd/chatd.env`:

```ini
CHATD_USERNAME=<your-shell-username>
CHATD_TOKEN=CHANGE_ME
CHATD_RELAY_URL=ws://127.0.0.1:7878/ws
```

Edit it:

```bash
$EDITOR ~/.config/chatd/chatd.env
```

Set:

- `CHATD_USERNAME` — how peers see you (`a-zA-Z0-9._-`, max 64)
- `CHATD_TOKEN` — the same value the relay was configured with
  (`CHATRELAY_TOKEN` on the server side)
- `CHATD_RELAY_URL` — `wss://relay.YOUR.DOMAIN/ws` for production,
  `ws://127.0.0.1:7878/ws` for a local relay
- `CHATD_TERMINAL` — optional override (`ghostty`, `kitty`,
  `alacritty`, `foot`, `xterm`); auto-detected when blank

Apply changes:

```bash
systemctl --user restart chatd.service
chat status
```

Expected: `relay: up`.

You can pre-fill the env file at install time so the prompt is
non-existent:

```bash
CHATD_USERNAME=alice CHATD_TOKEN=hex... \
CHATD_RELAY_URL=wss://relay.example.com/ws \
  bash chatd-v0.1.0-linux-amd64/install.sh
```

---

## 4. Use it

```bash
chat                       # dashboard in a Ghostty/etc window
chat dashboard             # dashboard inline, in the current terminal
chat open <peer>           # pop a conversation window
chat users                 # online peers + unread counts
chat status                # daemon + relay state
chat send <peer> <msg>     # one-shot send
chat logs                  # journalctl --user -u chatd.service -f
```

In the dashboard: ↑/↓ select, Enter opens the conversation, `q` quit.
On a box with a real terminal emulator, Enter pops a new
`CHAT — <peer>` window. Without one (headless install), Enter execs
`chat-client --peer <peer>` inline in the current terminal.

In a chat window: type, Enter sends, Shift+Enter newline,
PgUp/PgDn scroll, Ctrl+C quits.

`chat-client` is **not a shell**. Typing `rm -rf /` just sends those
six characters as a chat message; nothing is exec'd locally.

---

## 5. Verify it really works

```bash
systemctl --user status chatd.service
chat status                          # → relay: up
journalctl --user -u chatd.service -n 20

# round-trip test (assumes you have a peer named "bob" online)
chat send bob "hello from $(whoami)"
```

---

## 6. PATH

The installer puts binaries in `~/.local/bin` and warns if that's not
on `$PATH`. Add to `~/.bashrc` / `~/.zshrc`:

```bash
export PATH="$HOME/.local/bin:$PATH"
```

Re-source it:

```bash
exec $SHELL -l
```

---

## 7. Upgrade

Re-run the installer with a newer version:

```bash
# public repo
curl -fsSL https://raw.githubusercontent.com/alsoamit/chatd/main/scripts/install.sh \
  | bash -s -- --download --version v0.1.1
systemctl --user restart chatd.service

# private repo: scp the new tarball, extract, rerun install.sh
```

The installer keeps your existing `~/.config/chatd/chatd.env`
untouched on re-runs. Local message history at
`~/.local/share/chatd/data.db` is also preserved.

---

## 8. Uninstall

A bundled script:

```bash
bash chatd-v0.1.0-linux-amd64/uninstall.sh
```

It disables and stops the service, removes the unit and binaries,
and prompts before deleting your config and DB.

---

## 9. Common problems

| symptom | cause | fix |
|---|---|---|
| `chat status` reports `relay: down` | URL/firewall/token | fix `CHATD_RELAY_URL` and `CHATD_TOKEN`, then `systemctl --user restart chatd.service` |
| `chat: daemon socket … (is chatd running?)` | service not up | `systemctl --user status chatd.service`; inspect `journalctl --user -u chatd.service` |
| no popup window when peer pings me | no terminal emulator | `sudo apt install xterm` or install ghostty; `systemctl --user restart chatd.service` |
| `chatd: settings: CHATD_TOKEN is required` | starter env not edited | edit `~/.config/chatd/chatd.env`, restart the service |
| dashboard shows `relay ✕` despite daemon connected | older build cached | reinstall with the latest tarball; run `chatd --version` to confirm |

---

## 10. systemd cheat sheet (user services)

```bash
systemctl --user status chatd.service              # is it running?
systemctl --user restart chatd.service             # apply env changes
systemctl --user disable --now chatd.service       # stop + don't autostart
systemctl --user enable --now chatd.service        # opposite
journalctl --user -u chatd.service -f              # tail logs
journalctl --user -u chatd.service -n 100 --no-pager
loginctl enable-linger $USER                       # keep service running when you're logged out (optional)
```

`loginctl enable-linger` matters if you want chatd to keep your
presence "online" while you're disconnected from the desktop session
— otherwise systemd-user services stop when the last login closes.
