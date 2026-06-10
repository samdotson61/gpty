package platform

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func tmuxName() string {
	if runtime.GOOS == "windows" {
		return "tmux.exe"
	}
	return "tmux"
}

// writeFake creates an executable file named like tmux in dir.
func writeFake(t *testing.T, dir string) string {
	t.Helper()
	p := filepath.Join(dir, tmuxName())
	if err := os.WriteFile(p, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatalf("writeFake(%s): %v", dir, err)
	}
	return p
}

func TestIsExeDir(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Fatalf("os.Executable: %v", err)
	}
	if !isExeDir(filepath.Dir(exe)) {
		t.Error("isExeDir(own dir) = false, want true")
	}
	if isExeDir(t.TempDir()) {
		t.Error("isExeDir(temp dir) = true, want false")
	}
	if isExeDir("") {
		t.Error(`isExeDir("") = true, want false`)
	}
}

// The recursion guard: a `tmux` that is really the gmux alias sits in our own
// install dir; resolution must skip it and find the one further down PATH.
func TestLookPathSkipsOwnDir(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Fatalf("os.Executable: %v", err)
	}
	ownDir := filepath.Dir(exe)
	alias := writeFake(t, ownDir) // the trap: an alias next to the running binary
	defer os.Remove(alias)
	other := t.TempDir()
	real := writeFake(t, other)

	t.Setenv("PATH", ownDir+listSep+other)
	if got := lookPathSkipExeDir("tmux"); got != real {
		t.Errorf("lookPathSkipExeDir = %q, want %q (the non-alias copy)", got, real)
	}

	// With ONLY the alias on PATH, resolution must yield nothing rather than
	// ourselves.
	t.Setenv("PATH", ownDir)
	if got := lookPathSkipExeDir("tmux"); got != "" {
		t.Errorf("lookPathSkipExeDir with only own dir on PATH = %q, want \"\"", got)
	}
}
