package ctl

import "testing"

func TestEncodeCommand(t *testing.T) {
	cases := []struct {
		name string
		in   []string
		want string
	}{
		{"plain flags+target", []string{"capture-pane", "-p", "-t", "agent-pty-demo"}, "capture-pane -p -t agent-pty-demo"},
		{"pane id needs no quote", []string{"capture-pane", "-p", "-t", "%5"}, "capture-pane -p -t %5"},
		{"format string is quoted (the # trap)", []string{"list-sessions", "-F", "#{session_name}"}, `list-sessions -F "#{session_name}"`},
		{"hex send-keys", []string{"send-keys", "-H", "-t", "agent-pty-demo", "68", "69"}, "send-keys -H -t agent-pty-demo 68 69"},
		{"named key", []string{"send-keys", "-t", "agent-pty-demo", "C-c"}, "send-keys -t agent-pty-demo C-c"},
		{"space gets quoted", []string{"display-message", "-p", "a b"}, `display-message -p "a b"`},
		{"empty arg quoted", []string{"x", ""}, `x ""`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := encodeCommand(c.in); got != c.want {
				t.Fatalf("encodeCommand(%v) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}
