package terminal

import (
	"strings"
	"testing"
)

func TestBuildArgsGhostty(t *testing.T) {
	l := Launcher{Backend: Ghostty, Path: "/usr/bin/ghostty"}
	args := l.buildArgs("CHAT — alice", []string{"chat-client", "--peer", "alice"})
	got := strings.Join(args, " ")
	if !strings.Contains(got, "--title=CHAT — alice") {
		t.Errorf("ghostty title flag missing: %q", got)
	}
	if !strings.Contains(got, "-e chat-client --peer alice") {
		t.Errorf("ghostty -e args wrong: %q", got)
	}
}

func TestBuildArgsKitty(t *testing.T) {
	l := Launcher{Backend: Kitty}
	args := l.buildArgs("X", []string{"prog"})
	if args[0] != "--title" || args[1] != "X" || args[2] != "prog" {
		t.Errorf("kitty args wrong: %v", args)
	}
}

func TestSafeTitle(t *testing.T) {
	got := SafeTitle("CHAT — alice\x07\x1b]")
	if strings.ContainsAny(got, "\x07\x1b") {
		t.Errorf("control chars survived: %q", got)
	}
}

func TestDetectHintMissing(t *testing.T) {
	if _, err := Detect("zzz-not-a-thing"); err == nil {
		t.Error("expected error")
	}
}
