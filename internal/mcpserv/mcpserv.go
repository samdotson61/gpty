// Package mcpserv exposes the engine over the Model Context Protocol. The same
// tool set is served two ways — stdio (local agents) and streamable HTTP (cloud
// agents) — both over an engine.Engine, so the exec and control-mode engines
// are interchangeable behind it.
//
// Tool names are frozen to win-pty's (compat lock §4.1: pty_* and pane_*), so
// existing agent registrations upgrade by changing only the command path. Only
// the server name changes (win-pty -> gpty), and pane_info is added to complete
// the locked set.
package mcpserv

import (
	"context"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/samdotson61/gpty/internal/buildinfo"
	"github.com/samdotson61/gpty/internal/engine"
)

// NewServer builds an MCP server backed by eng with all gpty tools registered.
func NewServer(eng engine.Engine) *mcp.Server {
	s := mcp.NewServer(&mcp.Implementation{Name: "gpty", Version: buildinfo.Version}, nil)
	register(s, eng)
	return s
}

func textResult(s string) *mcp.CallToolResult {
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: s}}}
}

// --- tool input schemas (source-compatible with win-pty) --------------------

type spawnIn struct {
	Name string `json:"name" jsonschema:"session identifier"`
	Cmd  string `json:"cmd,omitempty" jsonschema:"command to run; empty opens the default shell"`
	Cwd  string `json:"cwd,omitempty" jsonschema:"working directory"`
	Cols int    `json:"cols,omitempty" jsonschema:"terminal columns (default 80)"`
	Rows int    `json:"rows,omitempty" jsonschema:"terminal rows (default 24)"`
}
type nameIn struct {
	Name string `json:"name" jsonschema:"session identifier"`
}
type sendIn struct {
	Name string `json:"name" jsonschema:"session identifier"`
	Text string `json:"text" jsonschema:"literal text and named keys, e.g. echo hi<Enter>"`
}
type waitIn struct {
	Name    string  `json:"name" jsonschema:"session identifier"`
	Pattern string  `json:"pattern" jsonschema:"substring or regex to wait for"`
	Timeout float64 `json:"timeout,omitempty" jsonschema:"seconds (default 10)"`
}
type emptyIn struct{}

type splitIn struct {
	Dir     string `json:"dir,omitempty" jsonschema:"'h' = side-by-side (left|right), 'v' = stacked (top/bottom). default h"`
	Target  string `json:"target,omitempty" jsonschema:"pane id to split; default = the agent's current pane ($TMUX_PANE)"`
	Cmd     string `json:"cmd,omitempty" jsonschema:"command for the new pane; empty = default shell (PowerShell 7 on Windows)"`
	Cwd     string `json:"cwd,omitempty" jsonschema:"working directory for the new pane"`
	Percent int    `json:"percent,omitempty" jsonschema:"new pane size as a percent of the split dimension, e.g. 30"`
}
type paneSendIn struct {
	Pane string `json:"pane" jsonschema:"target pane id, e.g. %5 (from pane_split or pane_list)"`
	Text string `json:"text" jsonschema:"literal text + named keys, e.g. ls<Enter> or <C-c>"`
}
type paneIn struct {
	Pane string `json:"pane" jsonschema:"target pane id, e.g. %5"`
}
type paneListIn struct {
	Target string `json:"target,omitempty" jsonschema:"window/pane to list; default = the agent's current window. 'all' = every pane on the server"`
}
type paneResizeIn struct {
	Pane   string `json:"pane" jsonschema:"target pane id"`
	Dir    string `json:"dir" jsonschema:"U, D, L or R (up/down/left/right)"`
	Amount int    `json:"amount,omitempty" jsonschema:"cells to resize by (default 5)"`
}
type paneLayoutIn struct {
	Target string `json:"target,omitempty" jsonschema:"window to re-tile; default = current"`
	Layout string `json:"layout,omitempty" jsonschema:"even-horizontal, even-vertical, main-horizontal, main-vertical, tiled (default tiled)"`
}

