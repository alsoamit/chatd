// Package notifications fires desktop notifications via the OS-native
// channel: libnotify's notify-send on Linux, osascript on macOS. If
// neither tool is available we silently degrade to a no-op so the
// daemon still works on headless boxes.
package notifications

import (
	"fmt"
	"os/exec"
	"strings"
)

type backend int

const (
	backendNone backend = iota
	backendNotifySend
	backendOsascript
)

// Notifier sends desktop notifications.
type Notifier struct {
	bin  string
	kind backend
}

// New returns a Notifier. If no notification tool is available the
// returned notifier is a silent no-op.
func New() *Notifier {
	if path, err := exec.LookPath("notify-send"); err == nil {
		return &Notifier{bin: path, kind: backendNotifySend}
	}
	if path, err := exec.LookPath("osascript"); err == nil {
		return &Notifier{bin: path, kind: backendOsascript}
	}
	return &Notifier{}
}

// Available reports whether desktop notifications will actually fire.
func (n *Notifier) Available() bool { return n.kind != backendNone }

// Notify pops up a notification. summary and body are sanitised. Exit
// status is ignored intentionally; we never want a notification
// failure to crash the daemon.
func (n *Notifier) Notify(summary, body string) {
	if n.kind == backendNone {
		return
	}
	summary = strings.ReplaceAll(summary, "\n", " ")
	switch n.kind {
	case backendNotifySend:
		cmd := exec.Command(n.bin,
			"--app-name=chatd",
			"--icon=preferences-system-network",
			"--urgency=normal",
			"--expire-time=4000",
			summary,
			body,
		)
		go func() { _ = cmd.Run() }()
	case backendOsascript:
		// macOS Notification Center via AppleScript. Quotes inside the
		// strings get escaped; backslashes too because AppleScript
		// double-handles them. Newlines in the body are kept.
		script := fmt.Sprintf(
			`display notification "%s" with title "%s"`,
			escapeAppleScript(body),
			escapeAppleScript(summary),
		)
		cmd := exec.Command(n.bin, "-e", script)
		go func() { _ = cmd.Run() }()
	}
}

// escapeAppleScript escapes backslashes and double-quotes for safe
// embedding inside an AppleScript string literal.
func escapeAppleScript(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `"`, `\"`)
	return s
}
