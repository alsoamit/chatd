// Command chat is the user-facing CLI. With no arguments it spawns the
// dashboard inside Ghostty. With subcommands it can drive the daemon
// from the terminal:
//
//	chat               -> open dashboard window
//	chat dashboard     -> render dashboard inline (no Ghostty)
//	chat open <peer>   -> open a conversation window
//	chat users         -> print user list
//	chat status        -> print daemon status
//	chat send <peer> <body>  -> oneshot send
//	chat logs          -> tail journald logs for chatd
package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strings"
	"time"

	"github.com/cedrx/chatd/internal/config"
	"github.com/cedrx/chatd/internal/dashboard"
	"github.com/cedrx/chatd/internal/ipc"
	"github.com/cedrx/chatd/internal/protocol"
	"github.com/cedrx/chatd/internal/terminal"
	"github.com/cedrx/chatd/internal/version"
	tea "github.com/charmbracelet/bubbletea"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "chat:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	paths, err := config.Resolve()
	if err != nil {
		return err
	}

	if len(args) == 0 {
		return openDashboardWindow(paths)
	}
	switch args[0] {
	case "--version", "-v", "version":
		fmt.Println(version.String("chat"))
		return nil
	case "dashboard", "tui":
		return runDashboardInline(paths)
	case "open":
		if len(args) < 2 {
			return fmt.Errorf("usage: chat open <peer>")
		}
		return rpcOpen(paths, args[1])
	case "users":
		return rpcUsers(paths)
	case "status":
		return rpcStatus(paths)
	case "send":
		if len(args) < 3 {
			return fmt.Errorf("usage: chat send <peer> <body>")
		}
		return rpcSend(paths, args[1], strings.Join(args[2:], " "))
	case "logs":
		return tailLogs()
	case "update":
		return runUpdate()
	case "config", "configure":
		return runConfig(paths)
	case "uninstall", "purge":
		return runUninstall(paths, args[1:])
	case "help", "-h", "--help":
		fmt.Print(usage)
		return nil
	default:
		return fmt.Errorf("unknown subcommand %q\n%s", args[0], usage)
	}
}

const usage = `chat — terminal-native messenger CLI
USAGE:
  chat                        open dashboard in a Ghostty window
  chat dashboard              render dashboard in the current terminal
  chat open <peer>            open a conversation window
  chat users                  print the active user list
  chat status                 print daemon status
  chat send <peer> <body>...  one-shot send (no UI)
  chat logs                   tail systemd journal for chatd.service
  chat update                 fetch the latest release and reinstall
  chat config                 re-run the username / relay URL prompts
  chat uninstall [--yes]      remove every trace of chatd from this user
  chat --version              print build metadata
`

const installerURL = "https://raw.githubusercontent.com/alsoamit/chatd/main/scripts/install.sh"

func openDashboardWindow(paths config.Paths) error {
	settings, _ := paths.LoadSettings()
	launcher, err := terminal.Detect(settings.Terminal)
	if err != nil {
		// fall back to inline rendering rather than fail
		return runDashboardInline(paths)
	}
	self, err := os.Executable()
	if err != nil {
		return err
	}
	return launcher.Spawn(terminal.SafeTitle("CHATD — DASHBOARD"),
		[]string{self, "dashboard"})
}

func runDashboardInline(paths config.Paths) error {
	prog := tea.NewProgram(dashboard.NewInline(paths.IPCSocket), tea.WithAltScreen())
	final, err := prog.Run()
	if err != nil {
		return err
	}
	if m, ok := final.(dashboard.Model); ok {
		if peer := m.Chosen(); peer != "" {
			return execChatClient(paths, peer)
		}
	}
	return nil
}

