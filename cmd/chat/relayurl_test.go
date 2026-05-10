package main

import "testing"

func TestNormalizeRelayURL(t *testing.T) {
	cases := []struct {
		in, want string
		ok       bool
	}{
		{"http://4.61.213.126:7878", "ws://4.61.213.126:7878/ws", true},
		{"https://relay.example.com", "wss://relay.example.com/ws", true},
		{"https://relay.example.com:443/ws", "wss://relay.example.com:443/ws", true},
		{"ws://localhost:7878/ws", "ws://localhost:7878/ws", true},
		{"wss://r.example/ws", "wss://r.example/ws", true},
		{"http://host/path", "ws://host/path", true},
		{"  http://host:80  ", "ws://host:80/ws", true},
		{"", "", false},
		{"relay.example.com", "", false},
		{"ftp://nope", "", false},
	}
	for _, c := range cases {
		got, err := normalizeRelayURL(c.in)
		if (err == nil) != c.ok {
			t.Errorf("normalizeRelayURL(%q) ok=%v err=%v", c.in, c.ok, err)
			continue
		}
		if c.ok && got != c.want {
			t.Errorf("normalizeRelayURL(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
