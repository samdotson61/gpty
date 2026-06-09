// Package panes provides pane-level control of the agent's CURRENT window —
// the core of win-pty's purpose. An agent running inside a gmux pane can split
// its own window and drive each pane, and the human attached to that session
// sees it happen live.
//
// The trick that makes "my current window" work: the MCP server is a child of
// the agent process running inside the pane, so it inherits $TMUX_PANE (the
// agent's pane) and $TMUX (the agent's server). tmux commands then target the
// same server the human is looking at. Splits use -d so focus stays on the
// agent's pane (the human keeps typing to the agent, not the new empty pane).
package panes

import (
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/samdotson61/gpty/internal/tmux"
)

// currentPane is the pane the agent's process runs in ($TMUX_PANE, inherited
// from the process that launched the server). Empty if not inside tmux.
func currentPane() string { return os.Getenv("TMUX_PANE") }

// resolveTarget returns target if non-empty, else the agent's current pane.
func resolveTarget(target string) (string, error) {
	if strings.TrimSpace(target) != "" {
		return target, nil
	}
	if p := currentPane(); p != "" {
		return p, nil
	}
	return "", fmt.Errorf("no target pane: $TMUX_PANE is unset, so I can't tell which pane I'm in. " +
		"Run the agent inside a gmux session, or pass an explicit pane id from pane_list")
}

// splitFlag maps a friendly direction to tmux's split-window flag.
// tmux convention: -h = side-by-side (left|right), -v = stacked (top/bottom).
func splitFlag(dir string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(dir)) {
	case "h", "horizontal", "lr", "left-right", "right", "side", "":
		return "-h", nil
	case "v", "vertical", "tb", "top-bottom", "down", "stack", "stacked":
		return "-v", nil
	}
	return "", fmt.Errorf("dir must be 'h' (side-by-side) or 'v' (stacked), got %q", dir)
}

// Split splits target (default: the agent's current pane) and returns the new
// pane's id (e.g. "%7"). The new pane runs cmd, or the default shell if cmd is
// empty. Created with -d so focus stays put.
func Split(target, dir, cmd, cwd string, percent int) (string, error) {
	tmux.EnsureServer()
	t, err := resolveTarget(target)
	if err != nil {
		return "", err
	}
	flag, err := splitFlag(dir)
	if err != nil {
		return "", err
	}
	args := []string{"split-window", flag, "-d", "-t", t, "-P", "-F", "#{pane_id}"}
	if percent > 0 && percent < 100 {
		args = append(args, "-l", strconv.Itoa(percent)+"%")
	}
	if cwd != "" {
		args = append(args, "-c", cwd)
	}
	if cmd != "" {
		args = append(args, cmd)
	}
	out, code, err := tmux.RunCapture(args...)
	if code != 0 || err != nil {
		return "", fmt.Errorf("split-window failed: %v %s", err, strings.TrimSpace(out))
	}
	id := strings.TrimSpace(out)
	if id == "" {
		return "", fmt.Errorf("split-window produced no pane id")
	}
	return id, nil
}

// Info returns a one-line summary of a pane's context: the session/window it
// lives in, how many panes that window now has, and how many clients are
// attached. The attached count is the anti-hallucination signal — 0 means NO
// human is viewing that session, so whatever you just did is invisible. Returns
// "" if the pane can't be described.
func Info(id string) string {
	out, code, _ := tmux.RunCapture("display-message", "-p", "-t", id,
		"#{session_name}|#{window_index}|#{window_name}|#{window_panes}|#{session_attached}")
	if code != 0 {
		return ""
	}
	f := strings.Split(strings.TrimSpace(out), "|")
	if len(f) < 5 {
		return ""
	}
	sess, win, wname, npanes, attached := f[0], f[1], f[2], f[3], f[4]
	msg := fmt.Sprintf("session '%s' window %s (%s) now has %s pane(s); %s client(s) attached",
		sess, win, wname, npanes, attached)
	if attached == "0" {
		msg += " — WARNING: 0 clients attached, so NO human is viewing this session and this pane is invisible. " +
			"To split the window a human is watching, run the agent INSIDE that gmux session (so $TMUX_PANE points at it), or target a pane from `pane_list all` whose session shows clients attached."
	}
	return msg
}

// List lists panes in target's window (default: the agent's current window), or
// every pane on the server when target == "all".
func List(target string) (string, error) {
	tmux.EnsureServer()
	args := []string{"list-panes"}
	if strings.EqualFold(strings.TrimSpace(target), "all") {
		args = append(args, "-a")
	} else {
		t, err := resolveTarget(target)
		if err != nil {
			return "", err
		}
		args = append(args, "-t", t)
	}
	args = append(args, "-F", "#{pane_id} [#{session_name}:#{window_index}.#{pane_index}] active=#{pane_active} attached=#{session_attached} #{pane_width}x#{pane_height} #{pane_current_command}")
	out, code, err := tmux.RunCapture(args...)
	if code != 0 {
		return "", fmt.Errorf("list-panes failed: %v %s", err, strings.TrimSpace(out))
	}
	return strings.TrimRight(out, "\n"), nil
}

// Capture returns the rendered text of a pane.
func Capture(pane string) (string, error) {
	if strings.TrimSpace(pane) == "" {
		return "", fmt.Errorf("pane id required")
	}
	out, code, err := tmux.RunCapture("capture-pane", "-p", "-t", pane)
	if code != 0 {
		return "", fmt.Errorf("capture-pane failed for %q: %v %s", pane, err, strings.TrimSpace(out))
	}
	return strings.TrimRight(out, "\n"), nil
}

// Send sends keystrokes (literal text + named keys) to a specific pane.
func Send(pane, text string) error {
	if strings.TrimSpace(pane) == "" {
		return fmt.Errorf("pane id required")
	}
	return tmux.Send(pane, text)
}

// Kill kills a pane (if it's the last pane in a window, the window closes).
func Kill(pane string) error {
	if strings.TrimSpace(pane) == "" {
		return fmt.Errorf("pane id required")
	}
	return tmux.RunQuiet("kill-pane", "-t", pane)
}

// Select focuses a pane — the human's cursor follows.
func Select(pane string) error {
	if strings.TrimSpace(pane) == "" {
		return fmt.Errorf("pane id required")
	}
	return tmux.RunQuiet("select-pane", "-t", pane)
}

// Resize grows a pane in a direction (U/D/L/R) by amount cells (default 5).
func Resize(pane, dir string, amount int) error {
	if strings.TrimSpace(pane) == "" {
		return fmt.Errorf("pane id required")
	}
	if amount <= 0 {
		amount = 5
	}
	var flag string
	switch strings.ToUpper(strings.TrimSpace(dir)) {
	case "U", "UP":
		flag = "-U"
	case "D", "DOWN":
		flag = "-D"
	case "L", "LEFT":
		flag = "-L"
	case "R", "RIGHT":
		flag = "-R"
	default:
		return fmt.Errorf("dir must be U, D, L or R, got %q", dir)
	}
	return tmux.RunQuiet("resize-pane", "-t", pane, flag, strconv.Itoa(amount))
}

// Layout re-tiles target's window (default: current) with a tmux layout:
// even-horizontal, even-vertical, main-horizontal, main-vertical, tiled.
func Layout(target, layout string) error {
	t, err := resolveTarget(target)
	if err != nil {
		return err
	}
	if strings.TrimSpace(layout) == "" {
		layout = "tiled"
	}
	return tmux.RunQuiet("select-layout", "-t", t, layout)
}