// execChatClient hands control over to the chat-client binary in place
// so that pressing Enter on the dashboard drops the user straight into
// the conversation, even on machines without any GUI terminal emulator.
func execChatClient(paths config.Paths, peer string) error {
	cc := locateChatClient()
	if cc == "" {
		return fmt.Errorf("chat-client not found on $PATH")
	}
	cmd := exec.Command(cc, "--peer", peer, "--socket", paths.IPCSocket)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func locateChatClient() string {
	if exe, err := os.Executable(); err == nil {
		candidate := filepath.Join(filepath.Dir(exe), "chat-client")
		if fi, err := os.Stat(candidate); err == nil && fi.Mode()&0o111 != 0 {
			return candidate
		}
	}
	if p, err := exec.LookPath("chat-client"); err == nil {
		return p
	}
	return ""
}

// dialControl establishes a control IPC client used by oneshot RPCs.
func dialControl(paths config.Paths) (*ipc.Client, error) {
	c := ipc.NewClient(paths.IPCSocket)
	if err := c.Dial(8, 200*time.Millisecond); err != nil {
		return nil, fmt.Errorf("daemon socket %s: %w (is chatd running?)", paths.IPCSocket, err)
	}
	if _, err := c.Hello(ipc.RoleControl, ""); err != nil {
		_ = c.Close()
		return nil, err
	}
	return c, nil
}

// readUntil reads frames until one matching op (or OpError) shows up.
func readUntil(ctx context.Context, c *ipc.Client, op string) ([]byte, error) {
	deadline := time.Now().Add(3 * time.Second)
	if dl, ok := ctx.Deadline(); ok {
		deadline = dl
	}
	for time.Now().Before(deadline) {
		line, err := c.ReadLine()
		if err != nil {
			return nil, err
		}
		got, _ := ipc.PeekOp(line)
		if got == op {
			return line, nil
		}
		if got == ipc.OpError {
			var e ipc.Error
			_ = json.Unmarshal(line, &e)
			return nil, fmt.Errorf("daemon: %s", e.Message)
		}
	}
	return nil, fmt.Errorf("timed out waiting for %s", op)
}

func rpcOpen(paths config.Paths, peer string) error {
	c, err := dialControl(paths)
	if err != nil {
		return err
	}
	defer c.Close()
	return c.Write(ipc.OpenReq{Op: ipc.OpOpen, Peer: peer})
}

func rpcUsers(paths config.Paths) error {
	c, err := dialControl(paths)
	if err != nil {
		return err
	}
	defer c.Close()
	if err := c.Write(ipc.UsersReq{Op: ipc.OpUsers}); err != nil {
		return err
	}
	line, err := readUntil(context.Background(), c, ipc.OpUsersData)
	if err != nil {
		return err
	}
	var u ipc.UsersData
	_ = json.Unmarshal(line, &u)
	for _, r := range u.Users {
		state := "offline"
		if r.Online {
			state = "online"
		}
		extra := ""
		if r.Unread > 0 {
			extra = fmt.Sprintf("  (%d unread)", r.Unread)
		}
		fmt.Printf("%-32s %s%s\n", r.Name, state, extra)
	}
	return nil
}

func rpcStatus(paths config.Paths) error {
	c, err := dialControl(paths)
	if err != nil {
		return err
	}
	defer c.Close()
	if err := c.Write(ipc.StatusReq{Op: ipc.OpStatus}); err != nil {
		return err
	}
	line, err := readUntil(context.Background(), c, ipc.OpStatusData)
	if err != nil {
		return err
	}
	var s ipc.StatusData
	_ = json.Unmarshal(line, &s)
	fmt.Printf("user:     %s\n", s.Username)
	fmt.Printf("relay:    %s (%s)\n", boolStr(s.RelayUp, "up", "down"), s.RelayURL)
	fmt.Printf("peers:    %d online\n", s.Peers)
	return nil
}

func boolStr(v bool, t, f string) string {
	if v {
		return t
	}
	return f
}

func rpcSend(paths config.Paths, peer, body string) error {
	c, err := dialControl(paths)
	if err != nil {
		return err
	}
	defer c.Close()
	return c.Write(ipc.Send{Op: ipc.OpSend, Peer: peer, Body: body})
}

func tailLogs() error {
	cmd := exec.Command("journalctl", "--user", "-u", "chatd.service", "-f", "--no-pager", "-n", "200")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// runConfig walks the user through username + relay URL prompts and
// rewrites ~/.config/chatd/chatd.env. Existing values become defaults
// so pressing Enter keeps them. After a successful write, restarts
// chatd.service so the daemon picks up the new env.
func runConfig(paths config.Paths) error {
	if err := paths.EnsureDirs(); err != nil {
		return err
	}
	cur, err := config.LoadEnvFile(paths.EnvFile)
	if err != nil {
		return fmt.Errorf("read %s: %w", paths.EnvFile, err)
	}
	in := bufio.NewReader(os.Stdin)

	// Username
	defUser := cur["CHATD_USERNAME"]
	if defUser == "" {
		if u, err := user.Current(); err == nil {
			defUser = u.Username
		}
	}
	username, err := promptDefault(in, fmt.Sprintf("Username [%s]: ", defUser), defUser)
	if err != nil {
		return err
	}
	if err := protocol.ValidateUsername(username); err != nil {
		return fmt.Errorf("invalid username: %w", err)
	}

	// Relay URL — required, but if there's a current value we accept Enter.
	defURL := cur["CHATD_RELAY_URL"]
	var relayURL string
	for {
		var prompt string
		if defURL != "" {
			prompt = fmt.Sprintf("Relay URL [%s]: ", defURL)
		} else {
			prompt = "Relay URL (http://host:port or https://domain) [required]: "
		}
		raw, err := promptDefault(in, prompt, defURL)
		if err != nil {
			return err
		}
		if raw == "" {
			return errors.New("relay URL is required — cancelled")
		}
		normalized, err := normalizeRelayURL(raw)
		if err != nil {
			fmt.Fprintln(os.Stderr, err.Error())
			continue
		}
		relayURL = normalized
		break
	}

	// Token — keep current value if present, else default to "open".
	token := cur["CHATD_TOKEN"]
	if token == "" {
		token = "open"
	}
	terminalHint := cur["CHATD_TERMINAL"]

	var b strings.Builder
	fmt.Fprintf(&b, "CHATD_USERNAME=%s\n", username)
	fmt.Fprintf(&b, "CHATD_TOKEN=%s\n", token)
	fmt.Fprintf(&b, "CHATD_RELAY_URL=%s\n", relayURL)
	if terminalHint != "" {
		fmt.Fprintf(&b, "CHATD_TERMINAL=%s\n", terminalHint)
	}
	if err := os.WriteFile(paths.EnvFile, []byte(b.String()), 0o600); err != nil {
		return fmt.Errorf("write %s: %w", paths.EnvFile, err)
	}
	fmt.Printf("wrote %s  (user=%s  relay=%s)\n", paths.EnvFile, username, relayURL)

	// Apply by restarting the daemon. Best-effort — don't fail if the
	// service isn't installed (e.g. dev runs without systemd).
	if _, err := exec.LookPath("systemctl"); err == nil {
		cmd := exec.Command("systemctl", "--user", "restart", "chatd.service")
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err == nil {
			fmt.Println("chatd.service restarted")
		}
	}
	return nil
}

// promptDefault writes prompt to stdout, reads one line from in, and
// returns the trimmed value. An empty response yields def.
func promptDefault(in *bufio.Reader, prompt, def string) (string, error) {
	fmt.Print(prompt)
	line, err := in.ReadString('\n')
	if err != nil && err.Error() != "EOF" {
		return "", err
	}
	v := strings.TrimSpace(line)
	if v == "" {
		return def, nil
	}
	return v, nil
}

// normalizeRelayURL accepts http://host:port, https://domain, ws://...,
// or wss://... and returns the canonical ws://.../ws form (path
// auto-appended if missing). Mirrors the logic in scripts/install.sh.
func normalizeRelayURL(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", errors.New("relay URL is required")
	}
	var url string
	switch {
	case strings.HasPrefix(raw, "http://"):
		url = "ws://" + strings.TrimPrefix(raw, "http://")
	case strings.HasPrefix(raw, "https://"):
		url = "wss://" + strings.TrimPrefix(raw, "https://")
	case strings.HasPrefix(raw, "ws://"), strings.HasPrefix(raw, "wss://"):
		url = raw
	default:
		return "", errors.New("URL must start with http://, https://, ws://, or wss://")
	}
	if i := strings.Index(url, "://"); i >= 0 {
		afterScheme := url[i+3:]
		if !strings.Contains(afterScheme, "/") {
			url += "/ws"
		}
	}
	return url, nil
}

// runUninstall removes every artefact installed by `bash install.sh`:
// the systemd-user service, the unit file, the config dir (env + IPC
// socket + pid file), the data dir (bbolt DB, logs), and the three
// binaries themselves. After confirmation, the chat binary deletes
// itself last; on Linux this is fine — the running process keeps its
// inode open until exit.
func runUninstall(paths config.Paths, args []string) error {
	yes := false
	for _, a := range args {
		switch a {
		case "--yes", "-y":
			yes = true
		case "--help", "-h":
			fmt.Println("usage: chat uninstall [--yes]")
			fmt.Println("removes binaries, systemd unit, config and local message DB")
			return nil
		}
	}

	binDir, binChatd, binChat, binClient := uninstallBinPaths()
	systemdDir := systemdUserDir()
	unitPath := filepath.Join(systemdDir, "chatd.service")
	wantsLink := filepath.Join(systemdDir, "default.target.wants", "chatd.service")

	plan := []string{
		"  systemctl --user disable --now chatd.service",
		"  " + unitPath,
		"  " + wantsLink + " (symlink, if present)",
		"  " + paths.ConfigDir + "/  (env, IPC socket, pid)",
		"  " + paths.DataDir + "/   (message DB, logs)",
		"  " + binChatd,
		"  " + binChat,
		"  " + binClient,
	}
	fmt.Println("This will remove:")
	for _, p := range plan {
		fmt.Println(p)
	}

	if !yes {
		in := bufio.NewReader(os.Stdin)
		ans, _ := promptDefault(in, "Proceed? [y/N]: ", "N")
		if !strings.HasPrefix(strings.ToLower(ans), "y") {
			fmt.Println("cancelled.")
			return nil
		}
	}

	// 1. Stop and disable the systemd-user service. This also removes
	//    the wants symlink. Best-effort — keep going on errors.
	if _, err := exec.LookPath("systemctl"); err == nil {
		_ = exec.Command("systemctl", "--user", "disable", "--now", "chatd.service").Run()
	}

	// 2. Remove unit file + any leftover symlink.
	for _, p := range []string{unitPath, wantsLink} {
		if err := os.Remove(p); err != nil && !errors.Is(err, os.ErrNotExist) {
			fmt.Fprintf(os.Stderr, "warn: %v\n", err)
		}
	}
	if _, err := exec.LookPath("systemctl"); err == nil {
		_ = exec.Command("systemctl", "--user", "daemon-reload").Run()
	}

	// 3. Wipe config + data directories.
	for _, d := range []string{paths.ConfigDir, paths.DataDir} {
		if err := os.RemoveAll(d); err != nil {
			fmt.Fprintf(os.Stderr, "warn: removing %s: %v\n", d, err)
		}
	}

	// 4. Remove the binaries. chatd / chat-client first, then chat
	//    last (the binary running this code). Linux holds the inode
	//    open for the live process so the unlink succeeds and chat
	//    finishes cleanly.
	for _, b := range []string{binChatd, binClient, binChat} {
		if err := os.Remove(b); err != nil && !errors.Is(err, os.ErrNotExist) {
			fmt.Fprintf(os.Stderr, "warn: removing %s: %v\n", b, err)
		}
	}

	fmt.Println("uninstalled. all traces removed.")
	fmt.Printf("(note: %s remains on your $PATH if it was added there manually)\n", binDir)
	fmt.Println("to reinstall later:")
	fmt.Println("  curl -fsSL https://raw.githubusercontent.com/alsoamit/chatd/main/scripts/install.sh | bash -s -- --download")
	return nil
}

// uninstallBinPaths returns the directory and absolute paths of the
// three binaries we install. We anchor on the running chat binary so
// the function works even if $PATH changed since install.
func uninstallBinPaths() (binDir, chatd, chat, client string) {
	exe, err := os.Executable()
	if err == nil {
		exe, _ = filepath.EvalSymlinks(exe)
	}
	if exe == "" {
		// Fall back to the conventional location.
		home, _ := os.UserHomeDir()
		binDir = filepath.Join(home, ".local", "bin")
	} else {
		binDir = filepath.Dir(exe)
	}
	return binDir,
		filepath.Join(binDir, "chatd"),
		filepath.Join(binDir, "chat"),
		filepath.Join(binDir, "chat-client")
}

// systemdUserDir returns the directory holding user-mode systemd units.
func systemdUserDir() string {
	if cfg := os.Getenv("XDG_CONFIG_HOME"); cfg != "" {
		return filepath.Join(cfg, "systemd", "user")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "systemd", "user")
}

// runUpdate re-invokes the published install.sh in --download mode.
// The installer detects the current version, asks the user to confirm
// the upgrade, swaps the binaries, and restarts chatd.service. We
// inherit the parent's stdio so prompts work and progress is visible.
func runUpdate() error {
	if _, err := exec.LookPath("curl"); err != nil {
		return fmt.Errorf("chat update needs curl on PATH: %w", err)
	}
	if _, err := exec.LookPath("bash"); err != nil {
		return fmt.Errorf("chat update needs bash on PATH: %w", err)
	}
	cmd := exec.Command("bash", "-c",
		"set -e; curl -fsSL "+installerURL+" | bash -s -- --download")
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}
