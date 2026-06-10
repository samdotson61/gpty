// Package mcpserv exposes the engine over the Model Context Protocol. The same
// tool set is served two ways — stdio (local agents) and streamable HTTP (cloud
// agents) — both over an engine.Engine, so the exec and control-mode engines
// are interchangeable behind it.
//
// Tool names are frozen to win-pty's (compat lock §4.1: pty_* and pane_*), so
// existing agent registrations upgrade by changing only the command path. Only
// the server name changes (win-pty -> gpty), and pane_info is added to complete
// the locked set.
//
// Tool definitions are deliberately terse: every description and schema rides
// in the context window of EVERY session that registers the server, so wording
// here is a token budget, not documentation (the docs live in README/help).
// TestToolBudget pins the ceiling. --tools (ParseToolFilter) cuts further by
// exposing only a subset.
package mcpserv

import (
	"context"
	"fmt"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/samdotson61/gpty/internal/buildinfo"
	"github.com/samdotson61/gpty/internal/engine"
)

// AllTools is the canonical tool-name list (compat lock §4.1).
var AllTools = []string{
	"pty_spawn", "pty_send", "pty_snapshot", "pty_wait_for", "pty_list", "pty_kill",
	"pane_split", "pane_send", "pane_capture", "pane_list", "pane_info",
	"pane_kill", "pane_select", "pane_resize", "pane_layout",
}

// ParseToolFilter parses a --tools spec into a registration filter. ""/"all"
// exposes every tool (nil filter); "session" only the pty_* suite; "panes"
// only the pane_* suite; anything else is a comma-separated list of exact
// tool names. Unknown names are an error — a typo must not silently drop a
// tool.
func ParseToolFilter(spec string) (func(string) bool, error) {
	switch spec {
	case "", "all":
		return nil, nil
	case "session":
		return func(n string) bool { return strings.HasPrefix(n, "pty_") }, nil
	case "panes":
		return func(n string) bool { return strings.HasPrefix(n, "pane_") }, nil
	}
	known := map[string]bool{}
	for _, n := range AllTools {
		known[n] = true
	}
	want := map[string]bool{}
	for _, n := range strings.Split(spec, ",") {
		n = strings.TrimSpace(n)
		if n == "" {
			continue
		}
		if !known[n] {
			return nil, fmt.Errorf("unknown tool %q in --tools; presets all|session|panes, or names: %s",
				n, strings.Join(AllTools, ", "))
		}
		want[n] = true
	}
	if len(want) == 0 {
		return nil, fmt.Errorf("--tools %q selects no tools", spec)
	}
	return func(n string) bool { return want[n] }, nil
}

// NewServer builds an MCP server backed by eng. allow filters which tools are
// registered; nil registers all of them.
func NewServer(eng engine.Engine, allow func(string) bool) *mcp.Server {
	s := mcp.NewServer(&mcp.Implementation{Name: "gpty", Version: buildinfo.Version}, nil)
	register(s, eng, allow)
	return s
}

// addTool registers a tool unless the filter excludes it.
func addTool[In any](s *mcp.Server, allow func(string) bool, t *mcp.Tool, h mcp.ToolHandlerFor[In, any]) {
	if allow == nil || allow(t.Name) {
		mcp.AddTool(s, t, h)
	}
}

func textResult(s string) *mcp.CallToolResult {
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: s}}}
}

// --- tool input schemas (source-compatible with win-pty) --------------------

type spawnIn struct {
	Name string `json:"name"`
	Cmd  string `json:"cmd,omitempty"`
	Cwd  string `json:"cwd,omitempty"`
	Cols int    `json:"cols,omitempty" jsonschema:"default 80"`
	Rows int    `json:"rows,omitempty" jsonschema:"default 24"`
}
type nameIn struct {
	Name string `json:"name"`
}
type sendIn struct {
	Name string `json:"name"`
	Text string `json:"text" jsonschema:"e.g. echo hi<Enter>"`
}
type waitIn struct {
	Name    string  `json:"name"`
	Pattern string  `json:"pattern" jsonschema:"regex or substring"`
	Timeout float64 `json:"timeout,omitempty" jsonschema:"seconds (default 10)"`
}
type emptyIn struct{}

