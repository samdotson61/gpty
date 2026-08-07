//go:build live

package mcpserv

// End-to-end crew exercise: a real MCP client (in-memory transport) calling
// the registered tools against engine.Exec — i.e. real tmux, real pwsh (and
// python) panes. This is the closest thing to an agent driving the fleet
// without spawning one. Spawns and kills only its own sessions and never
// touches kill-server, so it is safe on a box with live sessions:
//
//	go test -tags live -run LiveCrew -v ./internal/mcpserv
import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/samdotson61/gpty/internal/engine"
	"github.com/samdotson61/gpty/internal/session"
)

// client connects an in-memory MCP client to a full-surface server over the
// exec engine.
func client(t *testing.T) *mcp.ClientSession {
	t.Helper()
	ct, st := mcp.NewInMemoryTransports()
	srv := NewServer(engine.Exec{}, nil)
	ss, err := srv.Connect(context.Background(), st, nil)
	if err != nil {
		t.Fatalf("server connect: %v", err)
	}
	t.Cleanup(func() { ss.Close() })
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "livecrew", Version: "0"}, nil).
		Connect(context.Background(), ct, nil)
	if err != nil {
		t.Fatalf("client connect: %v", err)
	}
	t.Cleanup(func() { cs.Close() })
	return cs
}

func call(t *testing.T, cs *mcp.ClientSession, name string, args map[string]any) string {
	t.Helper()
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	if res.IsError {
		t.Fatalf("%s returned tool error: %s", name, toolResText(res))
	}
	return toolResText(res)
}

func toolResText(res *mcp.CallToolResult) string {
	var parts []string
	for _, c := range res.Content {
		if tc, ok := c.(*mcp.TextContent); ok {
			parts = append(parts, tc.Text)
		}
	}
	return strings.Join(parts, "\n")
}

// answered matches the bare rendered "got it" line the password step prints
// only after its ReadLine returns.
var answered = regexp.MustCompile(`(?m)^got it\s*$`)

var liveCounter int

func liveName(t *testing.T, label string) string {
	t.Helper()
	liveCounter++
	name := fmt.Sprintf("crewe2e-%s-%d-%d", label, os.Getpid(), liveCounter)
	t.Cleanup(func() { _ = session.Kill(name) })
	return name
}

func spawnShell(t *testing.T, cs *mcp.ClientSession, label string) string {
	t.Helper()
	name := liveName(t, label)
	call(t, cs, "pty_spawn", map[string]any{"name": name, "cols": 120, "rows": 30})
	call(t, cs, "pty_wait_for", map[string]any{"name": name, "pattern": `[$>]`, "timeout": 20})
	return name
}

