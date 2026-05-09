// Command chatd is the persistent local daemon. It's intended to run as
// a systemd-user service: see scripts/install.sh.
//
// All configuration is loaded from ~/.config/chatd/chatd.env. systemd's
// Environment= directives override individual values.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/cedrx/chatd/internal/config"
	"github.com/cedrx/chatd/internal/daemon"
	"github.com/cedrx/chatd/internal/version"
)

func main() {
	logger := log.New(os.Stdout, "chatd ", log.LstdFlags|log.Lmicroseconds)

	chatClientFlag := flag.String("chat-client", "", "absolute path to the chat-client binary; defaults to <self-dir>/chat-client")
	versionFlag := flag.Bool("version", false, "print version and exit")
	flag.Parse()
	if *versionFlag {
		fmt.Println(version.String("chatd"))
		return
	}
	logger.Printf("starting %s", version.String("chatd"))

	paths, err := config.Resolve()
	if err != nil {
		logger.Fatalf("resolve paths: %v", err)
	}
	if err := paths.EnsureDirs(); err != nil {
		logger.Fatalf("mkdir: %v", err)
	}
	settings, err := paths.LoadSettings()
	if err != nil {
		logger.Fatalf("settings: %v", err)
	}
	if err := settings.Validate(); err != nil {
		logger.Fatalf("settings: %v", err)
	}

	chatClient := *chatClientFlag
	if chatClient == "" {
		chatClient = locateSibling("chat-client")
	}
	if chatClient == "" {
		logger.Fatalf("could not locate chat-client binary; pass --chat-client=PATH")
	}

	d, err := daemon.New(daemon.Options{
		Paths: paths, Settings: settings,
		ChatClientPath: chatClient, Logger: logger,
	})
	if err != nil {
		logger.Fatalf("daemon: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-sig
		cancel()
	}()
	if err := d.Run(ctx); err != nil {
		logger.Fatalf("run: %v", err)
	}
}

// locateSibling finds an executable next to the current binary, or on
// $PATH as a last resort. Useful when chatd, chat, and chat-client all
// install into the same /usr/local/bin or ~/.local/bin.
func locateSibling(name string) string {
	if exe, err := os.Executable(); err == nil {
		dir := filepath.Dir(exe)
		candidate := filepath.Join(dir, name)
		if fi, err := os.Stat(candidate); err == nil && fi.Mode()&0o111 != 0 {
			return candidate
		}
	}
	if p, err := exec.LookPath(name); err == nil {
		return p
	}
	return ""
}