type splitIn struct {
	Dir     string `json:"dir,omitempty" jsonschema:"h = side-by-side, v = stacked (default h)"`
	Target  string `json:"target,omitempty" jsonschema:"pane to split (default: the agent's own)"`
	Cmd     string `json:"cmd,omitempty"`
	Cwd     string `json:"cwd,omitempty"`
	Percent int    `json:"percent,omitempty" jsonschema:"new-pane percent, e.g. 30"`
}
type paneSendIn struct {
	Pane string `json:"pane" jsonschema:"pane id, e.g. %5"`
	Text string `json:"text" jsonschema:"e.g. ls<Enter> or <C-c>"`
}
type paneIn struct {
	Pane string `json:"pane" jsonschema:"pane id, e.g. %5"`
}
type paneListIn struct {
	Target string `json:"target,omitempty" jsonschema:"'all' = whole server (default: current window)"`
}
type paneResizeIn struct {
	Pane   string `json:"pane" jsonschema:"pane id"`
	Dir    string `json:"dir" jsonschema:"U|D|L|R"`
	Amount int    `json:"amount,omitempty" jsonschema:"cells (default 5)"`
}
type paneLayoutIn struct {
	Target string `json:"target,omitempty" jsonschema:"window (default: current)"`
	Layout string `json:"layout,omitempty"`
}

