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
	"github.com/samdotson61/gpty/internal/session"
)

// ctlSession is the hidden session the control client attaches to. It is not
// prefixed "agent-pty-", so it never shows up in session.List().
const ctlSession = "agentpty_ctl"

const (
	startupIdle    = 250 * time.Millisecond // quiescence window that ends startup drain
	commandTimeout = 5 * time.Second        // per-command reply deadline
	// handshakeTimeout is the deadline for Dial's first command only. Cygwin
	// process spawn + server cold-start on a loaded Windows CI runner can far
	// exceed the steady-state budget, and a failed dial just means exec
	// fallback — so be patient before declaring the channel unusable.
	handshakeTimeout = 20 * time.Second
	// WaitPoll is the resident WaitFor's re-capture cadence when the
	// event-driven path is unavailable (window link failed). Far tighter than
	// the exec path's 200 ms because a capture here is a sub-ms pipe
	// round-trip, not a process spawn.
	WaitPoll = 25 * time.Millisecond
	// eventSafetyPoll is the slow recheck tick while waiting on %output
	// events — pure paranoia against a missed event (e.g. the agent switching
	// the session's current window mid-wait); the hot path is the push.
	eventSafetyPoll = 250 * time.Millisecond
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

	outMu sync.Mutex
	outCh chan struct{} // closed and replaced on every %output burst (broadcast)

	linkMu sync.Mutex
	links  map[string]*winLink // window-id -> refcounted link into ctlSession
}

// winLink tracks one target window linked into the hidden ctl session so its
// %output events reach this client (tmux only delivers pane output for windows
// in the control client's OWN session — verified live; see WaitFor).
type winLink struct {
	refs  int
	index string // window index inside ctlSession, needed to unlink
}

// Debug, when set before Dial, receives every raw control-mode line (prefixed
// "ctl< ") and every command written ("ctl> "). Used by `gpty doctor` to
// diagnose hosts where the channel won't come up (the open Windows question).
var Debug io.Writer

// Dial starts a control-mode client and returns once the startup handshake has
// been drained (so no command can race the unsolicited startup frames).
func Dial() (*Client, error) {
	cmd := dialCmd() // per-OS: plain -C on Unix, script(1)+-CC on Windows
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
		outCh:   make(chan struct{}),
		links:   map[string]*winLink{},
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
	if _, err := c.runT(handshakeTimeout, "display-message", "-p", "gpty-ctl-ok"); err != nil {
		c.Close()
		return nil, fmt.Errorf("control channel handshake failed: %w", err)
	}
	return c, nil
}

func scan(r io.Reader, out chan<- string) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024) // allow wide captures
	for sc.Scan() {
		line := sc.Text()
		// -CC (the Windows channel) is a pty stream: CRLF line endings, a DCS
		// intro glued to the first %begin, and a lone ST on exit. Normalizing
		// here keeps the protocol reader identical across both channels.
		line = strings.TrimRight(line, "\r")
		line = strings.TrimPrefix(line, "\x1bP1000p")
		if line == "\x1b\\" {
			continue
		}
		if Debug != nil {
			fmt.Fprintf(Debug, "ctl< %s\n", line)
		}
		out <- line
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
			// Between blocks: an async notification. %output is the WaitFor
			// wake signal (the pane bytes themselves aren't parsed — waiters
			// re-capture their own target, which is a sub-ms round-trip).
			if strings.HasPrefix(line, "%output ") {
				c.notifyOutput()
			}
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
	return c.runT(commandTimeout, args...)
}

