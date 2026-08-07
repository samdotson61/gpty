package mcpserv

// Mesh (M6) + Phase-2 crew (Red Alert, Bones, Prime Directive) tools, ported
// from upstream agent-pty. Same terseness contract as the core tools: every
// word here rides in each registering agent's context window.

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/samdotson61/gpty/internal/crew"
	"github.com/samdotson61/gpty/internal/engine"
	"github.com/samdotson61/gpty/internal/mesh"
)

// registry holds the stateful handles (subscriptions, lifecycle streams,
// alerters) that live across tool calls, keyed by opaque ids.
type registry struct {
	mu       sync.Mutex
	nextID   int
	subs     map[string]*mesh.Subscription
	streams  map[string]*mesh.LifecycleStream
	alerters map[string]*crew.Alerter
}

var reg = &registry{
	subs:     map[string]*mesh.Subscription{},
	streams:  map[string]*mesh.LifecycleStream{},
	alerters: map[string]*crew.Alerter{},
}

func (r *registry) id(prefix string) string {
	r.nextID++
	return fmt.Sprintf("%s-%d", prefix, r.nextID)
}

func jsonResult(v any) (*mcp.CallToolResult, any, error) {
	raw, err := json.Marshal(v)
	if err != nil {
		return nil, nil, err
	}
	return textResult(string(raw)), nil, nil
}

func secs(f float64) time.Duration { return time.Duration(f * float64(time.Second)) }

// --- tool input schemas ------------------------------------------------------

type sendDoneIn struct {
	Name       string  `json:"name"`
	Text       string  `json:"text" jsonschema:"literal prompt; < is sent as-is"`
	DoneMarker string  `json:"done_marker,omitempty" jsonschema:"default <<END>>"`
	Timeout    float64 `json:"timeout,omitempty" jsonschema:"seconds (default 60)"`
}
type sinceIn struct {
	Name   string `json:"name"`
	Marker string `json:"marker"`
}
type pipeIn struct {
	FromName string `json:"from_name"`
	ToName   string `json:"to_name"`
	Lines    int    `json:"lines,omitempty" jsonschema:"last N non-empty lines (0 = full screen)"`
}
type subCreateIn struct {
	Name    string `json:"name"`
	Pattern string `json:"pattern" jsonschema:"literal substring"`
}
type idTimeoutIn struct {
	ID      string  `json:"id"`
	Timeout float64 `json:"timeout,omitempty" jsonschema:"seconds (default 10)"`
}
type idIn struct {
	ID string `json:"id"`
}
type namesIn struct {
	Names []string `json:"names,omitempty" jsonschema:"default: all managed sessions"`
}
type notifyIn struct {
	Message string `json:"message"`
}
type watchIn struct {
	Names []string `json:"names,omitempty" jsonschema:"default: all managed sessions"`
	Poll  float64  `json:"poll,omitempty" jsonschema:"seconds (default 0.5)"`
}
type policyIn struct {
	Name   string `json:"name"`
	Policy string `json:"policy,omitempty" jsonschema:"conservative (default) | permissive"`
}
type enforceIn struct {
	Name        string `json:"name"`
	Policy      string `json:"policy,omitempty" jsonschema:"conservative (default) | permissive"`
	ApproveKeys string `json:"approve_keys,omitempty" jsonschema:"default y<Enter>"`
	DenyKeys    string `json:"deny_keys,omitempty" jsonschema:"default n<Enter>"`
}

