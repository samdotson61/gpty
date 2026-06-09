//go:build !windows

package tmux

// sendText types literal text on Unix. exec.Command passes argv directly (no
// shell, no cygwin re-parse), so send-keys -l is safe and fast: one exec, no
// key-name reinterpretation. The `--` terminates option parsing so text that
// begins with `-` is still taken literally.
func sendText(target, text string) error {
	if text == "" {
		return nil
	}
	return RunQuiet("send-keys", "-l", "-t", target, "--", text)
}