func register(s *mcp.Server, eng engine.Engine) {
	mcp.AddTool(s, &mcp.Tool{Name: "pty_spawn", Description: "Create a SEPARATE, detached background terminal session (agent-pty-<name>). It is NOT a pane in your current window — a human attached to a gmux session will NOT see it. To add panes to the window you're in (visible to the human), use pane_split instead. Use pty_spawn only for headless background terminals you drive programmatically. cmd empty = default shell (PowerShell 7 on Windows, your login shell on macOS/Linux)."},
		func(ctx context.Context, req *mcp.CallToolRequest, in spawnIn) (*mcp.CallToolResult, any, error) {
			if err := eng.Spawn(in.Name, in.Cmd, in.Cwd, in.Cols, in.Rows); err != nil {
				return nil, nil, err
			}
			return textResult(in.Name), nil, nil
		})

	mcp.AddTool(s, &mcp.Tool{Name: "pty_send", Description: "Send keystrokes (literal text + named keys like <Enter>, <C-c>, <Up>) to a session."},
		func(ctx context.Context, req *mcp.CallToolRequest, in sendIn) (*mcp.CallToolResult, any, error) {
			if err := eng.Send(in.Name, in.Text); err != nil {
				return nil, nil, err
			}
			return textResult("ok"), nil, nil
		})

	mcp.AddTool(s, &mcp.Tool{Name: "pty_snapshot", Description: "Return the current rendered screen of a session as plain text."},
		func(ctx context.Context, req *mcp.CallToolRequest, in nameIn) (*mcp.CallToolResult, any, error) {
			out, err := eng.Snapshot(in.Name)
			if err != nil {
				return nil, nil, err
			}
			return textResult(out), nil, nil
		})

	mcp.AddTool(s, &mcp.Tool{Name: "pty_wait_for", Description: "Block until a substring/regex appears in the session buffer; returns the snapshot."},
		func(ctx context.Context, req *mcp.CallToolRequest, in waitIn) (*mcp.CallToolResult, any, error) {
			to := in.Timeout
			if to == 0 {
				to = 10
			}
			out, err := eng.WaitFor(in.Name, in.Pattern, to)
			if err != nil {
				return nil, nil, err
			}
			return textResult(out), nil, nil
		})

	mcp.AddTool(s, &mcp.Tool{Name: "pty_list", Description: "List currently-managed session names."},
		func(ctx context.Context, req *mcp.CallToolRequest, in emptyIn) (*mcp.CallToolResult, any, error) {
			names, err := eng.List()
			if err != nil {
				return nil, nil, err
			}
			return textResult(strings.Join(names, "\n")), nil, nil
		})

	mcp.AddTool(s, &mcp.Tool{Name: "pty_kill", Description: "Kill a session and clean up its tmux state."},
		func(ctx context.Context, req *mcp.CallToolRequest, in nameIn) (*mcp.CallToolResult, any, error) {
			if err := eng.Kill(in.Name); err != nil {
				return nil, nil, err
			}
			return textResult("ok"), nil, nil
		})

	// ---- pane control: split & drive the agent's CURRENT gmux window --------

	mcp.AddTool(s, &mcp.Tool{Name: "pane_split", Description: "Split a pane in the agent's CURRENT gmux window, creating a new pane the human sees appear live. dir 'h'=side-by-side, 'v'=stacked; default target is the agent's own pane ($TMUX_PANE). Returns the new pane id (e.g. %5) plus its session/window and the attached-client count — if that count is 0, no human is watching and you split the wrong thing. Focus stays on the agent's pane."},
		func(ctx context.Context, req *mcp.CallToolRequest, in splitIn) (*mcp.CallToolResult, any, error) {
			id, err := eng.Split(in.Target, in.Dir, in.Cmd, in.Cwd, in.Percent)
			if err != nil {
				return nil, nil, err
			}
			msg := id
			if info := eng.PaneInfo(id); info != "" {
				msg = "created pane " + id + " — " + info
			}
			return textResult(msg), nil, nil
		})

	mcp.AddTool(s, &mcp.Tool{Name: "pane_send", Description: "Send keystrokes (literal text + named keys like <Enter>, <C-c>, <Up>) to a specific pane by id."},
		func(ctx context.Context, req *mcp.CallToolRequest, in paneSendIn) (*mcp.CallToolResult, any, error) {
			if err := eng.PaneSend(in.Pane, in.Text); err != nil {
				return nil, nil, err
			}
			return textResult("ok"), nil, nil
		})

	mcp.AddTool(s, &mcp.Tool{Name: "pane_capture", Description: "Return the current rendered text of a pane by id."},
		func(ctx context.Context, req *mcp.CallToolRequest, in paneIn) (*mcp.CallToolResult, any, error) {
			out, err := eng.PaneCapture(in.Pane)
			if err != nil {
				return nil, nil, err
			}
			return textResult(out), nil, nil
		})

	mcp.AddTool(s, &mcp.Tool{Name: "pane_list", Description: "List panes in the agent's current window (or target='all' for every pane). Columns: pane_id [session:win.pane] active attached WxH command."},
		func(ctx context.Context, req *mcp.CallToolRequest, in paneListIn) (*mcp.CallToolResult, any, error) {
			out, err := eng.PaneList(in.Target)
			if err != nil {
				return nil, nil, err
			}
			return textResult(out), nil, nil
		})

	mcp.AddTool(s, &mcp.Tool{Name: "pane_info", Description: "Describe a pane by id: its session/window, the window's pane count, and how many clients are attached (0 = no human is viewing it, so the pane is invisible)."},
		func(ctx context.Context, req *mcp.CallToolRequest, in paneIn) (*mcp.CallToolResult, any, error) {
			info := eng.PaneInfo(in.Pane)
			if info == "" {
				return textResult("no such pane " + in.Pane), nil, nil
			}
			return textResult(info), nil, nil
		})

	mcp.AddTool(s, &mcp.Tool{Name: "pane_kill", Description: "Kill a pane by id (closes its window if it was the last pane)."},
		func(ctx context.Context, req *mcp.CallToolRequest, in paneIn) (*mcp.CallToolResult, any, error) {
			if err := eng.PaneKill(in.Pane); err != nil {
				return nil, nil, err
			}
			return textResult("ok"), nil, nil
		})

	mcp.AddTool(s, &mcp.Tool{Name: "pane_select", Description: "Focus a pane by id — the human's cursor/keyboard moves to it."},
		func(ctx context.Context, req *mcp.CallToolRequest, in paneIn) (*mcp.CallToolResult, any, error) {
			if err := eng.PaneSelect(in.Pane); err != nil {
				return nil, nil, err
			}
			return textResult("ok"), nil, nil
		})

	mcp.AddTool(s, &mcp.Tool{Name: "pane_resize", Description: "Resize a pane by id: dir U/D/L/R, amount cells (default 5)."},
		func(ctx context.Context, req *mcp.CallToolRequest, in paneResizeIn) (*mcp.CallToolResult, any, error) {
			if err := eng.PaneResize(in.Pane, in.Dir, in.Amount); err != nil {
				return nil, nil, err
			}
			return textResult("ok"), nil, nil
		})

	mcp.AddTool(s, &mcp.Tool{Name: "pane_layout", Description: "Re-tile the current window with a layout: even-horizontal, even-vertical, main-horizontal, main-vertical, tiled (default tiled)."},
		func(ctx context.Context, req *mcp.CallToolRequest, in paneLayoutIn) (*mcp.CallToolResult, any, error) {
			if err := eng.PaneLayout(in.Target, in.Layout); err != nil {
				return nil, nil, err
			}
			return textResult("ok"), nil, nil
		})
}
