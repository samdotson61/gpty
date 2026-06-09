package ctl

import (
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/samdotson61/gpty/internal/engine"
	"github.com/samdotson61/gpty/internal/session"
)

// maxDialFailures is how many consecutive dial failures we tolerate before
// giving up on control mode for this process and staying on the exec engine.
// On hosts where the channel never comes up (e.g. cygwin tmux today), this
// stops a background goroutine from respawning tmux -C clients forever.
const maxDialFailures = 3

// Accel is the resident engine: it embeds the exec engine and overrides only
// the hot reads/sends to use the control channel, falling back to exec whenever
// the channel is unavailable. Infrequent ops (spawn, kill, split, pane mgmt)
// are inherited from the embedded exec engine unchanged — per build-plan §6,
// spawn is "same as one-shot" even on the resident path, so there's nothing to
// accelerate there.
type Accel struct {
	engine.Engine // exec fallback base (promotes the non-hot ops)

	mu        sync.RWMutex
	client    *Client
	redialing bool
	gaveUp    bool
	stop      chan struct{}
}

// NewAccel wraps base with a control-mode accelerator. It returns immediately:
// the channel is dialed in the background, ops use the exec base until it's up,
// and if dialing keeps failing (maxDialFailures) the process stays on exec for
// good. This keeps server startup instant even on hosts where the dial would
// block for the full handshake deadline before failing (cygwin tmux today).
func NewAccel(base engine.Engine) *Accel {
	a := &Accel{Engine: base, stop: make(chan struct{})}
	a.triggerRedial()
	return a
}

// get returns a healthy client, or nil (triggering a background redial unless
// we've given up) — callers fall back to exec on nil.
func (a *Accel) get() *Client {
	a.mu.RLock()
	cl, gaveUp := a.client, a.gaveUp
	a.mu.RUnlock()
	if cl != nil && cl.Healthy() {
		return cl
	}
	if gaveUp {
		return nil
	}
	a.triggerRedial()
	return nil
}

func (a *Accel) triggerRedial() {
	a.mu.Lock()
	if a.redialing || a.gaveUp {
		a.mu.Unlock()
		return
	}
	a.redialing = true
	a.mu.Unlock()

	go func() {
		failures := 0
		for {
			select {
			case <-a.stop:
				return
			default:
			}
			cl, err := Dial()
			if err == nil {
				a.mu.Lock()
				a.client = cl
				a.redialing = false
				a.mu.Unlock()
				return
			}
			failures++
			if failures >= maxDialFailures {
				fmt.Fprintf(os.Stderr, "gpty: control mode unavailable after %d attempts (%v); staying on the exec engine\n", failures, err)
				a.mu.Lock()
				a.gaveUp = true
				a.redialing = false
				a.mu.Unlock()
				return
			}
			time.Sleep(2 * time.Second)
		}
	}()
}

// Close stops redial attempts and tears down the channel.
func (a *Accel) Close() {
	select {
	case <-a.stop:
	default:
		close(a.stop)
	}
	a.mu.Lock()
	cl := a.client
	a.client = nil
	a.mu.Unlock()
	if cl != nil {
		cl.Close()
	}
}

func (a *Accel) Snapshot(name string) (string, error) {
	if cl := a.get(); cl != nil {
		if s, err := cl.Capture(session.Full(name)); err == nil {
			return s, nil
		}
	}
	return a.Engine.Snapshot(name)
}

func (a *Accel) Send(name, text string) error {
	if cl := a.get(); cl != nil {
		if err := cl.Send(session.Full(name), text); err == nil {
			return nil
		}
	}
	return a.Engine.Send(name, text)
}

func (a *Accel) List() ([]string, error) {
	if cl := a.get(); cl != nil {
		if l, err := cl.ListSessions(); err == nil {
			return l, nil
		}
	}
	return a.Engine.List()
}

func (a *Accel) WaitFor(name, pattern string, timeout float64) (string, error) {
	if cl := a.get(); cl != nil {
		s, err := cl.WaitFor(session.Full(name), pattern, timeout)
		if err == nil {
			return s, nil
		}
		if cl.Healthy() {
			return "", err // a genuine timeout/parse error — don't double-wait via exec
		}
		// channel died mid-wait: fall through to the exec path
	}
	return a.Engine.WaitFor(name, pattern, timeout)
}

func (a *Accel) PaneCapture(pane string) (string, error) {
	if cl := a.get(); cl != nil {
		if s, err := cl.Capture(pane); err == nil {
			return s, nil
		}
	}
	return a.Engine.PaneCapture(pane)
}

func (a *Accel) PaneSend(pane, text string) error {
	if cl := a.get(); cl != nil {
		if err := cl.Send(pane, text); err == nil {
			return nil
		}
	}
	return a.Engine.PaneSend(pane, text)
}

// Accel implements the full engine at compile time.
var _ engine.Engine = (*Accel)(nil)
