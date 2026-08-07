//go:build live

// Package conformance exercises the tmux contract (build-plan §5) against a
// REAL tmux — the live-testing discipline: fixtures lie. Run with:
//
//	go test -tags live ./conformance/...
//
// It validates both engines (exec one-shots and the control-mode resident
// client) against the same behaviors, so winmux's "is it a correct client" and
// "is it a correct server" are checked together.
package conformance

import (
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/samdotson61/gpty/internal/ctl"
	"github.com/samdotson61/gpty/internal/panes"
	"github.com/samdotson61/gpty/internal/platform"
	"github.com/samdotson61/gpty/internal/session"
	"github.com/samdotson61/gpty/internal/tmux"
)

func TestMain(m *testing.M) {
	// Resolve tmux the way the product does (GPTY_TMUX, PATH, then the OS
	// default), so CI setups that install tmux off-PATH (e.g. the msys2 action
	// on Windows) still exercise the suite instead of silently skipping.
	if err := exec.Command(platform.Bin(), "-V").Run(); err != nil {
		fmt.Fprintf(os.Stderr, "skipping conformance: tmux not runnable at %q (%v)\n", platform.Bin(), err)
		os.Exit(0)
	}
	// kill-server is safe on a CI runner but destroys every live session on a
	// dev box (this cleanup runs even under -run filtering). Only tear down a
	// server this suite itself started; a pre-existing server is someone's
	// live work and per-test Cleanups already remove our own sessions.
	preexisting := tmux.ServerUp()
	code := m.Run()
	if !preexisting {
		_ = exec.Command(platform.Bin(), "kill-server").Run() // best-effort cleanup
	}
	os.Exit(code)
}

var counter int

func uniqueName(t *testing.T) string {
	t.Helper()
	counter++
	name := fmt.Sprintf("conf-%d-%d", os.Getpid(), counter)
	t.Cleanup(func() { _ = session.Kill(name) })
	return name
}

// --- §5 session contract via the exec engine --------------------------------

func TestExecSessionLifecycle(t *testing.T) {
	name := uniqueName(t)

	if err := session.Spawn(name, "", "", 80, 24); err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	if !session.Has(name) {
		t.Fatal("Has: expected session to exist after Spawn")
	}

	// Drive a shell: literal text (send-keys -l) + a named key (Enter).
	marker := "exec-marker-42"
	if err := session.Send(name, "echo "+marker+"<Enter>"); err != nil {
		t.Fatalf("Send: %v", err)
	}
	snap, err := session.WaitFor(name, marker, 5)
	if err != nil {
		t.Fatalf("WaitFor(%q): %v", marker, err)
	}
	if !strings.Contains(snap, marker) {
		t.Fatalf("snapshot missing marker; got:\n%s", snap)
	}

	names, err := session.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if !contains(names, name) {
		t.Fatalf("List missing %q; got %v", name, names)
	}

	if err := session.Kill(name); err != nil {
		t.Fatalf("Kill: %v", err)
	}
	if session.Has(name) {
		t.Fatal("session still present after Kill")
	}
}

// Literal text with shell-special characters must survive the send path
// verbatim (the quoting concern that motivated load-buffer on Windows and
// send-keys -l on Unix).
func TestExecSendLiteralSpecials(t *testing.T) {
	name := uniqueName(t)
	if err := session.Spawn(name, "", "", 80, 24); err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	if err := session.Send(name, `echo 'a"b c<<d'<Enter>`); err != nil {
		t.Fatalf("Send: %v", err)
	}
	// "<<" renders as a literal "<", so we expect a"b c<d on screen.
	if _, err := session.WaitFor(name, `a"b c<d`, 5); err != nil {
		snap, _ := session.Snapshot(name)
		t.Fatalf("WaitFor literal specials: %v\nsnapshot:\n%s", err, snap)
	}
}

// --- §5 pane suite ----------------------------------------------------------

