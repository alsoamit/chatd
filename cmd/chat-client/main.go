// Command chat-client renders a single conversation window. It is
// spawned by the daemon inside a Ghostty terminal:
//
//	ghostty --title "CHAT — alice" -e chat-client --peer alice
//
// chat-client never spawns a shell and never executes user input as
// commands; everything typed is forwarded to the daemon as plain text.
package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/cedrx/chatd/internal/chatui"
	"github.com/cedrx/chatd/internal/config"
	"github.com/cedrx/chatd/internal/version"
	tea "github.com/charmbracelet/bubbletea"
)

func main() {
	peer := flag.String("peer", "", "conversation peer username")
	socket := flag.String("socket", "", "absolute path to daemon IPC socket; defaults to standard XDG location")
	versionFlag := flag.Bool("version", false, "print version and exit")
	flag.Parse()

	if *versionFlag {
		fmt.Println(version.String("chat-client"))
		return
	}
	if *peer == "" {
		fmt.Fprintln(os.Stderr, "chat-client: --peer is required")
		os.Exit(2)
	}
	socketPath := *socket
	if socketPath == "" {
		paths, err := config.Resolve()
		if err != nil {
			fmt.Fprintln(os.Stderr, "chat-client:", err)
			os.Exit(1)
		}
		socketPath = paths.IPCSocket
	}
	prog := tea.NewProgram(
		chatui.New(socketPath, *peer),
		tea.WithAltScreen(),
		tea.WithMouseCellMotion(),
	)
	if _, err := prog.Run(); err != nil {
		fmt.Fprintln(os.Stderr, "chat-client:", err)
		os.Exit(1)
	}
}
