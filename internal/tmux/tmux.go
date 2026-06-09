// Package tmux is the exec-based tmux driver: the one-shot path where each call
// spawns a short-lived tmux client (build-plan §2, latency decision 1). The
// resident control-mode path lives in internal/ctl and accelerates the hot
// reads/sends; this package is the always-correct fallback and the CLI engine.
//
// It ports win-pty's proven fixes:
//   - every tmux call gets -u (UTF-8) so block-art glyphs aren't mangled;
//   - session-creating / fire-and-forget calls use null std streams so a forked
//     pane never inherits a pipe we'd block on;
//   - literal keystrokes are injected without going through Windows->cygwin arg
//     quoting (send_windows.go uses load-buffer/paste; send_unix.go uses -l).
package tmux

import (
	"bytes"
	"os"
	"os/exec"

	"github.com/samdotson61/gpty/internal/keys"
	"github.com/samdotson61/gpty/internal/platform"
)

// pasteBuf is the dedicated tmux buffer used for the load-buffer/paste literal
// trick on Windows (compat lock §4.3).
const pasteBuf = "agentpty_io"

// cmd builds a `tmux -u <args...>` command with the right environment.
func cmd(interactive bool, args ...string) *exec.Cmd {
	c := exec.Command(platform.Bin(), append([]string{"-u"}, args...)...)
	c.Env = platform.Env(interactive)
	return c
}

// RunQuiet runs a tmux command with null std streams (Go connects nil streams
// to the null device), so a forked pane can't hold a pipe open. Use for
// session-creating and fire-and-forget commands.
func RunQuiet(args ...string) error {
	return cmd(false, args...).Run()
}

// RunCapture runs a tmux command and returns stdout + exit code. Safe only for
// commands that never fork a pane (list, capture-pane, has-session, display).
func RunCapture(args ...string) (string, int, error) {
	var out, errb bytes.Buffer
	c := cmd(false, args...)
	c.Stdout = &out
	c.Stderr = &errb
	err := c.Run()
	return out.String(), exitCode(err), err
}

// Send parses text into literal/key segments and types them into target (a
// session name like "agent-pty-foo" or a pane id like "%5"). Named keys go via
// send-keys; literal runs go via the platform-safe sendText.
func Send(target, text string) error {
	segs, err := keys.Parse(text)
	if err != nil {
		return err
	}
	for _, s := range segs {
		if s.Kind == keys.Key {
			if err := RunQuiet("send-keys", "-t", target, s.Val); err != nil {
				return err
			}
		} else if err := sendText(target, s.Val); err != nil {
			return err
		}
	}
	return nil
}

// RunQuietCode is like RunQuiet but returns an exit code instead of an error —
// for callers (gmux) that map tmux's status straight to their own.
func RunQuietCode(args ...string) int {
	return exitCode(cmd(false, args...).Run())
}

// Console runs tmux attached to the real terminal for human-facing,
// non-attaching output (gmux ls / kill).
func Console(args ...string) int {
	c := cmd(false, args...)
	c.Stdin, c.Stdout, c.Stderr = os.Stdin, os.Stdout, os.Stderr
	return exitCode(c.Run())
}

// Interactive runs an ATTACHING tmux on the real terminal with the interactive
// environment (Windows: no MSYS=noglob, so cygwin's pcon yields a real pty).
func Interactive(args ...string) int {
	c := cmd(true, args...)
	c.Stdin, c.Stdout, c.Stderr = os.Stdin, os.Stdout, os.Stderr
	return exitCode(c.Run())
}

func exitCode(err error) int {
	if err == nil {
		return 0
	}
	if ee, ok := err.(*exec.ExitError); ok {
		return ee.ExitCode()
	}
	return -1
}
