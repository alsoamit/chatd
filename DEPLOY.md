# Installing chatd on a user machine

`chatd` is the local daemon plus the two CLIs (`chat`, `chat-client`).
It runs as a **systemd-user service** under your home directory — no
root, no system-wide changes. Each user installs a copy on their own
laptop / desktop.

This doc walks through getting it onto a Linux user's box, given that
a `v*` tag has been published to
`https://github.com/alsoamit/rootchat/releases`.

---

## 1. Cut a release

```bash
cd ~/projects/lab/chatd
git init                                  # if not already
git remote add origin git@github.com:alsoamit/rootchat.git
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

```bash
# from a published public release
curl -fsSL https://raw.githubusercontent.com/alsoamit/rootchat/main/scripts/install.sh \
  | bash -s -- --download

# from a downloaded tarball (works for private repos)
tar xzf chatd-vX.Y.Z-linux-amd64.tar.gz
bash chatd-vX.Y.Z-linux-amd64/install.sh

# from a source checkout (developer install)
bash scripts/install.sh
```

**Do not run with sudo** — chatd is a per-user service.

### Interactive prompts

On the **first** install on a given user, the script asks for:

- `Username [<your-shell-user>]:` — press Enter to accept the default,
  or type something else (`a-zA-Z0-9._-`, max 64 chars).
- `Relay URL (http://host:port or https://domain) [required]:` — no
  default. Empty input cancels the install. The script accepts
  `http://`, `https://`, `ws://`, `wss://`; it auto-translates
  `http→ws` and `https→wss` and appends `/ws` if you don't include a
  path. Examples that all work:

  ```
  http://4.61.213.126:7878
  https://relay.example.com
  wss://relay.example.com/ws
  ```

You can pre-fill them via env to skip the prompts:

```bash
CHATD_USERNAME=alice \
CHATD_RELAY_URL=https://relay.example.com \
  bash chatd-vX.Y.Z-linux-amd64/install.sh
```

### Update flow

When the script detects an existing chatd install (matching binary on
`$PATH` or in `~/.local/bin`) it prints the current and target
version and prompts:

```
chatd v0.1.0 is installed; latest is v0.2.0.
Update v0.1.0 → v0.2.0? [Y/n]:
```

On confirmation it stops the service, swaps binaries, and restarts.
`~/.config/chatd/chatd.env` and `~/.local/share/chatd/data.db` (your
local message history) are preserved.

To re-run the same flow without remembering the curl line:

```bash
chat update
```

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
chat update                # pull the latest release and reinstall
chat --version             # build metadata
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

Easiest:

```bash
chat update
```

Or run the installer directly:

```bash
curl -fsSL https://raw.githubusercontent.com/alsoamit/rootchat/main/scripts/install.sh \
  | bash -s -- --download
```

Either path resolves the latest tag, prompts you to confirm
`current → latest`, stops the service, swaps binaries, and restarts.
`~/.config/chatd/chatd.env` and `~/.local/share/chatd/data.db` are
preserved.

To pin a specific version:

```bash
curl -fsSL https://raw.githubusercontent.com/alsoamit/rootchat/main/scripts/install.sh \
  | bash -s -- --download --version v0.1.1
```

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
