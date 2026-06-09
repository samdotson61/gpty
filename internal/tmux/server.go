package tmux

import (
	"sync"
	"sync/atomic"
	"time"
)

// keepalive is a hidden, durable session that keeps the tmux server alive so a
// cold first command can't hang on "the first command starts the server". It is
// not prefixed "agent-pty-", so it never appears in a managed session list.
const keepalive = "agentpty_srv_keepalive"

var (
	serverMu    sync.Mutex  // serializes cold-start so concurrent first calls don't race
	serverReady atomic.Bool // fast path: once true, EnsureServer is a lock-free no-op
)

// EnsureServer guarantees a live, connectable tmux server before an operation
// runs. This is the fix for the concurrency race surfaced by live MCP testing:
// the MCP SDK dispatches buffered tool calls concurrently, and without
// serialization several tmux clients would race the server's cold start and get
// "no server running" even though a peer was mid-creating it.
//
// After the first success the server is assumed up and this returns with a
// single atomic load — no exec — so the hot-path latency budget is preserved.
// If a later op finds the server gone, it calls MarkServerDown to force a
// re-ensure.
func EnsureServer() {
	if serverReady.Load() {
		return
	}
	serverMu.Lock()
	defer serverMu.Unlock()
	if serverReady.Load() {
		return
	}
	if serverUp() {
		serverReady.Store(true)
		return
	}
	// Start the server + durable keepalive, then wait until it actually accepts
	// connections (new-session can return microscopically before the socket is
	// ready under concurrent load — the source of the race).
	_ = RunQuiet("new-session", "-d", "-s", keepalive)
	for i := 0; i < 100; i++ {
		if serverUp() {
			serverReady.Store(true)
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// MarkServerDown forces the next EnsureServer to re-verify/recreate the server.
func MarkServerDown() { serverReady.Store(false) }

func serverUp() bool { return ServerUp() }

// ServerUp reports whether the tmux server is currently reachable (via the
// keepalive session). Used by callers to distinguish "session absent" from
// "server gone" when deciding whether to force a re-ensure.
func ServerUp() bool {
	_, code, _ := RunCapture("has-session", "-t", keepalive)
	return code == 0
}
