// Package ctl is the resident control-mode engine (build-plan §2, latency
// decision 2; Phase 4). It holds one persistent `tmux -C` client so that every
// command is a line on an already-open pipe instead of a fresh process — which
// on Windows skips the ~40–80 ms cygwin exec tax per call, and everywhere drops
// the hot reads/sends into the low-single-digit-ms range. Control mode needs no
// pty, so none of the cygwin pseudo-console dance applies here.
//
// Protocol (verified live against tmux 3.6a before implementation):
//   - commands are written one per line to stdin; tmux replies in submission
//     order, each reply wrapped in `%begin <ts> <n> <f>` … `%end`/`%error`;
//   - lines between %begin and %end/%error are the command's verbatim output —
//     even lines containing or starting with `%` (e.g. a shell prompt);
//   - async notifications (%output, %session-changed, %exit, …) arrive only
//     BETWEEN reply blocks, never inside one;
//   - `#` starts a comment in tmux's command lexer, so any format string we send
//     (e.g. -F "#{session_name}") MUST be quoted, or -F sees no argument.
//
// Every method falls back to the exec engine on any channel error, so the
// resident path is never less correct than the one-shot path — it just hangs up
// and lets exec take the call (build-plan §9).
package ctl

import (
	"bufio"
	"fmt"
	"io"
	"os/exec"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/samdotson61/gpty/internal/keys"
	"github.com/samdotson61/gpty/internal/platform"
	"github.com/samdotson61/gpty/internal/session"
)

// ctlSession is the hidden session the control client attaches to. It is not
// prefixed "agent-pty-", so it never shows up in session.List().
const ctlSession = "agentpty_ctl"

const (
	startupIdle    = 250 * time.Millisecond // quiescence window that ends startup drain
	commandTimeout = 5 * time.Second        // per-command reply deadline
	// WaitPoll is how often the resident WaitFor re-captures over the open
	// channel. Far tighter than the exec path's 200 ms because a capture here is
	// a sub-ms pipe round-trip, not a process spawn.
	WaitPoll = 25 * time.Millisecond
)

// reply collects the output lines of one command's %begin…%end block.
type reply struct {
	lines []string
	isErr bool
	done  chan struct{}
}

// Client is a live control-mode connection to tmux.
type Client struct {
	cmd   *exec.Cmd
	stdin io.WriteCloser

	mu      sync.Mutex  // serializes submit (enqueue-then-write) so reply FIFO matches write order
	pending chan *reply // commands awaiting their %begin
	closeMu sync.Mutex
	closed  chan struct{}
}

// Dial starts a control-mode client and returns once the startup handshake has
// been drained (so no command can race the unsolicited startup frames).
func Dial() (*Client, error) {
	bin := platform.Bin()
	args := []string{"-u", "-C", "new-session", "-A", "-s", ctlSession, "-x", "80", "-y", "24"}
	cmd := exec.Command(bin, args...)
	cmd.Env = platform.Env(false) // noglob-safe on Windows; control mode needs no pty
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	cmd.Stderr = nil
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start tmux -C: %w", err)
	}

	c := &Client{
		cmd:     cmd,
		stdin:   stdin,
		pending: make(chan *reply, 64),
		closed:  make(chan struct{}),
	}

	lines := make(chan string, 256)
	go scan(stdout, lines)

	// Drain the startup handshake (%begin/%end for the implicit attach, plus a
	// %session-changed) until the stream goes quiet, then hand the same channel
	// to the steady-state reader.
	if err := c.drainStartup(lines); err != nil {
		c.Close()
		return nil, err
	}
	go c.readLoop(lines)

	// Confirm the channel actually answers before declaring success.
	if _, err := c.run("display-message", "-p", "gpty-ctl-ok"); err != nil {
		c.Close()
		return nil, fmt.Errorf("control channel handshake failed: %w", err)
	}
	return c, nil
}

func scan(r io.Reader, out chan<- string) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024) // allow wide captures
	for sc.Scan() {
		out <- sc.Text()
	}
	close(out)
}

func (c *Client) drainStartup(lines chan string) error {
	for {
		select {
		case _, ok := <-lines:
			if !ok {
				return fmt.Errorf("tmux -C exited during startup")
			}
			// ignore everything until quiescent
		case <-time.After(startupIdle):
			return nil
		}
	}
}

