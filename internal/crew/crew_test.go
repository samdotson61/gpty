package crew

// Ported from upstream agent-pty's tests/test_spock.py (assess core),
// test_bones.py, test_red_alert.py and test_prime_directive.py, rehosted on
// a fake Term so the suite runs without a live tmux server.

import (
	"fmt"
	"reflect"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"
)

type fakeTerm struct {
	mu      sync.Mutex
	screens map[string]string
	sent    map[string][]string
	busy    map[string]bool // busy sessions grow their screen on every Snapshot
}

func newFake() *fakeTerm {
	return &fakeTerm{screens: map[string]string{}, sent: map[string][]string{}, busy: map[string]bool{}}
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

func (f *fakeTerm) markBusy(name string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.busy[name] = true
}

func (f *fakeTerm) sentTo(name string) []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string{}, f.sent[name]...)
}

func (f *fakeTerm) totalSent() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, s := range f.sent {
		n += len(s)
	}
	return n
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
	f.mu.Lock()
	defer f.mu.Unlock()
	s, ok := f.screens[name]
	if !ok {
		return "", fmt.Errorf("session %q not found", name)
	}
	if f.busy[name] {
		f.screens[name] = s + "."
	}
	return s, nil
}

func (f *fakeTerm) Send(name, text string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.screens[name]; !ok {
		return fmt.Errorf("session %q not found", name)
	}
	f.sent[name] = append(f.sent[name], text)
	return nil
}

func (f *fakeTerm) WaitFor(name, pattern string, timeout float64) (string, error) {
	return "", fmt.Errorf("WaitFor not used by crew")
}

func stateOf(r FleetReport, name string) string {
	for _, p := range r.Panes {
		if p.Name == name {
			return p.State
		}
	}
	return "<absent>"
}

// --- fleet assessor (Spock core) --------------------------------------------

func TestAssessClassifiesStates(t *testing.T) {
	f := newFake()
	f.set("blocked", "$ sudo x\n[sudo] password for sam:")
	f.set("idle", "done\n$")
	f.set("busy", "compiling")
	f.markBusy("busy")

	r := Assess(f, []string{"blocked", "idle", "busy", "ghost"})
	for name, want := range map[string]string{
		"blocked": "blocked", "idle": "idle", "busy": "busy", "ghost": "dead",
	} {
		if got := stateOf(r, name); got != want {
			t.Errorf("state of %s = %s, want %s", name, got, want)
		}
	}
	if r.Deadlock {
		t.Error("deadlock reported with a busy pane present")
	}
}

func TestAssessDeadlockAndSummary(t *testing.T) {
	f := newFake()
	f.set("stuck", "Overwrite? [y/n]")
	f.set("waiting", "done\n$")
	r := Assess(f, nil)
	if !r.Deadlock {
		t.Fatal("blocked pane + nothing busy must be a deadlock")
	}
	if !strings.Contains(r.Summary, "DEADLOCK") || !strings.Contains(r.Summary, "stuck") {
		t.Errorf("summary %q", r.Summary)
	}
}

// --- bones -------------------------------------------------------------------

func TestBonesQuiescentPromptIsHealthy(t *testing.T) {
	f := newFake()
	f.set("ok", "did things\n$")
	d := Examine(f, "ok")
	if !d.Healthy || len(d.Symptoms) != 0 {
		t.Errorf("quiescent prompt: %+v", d)
	}
}

func TestBonesErrorSignatures(t *testing.T) {
	for _, screen := range []string{
		"Traceback (most recent call last):\n  File x\nValueError\n$",
		"bash: frobnicate: command not found\n$",
		"error: linker `cc` not found\n$",
		"panic: runtime error: index out of range\n$",
	} {
		f := newFake()
		f.set("sick", screen)
		d := Examine(f, "sick")
		if d.Healthy || d.Symptoms[0] != "errors" {
			t.Errorf("screen %q: %+v", screen, d)
		}
	}
}

