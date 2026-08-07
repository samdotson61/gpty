// Package crew holds the opt-in orchestration crew modules ported from
// upstream agent-pty's M7-M17: today the Phase-2 set — the fleet assessor
// (Spock's core, used internally by Red Alert), Bones (read-only health
// pathology), Red Alert (escalation to the human) and Prime Directive
// (policy-driven prompt actuator). Bones and the assessor hold a hard
// no-pane-mutation invariant: they only ever List and Snapshot.
package crew

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/samdotson61/gpty/internal/mesh"
)

// SettleInterval is the double-sample window for busy-vs-idle and hung
// detection (upstream spock/bones SETTLE_INTERVAL).
const SettleInterval = 150 * time.Millisecond

// PaneReport is one pane's coarse state in a fleet assessment.
// State: "dead" | "blocked" | "idle" | "busy" (strict precedence).
type PaneReport struct {
	Name   string `json:"name"`
	State  string `json:"state"`
	Hint   string `json:"hint,omitempty"`   // blocked-prompt hint when State=="blocked"
	Digest string `json:"digest,omitempty"` // last non-empty screen line, trimmed
}

// FleetReport is a deterministic, token-cheap assessment of the fleet.
// Deadlock is true iff >=1 pane is blocked AND none is busy: all forward
// progress is stalled on the captain or human.
type FleetReport struct {
	Panes    []PaneReport `json:"panes"`
	Deadlock bool         `json:"deadlock"`
	Summary  string       `json:"summary"`
}

var statePriority = []string{"blocked", "dead", "idle", "busy"}

func lastNonEmpty(snap string) string {
	lines := strings.Split(snap, "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		if s := strings.TrimSpace(lines[i]); s != "" {
			return s
		}
	}
	return ""
}

// Assess observes the fleet and returns a structured report. names==nil
// covers all managed sessions; explicit names restrict scope (an unmanaged
// name reports as "dead"). One shared settle window is used across the whole
// fleet, so cost stays O(1) in sleeps. Read-only: never sends a keystroke.
func Assess(t mesh.Term, names []string) FleetReport {
	managedList, _ := t.List()
	managed := map[string]bool{}
	for _, n := range managedList {
		managed[n] = true
	}
	targets := names
	if targets == nil {
		targets = managedList
	}

	firsts := map[string]*string{}
	for _, n := range targets {
		if !managed[n] {
			firsts[n] = nil
			continue
		}
		if snap, err := t.Snapshot(n); err == nil {
			s := snap
			firsts[n] = &s
		} else {
			firsts[n] = nil
		}
	}
	time.Sleep(SettleInterval)

	panes := make([]PaneReport, 0, len(targets))
	for _, n := range targets {
		panes = append(panes, classify(t, n, firsts[n]))
	}
	counts := map[string]int{}
	for _, p := range panes {
		counts[p.State]++
	}
	deadlock := counts["blocked"] >= 1 && counts["busy"] == 0
	return FleetReport{panes, deadlock, summarize(panes, counts, deadlock)}
}

// classify decides (state, hint, digest) given the first snapshot; the caller
// has already slept the settle window, the second sample is taken here. A
// session that dies inside the window resolves to "dead".
func classify(t mesh.Term, name string, first *string) PaneReport {
	if first == nil {
		return PaneReport{Name: name, State: "dead"}
	}
	if hint := mesh.DetectBlockedIn(*first); hint != "" {
		return PaneReport{Name: name, State: "blocked", Hint: hint, Digest: lastNonEmpty(*first)}
	}
	second, err := t.Snapshot(name)
	if err != nil {
		return PaneReport{Name: name, State: "dead"}
	}
	state := "idle"
	if second != *first {
		state = "busy"
	}
	return PaneReport{Name: name, State: state, Digest: lastNonEmpty(second)}
}

func summarize(panes []PaneReport, counts map[string]int, deadlock bool) string {
	n := len(panes)
	if deadlock {
		for _, p := range panes {
			if p.State == "blocked" {
				return fmt.Sprintf("DEADLOCK — %s blocked on %s, nothing progressing (%d panes).", p.Name, p.Hint, n)
			}
		}
	}
	var parts []string
	for _, s := range statePriority {
		if counts[s] > 0 {
			parts = append(parts, fmt.Sprintf("%d %s", counts[s], s))
		}
	}
	body := "nothing"
	if len(parts) > 0 {
		body = strings.Join(parts, ", ")
	}
	return fmt.Sprintf("%d panes: %s.", n, body)
}

// sortDiagnoses orders sickest-first: dead always worst, then by descending
// symptom count, ties by name for determinism. Shared by Triage.
func sortDiagnoses(ds []Diagnosis) {
	dead := func(d Diagnosis) int {
		for _, s := range d.Symptoms {
			if s == "dead" {
				return 1
			}
		}
		return 0
	}
	sort.SliceStable(ds, func(i, j int) bool {
		if a, b := dead(ds[i]), dead(ds[j]); a != b {
			return a > b
		}
		if len(ds[i].Symptoms) != len(ds[j].Symptoms) {
			return len(ds[i].Symptoms) > len(ds[j].Symptoms)
		}
		return ds[i].Name < ds[j].Name
	})
}