// TestLiveCrewScenario walks the whole Phase 1+2 surface in one story.
func TestLiveCrewScenario(t *testing.T) {
	cs := client(t)

	// --- Captain drives a REAL interactive sub-process (python REPL) -------
	pyName := liveName(t, "py")
	call(t, cs, "pty_spawn", map[string]any{"name": pyName, "cmd": "python", "cols": 120, "rows": 30})
	call(t, cs, "pty_wait_for", map[string]any{"name": pyName, "pattern": ">>>", "timeout": 30})

	reply := call(t, cs, "mesh_send_with_done", map[string]any{
		"name":    pyName,
		"text":    "print('the answer is', 6*7); print('<<END>>')\n",
		"timeout": 20,
	})
	if reply != "the answer is 42" {
		t.Errorf("python round-trip reply = %q, want %q", reply, "the answer is 42")
	}

	// Incremental read past the answer.
	since := call(t, cs, "mesh_snapshot_since", map[string]any{"name": pyName, "marker": "the answer is 42"})
	if !strings.Contains(since, "<<END>>") {
		t.Errorf("snapshot_since lost the marker: %q", since)
	}

	// --- A worker blocks on a real y/n prompt ------------------------------
	worker := spawnShell(t, cs, "worker")
	call(t, cs, "pty_send", map[string]any{
		"name": worker,
		"text": "Write-Host -NoNewline 'Proceed? [y/n] '; $r = $Host.UI.ReadLine(); Write-Host ('ANSWER=' + $r)<Enter>",
	})
	call(t, cs, "pty_wait_for", map[string]any{"name": worker, "pattern": `(?m)^Proceed\? \[y/n\]\s*$`, "timeout": 20})

	hint := call(t, cs, "mesh_detect_blocked", map[string]any{"name": worker})
	if hint != "y/n confirmation" {
		t.Fatalf("detect_blocked = %q, want y/n confirmation", hint)
	}

	// Red Alert sees the stalled fleet (worker blocked, python idle at >>>).
	var alert struct {
		Kind  string   `json:"kind"`
		Names []string `json:"names"`
	}
	raw := call(t, cs, "red_alert_check", map[string]any{"names": []string{worker, pyName}})
	if err := json.Unmarshal([]byte(raw), &alert); err != nil {
		t.Fatalf("red_alert_check JSON: %v (%q)", err, raw)
	}
	if alert.Kind != "deadlock" || len(alert.Names) != 1 || alert.Names[0] != worker {
		t.Errorf("red_alert_check = %s, want deadlock naming %s", raw, worker)
	}

	// Conservative previews escalate; permissive answers it for real.
	if d := call(t, cs, "prime_directive_resolve", map[string]any{"name": worker}); d != "escalate" {
		t.Errorf("conservative resolve = %q", d)
	}
	if d := call(t, cs, "prime_directive_enforce", map[string]any{"name": worker, "policy": "permissive"}); d != "approve" {
		t.Errorf("permissive enforce = %q", d)
	}
	call(t, cs, "pty_wait_for", map[string]any{"name": worker, "pattern": "ANSWER=y", "timeout": 20})

	// --- Secrets can never be auto-answered --------------------------------
	call(t, cs, "pty_send", map[string]any{
		"name": worker,
		"text": "Write-Host -NoNewline 'password: '; $p = $Host.UI.ReadLine(); Write-Host 'got it'<Enter>",
	})
	call(t, cs, "pty_wait_for", map[string]any{"name": worker, "pattern": `(?m)^password:\s*$`, "timeout": 20})
	if d := call(t, cs, "prime_directive_enforce", map[string]any{"name": worker, "policy": "permissive"}); d != "escalate" {
		t.Errorf("secrets prompt enforce = %q, want escalate", d)
	}
	// The echoed command contains the literal 'got it'; only a RENDERED
	// bare line proves ReadLine returned (i.e. someone answered the prompt).
	snap := call(t, cs, "pty_snapshot", map[string]any{"name": worker})
	if answered.MatchString(snap) {
		t.Error("secrets prompt was answered — hard invariant broken")
	}
	call(t, cs, "pty_send", map[string]any{"name": worker, "text": "<Enter>"}) // release it ourselves

	// --- Bones diagnoses a sick pane vs a healthy one ----------------------
	sick := spawnShell(t, cs, "sick")
	call(t, cs, "pty_send", map[string]any{"name": sick, "text": "Write-Host 'error: simulated failure'<Enter>"})
	call(t, cs, "pty_wait_for", map[string]any{"name": sick, "pattern": "simulated failure", "timeout": 20})

	var diag struct {
		Healthy  bool     `json:"healthy"`
		Symptoms []string `json:"symptoms"`
	}
	raw = call(t, cs, "bones_examine", map[string]any{"name": sick})
	if err := json.Unmarshal([]byte(raw), &diag); err != nil {
		t.Fatalf("bones_examine JSON: %v (%q)", err, raw)
	}
	if diag.Healthy || len(diag.Symptoms) == 0 || diag.Symptoms[0] != "errors" {
		t.Errorf("bones_examine on error pane = %s", raw)
	}

	var triage []struct {
		Name    string `json:"name"`
		Healthy bool   `json:"healthy"`
	}
	raw = call(t, cs, "bones_triage", map[string]any{"names": []string{worker, sick}})
	if err := json.Unmarshal([]byte(raw), &triage); err != nil {
		t.Fatalf("bones_triage JSON: %v (%q)", err, raw)
	}
	if len(triage) != 2 || triage[0].Name != sick {
		t.Errorf("bones_triage order = %s, want %s first", raw, sick)
	}

	// --- Pipe: move the sick pane's tail into the worker without reading it -
	// (3 lines so the error output line rides along, not just the prompt.)
	call(t, cs, "mesh_pipe", map[string]any{"from_name": sick, "to_name": worker, "lines": 3})
	call(t, cs, "pty_wait_for", map[string]any{"name": worker, "pattern": "simulated failure", "timeout": 20})

	// --- Subscription: push-style watch fires on new output ----------------
	subID := call(t, cs, "mesh_subscribe_create", map[string]any{"name": sick, "pattern": "BUILD DONE"})
	call(t, cs, "pty_send", map[string]any{"name": sick, "text": "Write-Host 'BUILD DONE'<Enter>"})
	if snap := call(t, cs, "mesh_subscribe_next", map[string]any{"id": subID, "timeout": 15}); !strings.Contains(snap, "BUILD DONE") {
		t.Errorf("subscription missed the match: %q", snap)
	}
	call(t, cs, "mesh_subscribe_close", map[string]any{"id": subID})

	// --- Lifecycle: born + died for a scratch session ----------------------
	lifeID := call(t, cs, "mesh_lifecycle_create", map[string]any{})
	scratch := liveName(t, "scratch")
	call(t, cs, "pty_spawn", map[string]any{"name": scratch})
	waitEvent(t, cs, lifeID, "born", scratch)
	call(t, cs, "pty_kill", map[string]any{"name": scratch})
	waitEvent(t, cs, lifeID, "died", scratch)
	call(t, cs, "mesh_lifecycle_close", map[string]any{"id": lifeID})
}

// waitEvent drains the lifecycle stream until the wanted event (other
// sessions on the box may interleave their own events).
func waitEvent(t *testing.T, cs *mcp.ClientSession, streamID, kind, name string) {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		raw := call(t, cs, "mesh_lifecycle_next", map[string]any{"id": streamID, "timeout": 5})
		var ev struct {
			Kind string `json:"kind"`
			Name string `json:"name"`
		}
		if json.Unmarshal([]byte(raw), &ev) == nil && ev.Kind == kind && ev.Name == name {
			return
		}
	}
	t.Fatalf("lifecycle event %s/%s never arrived", kind, name)
}
