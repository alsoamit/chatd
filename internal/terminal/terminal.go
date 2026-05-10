// Package terminal abstracts spawning a terminal emulator window with a
// chosen process inside it. Ghostty is the primary target; we fall back
// to kitty, alacritty, foot, and finally xterm. The abstraction lets us
// keep daemon code portable to other emulators without changing the
// caller.
package terminal

import (
	"errors"
	"fmt"
	"os/exec"
	"strings"
)

// Backend is the terminal emulator we'll spawn.
type Backend string

const (
	Ghostty   Backend = "ghostty"
	Kitty     Backend = "kitty"
	Alacritty Backend = "alacritty"
	WezTerm   Backend = "wezterm"
	Foot      Backend = "foot"
	Xterm     Backend = "xterm"
)

// All preference order: try first available. Ghostty/kitty/alacritty/
// wezterm work identically on Linux and macOS; foot is Wayland-Linux
// only; xterm is X11-Linux only and exists as a last-ditch fallback.
var preferred = []Backend{Ghostty, Kitty, Alacritty, WezTerm, Foot, Xterm}

// Launcher knows how to spawn windows.
type Launcher struct {
	Backend Backend
	Path    string // absolute path to the emulator binary
}

// Detect picks a backend. If hint is non-empty we honour it (failing if
// missing). Otherwise we walk the preference list and return the first
// emulator we find on $PATH.
func Detect(hint string) (Launcher, error) {
	if hint != "" {
		path, err := exec.LookPath(hint)
		if err != nil {
			return Launcher{}, fmt.Errorf("terminal hint %q not found: %w", hint, err)
		}
		return Launcher{Backend: Backend(hint), Path: path}, nil
	}
	for _, b := range preferred {
		if path, err := exec.LookPath(string(b)); err == nil {
			return Launcher{Backend: b, Path: path}, nil
		}
	}
	return Launcher{}, errors.New("no terminal emulator found (install ghostty / kitty / alacritty / foot / xterm)")
}

// HeadlessLauncher is a Spawner that never opens windows. The daemon
// uses it on machines without any installed emulator so that the
// relay-side and IPC-side functionality still work; users can fall
// back to `chat dashboard` and `chat-client` directly in their terminal.
type HeadlessLauncher struct {
	Logger interface{ Printf(string, ...any) }
}

// Spawn logs the would-be invocation and returns nil.
func (h HeadlessLauncher) Spawn(title string, argv []string) error {
	if h.Logger != nil {
		h.Logger.Printf("terminal: headless mode — would spawn %q with %v", title, argv)
	}
	return nil
}

// Spawn launches the emulator with the given title and child argv. We
// detach the child fully — no Wait, no captured stdio — so Ghostty
// behaves like a desktop window.
func (l Launcher) Spawn(title string, argv []string) error {
	if len(argv) == 0 {
		return errors.New("terminal: empty argv")
	}
	args := l.buildArgs(title, argv)
	cmd := exec.Command(l.Path, args...)
	// We deliberately do not inherit stdin/stdout/stderr; the child runs
	// in its own emulator window which owns the tty.
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("spawn %s: %w", l.Path, err)
	}
	// Release the process so we don't accumulate zombies even if the
	// daemon outlives the window.
	go func() { _ = cmd.Wait() }()
	return nil
}

func (l Launcher) buildArgs(title string, argv []string) []string {
	switch l.Backend {
	case Ghostty:
		// Ghostty: --title=foo -e cmd args...
		args := []string{}
		if title != "" {
			args = append(args, "--title="+title)
		}
		args = append(args, "-e")
		args = append(args, argv...)
		return args
	case Kitty:
		args := []string{}
		if title != "" {
			args = append(args, "--title", title)
		}
		args = append(args, argv...)
		return args
	case Alacritty:
		args := []string{}
		if title != "" {
			args = append(args, "-T", title)
		}
		args = append(args, "-e")
		args = append(args, argv...)
		return args
	case WezTerm:
		// `wezterm start --class chatd-<title> -- prog args` opens a
		// fresh window. WezTerm sets the OS window title from --class
		// when it recognises the value, otherwise the program owns it.
		args := []string{"start"}
		if title != "" {
			args = append(args, "--class", title)
		}
		args = append(args, "--")
		args = append(args, argv...)
		return args
	case Foot:
		args := []string{}
		if title != "" {
			args = append(args, "-T", title)
		}
		args = append(args, argv...)
		return args
	case Xterm:
		args := []string{}
		if title != "" {
			args = append(args, "-T", title)
		}
		args = append(args, "-e")
		args = append(args, argv...)
		return args
	default:
		// Best-effort generic
		return append([]string{"-e"}, argv...)
	}
}

// SafeTitle returns title with control characters stripped — Ghostty
// honours OSC sequences in titles, so we strip anything dangerous before
// passing user-controlled strings.
func SafeTitle(s string) string {
	var b strings.Builder
	for _, r := range s {
		if r < 32 || r == 127 {
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}
