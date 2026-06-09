package ctl

import (
	"sync"
	"time"

	"github.com/samdotson61/gpty/internal/engine"
	"github.com/samdotson61/gpty/internal/session"
)

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
	stop      chan struct{}
}

// NewAccel dials a control client and wraps base. Returns an error (and leaves
// the caller to use base directly) if the channel can't be established.
func NewAccel(base engine.Engine) (*Accel, error) {
	cl, err := Dial()
	if err != nil {
		return nil, err
	}
	return &Accel{Engine: base, client: cl, stop: make(chan struct{})}, nil
}

// get returns a healthy client, or nil (triggering a background redial) when
// the channel is down — callers fall back to exec on nil.
func (a *Accel) get() *Client {
	a.mu.RLock()
	cl := a.client
	a.mu.RUnlock()
	if cl != nil && cl.Healthy() {
		return cl
	}
	a.triggerRedial()
	return nil
}

func (a *Accel) triggerRedial() {
	a.mu.Lock()
	if a.redialing {
		a.mu.Unlock()
		return
	}
	a.redialing = true
	a.mu.Unlock()

	go func() {
		for {
			select {
			case <-a.stop:
				return
			default:
			}
			if cl, err := Dial(); err == nil {
				a.mu.Lock()
				a.client = cl
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