// readLoop is the steady-state reader. After startup drain, every %begin
// corresponds to one of our submitted commands, in order.
func (c *Client) readLoop(lines chan string) {
	defer close(c.closed)
	var cur *reply
	for line := range lines {
		switch {
		case strings.HasPrefix(line, "%begin "):
			// Pair with the next pending command. If none is pending, this is an
			// unsolicited block (not expected post-startup) — discard it.
			select {
			case cur = <-c.pending:
			default:
				cur = &reply{done: make(chan struct{})}
			}
		case strings.HasPrefix(line, "%end ") || strings.HasPrefix(line, "%error "):
			if cur != nil {
				cur.isErr = strings.HasPrefix(line, "%error ")
				close(cur.done)
				cur = nil
			}
		case cur != nil:
			// Inside a block: verbatim content, even if it starts with '%'.
			cur.lines = append(cur.lines, line)
		default:
			// Between blocks: an async notification (%output, %exit, …). The
			// reliable hot path is capture-poll, so we just consume these.
		}
	}
	// Stream closed: fail any in-flight and future commands.
	if cur != nil {
		close(cur.done)
	}
	for {
		select {
		case r := <-c.pending:
			close(r.done)
		default:
			return
		}
	}
}

// run submits one command and returns its reply lines, or an error if tmux
// replied with %error, the command timed out, or the channel is dead.
func (c *Client) run(args ...string) ([]string, error) {
	line := encodeCommand(args)
	r := &reply{done: make(chan struct{})}

	c.mu.Lock()
	select {
	case <-c.closed:
		c.mu.Unlock()
		return nil, fmt.Errorf("control channel closed")
	default:
	}
	c.pending <- r
	_, werr := io.WriteString(c.stdin, line+"\n")
	c.mu.Unlock()
	if werr != nil {
		return nil, werr
	}

	select {
	case <-r.done:
		if r.isErr {
			return r.lines, fmt.Errorf("tmux: %s", strings.Join(r.lines, "; "))
		}
		return r.lines, nil
	case <-c.closed:
		return nil, fmt.Errorf("control channel closed")
	case <-time.After(commandTimeout):
		return nil, fmt.Errorf("control command timed out: %s", line)
	}
}

// Healthy reports whether the channel is still open.
func (c *Client) Healthy() bool {
	select {
	case <-c.closed:
		return false
	default:
		return true
	}
}

// Close shuts the channel and kills the tmux control client.
func (c *Client) Close() error {
	c.closeMu.Lock()
	defer c.closeMu.Unlock()
	if c.stdin != nil {
		_ = c.stdin.Close()
	}
	if c.cmd != nil && c.cmd.Process != nil {
		_ = c.cmd.Process.Kill()
		_ = c.cmd.Wait()
	}
	return nil
}

// --- operations over the channel --------------------------------------------

// Capture returns the rendered text of a target (session name or pane id).
func (c *Client) Capture(target string) (string, error) {
	out, err := c.run("capture-pane", "-p", "-t", target)
	if err != nil {
		return "", err
	}
	return strings.TrimRight(strings.Join(out, "\n"), "\n"), nil
}

// ListSessions returns user-facing managed session names.
func (c *Client) ListSessions() ([]string, error) {
	out, err := c.run("list-sessions", "-F", "#{session_name}")
	if err != nil {
		return nil, err
	}
	names := []string{}
	for _, raw := range out {
		raw = strings.TrimSpace(raw)
		if raw != "" && session.IsManaged(raw) {
			names = append(names, session.Strip(raw))
		}
	}
	return names, nil
}

// Send types literal text and named keys into a target. Literal runs go via
// send-keys -H (hex bytes) — immune to every quoting concern, since no user
// text ever appears in the command line. Named keys go via send-keys <Key>.
func (c *Client) Send(target, text string) error {
	segs, err := keys.Parse(text)
	if err != nil {
		return err
	}
	for _, s := range segs {
		if s.Kind == keys.Key {
			if _, err := c.run("send-keys", "-t", target, s.Val); err != nil {
				return err
			}
			continue
		}
		if s.Val == "" {
			continue
		}
		args := []string{"send-keys", "-H", "-t", target}
		for _, b := range []byte(s.Val) {
			args = append(args, fmt.Sprintf("%02x", b))
		}
		if _, err := c.run(args...); err != nil {
			return err
		}
	}
	return nil
}

// WaitFor polls Capture over the open channel until pattern (substring, or
// regex if it compiles) appears, or the timeout elapses. The tight poll is
// cheap because each capture is a pipe round-trip, not a process spawn.
func (c *Client) WaitFor(target, pattern string, timeoutSec float64) (string, error) {
	re, _ := regexp.Compile(pattern)
	deadline := time.Now().Add(time.Duration(timeoutSec * float64(time.Second)))
	for {
		snap, err := c.Capture(target)
		if err != nil {
			return "", err
		}
		if session.Match(snap, pattern, re) {
			return snap, nil
		}
		if time.Now().After(deadline) {
			return "", fmt.Errorf("timeout: pattern %q not found in %q within %.1fs", pattern, target, timeoutSec)
		}
		time.Sleep(WaitPoll)
	}
}
