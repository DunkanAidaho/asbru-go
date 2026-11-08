package ui

// M26: разбор IPC-сообщения "command|text" (легаси _sendAppMessage,
// PACMain.pm:1130-1154).

import "testing"

func TestParseIPC(t *testing.T) {
	cases := []struct {
		in, cmd, text string
	}{
		{"start-shell", "start-shell", ""},
		{"quick-conn", "quick-conn", ""},
		{"start-uuid|u1:prod", "start-uuid", "u1:prod"},
		{"edit-uuid|u2", "edit-uuid", "u2"},
		{"show-conn", "show-conn", ""},
		{"a|b|c", "a", "b|c"}, // текст может содержать '|'
	}
	for _, c := range cases {
		cmd, text := parseIPC(c.in)
		if cmd != c.cmd || text != c.text {
			t.Fatalf("parseIPC(%q) = (%q,%q), want (%q,%q)", c.in, cmd, text, c.cmd, c.text)
		}
	}
}
