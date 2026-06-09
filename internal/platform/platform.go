// Package platform isolates the only OS-specific code in gpty: locating the
// tmux binary and building the environment tmux runs with. Everything above
// this package is identical across Windows, macOS, and Linux.
//
// This is the merge point for win-pty's tmuxEnv and wmux's buildEnv, which had
// drifted apart across the two repos (build-plan §8, Phase 1). The Windows
// half preserves wmux's hard-won conditional-noglob logic verbatim; the Unix
// half is deliberately small.
package platform

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Bin locates the tmux binary: $GPTY_TMUX (or the legacy $WMUX_TMUX), then
// PATH, then the OS default (MSYS2 on Windows, plain "tmux" elsewhere).
func Bin() string {
	for _, k := range []string{"GPTY_TMUX", "WMUX_TMUX"} {
		if v := os.Getenv(k); v != "" {
			return v
		}
	}
	if p, err := exec.LookPath("tmux"); err == nil {
		return p
	}
	return defaultBin()
}

// Env returns the environment for invoking tmux.
//
// interactive=true is for an attaching client (gmux new/attach). interactive
// =false is for piped / format-string commands (everything the agent surface
// does). On Unix the flag is a no-op; on Windows it gates MSYS=noglob — see
// osEnv in windows.go for the why.
func Env(interactive bool) []string { return osEnv(interactive) }

// --- shared env helpers (used by both unix.go and windows.go) ---------------

// setEnv replaces KEY=… in env, or appends it.
func setEnv(env []string, k, v string) []string {
	pre := k + "="
	for i, e := range env {
		if strings.HasPrefix(e, pre) {
			env[i] = pre + v
			return env
		}
	}
	return append(env, pre+v)
}

// ensureUTF8 guarantees LANG/LC_CTYPE carry a UTF-8 locale so the block-art
// glyphs an agent draws survive the tmux round-trip. It only overrides LANG
// when it isn't already UTF-8, so a user's richer locale is left alone.
func ensureUTF8(env []string) []string {
	lang := os.Getenv("LANG")
	if u := strings.ToUpper(lang); !strings.Contains(u, "UTF-8") && !strings.Contains(u, "UTF8") {
		lang = "C.UTF-8"
		env = setEnv(env, "LANG", lang)
	}
	return setEnv(env, "LC_CTYPE", lang)
}

// pathWith prepends dirs (in order) to PATH so co-located tools — gpty, gmux,
// and tmux itself — resolve inside spawned panes.
func pathWith(env []string, dirs ...string) []string {
	var keep []string
	for _, d := range dirs {
		if d != "" {
			keep = append(keep, d)
		}
	}
	if len(keep) == 0 {
		return env
	}
	prefix := strings.Join(keep, listSep)
	for i, e := range env {
		if strings.HasPrefix(e, "PATH=") || strings.HasPrefix(e, "Path=") {
			env[i] = "PATH=" + prefix + listSep + strings.SplitN(e, "=", 2)[1]
			return env
		}
	}
	return append(env, "PATH="+prefix)
}

// exeDir is the directory holding the running gpty/gmux executable.
func exeDir() string {
	if exe, err := os.Executable(); err == nil {
		return filepath.Dir(exe)
	}
	return ""
}
