//go:build windows

package tmux

import "strings"

// pasteBuf is the dedicated tmux buffer used for the load-buffer/paste literal
// trick (compat lock §4.3).
const pasteBuf = "agentpty_io"

// sendText types literal text on Windows via load-buffer(stdin)+paste-buffer,
// never as send-keys args. This is win-pty's quoting-safe trick (compat lock
// §4.3): the Windows->cygwin command-line round-trip mangles quotes in argv
// (" -> \), corrupting literal text passed to send-keys. Routing the bytes
// through stdin into a tmux buffer sidesteps argv entirely.
func sendText(target, text string) error {
	if text == "" {
		return nil
	}
	c := cmd(false, "load-buffer", "-b", pasteBuf, "-")
	c.Stdin = strings.NewReader(text)
	if err := c.Run(); err != nil {
		return err
	}
	return RunQuiet("paste-buffer", "-b", pasteBuf, "-t", target, "-d")
}
