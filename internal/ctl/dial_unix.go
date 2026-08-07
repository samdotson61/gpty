//go:build !windows

package ctl

import (
	"os/exec"

	"github.com/samdotson61/gpty/internal/platform"
)

// dialCmd builds the resident control client. On Unix a plain `tmux -C` over
// pipes works: the client passes its stdin/stdout fds to the server over the
// unix socket (SCM_RIGHTS) and the server writes control output straight to
// them.
func dialCmd() *exec.Cmd {
	cmd := exec.Command(platform.Bin(),
		"-u", "-C", "new-session", "-A", "-s", ctlSession, "-x", "80", "-y", "24")
	cmd.Env = platform.Env(false) // noglob-safe; control mode needs no pty here
	return cmd
}