func TestBonesThrashing(t *testing.T) {
	f := newFake()
	f.set("loop", strings.Repeat("retrying connection...\n", ThrashRepeats+2)+"$")
	d := Examine(f, "loop")
	found := false
	for _, s := range d.Symptoms {
		if s == "thrashing" {
			found = true
		}
	}
	if d.Healthy || !found {
		t.Errorf("thrashing pane: %+v", d)
	}

	// At or below the threshold is not thrashing.
	f.set("fine", strings.Repeat("step\n", ThrashRepeats)+"$")
	if d := Examine(f, "fine"); !d.Healthy {
		t.Errorf("%d repeats must be healthy: %+v", ThrashRepeats, d)
	}
}

func TestBonesHungVsPrompt(t *testing.T) {
	// Unchanged and NOT at a prompt -> hung.
	f := newFake()
	f.set("mid", "downloading 47%")
	d := Examine(f, "mid")
	if d.Healthy || d.Symptoms[0] != "hung" {
		t.Errorf("mid-task frozen pane: %+v", d)
	}
}

func TestBonesDead(t *testing.T) {
	f := newFake()
	if d := Examine(f, "ghost"); d.Healthy || d.Symptoms[0] != "dead" {
		t.Errorf("unmanaged name: %+v", d)
	}
}

func TestBonesTriageSortsSickestFirst(t *testing.T) {
	f := newFake()
	f.set("healthy", "fine\n$")
	f.set("sick", "error: boom") // errors + hung (frozen, no prompt)
	ds := Triage(f, []string{"healthy", "sick", "ghost"})
	got := []string{ds[0].Name, ds[1].Name, ds[2].Name}
	want := []string{"ghost", "sick", "healthy"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("triage order %v, want %v", got, want)
	}
	if f.totalSent() != 0 {
		t.Error("bones typed into a pane — read-only invariant broken")
	}
}

// --- red alert ---------------------------------------------------------------

func TestRedAlertCheckDeadlock(t *testing.T) {
	f := newFake()
	f.set("stuck", "Proceed? (y/n)")
	a := Check(f, nil)
	if a == nil || a.Kind != "deadlock" || !reflect.DeepEqual(a.Names, []string{"stuck"}) {
		t.Errorf("got %+v", a)
	}
}

func TestRedAlertPrefersDeadlockOverDeath(t *testing.T) {
	f := newFake()
	f.set("stuck", "Proceed? (y/n)")
	a := Check(f, []string{"stuck", "ghost"})
	if a == nil || a.Kind != "deadlock" {
		t.Errorf("got %+v", a)
	}
}

func TestRedAlertDeathAndHealthy(t *testing.T) {
	f := newFake()
	f.set("fine", "done\n$")
	a := Check(f, []string{"fine", "ghost"})
	if a == nil || a.Kind != "death" || !reflect.DeepEqual(a.Names, []string{"ghost"}) {
		t.Errorf("dead pane: got %+v", a)
	}
	if a := Check(f, []string{"fine"}); a != nil {
		t.Errorf("healthy fleet: got %+v, want nil", a)
	}
}

func TestRedAlertNotifyCustomNotifier(t *testing.T) {
	var got []string
	Notify("the fleet needs you", func(m string) { got = append(got, m) })
	if !reflect.DeepEqual(got, []string{"the fleet needs you"}) {
		t.Errorf("notifier got %v", got)
	}
}

func TestRedAlertWatchFiresAndDedups(t *testing.T) {
	f := newFake()
	f.set("stuck", "Proceed? (y/n)")

	var mu sync.Mutex
	var fired []string
	al := Watch(f, nil, func(m string) {
		mu.Lock()
		fired = append(fired, m)
		mu.Unlock()
	}, 10*time.Millisecond)

	// Several poll cycles pass (each includes the settle window); the
	// identical alert must fire exactly once.
	time.Sleep(600 * time.Millisecond)
	al.Stop()
	al.Stop() // idempotent

	mu.Lock()
	defer mu.Unlock()
	if len(fired) != 1 {
		t.Errorf("fired %d times (%v), want exactly 1", len(fired), fired)
	}
	if f.totalSent() != 0 {
		t.Error("red alert typed into a pane — read-only invariant broken")
	}
}

