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
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/cedrx/chatd/internal/config"
	"github.com/cedrx/chatd/internal/dashboard"
	"github.com/cedrx/chatd/internal/ipc"
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
`

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