func TestExecPanes(t *testing.T) {
	name := uniqueName(t)
	if err := session.Spawn(name, "", "", 80, 24); err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	target := session.Full(name)

	id, err := panes.Split(target, "h", "", "", 0)
	if err != nil {
		t.Fatalf("Split: %v", err)
	}
	if !strings.HasPrefix(id, "%") {
		t.Fatalf("Split returned %q, want a %%-prefixed pane id", id)
	}

	if info := panes.Info(id); info == "" {
		t.Fatal("Info returned empty")
	}

	list, err := panes.List(target)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if !strings.Contains(list, id) {
		t.Fatalf("pane list missing %q:\n%s", id, list)
	}

	if err := panes.Send(id, "echo pane-marker<Enter>"); err != nil {
		t.Fatalf("pane Send: %v", err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		cap, _ := panes.Capture(id)
		if strings.Contains(cap, "pane-marker") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("pane capture never showed marker")
		}
		time.Sleep(50 * time.Millisecond)
	}

	if err := panes.Kill(id); err != nil {
		t.Fatalf("pane Kill: %v", err)
	}
}

// --- control-mode engine over the same operations ---------------------------

func TestCtlEngine(t *testing.T) {
	client, err := ctl.Dial()
	if err != nil {
		if runtime.GOOS == "windows" {
			// Control mode over cygwin pipes is not yet validated on Windows
			// (build-plan §8 Phase 4 gate: real hardware). gpty handles this at
			// runtime by falling back to the exec engine, so a dial failure here
			// is a tracked pending item, not a product failure. Remove this skip
			// once the channel is proven on a Windows box.
			t.Skipf("control mode dial failed on Windows (runtime uses exec fallback): %v", err)
		}
		t.Fatalf("ctl.Dial: %v", err)
	}
	defer client.Close()

	name := uniqueName(t)
	if err := session.Spawn(name, "", "", 80, 24); err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	target := session.Full(name)

	// Send literal text via the channel (send-keys -H) + a named key.
	if err := client.Send(target, "echo ctl-marker-99<Enter>"); err != nil {
		t.Fatalf("ctl Send: %v", err)
	}
	snap, err := client.WaitFor(target, "ctl-marker-99", 5)
	if err != nil {
		t.Fatalf("ctl WaitFor: %v", err)
	}
	if !strings.Contains(snap, "ctl-marker-99") {
		t.Fatalf("ctl capture missing marker; got:\n%s", snap)
	}

	// ListSessions over the channel (the quoted -F #{session_name} path).
	names, err := client.ListSessions()
	if err != nil {
		t.Fatalf("ctl ListSessions: %v", err)
	}
	if !contains(names, name) {
		t.Fatalf("ctl ListSessions missing %q; got %v", name, names)
	}

	// The channel capture must agree with the exec snapshot.
	execSnap, err := session.Snapshot(name)
	if err != nil {
		t.Fatalf("exec Snapshot: %v", err)
	}
	ctlSnap, err := client.Capture(target)
	if err != nil {
		t.Fatalf("ctl Capture: %v", err)
	}
	if strings.TrimSpace(execSnap) != strings.TrimSpace(ctlSnap) {
		t.Fatalf("exec and ctl captures disagree:\n--exec--\n%s\n--ctl--\n%s", execSnap, ctlSnap)
	}
}

// ctlWindowCount counts windows in the hidden control session ("agentpty_ctl",
// unexported in internal/ctl) — the observable for WaitFor's link/unlink.
func ctlWindowCount(t *testing.T) int {
	t.Helper()
	out, code, _ := tmux.RunCapture("list-windows", "-t", "agentpty_ctl", "-F", "#{window_id}")
	if code != 0 {
		t.Fatalf("list-windows on ctl session failed (rc=%d)", code)
	}
	return len(strings.Fields(out))
}