func registerCrew(s *mcp.Server, eng engine.Engine, allow func(string) bool) {
	// ---- mesh: Captain-Kirk orchestration primitives ------------------------

	addTool(s, allow, &mcp.Tool{Name: "mesh_send_with_done", Description: "Send a prompt, wait for done_marker, return only the reply between them. Tell the sub-agent to end with the marker. The reliable way to drive another LLM CLI in a session."},
		func(ctx context.Context, req *mcp.CallToolRequest, in sendDoneIn) (*mcp.CallToolResult, any, error) {
			out, err := mesh.SendWithDone(eng, in.Name, in.Text, in.DoneMarker, in.Timeout)
			if err != nil {
				return nil, nil, err
			}
			return textResult(out), nil, nil
		})

	addTool(s, allow, &mcp.Tool{Name: "mesh_snapshot_since", Description: "Screen text after the last occurrence of marker (full screen if absent). Cheap incremental read past a planted anchor."},
		func(ctx context.Context, req *mcp.CallToolRequest, in sinceIn) (*mcp.CallToolResult, any, error) {
			out, err := mesh.SnapshotSince(eng, in.Name, in.Marker)
			if err != nil {
				return nil, nil, err
			}
			return textResult(out), nil, nil
		})

	addTool(s, allow, &mcp.Tool{Name: "mesh_detect_blocked", Description: "Hint if the session looks stuck on a prompt (password, y/n, approval, 2FA...), else empty. Best-effort signal."},
		func(ctx context.Context, req *mcp.CallToolRequest, in nameIn) (*mcp.CallToolResult, any, error) {
			hint, err := mesh.DetectBlocked(eng, in.Name)
			if err != nil {
				return nil, nil, err
			}
			return textResult(hint), nil, nil
		})

	addTool(s, allow, &mcp.Tool{Name: "mesh_pipe", Description: "Inject one session's screen (or last N non-empty lines) into another's input, without returning it here — large artifacts move pane-to-pane free of orchestrator tokens. Newlines become Enter."},
		func(ctx context.Context, req *mcp.CallToolRequest, in pipeIn) (*mcp.CallToolResult, any, error) {
			if err := mesh.Pipe(eng, in.FromName, in.ToName, in.Lines); err != nil {
				return nil, nil, err
			}
			return textResult("ok"), nil, nil
		})

	addTool(s, allow, &mcp.Tool{Name: "mesh_subscribe_create", Description: "Watch a session for a literal substring; returns an id for mesh_subscribe_next/close. Each new screen position fires once."},
		func(ctx context.Context, req *mcp.CallToolRequest, in subCreateIn) (*mcp.CallToolResult, any, error) {
			sub, err := mesh.Subscribe(eng, in.Name, in.Pattern)
			if err != nil {
				return nil, nil, err
			}
			reg.mu.Lock()
			id := reg.id("sub")
			reg.subs[id] = sub
			reg.mu.Unlock()
			return textResult(id), nil, nil
		})

	addTool(s, allow, &mcp.Tool{Name: "mesh_subscribe_next", Description: "Block up to timeout for the subscription's next match; returns the snapshot, or empty on timeout. Repeatable."},
		func(ctx context.Context, req *mcp.CallToolRequest, in idTimeoutIn) (*mcp.CallToolResult, any, error) {
			reg.mu.Lock()
			sub := reg.subs[in.ID]
			reg.mu.Unlock()
			if sub == nil {
				return nil, nil, fmt.Errorf("unknown subscription id %q", in.ID)
			}
			to := in.Timeout
			if to <= 0 {
				to = 10
			}
			snap, _ := sub.Next(secs(to))
			return textResult(snap), nil, nil
		})

	addTool(s, allow, &mcp.Tool{Name: "mesh_subscribe_close", Description: "Close a subscription and free its watcher."},
		func(ctx context.Context, req *mcp.CallToolRequest, in idIn) (*mcp.CallToolResult, any, error) {
			reg.mu.Lock()
			sub := reg.subs[in.ID]
			delete(reg.subs, in.ID)
			reg.mu.Unlock()
			if sub == nil {
				return nil, nil, fmt.Errorf("unknown subscription id %q", in.ID)
			}
			sub.Close()
			return textResult("ok"), nil, nil
		})

	addTool(s, allow, &mcp.Tool{Name: "mesh_lifecycle_create", Description: "Open a stream of session lifecycle events (born, died, idle ~2s, busy); returns an id for mesh_lifecycle_next/close."},
		func(ctx context.Context, req *mcp.CallToolRequest, in emptyIn) (*mcp.CallToolResult, any, error) {
			stream := mesh.LifecycleEvents(eng, 0, 0)
			reg.mu.Lock()
			id := reg.id("life")
			reg.streams[id] = stream
			reg.mu.Unlock()
			return textResult(id), nil, nil
		})

	addTool(s, allow, &mcp.Tool{Name: "mesh_lifecycle_next", Description: "Block up to timeout for the next lifecycle event: {kind,name,timestamp}, or {} on timeout."},
		func(ctx context.Context, req *mcp.CallToolRequest, in idTimeoutIn) (*mcp.CallToolResult, any, error) {
			reg.mu.Lock()
			stream := reg.streams[in.ID]
			reg.mu.Unlock()
			if stream == nil {
				return nil, nil, fmt.Errorf("unknown stream id %q", in.ID)
			}
			to := in.Timeout
			if to <= 0 {
				to = 10
			}
			ev, ok := stream.Next(secs(to))
			if !ok {
				return textResult("{}"), nil, nil
			}
			return jsonResult(ev)
		})

	addTool(s, allow, &mcp.Tool{Name: "mesh_lifecycle_close", Description: "Close a lifecycle event stream."},
		func(ctx context.Context, req *mcp.CallToolRequest, in idIn) (*mcp.CallToolResult, any, error) {
			reg.mu.Lock()
			stream := reg.streams[in.ID]
			delete(reg.streams, in.ID)
			reg.mu.Unlock()
			if stream == nil {
				return nil, nil, fmt.Errorf("unknown stream id %q", in.ID)
			}
			stream.Close()
			return textResult("ok"), nil, nil
		})

	// ---- prime directive: policy actuator for blocked panes -----------------

	addTool(s, allow, &mcp.Tool{Name: "prime_directive_resolve", Description: "Preview a decision for a blocked pane without acting: none|approve|deny|escalate. Secrets always escalate."},
		func(ctx context.Context, req *mcp.CallToolRequest, in policyIn) (*mcp.CallToolResult, any, error) {
			policy, err := crew.PolicyByName(in.Policy)
			if err != nil {
				return nil, nil, err
			}
			decision, err := crew.Resolve(eng, in.Name, policy)
			if err != nil {
				return nil, nil, err
			}
			return textResult(decision), nil, nil
		})

	addTool(s, allow, &mcp.Tool{Name: "prime_directive_enforce", Description: "Resolve AND act: approve sends approve_keys, deny sends deny_keys, escalate/none do nothing. Never auto-answers a password/2FA prompt. Clears benign prompts so the fleet keeps moving."},
		func(ctx context.Context, req *mcp.CallToolRequest, in enforceIn) (*mcp.CallToolResult, any, error) {
			policy, err := crew.PolicyByName(in.Policy)
			if err != nil {
				return nil, nil, err
			}
			decision, err := crew.Enforce(eng, in.Name, policy, in.ApproveKeys, in.DenyKeys)
			if err != nil {
				return nil, nil, err
			}
			return textResult(decision), nil, nil
		})

	// ---- red alert: escalation to the human ---------------------------------

	addTool(s, allow, &mcp.Tool{Name: "red_alert_check", Description: "One-shot fleet probe: {kind,detail,names} if a human is needed (deadlock = all stalled on a prompt; death = dead pane), {} if fine."},
		func(ctx context.Context, req *mcp.CallToolRequest, in namesIn) (*mcp.CallToolResult, any, error) {
			alert := crew.Check(eng, in.Names)
			if alert == nil {
				return textResult("{}"), nil, nil
			}
			return jsonResult(alert)
		})

	addTool(s, allow, &mcp.Tool{Name: "red_alert_notify", Description: "Fire one notification to the human now (desktop toast where available, else stderr)."},
		func(ctx context.Context, req *mcp.CallToolRequest, in notifyIn) (*mcp.CallToolResult, any, error) {
			crew.Notify(in.Message, nil)
			return textResult("ok"), nil, nil
		})

	addTool(s, allow, &mcp.Tool{Name: "red_alert_notify_start", Description: "Start a background watcher that notifies the human on each NEW deadlock/death alert (deduped); returns an id for red_alert_notify_stop."},
		func(ctx context.Context, req *mcp.CallToolRequest, in watchIn) (*mcp.CallToolResult, any, error) {
			al := crew.Watch(eng, in.Names, nil, secs(in.Poll))
			reg.mu.Lock()
			id := reg.id("alert")
			reg.alerters[id] = al
			reg.mu.Unlock()
			return textResult(id), nil, nil
		})

	addTool(s, allow, &mcp.Tool{Name: "red_alert_notify_stop", Description: "Stop a background alert watcher."},
		func(ctx context.Context, req *mcp.CallToolRequest, in idIn) (*mcp.CallToolResult, any, error) {
			reg.mu.Lock()
			al := reg.alerters[in.ID]
			delete(reg.alerters, in.ID)
			reg.mu.Unlock()
			if al == nil {
				return nil, nil, fmt.Errorf("unknown alerter id %q", in.ID)
			}
			al.Stop()
			return textResult("ok"), nil, nil
		})

	// ---- bones: read-only health pathology ----------------------------------

	addTool(s, allow, &mcp.Tool{Name: "bones_examine", Description: "Read-only health check of one pane: {name,healthy,symptoms} — dead|errors|thrashing|hung. Never touches the pane."},
		func(ctx context.Context, req *mcp.CallToolRequest, in nameIn) (*mcp.CallToolResult, any, error) {
			return jsonResult(crew.Examine(eng, in.Name))
		})

	addTool(s, allow, &mcp.Tool{Name: "bones_triage", Description: "Read-only health check of the fleet, sickest first (default: all sessions). Never touches a pane."},
		func(ctx context.Context, req *mcp.CallToolRequest, in namesIn) (*mcp.CallToolResult, any, error) {
			return jsonResult(crew.Triage(eng, in.Names))
		})
}
