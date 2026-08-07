// Package mesh is the orchestration layer over multiple sessions — the
// Captain Kirk pattern: one agent driving N agents in other panes, with
// done-detection, incremental snapshots, blocked-on-prompt detection,
// cross-pane piping, pattern subscriptions and lifecycle notifications.
//
// Ported from upstream agent-pty's agent_pty/mesh.py (M6). Opt-in: core
// pty_*/pane_* users never touch this package. Every function takes a Term
// so it rides whichever engine (exec or control-mode resident) the caller
// holds — the mesh path is never less correct than the core path.
package mesh

import (
	"fmt"
	"regexp"
	"strings"
	"time"
)

// Term is the slice of engine.Engine the mesh primitives need. engine.Exec
// and ctl.Accel both satisfy it; tests use a fake.
type Term interface {
	List() ([]string, error)
	Snapshot(name string) (string, error)
	Send(name, text string) error
	WaitFor(name, pattern string, timeout float64) (string, error)
}

// DefaultDoneMarker is the Captain-Kirk reply sentinel.
const DefaultDoneMarker = "<<END>>"

// sendDonePoll is the screen-poll cadence while waiting for the done marker.
const sendDonePoll = 50 * time.Millisecond

// SendWithDone sends text, waits for marker, and returns the reply text
// bounded by them: the screen content after the sent prompt's last non-empty
// line and before the marker, marker excluded, whitespace trimmed.
//
// text is treated as literal: `<` is escaped to `<<` so named-key parsing
// doesn't fire (use Term.Send directly for keystrokes). The convention is to
// prompt the sub-agent to end its reply with the marker.
//
// Hardened over upstream: the prompt usually contains the marker itself
// ("End your reply with <<END>>"), and its on-screen ECHO appears before any
// output — so completion requires a marker occurrence AFTER the echoed
// prompt, not merely on screen. If that anchored parse never succeeds (the
// echo wrapped or scrolled off), the timeout falls back to upstream's
// best-effort extraction.
func SendWithDone(t Term, name, text, marker string, timeout float64) (string, error) {
	if marker == "" {
		marker = DefaultDoneMarker
	}
	if timeout <= 0 {
		timeout = 60
	}
	if err := t.Send(name, strings.ReplaceAll(text, "<", "<<")); err != nil {
		return "", err
	}
	deadline := time.Now().Add(time.Duration(timeout * float64(time.Second)))
	for {
		snap, err := t.Snapshot(name)
		if err != nil {
			return "", err
		}
		if reply, ok := tryExtractReply(snap, text, marker); ok {
			return reply, nil
		}
		if time.Now().After(deadline) {
			if strings.Contains(snap, marker) {
				return extractReply(snap, text, marker), nil
			}
			return "", fmt.Errorf("timeout: done marker %q not found in session %q within %.1fs", marker, name, timeout)
		}
		time.Sleep(sendDonePoll)
	}
}

// tryExtractReply succeeds only when the last marker occurrence sits after a
// complete on-screen echo of the prompt's anchor line — proof the marker
// belongs to the reply, not the echoed prompt.
func tryExtractReply(snap, sentText, marker string) (string, bool) {
	markerIdx := strings.LastIndex(snap, marker)
	if markerIdx == -1 {
		return "", false
	}
	anchor := lastNonEmptyLine(sentText)
	if anchor == "" {
		return strings.TrimSpace(strings.Trim(snap[:markerIdx], "\n")), true
	}
	anchorIdx := strings.LastIndex(snap[:markerIdx], anchor)
	if anchorIdx == -1 {
		return "", false
	}
	start := anchorIdx + len(anchor)
	return strings.TrimSpace(strings.Trim(snap[start:markerIdx], "\n")), true
}

func lastNonEmptyLine(text string) string {
	anchor := ""
	for _, line := range strings.Split(text, "\n") {
		if strings.TrimSpace(line) != "" {
			anchor = line
		}
	}
	return anchor
}

