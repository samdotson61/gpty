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
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
)

// Bin locates the tmux binary: $GPTY_TMUX (or the legacy $WMUX_TMUX), then
// PATH, then the OS default (MSYS2 on Windows, plain "tmux" elsewhere).
//
// Every resolution layer rejects candidates that are really US: the installers
// register gmux under the name `tmux` (busybox-style alias), and resolving
// that here would make gpty/gmux exec themselves forever. Two checks compose
// the guard — "same dir as the running executable" (catches the Windows copy
// next to gmux.exe) and os.SameFile against the running executable (catches a
// Unix symlink wherever it lives on PATH). The fallback validates too, because
// on Unix it is the bare name "tmux", which exec.Command would resolve through
// PATH straight back to the alias. GPTY_EXEC_DEPTH (see DepthGuard) backstops
// anything that slips through all of it.
func Bin() string {
	for _, k := range []string{"GPTY_TMUX", "WMUX_TMUX"} {
		if v := os.Getenv(k); v != "" {
			if isSelf(v) {
				warnAliasOnly(fmt.Sprintf("$%s points at gpty's own tmux alias — ignoring it", k))
				continue
			}
			return v
		}
	}
	if p, err := exec.LookPath("tmux"); err == nil && !isExeDir(filepath.Dir(p)) && !isSelf(p) {
		return p
	}
	if p := lookPathSkipExeDir("tmux"); p != "" {
		return p
	}
	return fallbackBin()
}

// poisonBin is returned when the only "tmux" anywhere is our own alias. It is
// a name that cannot resolve back to the alias, so the exec fails loud and
// clean ("executable file not found") instead of forking a chain.
const poisonBin = "tmux-not-found"

// fallbackBin validates the OS default before returning it. On Unix the
// default is the bare name "tmux" — and if we got this far, the only PATH hit
// left (if any) is our own alias, so handing the bare name to exec.Command
// would re-enter us once per call until OOM (the fork-chain bug).
func fallbackBin() string {
	d := defaultBin()
	if p, err := exec.LookPath(d); err == nil && (isExeDir(filepath.Dir(p)) || isSelf(p)) {
		warnAliasOnly("no real tmux found — the only `tmux` on PATH is gpty's own alias. Install tmux or set GPTY_TMUX; `gpty doctor` explains")
		return poisonBin
	}
	return d
}

var warnOnce sync.Once

func warnAliasOnly(msg string) {
	warnOnce.Do(func() { fmt.Fprintln(os.Stderr, "gpty: "+msg) })
}

// lookPathSkipExeDir scans PATH for name, skipping the running executable's
// own directory and anything that IS the running executable (the `tmux`
// alias of gmux, as a same-dir copy on Windows or a symlink anywhere on Unix).
func lookPathSkipExeDir(name string) string {
	for _, dir := range filepath.SplitList(os.Getenv("PATH")) {
		if dir == "" || isExeDir(dir) {
			continue
		}
		if p := executableIn(dir, name); p != "" && !isSelf(p) {
			return p
		}
	}
	return ""
}

// isExeDir reports whether dir is the running executable's directory.
// os.SameFile handles case differences and links.
func isExeDir(dir string) bool {
	ed := exeDir()
	if ed == "" || dir == "" {
		return false
	}
	a, err1 := os.Stat(dir)
	b, err2 := os.Stat(ed)
	return err1 == nil && err2 == nil && os.SameFile(a, b)
}

// isSelf reports whether p is the running executable itself. Stat follows
// symlinks, so a `tmux -> gmux` alias link compares equal wherever it lives.
func isSelf(p string) bool {
	exe, err := os.Executable()
	if err != nil {
		return false
	}
	a, err1 := os.Stat(p)
	b, err2 := os.Stat(exe)
	return err1 == nil && err2 == nil && os.SameFile(a, b)
}

// --- exec-depth sentinel ------------------------------------------------------

// maxExecDepth caps nested gpty/gmux→tmux executions. Legitimate nesting
// (gmux run inside a pane of a gmux session) adds one level per human step;
// a self-resolving alias adds one per process and hits the cap in
// milliseconds. Defense-in-depth behind Bin()'s self checks.
const maxExecDepth = 10

// DepthGuard exits loudly when GPTY_EXEC_DEPTH shows a runaway exec chain —
// the signature of the tmux alias resolving back to itself. Call first thing
// in main.
func DepthGuard(tool string) {
	if depthExceeded(os.Getenv("GPTY_EXEC_DEPTH")) {
		fmt.Fprintf(os.Stderr,
			"%s: %s nested tmux executions — gpty's tmux alias appears to be execing itself (no real tmux found?). Install tmux or set GPTY_TMUX; `gpty doctor` explains.\n",
			tool, os.Getenv("GPTY_EXEC_DEPTH"))
		os.Exit(1)
	}
}

func depthExceeded(v string) bool {
	n, err := strconv.Atoi(v)
	return err == nil && n >= maxExecDepth
}

// bumpDepth increments GPTY_EXEC_DEPTH in a child environment.
func bumpDepth(env []string) []string {
	n, _ := strconv.Atoi(os.Getenv("GPTY_EXEC_DEPTH"))
	return setEnv(env, "GPTY_EXEC_DEPTH", strconv.Itoa(n+1))
}

// Env returns the environment for invoking tmux.
//
// interactive=true is for an attaching client (gmux new/attach). interactive
// =false is for piped / format-string commands (everything the agent surface
// does). On Unix the flag is a no-op; on Windows it gates MSYS=noglob — see
// osEnv in windows.go for the why. Every invocation also bumps
// GPTY_EXEC_DEPTH for DepthGuard's runaway-alias backstop.
func Env(interactive bool) []string { return bumpDepth(osEnv(interactive)) }

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
