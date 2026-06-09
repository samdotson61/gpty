//go:build windows

package platform

import (
	"os"
	"path/filepath"
	"strings"
)

const listSep = ";"

// defaultBin locates the MSYS2 tmux. Override the root with MSYS2_ROOT, or the
// binary directly with GPTY_TMUX. There is no native Windows tmux — this is the
// cygwin-runtime tmux.exe that `gpty setup` / install.ps1 install.
func defaultBin() string {
	root := os.Getenv("MSYS2_ROOT")
	if root == "" {
		root = `C:\msys64`
	}
	return filepath.Join(root, `usr\bin\tmux.exe`)
}

// osEnv ports wmux's buildEnv. It always sets a UTF-8 locale and prepends the
// exe dir + tmux dir to PATH, then applies MSYS=noglob ONLY for non-interactive
// commands.
//
// Why noglob is conditional: MSYS=noglob disables Cygwin's pseudo-console
// (pcon), and the interactive tmux CLIENT needs pcon to attach to a native
// Windows console — without it tmux dies with "open terminal failed: not a
// terminal". So attach/new (interactive=true) run WITHOUT noglob. Only commands
// that pass tmux FORMAT strings as args — which Cygwin would otherwise
// brace-expand, #{x} -> #x — need noglob (interactive=false). Any inherited
// MSYS is stripped first so we fully control it.
func osEnv(interactive bool) []string {
	env := os.Environ()
	kept := env[:0]
	for _, e := range env {
		if !strings.HasPrefix(e, "MSYS=") {
			kept = append(kept, e)
		}
	}
	env = kept
	if !interactive {
		env = setEnv(env, "MSYS", "noglob")
	}
	env = ensureUTF8(env)
	env = pathWith(env, exeDir(), filepath.Dir(Bin()))
	return env
}