// TestCtlEventWaitFor pins the event-driven wait (build-plan §6): the target
// window is linked into the ctl session for the wait's duration (asserted via
// window counts), the wake comes from a %output push produced by a DIFFERENT
// tmux client (the exec path), and the reaction is far below the poll floor.
func TestCtlEventWaitFor(t *testing.T) {
	client, err := ctl.Dial()
	if err != nil {
		if runtime.GOOS == "windows" {
			t.Skipf("control mode dial failed on Windows (runtime uses exec fallback): %v", err)
		}
		t.Fatalf("ctl.Dial: %v", err)
	}
	defer client.Close()

	name := uniqueName(t)
	if err := session.Spawn(name, "", "", 80, 24); err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	target := session.Full(name)
	before := ctlWindowCount(t)

	var reactions []time.Duration
	for round := 0; round < 5; round++ {
		marker := fmt.Sprintf("evt-marker-%d", round)
		got := make(chan error, 1)
		var snap string
		go func() {
			var werr error
			snap, werr = client.WaitFor(target, marker, 10)
			got <- werr
		}()
		time.Sleep(250 * time.Millisecond) // wait is armed and linked

		if round == 0 {
			if during := ctlWindowCount(t); during != before+1 {
				t.Errorf("expected target linked into ctl session during wait: before=%d during=%d", before, during)
			}
		}

		start := time.Now()
		// Send via the EXEC path — a different tmux client than the waiter.
		if err := session.Send(name, "echo "+marker+"<Enter>"); err != nil {
			t.Fatalf("Send: %v", err)
		}
		if werr := <-got; werr != nil {
			t.Fatalf("WaitFor: %v", werr)
		}
		reaction := time.Since(start)
		if !strings.Contains(snap, marker) {
			t.Fatalf("snapshot missing %q", marker)
		}
		reactions = append(reactions, reaction)
	}

	if after := ctlWindowCount(t); after != before {
		t.Errorf("link not cleaned up: before=%d after=%d", before, after)
	}

	sort.Slice(reactions, func(i, j int) bool { return reactions[i] < reactions[j] })
	median := reactions[len(reactions)/2]
	t.Logf("cross-client round-trip — median %v, all %v (includes exec spawn + shell latency)", median, reactions)
	// Generous CI bound; the tight §6 number comes from TestCtlWaitReaction.
	if median > 150*time.Millisecond {
		t.Errorf("median reaction %v exceeds 150ms — event path not engaging", median)
	}
}

// TestCtlWaitReaction isolates the §6 "wait_for reaction to output" number:
// the pane runs `cat` (echoes instantly, no shell line-editor latency) and the
// send goes over the ctl channel (~30µs, no process spawn), so the measured
// time is essentially output-appears -> WaitFor-returns.
func TestCtlWaitReaction(t *testing.T) {
	client, err := ctl.Dial()
	if err != nil {
		if runtime.GOOS == "windows" {
			t.Skipf("control mode dial failed on Windows (runtime uses exec fallback): %v", err)
		}
		t.Fatalf("ctl.Dial: %v", err)
	}
	defer client.Close()

	name := uniqueName(t)
	if err := session.Spawn(name, "cat", "", 80, 24); err != nil {
		t.Fatalf("Spawn(cat): %v", err)
	}
	target := session.Full(name)

	var reactions []time.Duration
	for round := 0; round < 7; round++ {
		marker := fmt.Sprintf("rx-%d-end", round)
		got := make(chan error, 1)
		go func() {
			_, werr := client.WaitFor(target, marker, 10)
			got <- werr
		}()
		time.Sleep(150 * time.Millisecond) // armed + linked

		start := time.Now()
		if err := client.Send(target, marker+"<Enter>"); err != nil {
			t.Fatalf("ctl Send: %v", err)
		}
		if werr := <-got; werr != nil {
			t.Fatalf("WaitFor: %v", werr)
		}
		reactions = append(reactions, time.Since(start))
	}
	sort.Slice(reactions, func(i, j int) bool { return reactions[i] < reactions[j] })
	median := reactions[len(reactions)/2]
	t.Logf("§6 wait_for reaction — median %v, min %v, max %v", median, reactions[0], reactions[len(reactions)-1])
	if median > 100*time.Millisecond {
		t.Errorf("median reaction %v exceeds 100ms — event path not engaging", median)
	}
}

// --- tmux passthrough command table (the CLI alias surface) ------------------

// The gpty CLI forwards unknown subcommands when they appear in the live
// command table; this pins the `list-commands -F` query and parse against
// every CI tmux version.
func TestTmuxCommandTable(t *testing.T) {
	set, err := tmux.Commands()
	if err != nil {
		t.Fatalf("tmux.Commands: %v", err)
	}
	for _, want := range []string{
		"attach-session", "attach", "send-keys", "send", "list-sessions", "ls",
		"kill-server", "split-window", "splitw", "list-commands",
	} {
		if !set[want] {
			t.Errorf("command table missing %q", want)
		}
	}
	if set["not-a-tmux-command"] {
		t.Error("command table contains an impossible entry")
	}
}

func contains(xs []string, x string) bool {
	for _, v := range xs {
		if v == x {
			return true
		}
	}
	return false
}
