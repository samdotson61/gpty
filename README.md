# gpty

**One Go toolset that gives LLM agents — local and cloud — and humans a shared,
persistent terminal world over a real C `tmux`, on Windows (no WSL), macOS, and
Linux.**

gpty is the successor to [winmux](https://github.com/samdotson61/winmux) +
[win-pty](https://github.com/samdotson61/win-pty), merged into one module that
ships two commands:

- **`gpty`** — the agent surface (a Go rewrite of [agent-pty](https://github.com/AakeshF/agent-pty)):
  spawn / drive / snapshot / wait on name-addressable terminal sessions and
  panes, exposed three ways: **CLI**, **MCP over stdio** (local agents), and
  **MCP over HTTP** (cloud agents).
- **`gmux`** — the human surface (supersedes `wmux`): create / attach / list /
  kill the *same* sessions, with native interactive attach in PowerShell /
  Windows Terminal.

The agent and the human see the same tmux server: an agent can split a pane the
human is watching live, and a human can `gmux attach` to a session an agent is
driving.

> **tmux stays C.** gpty does **not** rewrite tmux (too mature, too fast) or
> replace cygwin — it orchestrates the real thing and routes around the cygwin
> per-call cost with a resident control-mode channel. See
> [docs/build-plan.md](docs/build-plan.md) for the full design and rationale.

## Why it's fast

Two paths, by design:

1. **One-shot CLI calls** exec a short-lived tmux client (~native latency; the
   Python→Go win is deleting interpreter startup).
2. **The MCP server holds one persistent `tmux -C` control-mode client.** Every
   command becomes a line on an already-open pipe — no process spawn. On Windows
   this skips the cygwin exec tax *per call*.

Measured on this dev Mac (tmux 3.6a), resident channel vs exec one-shot:

| Operation | exec one-shot | resident (control mode) | speedup |
|---|--:|--:|--:|
| snapshot | ~2.7 ms | **~27 µs** | ~100× |
| send | ~2.7 ms | **~27 µs** | ~100× |

(`go test -tags live -bench . ./conformance/` reproduces these.)

## Install

**macOS / Linux:**

```sh
./install.sh        # builds gpty + gmux, installs them + the tmux.conf
gpty doctor         # verify tmux >= 3.2
```

**Windows (no WSL):**

```powershell
./install.ps1       # or double-click install.cmd
```

Installs MSYS2 + tmux, ensures Go, builds `gpty.exe` + `gmux.exe`, installs the
tmux.conf (PowerShell-7 default panes), and puts them on PATH.

Requirements: Go ≥ 1.21 (to build) and tmux ≥ 3.2 (runtime). If either is
missing, the installers **offer to install it** via your package manager —
winget on Windows; brew on macOS; apt/dnf/pacman/zypper/apk on Linux. Pass
`--yes` (or set `GPTY_YES=1`) to `install.sh`, or `-Yes` to `install.ps1`, to
accept all offers non-interactively. `gpty doctor` verifies the result.

## Use it

**As a local agent (Claude Code / Desktop), over MCP stdio:**

```sh
claude mcp add gpty -- gpty mcp
```

Tools (names preserved from win-pty, so existing setups upgrade by changing only
the command): `pty_spawn`, `pty_send`, `pty_snapshot`, `pty_wait_for`,
`pty_list`, `pty_kill`, and the pane suite `pane_split`, `pane_send`,
`pane_capture`, `pane_list`, `pane_info`, `pane_kill`, `pane_select`,
`pane_resize`, `pane_layout`.

Tool definitions are kept terse — they sit in the agent's context window for
the whole session (~1,130 tokens for all 15). To spend less, expose only what
the agent needs with `--tools`:

```sh
claude mcp add gpty -- gpty mcp --tools session   # pty_* only  (~430 tokens)
claude mcp add gpty -- gpty mcp --tools panes     # pane_* only (~710 tokens)
gpty mcp --tools pty_send,pty_snapshot,pane_split # or an exact list
```

**As a cloud / remote agent, over MCP HTTP:**

```sh
gpty serve --addr 127.0.0.1:7683          # loopback only (no token needed)
GPTY_TOKEN=secret gpty serve --addr 0.0.0.0:7683   # any other bind REQUIRES a token
```

Then tunnel in (`ssh -R`, Tailscale, cloudflared). gpty refuses to bind a
non-loopback address without a token.

**From the shell (or a Bash-tool agent):**

```sh
gpty spawn build                        # create session "build"
gpty send build "make -j8<Enter>"       # keys: <Enter> <C-c> <Up> ...; << = literal <
gpty wait-for build "Error|\$"          # block until a pattern appears
gpty snapshot build                     # rendered screen as plain text
gpty list
gpty kill build
```

**Any tmux command works too.** A subcommand gpty doesn't recognize is checked
against the installed tmux's command table and, if real, forwarded as-is —
every tmux command and alias is a gpty alias:

```sh
gpty list-windows -t agent-pty-build    # any tmux command...
gpty splitw -h                          # ...or tmux alias
gpty send-keys -t agent-pty-build ls Enter
gpty kill-server
```

gpty's own names win on collision (`send`, `ls`, `wait`); use the full tmux
name there (`send-keys`, `list-sessions`). Typos still get gpty's
unknown-command error, not a confusing tmux one.

**As a human:**

```sh
gmux new -t work        # new session (PowerShell-7 pane on Windows), attached
gmux ls
gmux attach -t work
gmux kill -t work
```

**Prefer typing `tmux`?** The installers also register gmux under the name
`tmux` (one binary, dispatched on its invoked name) — by default on Windows,
where there is no native tmux on PATH to shadow, and opt-in on macOS/Linux
(`./install.sh --tmux-alias`):

```sh
tmux new -t sesh        # = gmux new -t sesh (opens a gmux session "sesh")
tmux ls                 # = gmux ls
tmux                    # bare: new attached session, like the real thing
tmux -V                 # anything gmux doesn't define passes through
tmux list-windows       #   to the REAL tmux, unchanged
```

gpty and gmux resolve the *real* tmux around this alias (a PATH hit in their
own install dir is skipped), so the passthrough cannot recurse into itself.

## How it fits together

```
local agents          cloud agents          humans
(stdio MCP / CLI)     (HTTP MCP + token)    (interactive)
      \                    |                    /
       \                   |                   /
        gpty  ────────── shared core ──────  gmux
              (exec one-shots │ resident tmux -C control client)
                          │
                     tmux (C)   — MSYS2 on Windows · system tmux on Unix
```

Sessions live under the `agent-pty-<name>` prefix on the default tmux socket, so
`tmux attach -t agent-pty-<name>` still works.

## Status

**v0.2.0 — Unix-verified (local + CI), Windows exec-verified in CI.** Linux and
macOS run the full live conformance suite green in CI; control mode is verified
end to end on macOS (unit + conformance + benchmarks + MCP drive). On Windows,
the exec engine (sessions, panes, literal-send) passes the live CI suite via
MSYS2 tmux; the control-mode channel is pending validation on real Windows
hardware — until then gpty automatically falls back to the exec engine there.
See [CHANGELOG.md](CHANGELOG.md) for details.

## Credit

The agent-pty concept and API originate with
[AakeshF](https://github.com/AakeshF/agent-pty). gpty re-implements that idea
natively in Go over tmux, with the Windows port and control-mode engine by Sam
Dotson. MIT licensed; see [LICENSE](LICENSE) and [NOTICE](NOTICE).
