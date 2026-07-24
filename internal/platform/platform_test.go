package platform

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// writeExe creates an executable file with the given content.
func writeExe(t *testing.T, dir, name, content string) string {
	t.Helper()
	if runtime.GOOS == "windows" && filepath.Ext(name) == "" {
		name += ".exe"
	}
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(content), 0o755); err != nil {
		t.Fatalf("writeExe(%s): %v", p, err)
	}
	t.Cleanup(func() { os.Remove(p) })
	return p
}

// writeFake creates a distinct (non-alias) "tmux" in dir — stands in for the
// real tmux binary.
func writeFake(t *testing.T, dir string) string {
	t.Helper()
	return writeExe(t, dir, "tmux", "#!/bin/sh\n# a real tmux, not ours\n")
}

func ownDir(t *testing.T) string {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatalf("os.Executable: %v", err)
	}
	return filepath.Dir(exe)
}

// isolateResolution clears the env overrides so a test exercises PATH
// resolution itself. Without this, any environment that sets GPTY_TMUX — the
// Windows CI job does, pointing at the MSYS2 tmux — short-circuits Bin()
// before PATH is ever consulted.
func isolateResolution(t *testing.T) {
	t.Helper()
	t.Setenv("GPTY_TMUX", "")
	t.Setenv("WMUX_TMUX", "")
}

func TestIsSelf(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Fatalf("os.Executable: %v", err)
	}
	if !isSelf(exe) {
		t.Error("isSelf(own executable) = false, want true")
	}
	if isSelf(writeFake(t, t.TempDir())) {
		t.Error("isSelf(unrelated file) = true, want false")
	}
	if isSelf(filepath.Join(t.TempDir(), "missing")) {
		t.Error("isSelf(missing path) = true, want false")
	}
}

// isAliasOfOurs must catch both alias forms and nothing else.
func TestIsAliasOfOurs(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Fatalf("os.Executable: %v", err)
	}

	if !isAliasOfOurs(exe) {
		t.Error("isAliasOfOurs(running executable) = false, want true")
	}

	// A real, distinct tmux — even sitting in our own directory (the Homebrew
	// layout) — is NOT our alias.
	if isAliasOfOurs(writeFake(t, ownDir(t))) {
		t.Error("isAliasOfOurs(distinct binary in own dir) = true, want false — this is the Homebrew case")
	}
	if isAliasOfOurs(writeFake(t, t.TempDir())) {
		t.Error("isAliasOfOurs(unrelated binary) = true, want false")
	}

	// The Windows-style alias: a byte-identical COPY of a sibling gmux.
	const body = "MZ fake gmux binary payload\n"
	writeExe(t, ownDir(t), "gmux", body)
	copyAlias := writeExe(t, t.TempDir(), "tmux", body)
	if !isAliasOfOurs(copyAlias) {
		t.Error("isAliasOfOurs(byte-identical copy of sibling gmux) = false, want true")
	}
	// Same size but different bytes must not trip it.
	differing := writeExe(t, t.TempDir(), "tmux", "MZ fake gmux binary payloaX\n")
	if len(body) == len("MZ fake gmux binary payloaX\n") && isAliasOfOurs(differing) {
		t.Error("isAliasOfOurs(same size, different content) = true, want false")
	}
}

// REGRESSION (found by live-testing the Homebrew tap at v0.7.0): brew symlinks
// gpty AND the real tmux into the same bin dir. The old location-based guard
// rejected everything in that directory, so a brew-installed gpty resolved
// tmux to the poison name and could not run at all.
func TestHomebrewLayoutFindsRealTmux(t *testing.T) {
	isolateResolution(t)
	dir := ownDir(t)
	real := writeFake(t, dir) // real tmux, same directory as the running binary
	t.Setenv("PATH", dir)
	if got := Bin(); got != real {
		t.Errorf("Bin() = %q, want %q — a real tmux beside our binary must be used, not rejected", got, real)
	}
}

// The recursion guard still holds: a `tmux` that really IS our alias must be
// skipped in favour of one further down PATH.
func TestLookPathSkipsAlias(t *testing.T) {
	isolateResolution(t)
	if runtime.GOOS == "windows" {
		t.Skip("uses a symlink; the copy form is covered by TestIsAliasOfOurs")
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatalf("os.Executable: %v", err)
	}
	aliasDir := t.TempDir()
	alias := filepath.Join(aliasDir, "tmux")
	if err := os.Symlink(exe, alias); err != nil {
		t.Skipf("symlink: %v", err)
	}
	other := t.TempDir()
	real := writeFake(t, other)

	t.Setenv("PATH", aliasDir+listSep+other)
	if got := lookPathSkipAlias("tmux"); got != real {
		t.Errorf("lookPathSkipAlias = %q, want %q (the real tmux)", got, real)
	}
	if got := Bin(); got != real {
		t.Errorf("Bin() = %q, want %q", got, real)
	}

	// With ONLY the alias on PATH, resolution must yield nothing rather than us.
	t.Setenv("PATH", aliasDir)
	if got := lookPathSkipAlias("tmux"); got != "" {
		t.Errorf("lookPathSkipAlias with only the alias on PATH = %q, want \"\"", got)
	}
}

// The fork-chain bug: with the alias as the only tmux on PATH, the bare-name
// fallback must yield the poison name, never something that resolves back to
// the alias.
func TestFallbackBinRejectsAliasOnlyPath(t *testing.T) {
	isolateResolution(t)
	if runtime.GOOS == "windows" {
		t.Skip("Windows fallback is an absolute MSYS2 path, not a PATH lookup")
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatalf("os.Executable: %v", err)
	}
	dir := t.TempDir()
	if err := os.Symlink(exe, filepath.Join(dir, "tmux")); err != nil {
		t.Skipf("symlink: %v", err)
	}
	t.Setenv("PATH", dir)
	if got := fallbackBin(); got != poisonBin {
		t.Errorf("fallbackBin with alias-only PATH = %q, want %q", got, poisonBin)
	}
	if got := Bin(); got != poisonBin {
		t.Errorf("Bin with alias-only PATH = %q, want %q", got, poisonBin)
	}
}

func TestEnvBumpsExecDepth(t *testing.T) {
	t.Setenv("GPTY_EXEC_DEPTH", "3")
	for _, e := range Env(false) {
		if e == "GPTY_EXEC_DEPTH=4" {
			return
		}
	}
	t.Error("Env did not bump GPTY_EXEC_DEPTH 3 -> 4")
}

func TestDepthExceeded(t *testing.T) {
	cases := map[string]bool{"": false, "abc": false, "0": false, "9": false, "10": true, "999": true}
	for in, want := range cases {
		if got := depthExceeded(in); got != want {
			t.Errorf("depthExceeded(%q) = %v, want %v", in, got, want)
		}
	}
}
