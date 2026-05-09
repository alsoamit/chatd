// Package chatui is the BubbleTea program that owns one Ghostty window.
// It is intentionally NOT a shell: keystrokes accumulate into a single
// outgoing message buffer that is sent to the daemon as plain text via
// the local IPC socket. There is no exec, no PTY, nothing on $PATH.
package chatui

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/cedrx/chatd/internal/ipc"
	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

var (
	titleStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#a3ff12"))
	peerStyle  = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#ff2bd6"))
	selfStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("#a3ff12"))
	otherStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("#5fd7ff"))
	tsStyle    = lipgloss.NewStyle().Faint(true)
	helpStyle  = lipgloss.NewStyle().Faint(true)
	statusBad  = lipgloss.NewStyle().Foreground(lipgloss.Color("#ff5555"))
	statusOK   = lipgloss.NewStyle().Foreground(lipgloss.Color("#a3ff12"))
)

type lineMsg []byte
type errMsg struct{ err error }
type connectedMsg struct {
	client  *ipc.Client
	self    string
	relayUp bool
}

// Model is the conversation TUI.
type Model struct {
	socket string
	peer   string
	self   string

	client   *ipc.Client
	textarea textarea.Model
	view     viewport.Model

	relayUp     bool
	peerOnline  bool
	peerTyping  bool
	width       int
	height      int
	messages    []ipc.Message
	err         error
	statusLabel string
}

// New constructs an unstarted chat-client model.
func New(socket, peer string) Model {
	ta := textarea.New()
	ta.Placeholder = "Type a message…"
	ta.Prompt = "▎ "
	ta.CharLimit = 8192
	ta.SetWidth(80)
	ta.SetHeight(3)
	ta.ShowLineNumbers = false
	ta.Focus()

	vp := viewport.New(80, 20)

	return Model{
		socket:      socket,
		peer:        peer,
		textarea:    ta,
		view:        vp,
		statusLabel: "connecting…",
	}
}

func (m Model) Init() tea.Cmd {
	return tea.Batch(connect(m.socket, m.peer), textarea.Blink)
}

// Update handles events. We split keys carefully: Enter sends, but
// Shift+Enter (and Alt+Enter) inserts a newline so multiline messages
// remain possible.
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd

	switch v := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = v.Width, v.Height
		m.layout()
		return m, nil

	case errMsg:
		m.err = v.err
		m.statusLabel = "disconnected"
		return m, nil

	case connectedMsg:
		m.client = v.client
		m.self = v.self
		m.relayUp = v.relayUp
		m.statusLabel = "connected"
		// Pull current users + history once we have a client.
		_ = m.client.Write(ipc.UsersReq{Op: ipc.OpUsers})
		_ = m.client.Write(ipc.HistoryReq{Op: ipc.OpHistory, Peer: m.peer})
		cmds = append(cmds, readLine(m.client))
		return m, tea.Batch(cmds...)

	case lineMsg:
		if c := m.handle(v); c != nil {
			cmds = append(cmds, c)
		}
		cmds = append(cmds, readLine(m.client))
		return m, tea.Batch(cmds...)

	case tea.KeyMsg:
		switch v.String() {
		case "ctrl+c":
			return m, tea.Quit
		case "enter":
			if m.client != nil {
				body := strings.TrimRight(m.textarea.Value(), "\n")
				if body != "" {
					_ = m.client.Write(ipc.Send{Op: ipc.OpSend, Peer: m.peer, Body: body})
					m.textarea.Reset()
				}
			}
			return m, nil
		case "alt+enter", "shift+enter", "ctrl+j":
			m.textarea.InsertString("\n")
			return m, nil
		case "pgup":
			m.view.HalfViewUp()
			return m, nil
		case "pgdown":
			m.view.HalfViewDown()
			return m, nil
		case "esc":
			// no-op — esc otherwise blurs the textarea, which makes
			// keys feel dead. Swallow it.
			return m, nil
		}
	}

	var taCmd, vpCmd tea.Cmd
	m.textarea, taCmd = m.textarea.Update(msg)
	m.view, vpCmd = m.view.Update(msg)
	cmds = append(cmds, taCmd, vpCmd)
	m.refreshView()

	// Send typing indicator on text changes.
	if m.client != nil {
		active := strings.TrimSpace(m.textarea.Value()) != ""
		_ = m.client.Write(ipc.Typing{Op: ipc.OpTyping, Peer: m.peer, Active: active})
	}

	return m, tea.Batch(cmds...)
}