// extractReply is the upstream best-effort fallback: bounds the reply between
// the last on-screen occurrence of the prompt's anchor line (if found) and
// the last marker occurrence.
func extractReply(snap, sentText, marker string) string {
	markerIdx := strings.LastIndex(snap, marker)
	if markerIdx == -1 {
		return ""
	}
	anchor := lastNonEmptyLine(sentText)
	start := 0
	if anchor != "" {
		if i := strings.LastIndex(snap[:markerIdx], anchor); i != -1 {
			start = i + len(anchor)
		}
	}
	return strings.TrimSpace(strings.Trim(snap[start:markerIdx], "\n"))
}

// SnapshotSince returns the text after the most recent occurrence of marker
// on the screen. If marker is absent the full snapshot is returned (the
// caller asked for everything-since-something-not-there, which is everything).
func SnapshotSince(t Term, name, marker string) (string, error) {
	snap, err := t.Snapshot(name)
	if err != nil {
		return "", err
	}
	idx := strings.LastIndex(snap, marker)
	if idx == -1 {
		return snap, nil
	}
	return strings.TrimLeft(snap[idx+len(marker):], "\n"), nil
}

// blockedPatterns are the heuristic prompt signatures, matched against the
// bottom non-empty rows of the screen. Order is significant: first hit wins.
var blockedPatterns = []struct {
	re   *regexp.Regexp
	hint string
}{
	{regexp.MustCompile(`(?i)password[^:\n]*:\s*$`), "password prompt"},
	{regexp.MustCompile(`(?i)(\[y/?n\]|\(y/?n\)|\[yes/no\]|\(yes/no\))\s*\??\s*$`), "y/n confirmation"},
	{regexp.MustCompile(`(?i)\bcontinue\?\s*$`), "continue prompt"},
	{regexp.MustCompile(`(?i)(allow|approve)\b[^\n]{0,80}\?\s*$`), "approval prompt"},
	{regexp.MustCompile(`(?i)press\s+any\s+key`), "any-key prompt"},
	{regexp.MustCompile(`(?i)(2fa|verification)\s+code\s*:?\s*$`), "2FA code prompt"},
}

// DetectBlocked returns a hint string if the session looks blocked on input,
// else "". Best-effort regex over the bottom rows: false positives (a
// `read -p "Continue?"` script) and negatives (custom prompts) are possible.
// A signal, not a guarantee.
func DetectBlocked(t Term, name string) (string, error) {
	snap, err := t.Snapshot(name)
	if err != nil {
		return "", err
	}
	return DetectBlockedIn(snap), nil
}

// DetectBlockedIn runs the blocked-prompt heuristic over an already-captured
// snapshot (shared with the fleet assessor, which double-samples).
func DetectBlockedIn(snap string) string {
	var lines []string
	for _, line := range strings.Split(snap, "\n") {
		if strings.TrimSpace(line) != "" {
			lines = append(lines, line)
		}
	}
	if len(lines) == 0 {
		return ""
	}
	if len(lines) > 3 {
		lines = lines[len(lines)-3:]
	}
	tail := strings.Join(lines, "\n")
	for _, p := range blockedPatterns {
		if p.re.MatchString(tail) {
			return p.hint
		}
	}
	return ""
}

// Pipe injects content from one session's screen into another's input stream.
// lines<=0 pipes the entire current screen; lines=N the last N non-empty
// lines. The payload never surfaces as a return value, so large artifacts
// don't cost orchestrator tokens. Fire-and-forget keystroke injection: the
// destination receives newlines as Enter — caller sanitizes.
func Pipe(t Term, fromName, toName string, lines int) error {
	snap, err := t.Snapshot(fromName)
	if err != nil {
		return err
	}
	payload := snap
	if lines > 0 {
		var nonempty []string
		for _, line := range strings.Split(snap, "\n") {
			if strings.TrimSpace(line) != "" {
				nonempty = append(nonempty, line)
			}
		}
		if len(nonempty) > lines {
			nonempty = nonempty[len(nonempty)-lines:]
		}
		payload = strings.Join(nonempty, "\n")
	}
	if payload == "" {
		return nil
	}
	return t.Send(toName, strings.ReplaceAll(payload, "<", "<<"))
}
