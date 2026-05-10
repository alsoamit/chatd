package notifications

import "testing"

func TestEscapeAppleScript(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"hello", "hello"},
		{`with "quotes"`, `with \"quotes\"`},
		{`back\slash`, `back\\slash`},
		{`both "and" \this`, `both \"and\" \\this`},
		{"line1\nline2", "line1\nline2"}, // newlines preserved
	}
	for _, c := range cases {
		if got := escapeAppleScript(c.in); got != c.want {
			t.Errorf("escapeAppleScript(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestNoneIsSilent(t *testing.T) {
	n := &Notifier{kind: backendNone}
	if n.Available() {
		t.Error("backendNone should not be available")
	}
	// Must not panic.
	n.Notify("title", "body")
}
