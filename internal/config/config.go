// Package config resolves filesystem paths and loads the user-supplied
// configuration. We honour the XDG basedir spec because the daemon runs
// under systemd-user; falling back to ~/.config and ~/.local/share when
// XDG_* are unset.
package config

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Paths bundles every filesystem location the daemon and its CLIs need.
type Paths struct {
	ConfigDir string // ~/.config/chatd
	DataDir   string // ~/.local/share/chatd
	EnvFile   string // ~/.config/chatd/chatd.env
	IPCSocket string // ~/.config/chatd/ipc.sock
	PIDFile   string // ~/.config/chatd/chatd.pid
	DBFile    string // ~/.local/share/chatd/data.db
	LogFile   string // ~/.local/share/chatd/chatd.log
}

// Resolve computes the standard directory layout. It does not create
// any directories.
func Resolve() (Paths, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return Paths{}, err
	}
	configHome := os.Getenv("XDG_CONFIG_HOME")
	if configHome == "" {
		configHome = filepath.Join(home, ".config")
	}
	dataHome := os.Getenv("XDG_DATA_HOME")
	if dataHome == "" {
		dataHome = filepath.Join(home, ".local", "share")
	}
	cfgDir := filepath.Join(configHome, "chatd")
	dataDir := filepath.Join(dataHome, "chatd")
	return Paths{
		ConfigDir: cfgDir,
		DataDir:   dataDir,
		EnvFile:   filepath.Join(cfgDir, "chatd.env"),
		IPCSocket: filepath.Join(cfgDir, "ipc.sock"),
		PIDFile:   filepath.Join(cfgDir, "chatd.pid"),
		DBFile:    filepath.Join(dataDir, "data.db"),
		LogFile:   filepath.Join(dataDir, "chatd.log"),
	}, nil
}

// EnsureDirs creates ConfigDir and DataDir with 0700 permissions.
func (p Paths) EnsureDirs() error {
	for _, d := range []string{p.ConfigDir, p.DataDir} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			return err
		}
	}
	return nil
}

// Settings is the parsed user configuration.
type Settings struct {
	Username string
	Token    string
	RelayURL string // ws://host:port/ws
	Terminal string // ghostty | kitty | alacritty | xterm — autodetect when empty
}

// LoadEnvFile reads a simple KEY=VALUE file and returns the entries as a
// map. Missing files return an empty map (not an error). Comment lines
// start with '#'. Quotes around values are stripped.
func LoadEnvFile(path string) (map[string]string, error) {
	out := map[string]string{}
	f, err := os.Open(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return out, nil
		}
		return nil, err
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 4096), 64*1024)
	lineno := 0
	for sc.Scan() {
		lineno++
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		eq := strings.IndexByte(line, '=')
		if eq <= 0 {
			return nil, fmt.Errorf("%s:%d: missing '='", path, lineno)
		}
		k := strings.TrimSpace(line[:eq])
		v := strings.TrimSpace(line[eq+1:])
		if len(v) >= 2 && (v[0] == '"' && v[len(v)-1] == '"' || v[0] == '\'' && v[len(v)-1] == '\'') {
			v = v[1 : len(v)-1]
		}
		out[k] = v
	}
	return out, sc.Err()
}

// LoadSettings combines the env file with process-environment overrides.
// Process env always wins so systemd's Environment= directives can pin
// values. Unset entries fall through to defaults.
func (p Paths) LoadSettings() (Settings, error) {
	envFile, err := LoadEnvFile(p.EnvFile)
	if err != nil {
		return Settings{}, err
	}
	get := func(key string) string {
		if v := os.Getenv(key); v != "" {
			return v
		}
		return envFile[key]
	}
	s := Settings{
		Username: get("CHATD_USERNAME"),
		Token:    get("CHATD_TOKEN"),
		RelayURL: get("CHATD_RELAY_URL"),
		Terminal: get("CHATD_TERMINAL"),
	}
	if s.RelayURL == "" {
		s.RelayURL = "ws://127.0.0.1:7878/ws"
	}
	return s, nil
}

// Validate checks that the minimum required settings are present.
func (s Settings) Validate() error {
	if s.Username == "" {
		return errors.New("CHATD_USERNAME is required")
	}
	if s.Token == "" {
		return errors.New("CHATD_TOKEN is required")
	}
	if s.RelayURL == "" {
		return errors.New("CHATD_RELAY_URL is required")
	}
	return nil
}
