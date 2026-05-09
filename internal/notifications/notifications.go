// Package notifications fires desktop notifications via libnotify's
// notify-send. If notify-send is unavailable we silently degrade to a
// no-op so the daemon still works on headless boxes.
package notifications

import (
	"os/exec"
	"strings"
)

// Notifier sends desktop notifications.
type Notifier struct {
	bin string // empty if notify-send is missing
}

// New returns a Notifier. If notify-send is missing the returned
// notifier is a silent no-op.
func New() *Notifier {
	bin, _ := exec.LookPath("notify-send")
	return &Notifier{bin: bin}
}

// Available reports whether desktop notifications will actually fire.
func (n *Notifier) Available() bool { return n.bin != "" }

// Notify pops up a notification. summary and body are sanitised — newlines
// in the summary become spaces, body keeps newlines. Exit status is
// ignored intentionally; we never want a notification failure to crash
// the daemon.
func (n *Notifier) Notify(summary, body string) {
	if n.bin == "" {
		return
	}
	summary = strings.ReplaceAll(summary, "\n", " ")
	cmd := exec.Command(n.bin,
		"--app-name=chatd",
		"--icon=preferences-system-network",
		"--urgency=normal",
		"--expire-time=4000",
		summary,
		body,
	)
	go func() { _ = cmd.Run() }()
}