func register(s *mcp.Server, eng engine.Engine, allow func(string) bool) {
	addTool(s, allow, &mcp.Tool{Name: "pty_spawn", Description: "Create a detached background session (agent-pty-<name>). NOT a pane: invisible to a human watching a gmux window — use pane_split for that. Empty cmd = default shell."},
		func(ctx context.Context, req *mcp.CallToolRequest, in spawnIn) (*mcp.CallToolResult, any, error) {
			if err := eng.Spawn(in.Name, in.Cmd, in.Cwd, in.Cols, in.Rows); err != nil {
				return nil, nil, err
			}
			return textResult(in.Name), nil, nil
		})

	addTool(s, allow, &mcp.Tool{Name: "pty_send", Description: "Type into a session: literal text + named keys <Enter> <C-c> <Up>; << = literal <."},
		func(ctx context.Context, req *mcp.CallToolRequest, in sendIn) (*mcp.CallToolResult, any, error) {
			if err := eng.Send(in.Name, in.Text); err != nil {
				return nil, nil, err
			}
			return textResult("ok"), nil, nil
		})

	addTool(s, allow, &mcp.Tool{Name: "pty_snapshot", Description: "Rendered screen of a session, as text."},
		func(ctx context.Context, req *mcp.CallToolRequest, in nameIn) (*mcp.CallToolResult, any, error) {
			out, err := eng.Snapshot(in.Name)
			if err != nil {
				return nil, nil, err
			}
			return textResult(out), nil, nil
		})

	addTool(s, allow, &mcp.Tool{Name: "pty_wait_for", Description: "Block until a regex/substring appears in a session; returns the snapshot."},
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

	addTool(s, allow, &mcp.Tool{Name: "pty_list", Description: "List managed session names."},
		func(ctx context.Context, req *mcp.CallToolRequest, in emptyIn) (*mcp.CallToolResult, any, error) {
			names, err := eng.List()
			if err != nil {
				return nil, nil, err
			}
			return textResult(strings.Join(names, "\n")), nil, nil
		})

	addTool(s, allow, &mcp.Tool{Name: "pty_kill", Description: "Kill a session."},
		func(ctx context.Context, req *mcp.CallToolRequest, in nameIn) (*mcp.CallToolResult, any, error) {
			if err := eng.Kill(in.Name); err != nil {
				return nil, nil, err
			}
			return textResult("ok"), nil, nil
		})

	// ---- pane control: split & drive the agent's CURRENT gmux window --------

	addTool(s, allow, &mcp.Tool{Name: "pane_split", Description: "Split a pane in the agent's CURRENT window into a new pane the human sees live. Returns the new pane id + attached-client count (0 = no human is watching: wrong target). Focus stays put."},
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

	addTool(s, allow, &mcp.Tool{Name: "pane_send", Description: "Type into a pane (same key syntax as pty_send)."},
		func(ctx context.Context, req *mcp.CallToolRequest, in paneSendIn) (*mcp.CallToolResult, any, error) {
			if err := eng.PaneSend(in.Pane, in.Text); err != nil {
				return nil, nil, err
			}
			return textResult("ok"), nil, nil
		})

	addTool(s, allow, &mcp.Tool{Name: "pane_capture", Description: "Rendered text of a pane."},
		func(ctx context.Context, req *mcp.CallToolRequest, in paneIn) (*mcp.CallToolResult, any, error) {
			out, err := eng.PaneCapture(in.Pane)
			if err != nil {
				return nil, nil, err
			}
			return textResult(out), nil, nil
		})

	addTool(s, allow, &mcp.Tool{Name: "pane_list", Description: "List panes (default: current window): id [session:win.pane] active attached WxH cmd."},
		func(ctx context.Context, req *mcp.CallToolRequest, in paneListIn) (*mcp.CallToolResult, any, error) {
			out, err := eng.PaneList(in.Target)
			if err != nil {
				return nil, nil, err
			}
			return textResult(out), nil, nil
		})

	addTool(s, allow, &mcp.Tool{Name: "pane_info", Description: "A pane's session/window, pane count, attached clients (0 = invisible to humans)."},
		func(ctx context.Context, req *mcp.CallToolRequest, in paneIn) (*mcp.CallToolResult, any, error) {
			info := eng.PaneInfo(in.Pane)
			if info == "" {
				return textResult("no such pane " + in.Pane), nil, nil
			}
			return textResult(info), nil, nil
		})

	addTool(s, allow, &mcp.Tool{Name: "pane_kill", Description: "Kill a pane (last pane closes its window)."},
		func(ctx context.Context, req *mcp.CallToolRequest, in paneIn) (*mcp.CallToolResult, any, error) {
			if err := eng.PaneKill(in.Pane); err != nil {
				return nil, nil, err
			}
			return textResult("ok"), nil, nil
		})

	addTool(s, allow, &mcp.Tool{Name: "pane_select", Description: "Focus a pane — the human's cursor moves there."},
		func(ctx context.Context, req *mcp.CallToolRequest, in paneIn) (*mcp.CallToolResult, any, error) {
			if err := eng.PaneSelect(in.Pane); err != nil {
				return nil, nil, err
			}
			return textResult("ok"), nil, nil
		})

	addTool(s, allow, &mcp.Tool{Name: "pane_resize", Description: "Resize a pane U/D/L/R by amount cells."},
		func(ctx context.Context, req *mcp.CallToolRequest, in paneResizeIn) (*mcp.CallToolResult, any, error) {
			if err := eng.PaneResize(in.Pane, in.Dir, in.Amount); err != nil {
				return nil, nil, err
			}
			return textResult("ok"), nil, nil
		})

	addTool(s, allow, &mcp.Tool{Name: "pane_layout", Description: "Re-tile a window: even-horizontal|even-vertical|main-horizontal|main-vertical|tiled."},
		func(ctx context.Context, req *mcp.CallToolRequest, in paneLayoutIn) (*mcp.CallToolResult, any, error) {
			if err := eng.PaneLayout(in.Target, in.Layout); err != nil {
				return nil, nil, err
			}
			return textResult("ok"), nil, nil
		})
}
