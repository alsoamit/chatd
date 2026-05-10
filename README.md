# chatd

Realtime chat that lives in your terminal. One command to install,
type `chat`, message your friends.

```
   ◆ chatd  ▸  alice
   relay ✓  connected

   ACTIVE USERS
    ▸ 1. bob          (2 unread)
      2. charlie

   ↑/↓ select  •  Enter open  •  q quit
```

## What you'll need

- A Linux machine (Ubuntu, Debian, Fedora, anything with systemd)
- A **relay URL** — the address of the server that ferries messages
  between people. Whoever set up your group will give you one, e.g.
  `http://3.91.216.126:7878` or `https://relay.example.com`. To run
  your own, see [chatrelay](https://github.com/alsoamit/chatrelay).

## Install

```bash
curl -fsSL https://raw.githubusercontent.com/alsoamit/chatd/main/scripts/install.sh | bash -s -- --download
```

That's it. The installer will ask you three quick questions:

1. **Server install?** — press Enter (no, this is a regular desktop/laptop).
2. **Username** — press Enter to use your shell name, or type your own.
3. **Relay URL** — paste the URL you were given.

When it finishes, open a new terminal (or run `source ~/.bashrc`) and type:

```bash
chat
```

If `chat` says *command not found*, run `source ~/.bashrc` and try again.

## Commands

| command | what it does |
|---|---|
| `chat` | Open the dashboard — your home screen with everyone online. |
| `chat open alice` | Jump straight into a conversation with alice. |
| `chat send alice "hi"` | One-shot send, no window opens. Useful from scripts. |
| `chat users` | List who's online. |
| `chat status` | Show whether the background service is connected. |
| `chat logs` | Tail the background service's log (when something feels off). |
| `chat config` | Change your username or relay URL after install. |
| `chat update` | Fetch the latest release and update yourself. |
| `chat uninstall` | Remove every trace of chatd from your machine. |
| `chat dashboard` | Same as `chat`, but always renders in *this* terminal (no popup window). |

Inside a chat window: type and press **Enter** to send. **Shift+Enter** for a newline. **Ctrl+C** to leave.

## How it works

Three pieces:

1. **A relay server** on the internet. All it does is shuttle messages
   between people. It's a meeting point — it doesn't *do* anything else.

2. **A tiny background service on your machine** called `chatd`. It
   keeps a persistent connection to the relay so you get messages
   the instant they arrive, even with no chat window open. systemd
   starts it on login and restarts it if it ever dies.

3. **The `chat` command**. It never talks to the relay directly — it
   talks to your local background service, which talks to the relay.

```
you type in `chat`  →  your local chatd  →  relay  →  friend's chatd  →  friend's `chat`
```

When someone messages you and you don't have a chat window open:

- A desktop notification pops up.
- If you have a supported terminal (Ghostty, Kitty, Alacritty, foot,
  xterm), a new chat window opens automatically.
- On a server or a machine without one, the message stores quietly.
  Open `chat` later and you'll see an unread badge next to that peer.

### What it is *not*

- **Not SSH.** Chat windows can't execute commands. Typing
  `rm -rf /` just sends those characters as a message. It's text.
- **Not a browser app.** Native terminal, native OS windows.
- **Not snitching on you.** Messages go through the relay you
  configured. Nowhere else.

## More

- [`DEPLOY.md`](DEPLOY.md) — server admin guide, advanced install,
  troubleshooting.
- Issues / questions: <https://github.com/alsoamit/chatd/issues>
