package mesh

import (
	"strings"
	"sync"
	"time"
)

// Poll cadences, ported from upstream mesh.py. Fields on the watchers (not
// consts) so tests can shrink them.
const (
	SubscribePollInterval = 25 * time.Millisecond
	LifecyclePollInterval = 500 * time.Millisecond
	IdleThreshold         = 2 * time.Second
)

// Subscription is a live literal-substring match against a session's screen.
// Each time the pattern hits a NEW position in the rendered screen a snapshot
// is delivered; static matches that don't move don't refire. The watcher
// stops itself if the session disappears.
type Subscription struct {
	stop chan struct{}
	done chan struct{}
	ch   chan string
	once sync.Once
}

// Subscribe starts a background subscription to pattern (literal substring)
// in the named session's screen.
func Subscribe(t Term, name, pattern string) (*Subscription, error) {
	if _, err := t.Snapshot(name); err != nil {
		return nil, err
	}
	s := &Subscription{
		stop: make(chan struct{}),
		done: make(chan struct{}),
		ch:   make(chan string, 16),
	}
	go s.run(t, name, pattern, SubscribePollInterval)
	return s, nil
}

func (s *Subscription) run(t Term, name, pattern string, poll time.Duration) {
	defer close(s.done)
	lastIdx := -1
	for {
		select {
		case <-s.stop:
			return
		case <-time.After(poll):
		}
		snap, err := t.Snapshot(name)
		if err != nil {
			return // session gone: the subscription dies with it
		}
		if idx := strings.LastIndex(snap, pattern); idx != -1 && idx != lastIdx {
			lastIdx = idx
			select {
			case s.ch <- snap:
			default: // a slow consumer drops the oldest-style overflow silently
			}
		}
	}
}

// Next blocks up to timeout for the next matching snapshot. ok=false on
// timeout (the subscription stays open).
func (s *Subscription) Next(timeout time.Duration) (string, bool) {
	select {
	case snap := <-s.ch:
		return snap, true
	case <-time.After(timeout):
		return "", false
	}
}

// Close stops the watcher goroutine. Idempotent.
func (s *Subscription) Close() {
	s.once.Do(func() { close(s.stop) })
	<-s.done
}

// LifecycleEvent is a managed-session state transition.
// Kind: "born" | "died" | "idle" (no screen change for IdleThreshold) |
// "busy" (an idle session's screen changed again).
type LifecycleEvent struct {
	Kind      string  `json:"kind"`
	Name      string  `json:"name"`
	Timestamp float64 `json:"timestamp"` // unix seconds
}

// LifecycleStream watches all managed sessions and delivers lifecycle events.
// Unlike upstream's shared singleton monitor, each stream runs its own
// watcher goroutine — streams are few (an orchestrator opens one) and this
// keeps lifetimes obvious.
type LifecycleStream struct {
	stop chan struct{}
	done chan struct{}
	ch   chan LifecycleEvent
	once sync.Once
}

// LifecycleEvents opens a lifecycle event stream. poll/idle <=0 use the
// package defaults. Caller must Close it.
func LifecycleEvents(t Term, poll, idle time.Duration) *LifecycleStream {
	if poll <= 0 {
		poll = LifecyclePollInterval
	}
	if idle <= 0 {
		idle = IdleThreshold
	}
	s := &LifecycleStream{
		stop: make(chan struct{}),
		done: make(chan struct{}),
		ch:   make(chan LifecycleEvent, 64),
	}
	// Baseline the known set synchronously: a session spawned right after
	// this call returns must count as born, not as pre-existing.
	known := map[string]bool{}
	if names, err := t.List(); err == nil {
		for _, n := range names {
			known[n] = true
		}
	}
	go s.run(t, known, poll, idle)
	return s
}

func (s *LifecycleStream) run(t Term, known map[string]bool, poll, idleAfter time.Duration) {
	defer close(s.done)
	type screenState struct {
		at   time.Time
		snap string
	}
	lastScreen := map[string]screenState{}
	idle := map[string]bool{}
	emit := func(kind, name string) {
		ev := LifecycleEvent{Kind: kind, Name: name, Timestamp: float64(time.Now().UnixNano()) / 1e9}
		select {
		case s.ch <- ev:
		default:
		}
	}
	for {
		select {
		case <-s.stop:
			return
		case <-time.After(poll):
		}
		names, err := t.List()
		if err != nil {
			continue
		}
		current := map[string]bool{}
		for _, n := range names {
			current[n] = true
		}
		now := time.Now()
		for n := range current {
			if !known[n] {
				emit("born", n)
			}
		}
		for n := range known {
			if !current[n] {
				emit("died", n)
				delete(lastScreen, n)
				delete(idle, n)
			}
		}
		known = current
		for n := range current {
			snap, err := t.Snapshot(n)
			if err != nil {
				continue
			}
			last, seen := lastScreen[n]
			if !seen {
				lastScreen[n] = screenState{now, snap}
				continue
			}
			if snap != last.snap {
				lastScreen[n] = screenState{now, snap}
				if idle[n] {
					emit("busy", n)
					delete(idle, n)
				}
			} else if !idle[n] && now.Sub(last.at) >= idleAfter {
				emit("idle", n)
				idle[n] = true
			}
		}
	}
}

// Next blocks up to timeout for the next event. ok=false on timeout.
func (s *LifecycleStream) Next(timeout time.Duration) (LifecycleEvent, bool) {
	select {
	case ev := <-s.ch:
		return ev, true
	case <-time.After(timeout):
		return LifecycleEvent{}, false
	}
}

// Close stops the watcher goroutine. Idempotent.
func (s *LifecycleStream) Close() {
	s.once.Do(func() { close(s.stop) })
	<-s.done
}
