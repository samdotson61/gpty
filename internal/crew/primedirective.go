package crew

import (
	"fmt"
	"strings"

	"github.com/samdotson61/gpty/internal/mesh"
)

// Prime Directive: policy / auto-approval actuator for blocked panes. Given
// a blocked pane it consults a Policy and either auto-approves, auto-denies,
// or escalates to the human. This is an ACTUATOR: "approve"/"deny" SEND
// keystrokes into the pane. Use deliberately.
//
// Security stance (hard-coded, non-negotiable): a prompt that looks like it
// wants a secret (password / passphrase / 2fa / verification / secret) is
// ALWAYS escalated, even under a permissive policy. Prime Directive never
// auto-answers a secrets prompt. Decisions ride on mesh.DetectBlocked's
// best-effort hint — a convenience-and-safety net, not an oracle.

// secretMarkers force an escalate regardless of policy when present in the
// blocked-hint (case-insensitive).
var secretMarkers = []string{"password", "passphrase", "2fa", "verification", "secret"}

var validDecisions = map[string]bool{"approve": true, "deny": true, "escalate": true}

// Rule maps a case-insensitive substring of the blocked-hint to a decision.
type Rule struct {
	Substring string
	Decision  string // "approve" | "deny" | "escalate"
}

// Policy decides what to do with a blocked-hint. Rules are checked in order,
// first match wins; Default applies when none match. Secrets are checked
// before any rule and cannot be approved or denied.
type Policy struct {
	Rules   []Rule
	Default string
}

// Validate fails loudly on a decision the actuator can't act on (a typo'd
// "aprove" would otherwise be silently ignored by Enforce).
func (p Policy) Validate() error {
	for _, r := range p.Rules {
		if !validDecisions[r.Decision] {
			return fmt.Errorf("invalid decision %q; expected approve|deny|escalate", r.Decision)
		}
	}
	if !validDecisions[p.Default] {
		return fmt.Errorf("invalid decision %q; expected approve|deny|escalate", p.Default)
	}
	return nil
}

// Conservative escalates EVERYTHING. No rules. The safe baseline.
func Conservative() Policy { return Policy{Default: "escalate"} }

// Permissive auto-approves ordinary y/n / continue / approval prompts,
// still escalating anything unmatched — and always escalating secrets.
func Permissive() Policy {
	return Policy{
		Rules: []Rule{
			{"y/n", "approve"},
			{"continue", "approve"},
			{"approval", "approve"},
		},
		Default: "escalate",
	}
}

// PolicyByName maps the MCP-facing policy names to policies.
func PolicyByName(name string) (Policy, error) {
	switch name {
	case "", "conservative":
		return Conservative(), nil
	case "permissive":
		return Permissive(), nil
	}
	return Policy{}, fmt.Errorf("unknown policy %q: want conservative|permissive", name)
}

func isSecret(hint string) bool {
	low := strings.ToLower(hint)
	for _, m := range secretMarkers {
		if strings.Contains(low, m) {
			return true
		}
	}
	return false
}

// Resolve decides what to do about a blocked pane WITHOUT acting.
// "none" -> not blocked; "escalate" -> defer to the human (also: secrets,
// always); "approve"/"deny" -> a policy rule matched.
func Resolve(t mesh.Term, name string, policy Policy) (string, error) {
	if err := policy.Validate(); err != nil {
		return "", err
	}
	hint, err := mesh.DetectBlocked(t, name)
	if err != nil {
		return "", err
	}
	if hint == "" {
		return "none", nil
	}
	if isSecret(hint) {
		return "escalate", nil
	}
	low := strings.ToLower(hint)
	for _, r := range policy.Rules {
		if strings.Contains(low, strings.ToLower(r.Substring)) {
			return r.Decision, nil
		}
	}
	return policy.Default, nil
}

// Enforce resolves a decision and ACTS on it: "approve" sends approveKeys,
// "deny" sends denyKeys, "escalate"/"none" do nothing. Keys go through the
// core Send, which DOES parse named-key tokens like <Enter> — intended, so
// the answer is actually submitted.
func Enforce(t mesh.Term, name string, policy Policy, approveKeys, denyKeys string) (string, error) {
	if approveKeys == "" {
		approveKeys = "y<Enter>"
	}
	if denyKeys == "" {
		denyKeys = "n<Enter>"
	}
	decision, err := Resolve(t, name, policy)
	if err != nil {
		return "", err
	}
	switch decision {
	case "approve":
		err = t.Send(name, approveKeys)
	case "deny":
		err = t.Send(name, denyKeys)
	}
	if err != nil {
		return "", err
	}
	return decision, nil
}
