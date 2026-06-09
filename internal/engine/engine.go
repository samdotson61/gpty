// Package engine is the seam between the operation implementations (session,
// panes) and the consumers that need them as one object (mcpserv, ctl). It
// defines the Engine interface and Exec, the always-correct exec implementation.
//
// The resident control-mode accelerator (internal/ctl) embeds an Engine and
// overrides only the hot reads/sends, delegating everything else here — so the
// fast path is never less correct than the exec path.
package engine

import (
	"github.com/samdotson61/gpty/internal/panes"
	"github.com/samdotson61/gpty/internal/session"
)

// Engine is the full set of terminal operations the MCP server exposes. Both
// Exec (exec one-shots) and ctl.Accel (control-mode resident) implement it.
type Engine interface {
	Spawn(name, cmd, cwd string, cols, rows int) error
	Kill(name string) error
	List() ([]string, error)
	Snapshot(name string) (string, error)
	Send(name, text string) error
	WaitFor(name, pattern string, timeout float64) (string, error)

	Split(target, dir, cmd, cwd string, percent int) (string, error)
	PaneInfo(id string) string
	PaneList(target string) (string, error)
	PaneCapture(pane string) (string, error)
	PaneSend(pane, text string) error
	PaneKill(pane string) error
	PaneSelect(pane string) error
	PaneResize(pane, dir string, amount int) error
	PaneLayout(target, layout string) error
}

// Exec is the exec-based engine: every call spawns a short-lived tmux client.
// It is the CLI engine and the control-mode fallback.
type Exec struct{}

func (Exec) Spawn(name, cmd, cwd string, cols, rows int) error {
	return session.Spawn(name, cmd, cwd, cols, rows)
}
func (Exec) Kill(name string) error               { return session.Kill(name) }
func (Exec) List() ([]string, error)              { return session.List() }
func (Exec) Snapshot(name string) (string, error) { return session.Snapshot(name) }
func (Exec) Send(name, text string) error         { return session.Send(name, text) }
func (Exec) WaitFor(name, pattern string, timeout float64) (string, error) {
	return session.WaitFor(name, pattern, timeout)
}

func (Exec) Split(target, dir, cmd, cwd string, percent int) (string, error) {
	return panes.Split(target, dir, cmd, cwd, percent)
}
func (Exec) PaneInfo(id string) string               { return panes.Info(id) }
func (Exec) PaneList(target string) (string, error)  { return panes.List(target) }
func (Exec) PaneCapture(pane string) (string, error) { return panes.Capture(pane) }
func (Exec) PaneSend(pane, text string) error        { return panes.Send(pane, text) }
func (Exec) PaneKill(pane string) error              { return panes.Kill(pane) }
func (Exec) PaneSelect(pane string) error            { return panes.Select(pane) }
func (Exec) PaneResize(pane, dir string, amount int) error {
	return panes.Resize(pane, dir, amount)
}
func (Exec) PaneLayout(target, layout string) error { return panes.Layout(target, layout) }

// Ensure Exec satisfies Engine at compile time.
var _ Engine = Exec{}
