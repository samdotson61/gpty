//go:build windows

package ctl

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/samdotson61/gpty/internal/platform"
)

// dialCmd builds the resident control client for Windows.
//
// A plain `tmux -C` over pipes CANNOT work on the msys runtime: the client
// dups its stdin/stdout and passes the fds to the server over the unix
// socket (client.c client_send_identify), but msys's AF_UNIX emulation does
// not deliver SCM_RIGHTS — the server logs IDENTIFY_STDIN/STDOUT -1 and
// writes every control line into a bufferevent on fd -1. Diagnosed live
// 2026-08-06 with -vv server logs: control_vwrite happily "writing line:
// %begin ..." while the client receives zero bytes, on Go pipes and cygwin
// shell pipes alike.
//
// What DOES cross the socket is the tty NAME: the server opens the client's
// terminal by path, no fd involved — which is why interactive attach always
// worked. So we dial `tmux -CC` (control mode over the client tty, the
// iTerm2 channel) inside a real cygwin pty allocated by script(1), and talk
// to the pty master through script's ordinary pipe stdio. -CC sets the tty
// raw, so there is no echo; the stream is the normal %-line protocol wrapped
// in a DCS intro (\x1bP1000p) and terminator (\x1b\), which scan() strips.
// Verified live before implementation: display-message round-trips through
// script+-CC where -C gets nothing.
func dialCmd() *exec.Cmd {
	// script(1) must come from the SAME msys installation as the tmux we
	// dial: mixing two cygwin runtimes (e.g. the GitHub runner image's
	// C:\msys64 and the msys2 action's D:\a\_temp\msys64) gives script a pty
	// the other runtime only half-understands — the channel comes up and
	// then collapses. Prefer tmux's own bin dir, then the explicit root,
	// then the default install.
	candidates := []string{filepath.Join(filepath.Dir(platform.Bin()), "script.exe")}
	if root := os.Getenv("MSYS2_ROOT"); root != "" {
		candidates = append(candidates, filepath.Join(root, `usr\bin\script.exe`))
	}
	candidates = append(candidates, `C:\msys64\usr\bin\script.exe`)
	scriptExe := ""
	for _, cand := range candidates {
		if fi, err := os.Stat(cand); err == nil && fi.Mode().IsRegular() {
			scriptExe = cand
			break
		}
	}
	if scriptExe == "" {
		// No script(1) on this install: dial -C directly. On runtimes without
		// fd passing the handshake fails and exec takes over, same as before.
		cmd := exec.Command(platform.Bin(),
			"-u", "-C", "new-session", "-A", "-s", ctlSession, "-x", "80", "-y", "24")
		cmd.Env = platform.Env(false)
		return cmd
	}
	// script runs the inner command via sh -c; forward slashes and single
	// quotes keep the tmux path intact through sh.
	tmuxPath := strings.ReplaceAll(platform.Bin(), `\`, `/`)
	inner := fmt.Sprintf("'%s' -u -CC new-session -A -s %s -x 80 -y 24", tmuxPath, ctlSession)
	cmd := exec.Command(scriptExe, "-qfc", inner, "/dev/null")
	cmd.Env = platform.Env(false)
	return cmd
}