// View renders the conversation.
func (m Model) View() string {
	var b strings.Builder
	rstat := statusBad.Render("relay ✕")
	if m.relayUp {
		rstat = statusOK.Render("relay ✓")
	}
	pstat := statusBad.Render("offline")
	if m.peerOnline {
		pstat = statusOK.Render("online")
	}
	b.WriteString(titleStyle.Render("◆ chatd"))
	b.WriteString("  ▸  ")
	b.WriteString(peerStyle.Render(m.peer))
	b.WriteString("  ")
	b.WriteString(rstat)
	b.WriteString("  peer: ")
	b.WriteString(pstat)
	if m.peerTyping {
		b.WriteString("  ")
		b.WriteString(helpStyle.Render(m.peer + " is typing…"))
	}
	b.WriteString("\n")
	b.WriteString(strings.Repeat("─", maxInt(20, m.width-2)))
	b.WriteString("\n")
	b.WriteString(m.view.View())
	b.WriteString("\n")
	b.WriteString(strings.Repeat("─", maxInt(20, m.width-2)))
	b.WriteString("\n")
	b.WriteString(m.textarea.View())
	b.WriteString("\n")
	b.WriteString(helpStyle.Render("Enter send  •  Shift+Enter newline  •  PgUp/PgDn scroll  •  Ctrl+C quit"))
	if m.err != nil {
		b.WriteString("\n")
		b.WriteString(statusBad.Render(m.err.Error()))
	}
	return b.String()
}

func (m *Model) layout() {
	if m.width == 0 || m.height == 0 {
		return
	}
	tah := 3
	headerLines := 4
	footerLines := 2
	vh := m.height - tah - headerLines - footerLines
	if vh < 4 {
		vh = 4
	}
	m.view.Width = m.width
	m.view.Height = vh
	m.textarea.SetWidth(m.width)
	m.refreshView()
}

func (m *Model) refreshView() {
	var b strings.Builder
	for _, msg := range m.messages {
		b.WriteString(formatMessage(msg, m.self))
		b.WriteString("\n")
	}
	m.view.SetContent(b.String())
	m.view.GotoBottom()
}

func formatMessage(msg ipc.Message, self string) string {
	t := time.UnixMilli(msg.TS).Format("15:04:05")
	prefix := otherStyle.Render(msg.From)
	if msg.Self || msg.From == self {
		prefix = selfStyle.Render(msg.From)
	}
	return fmt.Sprintf("%s %s  %s",
		tsStyle.Render(t),
		prefix,
		msg.Body,
	)
}

func (m *Model) handle(line []byte) tea.Cmd {
	op, err := ipc.PeekOp(line)
	if err != nil {
		return nil
	}
	switch op {
	case ipc.OpHistoryData:
		var h ipc.HistoryData
		if err := json.Unmarshal(line, &h); err == nil && h.Peer == m.peer {
			m.messages = h.Messages
			// daemon already cleared unread when we subscribed; tell
			// it again so the dashboard reflects it.
			_ = m.client.Write(ipc.ReadAll{Op: ipc.OpReadAll, Peer: m.peer})
			m.refreshView()
		}
	case ipc.OpMessage:
		var msg ipc.Message
		if err := json.Unmarshal(line, &msg); err == nil && msg.Peer == m.peer {
			m.messages = append(m.messages, msg)
			m.refreshView()
		}
	case ipc.OpStatusData:
		var s ipc.StatusData
		if err := json.Unmarshal(line, &s); err == nil {
			m.relayUp = s.RelayUp
		}
	case ipc.OpPresence:
		var p ipc.Presence
		if err := json.Unmarshal(line, &p); err == nil && p.User == m.peer {
			m.peerOnline = p.Online
		}
	case ipc.OpUsersData:
		var u ipc.UsersData
		if err := json.Unmarshal(line, &u); err == nil {
			for _, row := range u.Users {
				if row.Name == m.peer {
					m.peerOnline = row.Online
					break
				}
			}
		}
	case ipc.OpTypingFrom:
		var t ipc.TypingFrom
		if err := json.Unmarshal(line, &t); err == nil && t.From == m.peer {
			m.peerTyping = t.Active
		}
	case ipc.OpAck:
		// Ack handling could decorate sent messages; we keep it simple.
	}
	return nil
}

func connect(socket, peer string) tea.Cmd {
	return func() tea.Msg {
		c := ipc.NewClient(socket)
		if err := c.Dial(20, 250_000_000); err != nil {
			return errMsg{err: err}
		}
		ok, err := c.Hello(ipc.RoleConversation, peer)
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

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
