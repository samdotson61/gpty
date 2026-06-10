package tmux

import "testing"

func TestParseCommandList(t *testing.T) {
	out := "attach-session attach\nchoose-buffer\nkill-server\nsend-keys send\n\n"
	set := parseCommandList(out)
	for _, want := range []string{"attach-session", "attach", "choose-buffer", "kill-server", "send-keys", "send"} {
		if !set[want] {
			t.Errorf("parseCommandList missing %q", want)
		}
	}
	for _, no := range []string{"", "spawn", "snapshto"} {
		if set[no] {
			t.Errorf("parseCommandList wrongly contains %q", no)
		}
	}
}

func TestIsAttaching(t *testing.T) {
	cases := []struct {
		name string
		args []string
		want bool
	}{
		{"attach-session", nil, true},
		{"attach", []string{"-t", "work"}, true},
		{"attach", []string{"-d", "-t", "work"}, true}, // attach -d detaches OTHERS, still attaches
		{"new-session", []string{"-s", "work"}, true},
		{"new", nil, true},
		{"new-session", []string{"-d", "-s", "work"}, false},
		{"new", []string{"-d"}, false},
		{"list-sessions", nil, false},
		{"kill-server", nil, false},
		{"send-keys", []string{"-t", "work", "ls", "Enter"}, false},
	}
	for _, c := range cases {
		if got := isAttaching(c.name, c.args); got != c.want {
			t.Errorf("isAttaching(%q, %v) = %v, want %v", c.name, c.args, got, c.want)
		}
	}
}
