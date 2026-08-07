package mesh

// Ported from upstream agent-pty's tests/test_mesh.py, rehosted on a fake
// Term so the suite runs without a live tmux server (the live path is
// covered by the conformance suite).

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeTerm is an in-memory Term. Screens maps name -> rendered screen;
// OnSend, if set, runs after a successful Send (to simulate the sub-agent
// reacting); OnSnapshot, if set, can rewrite the screen before each read.
type fakeTerm struct {
	mu         sync.Mutex
	screens    map[string]string
	sent       map[string][]string
	OnSend     func(f *fakeTerm, name, text string)
	OnSnapshot func(f *fakeTerm, name string)
}

func newFake() *fakeTerm {
	return &fakeTerm{screens: map[string]string{}, sent: map[string][]string{}}
}

func (f *fakeTerm) set(name, screen string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.screens[name] = screen
}

func (f *fakeTerm) kill(name string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.screens, name)
}

func (f *fakeTerm) sentTo(name string) []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string{}, f.sent[name]...)
}

func (f *fakeTerm) List() ([]string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	names := make([]string, 0, len(f.screens))
	for n := range f.screens {
		names = append(names, n)
	}
	sort.Strings(names)
	return names, nil
}

func (f *fakeTerm) Snapshot(name string) (string, error) {
	if f.OnSnapshot != nil {
		f.OnSnapshot(f, name)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	s, ok := f.screens[name]
	if !ok {
		return "", fmt.Errorf("session %q not found", name)
	}
	return s, nil
}

func (f *fakeTerm) Send(name, text string) error {
	f.mu.Lock()
	if _, ok := f.screens[name]; !ok {
		f.mu.Unlock()
		return fmt.Errorf("session %q not found", name)
	}
	f.sent[name] = append(f.sent[name], text)
	f.mu.Unlock()
	if f.OnSend != nil {
		f.OnSend(f, name, text)
	}
	return nil
}

func (f *fakeTerm) WaitFor(name, pattern string, timeout float64) (string, error) {
	re, _ := regexp.Compile(pattern)
	deadline := time.Now().Add(time.Duration(timeout * float64(time.Second)))
	for {
		snap, err := f.Snapshot(name)
		if err != nil {
			return "", err
		}
		if strings.Contains(snap, pattern) || (re != nil && re.MatchString(snap)) {
			return snap, nil
		}
		if time.Now().After(deadline) {
			return "", fmt.Errorf("timeout: pattern %q not found in session %q", pattern, name)
		}
		time.Sleep(2 * time.Millisecond)
	}
}

// --- reply extraction --------------------------------------------------------

func TestExtractReply(t *testing.T) {
	snap := "$ some old output\nWhat is 2+2? End with <<END>>\nThe answer is 4.\n<<END>>\n$"
	got := extractReply(snap, "What is 2+2? End with <<END>>", "<<END>>")
	if got != "The answer is 4." {
		t.Errorf("extractReply = %q, want %q", got, "The answer is 4.")
	}

	if got := extractReply("no marker here", "prompt", "<<END>>"); got != "" {
		t.Errorf("missing marker: got %q, want empty", got)
	}

	// The anchor is the LAST non-empty line of the sent text, and the reply
	// is bounded by the LAST marker occurrence before trailing output.
	multi := "line one\nline two"
	snap = "junk\nline two\nreply body\n<<END>>"
	if got := extractReply(snap, multi, "<<END>>"); got != "reply body" {
		t.Errorf("multiline anchor: got %q, want %q", got, "reply body")
	}
}

func TestSendWithDoneReturnsReplyBoundedByMarker(t *testing.T) {
	f := newFake()
	f.set("kirk", "$")
	f.OnSend = func(f *fakeTerm, name, text string) {
		// The sub-agent echoes the prompt (unescaped, as a terminal would
		// render it) and then replies, ending with the marker.
		echoed := strings.ReplaceAll(text, "<<", "<")
		f.set(name, "$ "+echoed+"\n42\n<<END>>")
	}
	reply, err := SendWithDone(f, "kirk", "What is 6*7? End with <<END>>", "", 2)
	if err != nil {
		t.Fatalf("SendWithDone: %v", err)
	}
	if reply != "42" {
		t.Errorf("reply = %q, want %q", reply, "42")
	}
	// < must have been escaped to << on the wire so key parsing can't fire.
	sent := f.sentTo("kirk")
	if len(sent) != 1 || strings.Contains(strings.ReplaceAll(sent[0], "<<", ""), "<") {
		t.Errorf("sent text not fully <-escaped: %q", sent)
	}
}

func TestSendWithDoneDoesNotLeakSubsequentOutput(t *testing.T) {
	f := newFake()
	f.set("kirk", "$")
	f.OnSend = func(f *fakeTerm, name, text string) {
		f.set(name, "prompt line\nthe reply\n<<END>>\n$ trailing shell prompt")
	}
	reply, err := SendWithDone(f, "kirk", "prompt line", "<<END>>", 2)
	if err != nil {
		t.Fatalf("SendWithDone: %v", err)
	}
	if reply != "the reply" {
		t.Errorf("reply = %q, want %q (no post-marker leakage)", reply, "the reply")
	}
}

// --- snapshot_since ----------------------------------------------------------

func TestSnapshotSinceReturnsOnlyPostMarkerContent(t *testing.T) {
	f := newFake()
	f.set("s", "before\n===MARK===\nafter one\nafter two")
	got, err := SnapshotSince(f, "s", "===MARK===")
	if err != nil {
		t.Fatal(err)
	}
	if got != "after one\nafter two" {
		t.Errorf("got %q", got)
	}

	// Marker absent -> full snapshot.
	got, _ = SnapshotSince(f, "s", "NOPE")
	if got != "before\n===MARK===\nafter one\nafter two" {
		t.Errorf("absent marker: got %q, want full screen", got)
	}
}

// --- detect_blocked ----------------------------------------------------------

func TestDetectBlockedPatterns(t *testing.T) {
	cases := []struct {
		screen string
		hint   string
	}{
		{"$ sudo ls\n[sudo] password for sam:", "password prompt"},
		{"Overwrite file? [y/n]", "y/n confirmation"},
		{"Proceed (yes/no)?", "y/n confirmation"},
		{"Do you want to continue?", "continue prompt"},
		{"Allow network access to python.exe?", "approval prompt"},
		{"Press any key to continue . . .", "any-key prompt"},
		{"Enter verification code:", "2FA code prompt"},
		{"Enter your 2FA code:", "2FA code prompt"},
		{"building...\ncompiling module 3 of 7", ""},
		{"", ""},
		// Only the bottom rows count: a stale prompt scrolled past three
		// non-empty lines of output no longer reads as blocked.
		{"password:\nout1\nout2\nout3", ""},
	}
	for _, c := range cases {
		if got := DetectBlockedIn(c.screen); got != c.hint {
			t.Errorf("DetectBlockedIn(%q) = %q, want %q", c.screen, got, c.hint)
		}
	}
}

func TestDetectBlockedReadsSession(t *testing.T) {
	f := newFake()
	f.set("s", "$ sudo apt install\n[sudo] password for sam:")
	hint, err := DetectBlocked(f, "s")
	if err != nil || hint != "password prompt" {
		t.Errorf("got (%q, %v)", hint, err)
	}
	if _, err := DetectBlocked(f, "ghost"); err == nil {
		t.Error("missing session: want error")
	}
}

// --- pipe --------------------------------------------------------------------

func TestPipeBetweenPanes(t *testing.T) {
	f := newFake()
	f.set("src", "line1\n\nline2\nline3")
	f.set("dst", "$")

	if err := Pipe(f, "src", "dst", 2); err != nil {
		t.Fatal(err)
	}
	sent := f.sentTo("dst")
	if len(sent) != 1 || sent[0] != "line2\nline3" {
		t.Errorf("lines=2: sent %q", sent)
	}

	// lines<=0 pipes the whole screen, with < escaped for literal delivery.
	f2 := newFake()
	f2.set("src", "a <tag> b")
	f2.set("dst", "$")
	if err := Pipe(f2, "src", "dst", 0); err != nil {
		t.Fatal(err)
	}
	if sent := f2.sentTo("dst"); len(sent) != 1 || sent[0] != "a <<tag> b" {
		t.Errorf("full-screen pipe: sent %q", sent)
	}

	// Empty source screen: nothing is sent at all.
	f3 := newFake()
	f3.set("src", "")
	f3.set("dst", "$")
	if err := Pipe(f3, "src", "dst", 0); err != nil {
		t.Fatal(err)
	}
	if sent := f3.sentTo("dst"); len(sent) != 0 {
		t.Errorf("empty source: sent %q, want nothing", sent)
	}
}

// --- subscriptions -----------------------------------------------------------

func TestSubscriptionYieldsOnMatchAndNewPositionsOnly(t *testing.T) {
	f := newFake()
	f.set("s", "quiet")
	sub, err := Subscribe(f, "s", "ERROR")
	if err != nil {
		t.Fatal(err)
	}
	defer sub.Close()

	f.set("s", "quiet\nERROR: it broke")
	if snap, ok := sub.Next(time.Second); !ok || !strings.Contains(snap, "ERROR") {
		t.Fatalf("first match: got (%q, %v)", snap, ok)
	}

	// Static match at the same position must not refire.
	if snap, ok := sub.Next(150 * time.Millisecond); ok {
		t.Errorf("static match refired: %q", snap)
	}

	// The pattern moving to a new position fires again.
	f.set("s", "quiet\nERROR: it broke\nmore\nERROR: again")
	if _, ok := sub.Next(time.Second); !ok {
		t.Error("moved match did not fire")
	}
}

func TestSubscriptionCloseStopsYielding(t *testing.T) {
	f := newFake()
	f.set("s", "quiet")
	sub, err := Subscribe(f, "s", "X")
	if err != nil {
		t.Fatal(err)
	}
	sub.Close()
	sub.Close() // idempotent
	f.set("s", "X marks the spot")
	if snap, ok := sub.Next(100 * time.Millisecond); ok {
		t.Errorf("closed subscription yielded %q", snap)
	}
}

func TestSubscribeUnknownSessionErrors(t *testing.T) {
	if _, err := Subscribe(newFake(), "ghost", "X"); err == nil {
		t.Error("want error for unknown session")
	}
}

// --- lifecycle ---------------------------------------------------------------

// collectEvents drains the stream until deadline, returning kinds per name.
func collectEvents(s *LifecycleStream, until time.Duration) []LifecycleEvent {
	var evs []LifecycleEvent
	deadline := time.Now().Add(until)
	for time.Now().Before(deadline) {
		if ev, ok := s.Next(20 * time.Millisecond); ok {
			evs = append(evs, ev)
		}
	}
	return evs
}

func hasEvent(evs []LifecycleEvent, kind, name string) bool {
	for _, ev := range evs {
		if ev.Kind == kind && ev.Name == name {
			return true
		}
	}
	return false
}

func TestLifecycleBirthAndDeath(t *testing.T) {
	f := newFake()
	stream := LifecycleEvents(f, 5*time.Millisecond, time.Hour)
	defer stream.Close()

	f.set("newborn", "$")
	time.Sleep(30 * time.Millisecond)
	f.kill("newborn")
	evs := collectEvents(stream, 100*time.Millisecond)

	if !hasEvent(evs, "born", "newborn") {
		t.Errorf("no born event in %v", evs)
	}
	if !hasEvent(evs, "died", "newborn") {
		t.Errorf("no died event in %v", evs)
	}
}

func TestLifecycleIdleThenBusy(t *testing.T) {
	f := newFake()
	f.set("worker", "output")
	stream := LifecycleEvents(f, 5*time.Millisecond, 30*time.Millisecond)
	defer stream.Close()

	// Unchanged past the idle threshold -> idle.
	evs := collectEvents(stream, 120*time.Millisecond)
	if !hasEvent(evs, "idle", "worker") {
		t.Fatalf("no idle event in %v", evs)
	}
	// Screen change after idle -> busy.
	f.set("worker", "output\nmore")
	evs = collectEvents(stream, 120*time.Millisecond)
	if !hasEvent(evs, "busy", "worker") {
		t.Errorf("no busy event in %v", evs)
	}
}
