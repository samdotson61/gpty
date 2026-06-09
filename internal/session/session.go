// Package session implements name-addressable background terminal sessions —
// the agent-pty abstraction. Sessions live on the default tmux socket under the
// frozen prefix "agent-pty-<name>" (compat lock §4.2), so existing tooling and
// `tmux attach -t agent-pty-<name>` keep working.
package session

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/samdotson61/gpty/internal/tmux"
)

// Prefix namespaces gpty sessions on the default tmux socket (compat lock §4.2).
const Prefix = "agent-pty-"

// PollInterval is how often the exec WaitFor re-captures the pane. The resident
// control-mode path (internal/ctl) polls far faster over its open channel.
const PollInterval = 200 * time.Millisecond

// Full returns the tmux session name for a user-facing name.
func Full(name string) string { return Prefix + name }

// Strip removes the prefix from a tmux session name.
func Strip(s string) string { return strings.TrimPrefix(s, Prefix) }

// IsManaged reports whether a raw tmux session name is a user-facing gpty
// session (i.e. prefixed and not the hidden keepalive/control sessions).
func IsManaged(raw string) bool {
	return strings.HasPrefix(raw, Prefix)
}

// Has reports whether session <name> exists.
func Has(name string) bool {
	_, code, _ := tmux.RunCapture("has-session", "-t", Full(name))
	return code == 0
}

// healIfDown clears the cached server-ready state when the server has actually
// gone away (e.g. an external kill), so the next operation rebuilds it. Called
// only on the failure path, so it costs nothing in the common case.
func healIfDown() {
	if !tmux.ServerUp() {
		tmux.MarkServerDown()
	}
}

// Spawn creates a detached session. cmd=="" uses tmux's default-command (on
// Windows our tmux.conf makes that PowerShell 7; on Unix it's the user's shell).
func Spawn(name, cmd, cwd string, cols, rows int) error {
	tmux.EnsureServer()
	if Has(name) {
		return fmt.Errorf("session %q already exists", name)
	}
	if cols <= 0 {
		cols = 80
	}
	if rows <= 0 {
		rows = 24
	}
	args := []string{"new-session", "-d", "-s", Full(name), "-x", strconv.Itoa(cols), "-y", strconv.Itoa(rows)}
	if cwd != "" {
		args = append(args, "-c", cwd)
	}
	if cmd != "" {
		args = append(args, cmd)
	}
	return tmux.RunQuiet(args...)
}

// Kill removes a session.
func Kill(name string) error {
	tmux.EnsureServer()
	if !Has(name) {
		return fmt.Errorf("session %q not found", name)
	}
	return tmux.RunQuiet("kill-session", "-t", Full(name))
}

// List returns the user-facing names of all managed sessions.
func List() ([]string, error) {
	tmux.EnsureServer()
	out, code, _ := tmux.RunCapture("list-sessions", "-F", "#{session_name}")
	if code != 0 {
		return []string{}, nil
	}
	names := []string{}
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || !IsManaged(line) {
			continue
		}
		names = append(names, Strip(line))
	}
	return names, nil
}

// Snapshot returns the rendered screen of a session as plain text.
func Snapshot(name string) (string, error) {
	tmux.EnsureServer()
	out, code, err := tmux.RunCapture("capture-pane", "-p", "-t", Full(name))
	if code != 0 {
		healIfDown()
		if !Has(name) {
			return "", fmt.Errorf("session %q not found", name)
		}
		return "", fmt.Errorf("capture-pane failed: %v", err)
	}
	return strings.TrimRight(out, "\n"), nil
}

// Send types literal text and named keys into a session (see internal/keys for
// the grammar).
func Send(name, text string) error {
	tmux.EnsureServer()
	if err := tmux.Send(Full(name), text); err != nil {
		healIfDown()
		if !Has(name) {
			return fmt.Errorf("session %q not found", name)
		}
		return err
	}
	return nil
}

// WaitFor polls the pane until pattern (substring, or regex if it compiles)
// appears, or the timeout elapses. This is the exec one-shot implementation;
// the resident path overrides it with a faster channel poll.
func WaitFor(name, pattern string, timeoutSec float64) (string, error) {
	re, _ := regexp.Compile(pattern)
	deadline := time.Now().Add(time.Duration(timeoutSec * float64(time.Second)))
	for {
		snap, err := Snapshot(name)
		if err != nil {
			return "", err
		}
		if Match(snap, pattern, re) {
			return snap, nil
		}
		if time.Now().After(deadline) {
			return "", fmt.Errorf("timeout: pattern %q not found in session %q within %.1fs", pattern, name, timeoutSec)
		}
		time.Sleep(PollInterval)
	}
}

// Match reports whether pattern is present in snap, as a literal substring or
// (if re != nil) a regex match. Shared with the resident path so both behave
// identically.
func Match(snap, pattern string, re *regexp.Regexp) bool {
	if strings.Contains(snap, pattern) {
		return true
	}
	return re != nil && re.MatchString(snap)
}
