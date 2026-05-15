// Package daemon wires every long-lived component together: the relay
// connection, the IPC server, the conversation manager, the storage
// handle and the terminal launcher. cmd/daemon is a thin shell over
// Daemon.Run.
package daemon

import (
	"context"
	"fmt"
	"log"
	"os"

	"github.com/cedrx/chatd/internal/config"
	"github.com/cedrx/chatd/internal/conversation"
	"github.com/cedrx/chatd/internal/crypto"
	"github.com/cedrx/chatd/internal/ipc"
	"github.com/cedrx/chatd/internal/notifications"
	"github.com/cedrx/chatd/internal/storage"
	"github.com/cedrx/chatd/internal/terminal"
	"github.com/cedrx/chatd/internal/wsclient"
)

// Options controls a daemon instance.
type Options struct {
	Paths          config.Paths
	Settings       config.Settings
	ChatClientPath string
	Logger         *log.Logger
}

// Daemon is the composed runtime.
type Daemon struct {
	opts    Options
	store   *storage.Store
	ws      *wsclient.Client
	ipc     *ipc.Server
	manager *conversation.Manager
	notif   *notifications.Notifier
}

// New constructs a Daemon. Call Run to start it.
func New(opts Options) (*Daemon, error) {
	if opts.Logger == nil {
		opts.Logger = log.New(os.Stdout, "chatd ", log.LstdFlags|log.Lmicroseconds)
	}
	if err := opts.Paths.EnsureDirs(); err != nil {
		return nil, err
	}
	if err := opts.Settings.Validate(); err != nil {
		return nil, err
	}
	st, err := storage.Open(opts.Paths.DBFile)
	if err != nil {
		return nil, err
	}

	identity, err := crypto.LoadOrCreate(opts.Paths.IdentityFile)
	if err != nil {
		_ = st.Close()
		return nil, fmt.Errorf("identity: %w", err)
	}
	opts.Logger.Printf("daemon: identity %s (fp %s)",
		opts.Paths.IdentityFile, crypto.Fingerprint(identity.PublicKey()))

	var spawner conversation.Spawner
	launcher, err := terminal.Detect(opts.Settings.Terminal)
	if err != nil {
		opts.Logger.Printf("daemon: %v — falling back to headless launcher", err)
		spawner = terminal.HeadlessLauncher{Logger: opts.Logger}
	} else {
		opts.Logger.Printf("daemon: terminal=%s path=%s", launcher.Backend, launcher.Path)
		spawner = launcher
	}

	ws := wsclient.New(wsclient.Config{
		URL:      opts.Settings.RelayURL,
		Username: opts.Settings.Username,
		Token:    opts.Settings.Token,
		PubKey:   identity.PublicKeyB64(),
		Logger:   opts.Logger,
	})

	srv := ipc.NewServer(opts.Paths.IPCSocket, nil, opts.Logger)

	notif := notifications.New()

	m := conversation.New(conversation.Config{
		Username:       opts.Settings.Username,
		RelayURL:       opts.Settings.RelayURL,
		Identity:       identity,
		Storage:        st,
		WS:             ws,
		IPC:            srv,
		Spawner:        spawner,
		Notifier:       notif,
		ChatClientPath: opts.ChatClientPath,
		ChatClientArgs: []string{"--socket", opts.Paths.IPCSocket},
		Logger:         opts.Logger,
	})

	srv.SetHandler(m)

	return &Daemon{
		opts: opts, store: st, ws: ws, ipc: srv,
		manager: m, notif: notif,
	}, nil
}

// Run blocks until ctx is cancelled.
func (d *Daemon) Run(ctx context.Context) error {
	if err := d.ipc.Start(); err != nil {
		return fmt.Errorf("ipc start: %w", err)
	}
	defer d.ipc.Stop()
	defer func() { _ = d.store.Close() }()

	if err := writePID(d.opts.Paths.PIDFile); err != nil {
		d.opts.Logger.Printf("daemon: pidfile: %v", err)
	}
	defer func() { _ = os.Remove(d.opts.Paths.PIDFile) }()

	go d.ipc.Serve(ctx)
	go d.ws.Run(ctx)
	go d.manager.Run(ctx)

	d.opts.Logger.Printf("daemon: ready (user=%s relay=%s socket=%s)",
		d.opts.Settings.Username, d.opts.Settings.RelayURL, d.opts.Paths.IPCSocket)
	<-ctx.Done()
	d.opts.Logger.Printf("daemon: shutting down")
	return nil
}

func writePID(path string) error {
	return os.WriteFile(path, []byte(fmt.Sprintf("%d\n", os.Getpid())), 0o600)
}