// runT is run with an explicit reply deadline (Dial's handshake is more
// patient than steady-state commands).
func (c *Client) runT(timeout time.Duration, args ...string) ([]string, error) {
	line := encodeCommand(args)
	if Debug != nil {
		fmt.Fprintf(Debug, "ctl> %s\n", line)
	}
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
		// The write failed with r already enqueued. If the channel kept running,
		// the NEXT command's %begin would pair with this orphan and every later
		// reply would correlate off by one. A broken stdin means the channel is
		// dead anyway — tear it down so readLoop fails all pending replies and
		// callers fall back to the exec engine.
		c.Close()
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
	case <-time.After(timeout):
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
		// Detach explicitly before tearing the process down. On Windows the
		// dialed process is script(1) and the tmux -CC client is its child on
		// a cygwin pty — killing script alone orphans the client, which stays
		// ATTACHED server-side and (being a control client that no longer
		// drains) drags the server's %output flow for every other client.
		// Nine such zombies accumulated during live-testing before this line
		// existed. detach-client makes the client exit itself; the kill below
		// is then just cleanup.
		_, _ = io.WriteString(c.stdin, "detach-client\n")
		if c.closed != nil {
			select {
			case <-c.closed: // reader saw EOF: client really exited
			case <-time.After(500 * time.Millisecond):
			}
		}
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

// notifyOutput broadcasts "a linked pane produced output" to every waiter by
// closing the current signal channel and replacing it.
func (c *Client) notifyOutput() {
	c.outMu.Lock()
	close(c.outCh)
	c.outCh = make(chan struct{})
	c.outMu.Unlock()
}

// outputSignal returns the channel the NEXT %output burst will close.
func (c *Client) outputSignal() <-chan struct{} {
	c.outMu.Lock()
	defer c.outMu.Unlock()
	return c.outCh
}

// linkForEvents links target's current window into the hidden ctl session so
// tmux delivers its %output here — control clients only receive pane output
// for windows in their OWN attached session (verified live: an unlinked
// session is silent, a linked window streams, unlink silences it again).
// Links are refcounted per window id so concurrent waits on the same target
// share one link. Returns the unlink func, or nil if linking failed (the
// caller then falls back to the WaitPoll capture loop).
func (c *Client) linkForEvents(target string) func() {
	c.linkMu.Lock()
	defer c.linkMu.Unlock()

	wid, err := c.run("display-message", "-p", "-t", target, "#{window_id}")
	if err != nil || len(wid) == 0 || strings.TrimSpace(wid[0]) == "" {
		return nil
	}
	id := strings.TrimSpace(wid[0])
	if l, ok := c.links[id]; ok {
		l.refs++
		return func() { c.unlink(id) }
	}
	if _, err := c.run("link-window", "-d", "-s", target, "-t", ctlSession+":"); err != nil {
		return nil
	}
	// Find where it landed — unlink-window needs the index within ctlSession.
	out, err := c.run("list-windows", "-t", ctlSession, "-F", "#{window_index} #{window_id}")
	if err != nil {
		return nil
	}
	for _, ln := range out {
		if f := strings.Fields(ln); len(f) == 2 && f[1] == id {
			c.links[id] = &winLink{refs: 1, index: f[0]}
			return func() { c.unlink(id) }
		}
	}
	return nil
}

func (c *Client) unlink(id string) {
	c.linkMu.Lock()
	defer c.linkMu.Unlock()
	l := c.links[id]
	if l == nil {
		return
	}
	if l.refs--; l.refs > 0 {
		return
	}
	delete(c.links, id)
	// Best-effort: the window may already be gone (session killed mid-wait).
	_, _ = c.run("unlink-window", "-t", ctlSession+":"+l.index)
}

// WaitFor blocks until pattern (substring, or regex if it compiles) appears in
// target's rendered screen, or the timeout elapses.
//
// Event-driven (build-plan §6): the target window is linked into the ctl
// session so its %output pushes wake the wait — reaction is one sub-ms capture
// after the push, with a slow safety tick behind it. If linking fails, it
// degrades to the plain WaitPoll capture loop.
func (c *Client) WaitFor(target, pattern string, timeoutSec float64) (string, error) {
	re, _ := regexp.Compile(pattern)
	deadline := time.Now().Add(time.Duration(timeoutSec * float64(time.Second)))

	unlink := c.linkForEvents(target)
	if unlink != nil {
		defer unlink()
	}
	tick := WaitPoll
	if unlink != nil {
		tick = eventSafetyPoll
	}
	for {
		// Grab the signal BEFORE capturing: output that lands during the
		// capture closes this channel, so the select below fires immediately
		// instead of stalling a full tick.
		sig := c.outputSignal()
		snap, err := c.Capture(target)
		if err != nil {
			return "", err
		}
		if session.Match(snap, pattern, re) {
			return snap, nil
		}
		remain := time.Until(deadline)
		if remain <= 0 {
			return "", fmt.Errorf("timeout: pattern %q not found in %q within %.1fs", pattern, target, timeoutSec)
		}
		wait := tick
		if remain < wait {
			wait = remain
		}
		select {
		case <-sig:
		case <-time.After(wait):
		case <-c.closed:
			return "", fmt.Errorf("control channel closed")
		}
	}
}
