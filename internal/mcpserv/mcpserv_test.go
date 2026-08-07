package mcpserv

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/samdotson61/gpty/internal/engine"
)

func TestParseToolFilter(t *testing.T) {
	count := func(allow func(string) bool) int {
		if allow == nil {
			return len(AllTools)
		}
		n := 0
		for _, name := range AllTools {
			if allow(name) {
				n++
			}
		}
		return n
	}

	for _, spec := range []string{"", "all"} {
		allow, err := ParseToolFilter(spec)
		if err != nil || allow != nil {
			t.Errorf("ParseToolFilter(%q): want nil filter and nil error, got filter-set=%v err=%v", spec, allow != nil, err)
		}
	}

	allow, err := ParseToolFilter("session")
	if err != nil || count(allow) != 6 || !allow("pty_spawn") || allow("pane_split") {
		t.Errorf("session preset: got %d tools, err %v; want the 6 pty_* tools", count(allow), err)
	}

	allow, err = ParseToolFilter("panes")
	if err != nil || count(allow) != 9 || allow("pty_spawn") || !allow("pane_split") {
		t.Errorf("panes preset: got %d tools, err %v; want the 9 pane_* tools", count(allow), err)
	}

	allow, err = ParseToolFilter("core")
	if err != nil || count(allow) != len(CoreTools) || !allow("pty_spawn") || !allow("pane_split") || allow("mesh_pipe") {
		t.Errorf("core preset: got %d tools, err %v; want the %d frozen core tools", count(allow), err, len(CoreTools))
	}

	allow, err = ParseToolFilter("mesh")
	if err != nil || count(allow) != 10 || !allow("mesh_send_with_done") || allow("bones_examine") || allow("pty_spawn") {
		t.Errorf("mesh preset: got %d tools, err %v; want the 10 mesh_* tools", count(allow), err)
	}

	allow, err = ParseToolFilter("crew")
	if err != nil || count(allow) != len(CrewTools) || !allow("mesh_send_with_done") || !allow("bones_examine") || allow("pty_spawn") {
		t.Errorf("crew preset: got %d tools, err %v; want the %d crew tools", count(allow), err, len(CrewTools))
	}

	allow, err = ParseToolFilter("pty_send, pane_capture")
	if err != nil || count(allow) != 2 || !allow("pty_send") || !allow("pane_capture") {
		t.Errorf("name list: got %d tools, err %v; want exactly the 2 named", count(allow), err)
	}

	if _, err = ParseToolFilter("pty_snapshto"); err == nil {
		t.Error("typo'd tool name: want error, got nil")
	}
	if _, err = ParseToolFilter(","); err == nil {
		t.Error("empty selection: want error, got nil")
	}
}

// listTools connects an in-memory client and returns what a real agent sees.
func listTools(t *testing.T, allow func(string) bool) []*mcp.Tool {
	t.Helper()
	ct, st := mcp.NewInMemoryTransports()
	srv := NewServer(engine.Exec{}, allow) // handlers are never invoked: no tmux touched
	ss, err := srv.Connect(context.Background(), st, nil)
	if err != nil {
		t.Fatalf("server connect: %v", err)
	}
	defer ss.Close()
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "0"}, nil).
		Connect(context.Background(), ct, nil)
	if err != nil {
		t.Fatalf("client connect: %v", err)
	}
	defer cs.Close()
	res, err := cs.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	return res.Tools
}

func TestToolSetMatchesCompatLock(t *testing.T) {
	tools := listTools(t, nil)
	got := map[string]bool{}
	for _, tool := range tools {
		got[tool.Name] = true
	}
	if len(tools) != len(AllTools) {
		t.Errorf("registered %d tools, want %d", len(tools), len(AllTools))
	}
	for _, name := range AllTools {
		if !got[name] {
			t.Errorf("compat-locked tool %q is not registered", name)
		}
	}

	session := listTools(t, mustFilter(t, "session"))
	if len(session) != 6 {
		t.Errorf("--tools session exposes %d tools, want 6", len(session))
	}
	for _, tool := range session {
		if !strings.HasPrefix(tool.Name, "pty_") {
			t.Errorf("--tools session leaked %q", tool.Name)
		}
	}
}

// Tool definitions ride in every agent session's context window. This pins
// the budget so descriptions don't creep back toward documentation.
func TestToolBudget(t *testing.T) {
	tools := listTools(t, nil)
	total := 0
	for _, tool := range tools {
		if n := len(tool.Description); n > 200 {
			t.Errorf("%s: description is %d chars (budget 200)", tool.Name, n)
		}
		raw, err := json.Marshal(tool)
		if err != nil {
			t.Fatalf("%s: marshal: %v", tool.Name, err)
		}
		total += len(raw)
	}
	// 5000 covered the 15 core tools; the 18 crew tools ride the same
	// per-tool discipline, so the ceiling scales with the surface.
	if total > 12000 {
		t.Errorf("serialized tool definitions total %d chars (budget 12000)", total)
	}
	t.Logf("tool definitions: %d tools, %d chars total", len(tools), total)
}

func mustFilter(t *testing.T, spec string) func(string) bool {
	t.Helper()
	allow, err := ParseToolFilter(spec)
	if err != nil {
		t.Fatalf("ParseToolFilter(%q): %v", spec, err)
	}
	return allow
}
