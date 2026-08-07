package crew

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/samdotson61/gpty/internal/mesh"
)

// Red Alert: escalation to the human when the fleet needs attention. The
// assessor OBSERVES; Red Alert watches on a background goroutine and FIRES A
// NOTIFICATION the moment something needs a human. Read-only on panes — the
// only side effect is the notification itself (a desktop toast via
// notify-send where available, else a line on stderr, or any Notifier the
// caller supplies). Inherits the assessor's heuristics: a signal, not a
// guarantee.

// AlertPollInterval is the default fleet-watch cadence.
const AlertPollInterval = 500 * time.Millisecond

// NotifyTitle is the toast title for the default notifier.
const NotifyTitle = "gpty RED ALERT"

// Notifier delivers one escalation message to the human.
type Notifier func(message string)

// Alert says a human is needed. Kind: "deadlock" (>=1 blocked pane and
// nothing busy — the whole fleet stalled) or "death" (>=1 dead pane).
type Alert struct {
	Kind   string   `json:"kind"`
	Detail string   `json:"detail"`
	Names  []string `json:"names"`
}

// Check inspects the fleet once; nil means the fleet is fine. Deadlock is
// the highest-value signal and is preferred when both conditions hold.
func Check(t mesh.Term, names []string) *Alert {
	report := Assess(t, names)
	if report.Deadlock {
		var blocked []string
		for _, p := range report.Panes {
			if p.State == "blocked" {
				blocked = append(blocked, p.Name)
			}
		}
		return &Alert{"deadlock", report.Summary, blocked}
	}
	var dead []string
	for _, p := range report.Panes {
		if p.State == "dead" {
			dead = append(dead, p.Name)
		}
	}
	if len(dead) > 0 {
		return &Alert{"death", fmt.Sprintf("%d dead pane(s): %s.", len(dead), strings.Join(dead, ", ")), dead}
	}
	return nil
}

// defaultNotify fires a desktop toast via notify-send if present (Linux
// desktops), else prints to stderr. Dependency-free and non-fatal: a failed
// notification never crashes the watcher.
func defaultNotify(message string) {
	if path, err := exec.LookPath("notify-send"); err == nil {
		cmd := exec.Command(path, NotifyTitle, message)
		cmd.Stdout, cmd.Stderr = nil, nil
		if cmd.Run() == nil {
			return
		}
	}
	fmt.Fprintf(os.Stderr, "[%s] %s\n", NotifyTitle, message)
}

// Notify sends one notification through notifier (nil = the default).
func Notify(message string, notifier Notifier) {
	if notifier == nil {
		notifier = defaultNotify
	}
	notifier(message)
}

// Alerter is a background watcher that polls Check and notifies on each NEW
// alert. Identical consecutive alerts fire once; a return to a healthy fleet
// resets the dedup, so a re-occurring problem re-alerts.
type Alerter struct {
	stop chan struct{}
	done chan struct{}
	once sync.Once
}

// Watch starts an Alerter over names (nil = all managed sessions). poll<=0
// uses AlertPollInterval. Caller must Stop it.
func Watch(t mesh.Term, names []string, notifier Notifier, poll time.Duration) *Alerter {
	if poll <= 0 {
		poll = AlertPollInterval
	}
	a := &Alerter{stop: make(chan struct{}), done: make(chan struct{})}
	go a.run(t, names, notifier, poll)
	return a
}

func (a *Alerter) run(t mesh.Term, names []string, notifier Notifier, poll time.Duration) {
	defer close(a.done)
	var last string // "" = healthy; else kind+"\x00"+detail dedup key
	for {
		select {
		case <-a.stop:
			return
		default:
		}
		alert := Check(t, names)
		if alert == nil {
			last = ""
		} else if key := alert.Kind + "\x00" + alert.Detail; key != last {
			last = key
			func() {
				defer func() { recover() }() // a broken notifier must not kill the watcher
				Notify(alert.Detail, notifier)
			}()
		}
		select {
		case <-a.stop:
			return
		case <-time.After(poll):
		}
	}
}

// Stop halts the watcher. Idempotent.
func (a *Alerter) Stop() {
	a.once.Do(func() { close(a.stop) })
	<-a.done
}
