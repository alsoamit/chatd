package protocol

import (
	"strings"
	"testing"
)

func TestValidateUsername(t *testing.T) {
	cases := []struct {
		name string
		ok   bool
	}{
		{"alice", true},
		{"a-b_c.0", true},
		{"", false},
		{"with space", false},
		{strings.Repeat("a", 65), false},
	}
	for _, c := range cases {
		err := ValidateUsername(c.name)
		if (err == nil) != c.ok {
			t.Errorf("ValidateUsername(%q): ok=%v err=%v", c.name, c.ok, err)
		}
	}
}

func TestValidateBody(t *testing.T) {
	if _, err := ValidateBody(""); err == nil {
		t.Error("expected reject")
	}
	out, _ := ValidateBody("hi  \n")
	if out != "hi" {
		t.Errorf("trim wrong: %q", out)
	}
}

func TestConversationKey(t *testing.T) {
	if ConversationKey("b", "a") != ConversationKey("a", "b") {
		t.Error("not symmetric")
	}
}
