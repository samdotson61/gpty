# Orchestration: mesh + crew

gpty 0.8.0 ports the orchestration layers from upstream
[agent-pty](https://github.com/AakeshF/agent-pty) (mesh M6, plus the first
three crew modules of M7-M17) to Go. The pattern they serve: **one agent
driving N interactive sub-agents in other sessions** — spawn a `claude` (or
any REPL/CLI) per session, hand each a task, and coordinate without burning
the orchestrator's context on raw screen dumps.

Everything here is opt-in. The core `pty_*`/`pane_*` surface is unchanged;
pin it with `--tools core` if you want none of this. All modules are
engine-agnostic and ride the control-mode accelerator when it's resident.

## Mesh — the primitives (`--tools mesh`)

| Tool | What it does |
|---|---|
| `mesh_send_with_done` | Send a prompt, wait for a sentinel (`<<END>>` by default), return only the reply between prompt and sentinel. The reliable way to drive another LLM CLI: tell it to end its reply with the marker. |
| `mesh_snapshot_since` | Screen text after the last occurrence of a marker — cheap incremental reads past a planted anchor. |
| `mesh_detect_blocked` | Heuristic hint when a session sits on a password / y/n / approval / 2FA / any-key prompt. Empty string = not blocked. |
| `mesh_pipe` | Inject one session's screen (or last N non-empty lines) into another's input. The payload never returns to the orchestrator, so big diffs/logs move for free. |
| `mesh_subscribe_create/next/close` | Push-style watch for a literal substring; each new screen position fires once. |
| `mesh_lifecycle_create/next/close` | Event stream over managed sessions: `born`, `died`, `idle` (~2s unchanged), `busy` (idle session changed again). |

Reply extraction in `mesh_send_with_done` is hardened over upstream: because
the prompt usually *contains* the marker, completion requires a marker
occurrence **after** the echoed prompt line — the echo alone can't satisfy
the wait.

## Crew — Phase 2 modules (`--tools crew` includes mesh)

**Prime Directive** (`prime_directive_resolve`/`_enforce`) — the decision
layer over `mesh_detect_blocked`. `resolve` previews a decision
(none/approve/deny/escalate); `enforce` acts on it, sending `y<Enter>` /
`n<Enter>` (configurable). Policies: `conservative` (escalate everything —
default) and `permissive` (auto-approve ordinary y/n / continue / approval
prompts). **A secrets prompt always escalates, under every policy.**

**Red Alert** (`red_alert_check`, `red_alert_notify`,
`red_alert_notify_start`/`_stop`) — escalation to the human. `check` is a
one-shot probe returning `{kind, detail, names}` for a `deadlock` (≥1 pane
blocked and nothing busy — the whole fleet stalled on you) or `death` (dead
pane), `{}` when fine. `notify_start` watches in the background and fires a
notification (desktop toast where `notify-send` exists, else stderr) on each
NEW alert, deduped until the fleet recovers. Read-only on panes.

**Bones** (`bones_examine`, `bones_triage`) — read-only health pathology for
a still-running pane: `errors` (error signature on screen), `thrashing` (one
line repeated >8 times), `hung` (unchanged across a settle window and not at
a shell prompt), `dead`. `triage` covers the fleet, sickest first. Never
sends a keystroke.

All detectors are best-effort screen heuristics — signals, not guarantees.

## Not yet ported (upstream M8-M17 remainder)

Phase 3 candidates: Scotty (crash recovery), Sulu (backlog dispatch), Uhura
(structured messaging), Captain's Log (transcript recorder). Phase 4
judgment calls: Holodeck (worktree sandboxes), Transporter (context
checkpoints), Spock (fleet analyst — its assessment core is already in
`internal/crew` as `Assess`, feeding Red Alert), Worf (adversarial reviewer).

## Testing

Unit suites (fake terminal, no tmux needed): `go test ./internal/mesh
./internal/crew`. Live smoke against a real tmux server — safe on a box with
live sessions, it never calls `kill-server`:

```sh
go test -tags live -run Live ./internal/mesh
```
