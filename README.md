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

Requirements: Go (to build), and tmux ≥ 3.2 (the installer handles it; `gpty
doctor` checks it).

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

**As a human:**

```sh
gmux new -t work        # new session (PowerShell-7 pane on Windows), attached
gmux ls
gmux attach -t work
gmux kill -t work
```

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

**v0.1.0 — Unix-verified, Windows port included.** Built and tested live against
real tmux on macOS (unit + conformance + benchmarks green; control mode end to
end via MCP). The Windows path is fully implemented (it's the merge of the
shipping winmux/win-pty code) and exercised in CI via MSYS2, but has not yet been
re-validated by hand on a Windows box. See [CHANGELOG.md](CHANGELOG.md) for
what's done and what's pending.

## Credit

The agent-pty concept and API originate with
[AakeshF](https://github.com/AakeshF/agent-pty). gpty re-implements that idea
natively in Go over tmux, with the Windows port and control-mode engine by Sam
Dotson. MIT licensed; see [LICENSE](LICENSE) and [NOTICE](NOTICE).
