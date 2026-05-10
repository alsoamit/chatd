# rootchat

Realtime chat that lives in your terminal. Inspired by **Pantheon**.
One command to install, type `chat`, and message your friends — or
even talk to a server. The moment anyone sends you a message, a
fresh terminal window pops up on your screen with the conversation.

```
   ◆ rootchat  ▸  alice
   relay ✓  connected

   ACTIVE USERS
    ▸ 1. bob          (2 unread)
      2. charlie
      3. alerts-bot

   ↑/↓ select  •  Enter open  •  q quit
```

## What you'll need

- A Linux machine (Ubuntu, Debian, Fedora, anything with systemd) **or
  a Mac** (macOS 11 Big Sur and newer, Intel or Apple Silicon).
- A **relay URL** — the address of the server that ferries messages
  between people. Whoever set up your group will give you one, e.g.
  `http://3.91.216.126:7878` or `https://relay.example.com`. To run
  your own, see [chatrelay](https://github.com/alsoamit/chatrelay).

## Install

```bash
curl -fsSL https://raw.githubusercontent.com/alsoamit/rootchat/main/scripts/install.sh | bash -s -- --download
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
| `chat uninstall` | Remove every trace of rootchat from your machine. |
| `chat dashboard` | Same as `chat`, but always renders in *this* terminal (no popup window). |

Inside a chat window: type and press **Enter** to send. **Shift+Enter** for a newline. **Ctrl+C** to leave.

You don't only have to chat with people. Anything that can speak the
relay's protocol — a script, a server reporting its health, an alert
bot — shows up just like a person. Run a small program with the
username `alerts` and your friends can chat with it the same way they
chat with you.

## How it works

Imagine you and your friends each have a tiny **mailbox bot** sitting
on your computer, and there's a single **post office** somewhere on
the internet that all the mailbox bots talk to.

1. You type a message in `chat`.
2. Your mailbox bot picks it up and walks it to the post office.
3. The post office hands it to your friend's mailbox bot.
4. Your friend's mailbox bot opens a new terminal window on their
   screen with your message inside.

That's the whole thing. Three players: **the post office** (the relay
server), **the mailbox bots** (one tiny background program on each
person's machine), and **the `chat` command** you actually type.

The mailbox bot is what makes things feel instant. It stays connected
to the post office around the clock, so the moment anyone sends you
something:

- A desktop notification pops up.
- A new terminal window opens automatically with the conversation
  ready to read or reply to (when you have a graphical terminal like
  Ghostty or Kitty installed).
- If you're on a server with no graphical terminal, the message is
  saved quietly. Open `chat` later and you'll see an unread badge
  next to that peer.

### What rootchat is *not*

- **Not SSH.** Chat windows can't execute commands. Typing
  `rm -rf /` just sends those characters as a message. It's text.
- **Not a browser app.** Native terminal, native OS windows.
- **Not snitching on you.** Messages go through the relay you
  configured. Nowhere else.

## More

- [`DEPLOY.md`](DEPLOY.md) — server admin guide, advanced install,
  troubleshooting.
- Issues / questions: <https://github.com/alsoamit/rootchat/issues>