// --- prime directive ---------------------------------------------------------

func TestPrimeDirectiveConservativeEscalates(t *testing.T) {
	f := newFake()
	f.set("stuck", "Overwrite? [y/n]")
	d, err := Resolve(f, "stuck", Conservative())
	if err != nil || d != "escalate" {
		t.Errorf("resolve: (%q, %v)", d, err)
	}
	d, err = Enforce(f, "stuck", Conservative(), "", "")
	if err != nil || d != "escalate" || f.totalSent() != 0 {
		t.Errorf("conservative enforce must not answer: (%q, %v, sent=%d)", d, err, f.totalSent())
	}
}

func TestPrimeDirectivePermissiveApproves(t *testing.T) {
	f := newFake()
	f.set("stuck", "Do you want to continue?")
	d, err := Enforce(f, "stuck", Permissive(), "", "")
	if err != nil || d != "approve" {
		t.Fatalf("(%q, %v)", d, err)
	}
	if sent := f.sentTo("stuck"); !reflect.DeepEqual(sent, []string{"y<Enter>"}) {
		t.Errorf("sent %v, want [y<Enter>]", sent)
	}
}

func TestPrimeDirectiveDenyRule(t *testing.T) {
	f := newFake()
	f.set("stuck", "Overwrite? [y/n]")
	deny := Policy{Rules: []Rule{{"y/n", "deny"}}, Default: "escalate"}
	d, err := Enforce(f, "stuck", deny, "", "")
	if err != nil || d != "deny" {
		t.Fatalf("(%q, %v)", d, err)
	}
	if sent := f.sentTo("stuck"); !reflect.DeepEqual(sent, []string{"n<Enter>"}) {
		t.Errorf("sent %v, want [n<Enter>]", sent)
	}
}

func TestPrimeDirectiveSecretsAlwaysEscalate(t *testing.T) {
	f := newFake()
	f.set("stuck", "[sudo] password for sam:")
	// Even a policy that tries to approve passwords cannot.
	reckless := Policy{Rules: []Rule{{"password", "approve"}}, Default: "approve"}
	d, err := Enforce(f, "stuck", reckless, "", "")
	if err != nil || d != "escalate" || f.totalSent() != 0 {
		t.Errorf("secrets must always escalate: (%q, %v, sent=%d)", d, err, f.totalSent())
	}
}

func TestPrimeDirectiveUnblockedIsNone(t *testing.T) {
	f := newFake()
	f.set("calm", "working...\nstill working")
	d, err := Enforce(f, "calm", Permissive(), "", "")
	if err != nil || d != "none" || f.totalSent() != 0 {
		t.Errorf("(%q, %v, sent=%d)", d, err, f.totalSent())
	}
}

func TestPrimeDirectivePolicyValidation(t *testing.T) {
	bad := Policy{Rules: []Rule{{"y/n", "aprove"}}, Default: "escalate"}
	if err := bad.Validate(); err == nil {
		t.Error("typo'd decision must fail validation")
	}
	f := newFake()
	f.set("s", "$")
	if _, err := Resolve(f, "s", bad); err == nil {
		t.Error("Resolve must reject an invalid policy")
	}
	if _, err := PolicyByName("chaotic"); err == nil {
		t.Error("unknown policy name must error")
	}
	for _, name := range []string{"", "conservative", "permissive"} {
		if _, err := PolicyByName(name); err != nil {
			t.Errorf("PolicyByName(%q): %v", name, err)
		}
	}
}
