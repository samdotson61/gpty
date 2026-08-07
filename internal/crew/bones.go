package crew

import (
	"regexp"
	"strings"
	"time"

	"github.com/samdotson61/gpty/internal/mesh"
)

// Bones: ship's doctor — read-only health pathology over a still-running
// pane. Where the fleet assessor reports a coarse *state* (dead/blocked/
// idle/busy), Bones diagnoses *sickness*: errors on screen, a thrashing
// loop, a hung mid-task pane. Never sends a keystroke, never mutates a pane.
// All detectors are best-effort screen heuristics: they read the rendered
// screen, not process state — a signal, not a guarantee.

// ThrashRepeats: a non-empty visible line repeated more than this many times
// reads as thrashing.
const ThrashRepeats = 8

// promptEndings are bottom-line endings that look like a ready prompt
// (=> not hung, just waiting).
var promptEndings = []string{"$", "#", ">>>", ">"}

var errorPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)\btraceback\b`),
	regexp.MustCompile(`(?i)\bfatal\b`),
	regexp.MustCompile(`(?i)\bpanic\b`),
	regexp.MustCompile(`(?i)segmentation fault`),
	regexp.MustCompile(`(?i)error:`),
	regexp.MustCompile(`(?i)\bexception\b`),
	regexp.MustCompile(`(?i)command not found`),
}

// Diagnosis is one pane's health verdict. Healthy == no symptoms detected.
// Symptom order is stable: dead | errors, thrashing, hung.
type Diagnosis struct {
	Name     string   `json:"name"`
	Healthy  bool     `json:"healthy"`
	Symptoms []string `json:"symptoms"`
}

func hasErrors(snap string) bool {
	for _, p := range errorPatterns {
		if p.MatchString(snap) {
			return true
		}
	}
	return false
}

func isThrashing(snap string) bool {
	counts := map[string]int{}
	for _, line := range strings.Split(snap, "\n") {
		s := strings.TrimSpace(line)
		if s == "" {
			continue
		}
		counts[s]++
		if counts[s] > ThrashRepeats {
			return true
		}
	}
	return false
}

func looksLikePrompt(snap string) bool {
	last := lastNonEmpty(snap)
	if last == "" {
		return false
	}
	for _, end := range promptEndings {
		if strings.HasSuffix(last, end) {
			return true
		}
	}
	return false
}

// isHung: unchanged across the settle window AND not sitting at a ready
// prompt — "stuck mid-task", not "done and waiting". A pane that merely
// paused during the window can false-positive.
func isHung(first, second string) bool {
	return first == second && !looksLikePrompt(second)
}

// Examine diagnoses a single pane, read-only. Symptoms: "dead" (gone),
// "errors" (error signature on screen), "thrashing" (one line repeated >
// ThrashRepeats times), "hung" (unchanged across the settle window, not at
// a prompt).
func Examine(t mesh.Term, name string) Diagnosis {
	managed, _ := t.List()
	found := false
	for _, n := range managed {
		if n == name {
			found = true
			break
		}
	}
	if !found {
		return Diagnosis{name, false, []string{"dead"}}
	}
	first, err := t.Snapshot(name)
	if err != nil {
		return Diagnosis{name, false, []string{"dead"}}
	}

	var symptoms []string
	if hasErrors(first) {
		symptoms = append(symptoms, "errors")
	}
	if isThrashing(first) {
		symptoms = append(symptoms, "thrashing")
	}

	time.Sleep(SettleInterval)
	second, err := t.Snapshot(name)
	if err != nil {
		// Died inside the settle window — the worst symptom wins outright.
		return Diagnosis{name, false, []string{"dead"}}
	}
	if isHung(first, second) {
		symptoms = append(symptoms, "hung")
	}
	if symptoms == nil {
		symptoms = []string{}
	}
	return Diagnosis{name, len(symptoms) == 0, symptoms}
}

// Triage examines panes and returns them sickest-first. names==nil covers
// all managed sessions; an unmanaged name diagnoses as "dead". Sorted dead
// worst, then descending symptom count, ties by name.
func Triage(t mesh.Term, names []string) []Diagnosis {
	targets := names
	if targets == nil {
		targets, _ = t.List()
	}
	ds := make([]Diagnosis, 0, len(targets))
	for _, n := range targets {
		ds = append(ds, Examine(t, n))
	}
	sortDiagnoses(ds)
	return ds
}
