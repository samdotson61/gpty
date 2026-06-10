package main

import (
	"path/filepath"
	"testing"
)

func TestInvokedAsTmux(t *testing.T) {
	cases := []struct {
		argv0 string
		want  bool
	}{
		{"tmux", true},
		{"tmux.exe", true},
		{"TMUX.EXE", true},
		{filepath.Join("opt", "gpty", "tmux"), true},
		{filepath.Join("opt", "gpty", "tmux.exe"), true},
		{"gmux", false},
		{"gmux.exe", false},
		{filepath.Join("opt", "gpty", "gmux.exe"), false},
		{"tmux2", false},
	}
	for _, c := range cases {
		if got := invokedAsTmux(c.argv0); got != c.want {
			t.Errorf("invokedAsTmux(%q) = %v, want %v", c.argv0, got, c.want)
		}
	}
}
