package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadEnvFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "chatd.env")
	body := "# comment\n" +
		"CHATD_USERNAME = alice\n" +
		"CHATD_TOKEN=\"secret\"\n" +
		"CHATD_RELAY_URL='ws://127.0.0.1:7878/ws'\n" +
		"\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	m, err := LoadEnvFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if m["CHATD_USERNAME"] != "alice" || m["CHATD_TOKEN"] != "secret" || m["CHATD_RELAY_URL"] != "ws://127.0.0.1:7878/ws" {
		t.Errorf("got %v", m)
	}

	missing, err := LoadEnvFile(filepath.Join(dir, "no-such"))
	if err != nil {
		t.Fatal(err)
	}
	if len(missing) != 0 {
		t.Error("missing file should yield empty map")
	}
}

func TestLoadEnvFileBad(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "x.env")
	_ = os.WriteFile(path, []byte("noequals\n"), 0o600)
	if _, err := LoadEnvFile(path); err == nil {
		t.Error("expected parse error")
	}
}

func TestLoadSettingsOverrides(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	t.Setenv("XDG_DATA_HOME", dir)
	p, err := Resolve()
	if err != nil {
		t.Fatal(err)
	}
	if err := p.EnsureDirs(); err != nil {
		t.Fatal(err)
	}
	envBody := "CHATD_USERNAME=alice\nCHATD_TOKEN=t\nCHATD_RELAY_URL=ws://r/ws\n"
	if err := os.WriteFile(p.EnvFile, []byte(envBody), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CHATD_USERNAME", "override")
	s, err := p.LoadSettings()
	if err != nil {
		t.Fatal(err)
	}
	if s.Username != "override" {
		t.Errorf("expected env override, got %q", s.Username)
	}
	if s.Token != "t" {
		t.Errorf("env file fallback failed: %q", s.Token)
	}
	if err := s.Validate(); err != nil {
		t.Error(err)
	}
}

func TestSettingsValidate(t *testing.T) {
	s := Settings{}
	if err := s.Validate(); err == nil {
		t.Error("expected error")
	}
	s = Settings{Username: "a", Token: "t", RelayURL: "ws://x"}
	if err := s.Validate(); err != nil {
		t.Errorf("unexpected: %v", err)
	}
}
