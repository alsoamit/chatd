// Package dashboard renders the cyberpunk presence dashboard. It opens
// an IPC connection to the daemon, subscribes as RoleDashboard, and
// reflects user list, presence, and unread state in real time.
package dashboard

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/cedrx/chatd/internal/ipc"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// styles
var (
	titleStyle = lipgloss.NewStyle().Bold(true).
			Foreground(lipgloss.Color("#a3ff12"))
	headerStyle = lipgloss.NewStyle().Bold(true).
			Foreground(lipgloss.Color("#ff2bd6"))
	userOnline = lipgloss.NewStyle().Foreground(lipgloss.Color("#a3ff12"))
	userOff    = lipgloss.NewStyle().Foreground(lipgloss.Color("#666666"))
	unreadStl  = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#ff2bd6"))
	statusOK   = lipgloss.NewStyle().Foreground(lipgloss.Color("#a3ff12"))
	statusBad  = lipgloss.NewStyle().Foreground(lipgloss.Color("#ff5555"))
	helpStyle  = lipgloss.NewStyle().Faint(true)
	cursorStl  = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#000000")).
			Background(lipgloss.Color("#a3ff12"))
)

type lineMsg []byte
type errMsg struct{ err error }
type connectedMsg struct {
	client  *ipc.Client
	self    string
	relayUp bool
}

// Model is the BubbleTea state.
type Model struct {
	socket string
	client *ipc.Client
	inline bool // true when the dashboard runs inside the user's own terminal

	self    string
	relayUp bool
	users   []ipc.UserRow
	cursor  int
	width   int
	height  int
	err     error
	stat    string
	chosen  string // peer the user picked with Enter (inline mode only)
}

// New constructs an unstarted dashboard model.
func New(socketPath string) Model {
	return Model{socket: socketPath, stat: "connecting…"}
}

// NewInline constructs a dashboard intended to run inside the user's
// own terminal. Enter quits the dashboard and records the selected
// peer so the caller can exec chat-client in place.
func NewInline(socketPath string) Model {
	return Model{socket: socketPath, stat: "connecting…", inline: true}
}

// Chosen returns the peer the user picked with Enter. Only meaningful
// for models constructed via NewInline.
func (m Model) Chosen() string { return m.chosen }

// Init connects to the IPC socket and fires the read pump.
func (m Model) Init() tea.Cmd {
	return tea.Batch(connect(m.socket))
}

// Update handles inbound IPC frames and key events.
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch v := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = v.Width, v.Height
		return m, nil

	case errMsg:
		m.err = v.err
		m.stat = "disconnected"
		return m, nil

	case connectedMsg:
		m.client = v.client
		m.self = v.self
		m.relayUp = v.relayUp
		m.stat = "connected"
		return m, readLine(m.client)

	case lineMsg:
		var cmds []tea.Cmd
		if c := m.handle(v); c != nil {
			cmds = append(cmds, c)
		}
		cmds = append(cmds, readLine(m.client))
		return m, tea.Batch(cmds...)

	case tea.KeyMsg:
		switch v.String() {
		case "q", "ctrl+c":
			return m, tea.Quit
		case "j", "down":
			if m.cursor < len(m.users)-1 {
				m.cursor++
			}
		case "k", "up":
			if m.cursor > 0 {
				m.cursor--
			}
		case "r":
			if m.client != nil {
				_ = m.client.Write(ipc.UsersReq{Op: ipc.OpUsers})
			}
		case "enter":
			if len(m.users) == 0 {
				return m, nil
			}
			peer := m.users[m.cursor].Name
			if m.inline {
				// In inline mode, hand off to chat-client by quitting and
				// letting the caller exec it. Skip OpOpen so the daemon
				// doesn't also try to pop a window.
				m.chosen = peer
				return m, tea.Quit
			}
			if m.client != nil {
				_ = m.client.Write(ipc.OpenReq{Op: ipc.OpOpen, Peer: peer})
			}
		}
	}
	return m, nil
}

// View renders the dashboard.
func (m Model) View() string {
	var b strings.Builder
	title := titleStyle.Render(fmt.Sprintf("◆ chatd  ▸  %s", m.self))
	b.WriteString(title)
	b.WriteString("\n")

	rstat := statusBad.Render("relay ✕")
	if m.relayUp {
		rstat = statusOK.Render("relay ✓")
	}
	b.WriteString(fmt.Sprintf("%s  %s\n\n", rstat, helpStyle.Render(m.stat)))

	b.WriteString(headerStyle.Render("ACTIVE USERS"))
	b.WriteString("\n")
	if len(m.users) == 0 {
		b.WriteString(helpStyle.Render("  (no peers known yet)\n"))
	}
	for i, u := range m.users {
		marker := "  "
		if i == m.cursor {
			marker = cursorStl.Render(" ▸ ")
		}
		name := userOff.Render(u.Name)
		if u.Online {
			name = userOnline.Render(u.Name)
		}
		line := fmt.Sprintf("%s %2d. %s", marker, i+1, name)
		if u.Unread > 0 {
			line += "  " + unreadStl.Render(fmt.Sprintf("(%d unread)", u.Unread))
		}
		b.WriteString(line)
		b.WriteString("\n")
	}

	b.WriteString("\n")
	b.WriteString(helpStyle.Render("↑/↓ select  •  Enter open  •  r refresh  •  q quit"))
	if m.err != nil {
		b.WriteString("\n")
		b.WriteString(statusBad.Render(m.err.Error()))
	}
	return b.String()
}

func (m *Model) handle(line []byte) tea.Cmd {
	op, err := ipc.PeekOp(line)
	if err != nil {
		return nil
	}
	switch op {
	case ipc.OpUsersData:
		var u ipc.UsersData
		if err := json.Unmarshal(line, &u); err == nil {
			m.users = u.Users
			if m.cursor >= len(m.users) {
				m.cursor = max(0, len(m.users)-1)
			}
		}
	case ipc.OpStatusData:
		var s ipc.StatusData
		if err := json.Unmarshal(line, &s); err == nil {
			m.relayUp = s.RelayUp
			if s.Username != "" {
				m.self = s.Username
			}
		}
	case ipc.OpPresence:
		// trigger a user refresh
		if m.client != nil {
			_ = m.client.Write(ipc.UsersReq{Op: ipc.OpUsers})
		}
	case ipc.OpUnread:
		if m.client != nil {
			_ = m.client.Write(ipc.UsersReq{Op: ipc.OpUsers})
		}
	}
	return nil
}

func connect(socket string) tea.Cmd {
	return func() tea.Msg {
		c := ipc.NewClient(socket)
		if err := c.Dial(20, 250_000_000); err != nil { // 20 attempts × 250ms
			return errMsg{err: err}
		}
		ok, err := c.Hello(ipc.RoleDashboard, "")
		if err != nil {
			return errMsg{err: err}
		}
		return connectedMsg{client: c, self: ok.Self, relayUp: ok.RelayUp}
	}
}

func readLine(c *ipc.Client) tea.Cmd {
	if c == nil {
		return nil
	}
	return func() tea.Msg {
		line, err := c.ReadLine()
		if err != nil {
			return errMsg{err: err}
		}
		return lineMsg(line)
	}
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
