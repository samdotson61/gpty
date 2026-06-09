# gpty — Build Plan

> **Status:** approved for implementation · **Plan date:** 2026-06-09 · **Owner:** Sam Dotson
> Successor product merging [winmux](https://github.com/samdotson61/winmux) + [win-pty](https://github.com/samdotson61/win-pty) into one cross-platform Go toolset over C tmux.

---

## 1. Product definition

**gpty** is a single Go module shipping two commands that give LLM agents (local and cloud) and humans a shared, persistent terminal world on Windows, macOS, and Linux:

- **`gpty`** — the agent surface (rewrite of agent-pty): spawn / drive / snapshot / wait-on terminal sessions and panes, exposed three ways: CLI, MCP over stdio, MCP over HTTP.
- **`gmux`** — the human surface (supersedes wmux): create / attach / list / kill the same tmux sessions, with native interactive attach in PowerShell / Windows Terminal.

Both drive **real C tmux**: MSYS2's native `tmux.exe` on Windows, the system tmux on macOS/Linux. The agent and the human see the *same* sessions — an agent can split a pane the human is watching live, and a human can attach to a session an agent is driving.

**Goals**
1. Function nearly as fast as native tmux (latency budget in §6).
2. Natively callable by local agents (Claude Code et al., stdio MCP + plain CLI) and cloud agents (streamable HTTP MCP).
3. Open and manipulate tmux windows running PowerShell 7 on Windows; user shell on Unix.
4. Single static binaries, zero runtime dependencies beyond tmux itself.
5. Drop-in upgrade from win-pty/winmux (compat locks in §4).

**Non-goals (explicit, decided 2026-06-09)**
- ❌ No tmux rewrite — tmux (C, ~65k lines) is too mature and too fast to replace. We orchestrate it.
- ❌ No cygwin/MSYS2 replacement — its costs are architectural (fork emulation, process-spawn floor); we route around them via control mode (§5) instead of paying per-call.
- ❌ No full tmux-feature parity in our surface — only the command subset agents and the human flow need.
- ❌ No Python. The `agent_pty` package does not carry over (lineage credited in NOTICE).

**Naming:** repo **`gpty`** (github.com/samdotson61/gpty). The name "go-pty" is taken by aymanbagabas/go-pty (a PTY library in the same problem space) — avoid.

---

## 2. Architecture

```
  local agents              cloud agents            humans
  (Claude Code: stdio MCP   (streamable HTTP MCP    (interactive
   or Bash-tool CLI)         + bearer token)         terminal)
        │                        │                      │
        ▼                        ▼                      ▼
   ┌─────────────────── gpty ───────────────────┐   ┌─ gmux ─┐
   │  CLI one-shots        resident server      │   │ new    │
   │  (exec per call)      (gpty mcp / serve)   │   │ attach │
   │       │                    │               │   │ ls/kill│
   │       │             persistent `tmux -C`   │   └────┬───┘
   │       │             control-mode client    │        │
   └───────┼────────────────────┼───────────────┘        │
           ▼                    ▼                         ▼
        ───────────────── tmux (C) ──────────────────────────
        Windows: MSYS2 tmux.exe (cygwin runtime, conditional env)
        macOS/Linux: tmux from PATH
```

### The two latency decisions

1. **One-shot CLI calls are exec-based.** Native `tmux send-keys` is itself a client exec (~3–6 ms Unix). Go binary startup is ~3–5 ms, so `gpty send` ≈ 10 ms on Unix — effectively native. (This is the Python→Go win: the old agent-pty CLI paid 80–150 ms of interpreter startup per call.)
2. **Resident server modes hold one persistent `tmux -C` (control-mode) client.** Every command becomes a line on an already-open connection — zero process spawns on the hot path. On Windows this skips the ~40–80 ms cygwin exec tax *per call*, and `wait_for` becomes event-driven (`%output` push) instead of polled. Control mode needs **no pty**, so the cygwin pseudo-console (pcon) dance exists only inside `gmux attach`.

### Access planes (all three are first-class)

| Plane | Consumer | Mechanism |
|---|---|---|
| CLI | Bash-tool agents, humans, scripts | `gpty <cmd>` one-shots, `--json` output |
| MCP stdio | Local agents (Claude Code/Desktop) | `gpty mcp` — official `modelcontextprotocol/go-sdk` |
| MCP HTTP | Cloud/remote agents | `gpty serve` — streamable HTTP, bearer token, loopback default |

### Platform layer (the only OS-specific code)

- **Windows** (`internal/platform/windows.go`): tmux at `%MSYS2_ROOT%\usr\bin\tmux.exe` (default `C:\msys64`); env built fresh — strip inherited `MSYS`, set UTF-8 `LANG`, always `tmux -u`; `MSYS=noglob` **only** for non-interactive/piped commands (noglob disables cygwin's pseudo-console, which interactive attach requires — this is wmux's hard-won conditional; port its comments verbatim). Default pane command: PowerShell 7 (`pwsh`), via shipped `conf/tmux.conf` + `conf/pane-init.ps1`.
- **Unix** (`internal/platform/unix.go`): `exec.LookPath("tmux")`, plain environment, user's default shell. Small by design.

---

## 3. Repo layout

```
gpty/
├── cmd/
│   ├── gpty/main.go          # agent CLI + mcp/serve subcommands
│   └── gmux/main.go          # human CLI: new/attach/ls/kill
├── internal/
│   ├── platform/             # tmux discovery + env (windows.go, unix.go)
│   ├── tmux/                 # exec client (one-shot path)
│   ├── ctl/                  # control-mode client (resident path)   [Phase 4]
│   ├── keys/                 # <Enter>/<C-x>/<F1> key-name grammar + tests
│   ├── session/              # naming (agent-pty-<name>), spawn/kill/list/snapshot
│   ├── panes/                # split/select/resize/layout/info
│   └── mcpserv/              # shared tool defs; stdio + HTTP transports
├── conformance/              # live tests against real tmux (all OSes)  [build tag: live]
├── conf/                     # tmux.conf, pane-init.ps1
├── docs/                     # this vault (build-plan.md, then 01–0N as it grows)
├── install.ps1 / install.cmd / install.sh
├── .github/workflows/ci.yml  # 3-OS matrix (see §7)
├── README.md · CHANGELOG.md · LICENSE (MIT) · NOTICE (AakeshF lineage)
└── go.mod                    # module github.com/samdotson61/gpty
```

Dependencies: `modelcontextprotocol/go-sdk` (already proven in win-pty), `golang.org/x/sys` if needed. Otherwise stdlib. No PTY library required — tmux owns the ptys; `gmux attach` inherits stdio exactly as wmux does today.

---

## 4. Compatibility locks (drop-in upgrade guarantees)

These are frozen so existing setups, docs, and muscle memory keep working:

1. **MCP tool names** (unchanged from win-pty): `pty_spawn`, `pty_send`, `pty_snapshot`, `pty_wait_for`, `pty_list`, `pty_kill`, `pane_split`, `pane_list`, `pane_send`, `pane_capture`, `pane_kill`, `pane_select`, `pane_resize`, `pane_layout`, `pane_info`. Server name changes `win-pty` → `gpty`; tool schemas stay source-compatible.
2. **Session prefix**: `agent-pty-<name>` on the default tmux socket. `tmux attach -t agent-pty-demo` keeps working.
3. **Key grammar**: literal text + `<Enter>`, `<Esc>`, `<Tab>`, `<BS>`, arrows, `<Home>/<End>/<PgUp>/<PgDn>/<Del>`, `<F1>`–`<F12>`, `<C-x>/<S-x>/<M-x>`, `<<` → literal `<`. Long literal text goes via `load-buffer`/`paste-buffer` (win-pty's quoting-safe trick — keep it).
4. **CLI verbs**: `gpty` keeps win-pty's surface (`spawn send snapshot wait-for list kill split panes pane-*`) and adds `mcp serve doctor setup version`; `gmux` keeps wmux's (`new attach ls kill` + `version`).

## 5. tmux contract (the seam)

The exact tmux surface we consume — conformance tests (§7) pin each item:

- `new-session -d -s <n> -x <cols> -y <rows> [<cmd>]` · `has-session -t` · `kill-session -t` · `list-sessions -F`
- `send-keys -t` (named keys) · `load-buffer -b` / `paste-buffer -b` (literal text)
- `capture-pane -p -t` (snapshot; plain text, no escapes)
- `split-window`, `select-pane`, `resize-pane`, `select-layout`, `list-panes -F`, `kill-pane`, `display-message -p` (pane suite)
- `attach-session -t` (gmux, interactive)
- **Control mode** (resident path): `tmux -u -C new-session -A -s <ctl>`; parse `%begin/%end/%error` reply blocks (correlated by command), consume `%output`, `%pause/%continue`, `%session-changed`, `%window-add`, `%exit` events; octal-escape decoding for `%output` payloads.

**Minimum tmux: 3.2** (stable control mode + flow control). `gpty doctor` enforces via `tmux -V`; MSYS2 pacman and brew both ship newer.

---

## 6. Performance budget (benchmark gates, enforced in Phase 4)

| Operation | Native tmux (Unix) | gpty one-shot Unix | gpty one-shot Windows | gpty resident (any OS) |
|---|---|---|---|---|
| send / snapshot | ~3–6 ms | **≤ 15 ms p50** | ~40–80 ms (cygwin exec floor — documented, not fought) | **≤ 5 ms p50** |
| wait_for reaction to output | n/a | poll interval (200 ms default) | poll interval | **≤ 10 ms (event push)** |
| spawn (new session/pane) | ~10 ms | ~20 ms | ~100–200 ms (fork tax, paid once per pane) | same as one-shot |

`go test -bench` in `conformance/` produces these numbers in CI; Phase 4 does not exit until the resident column passes on a real Windows machine (not just CI).

---

## 7. Quality gates & process discipline

- **Tests in Go, ported from win-pty's Python suite** (`test_keys.py`, `test_session.py`, `test_wait.py`, `test_mesh.py` → Go). The shipped code is finally the tested code.
- **Conformance suite, run live** (per the live-testing lesson: fixtures lie): `conformance/` runs the §5 contract against real tmux. CI matrix: `ubuntu-latest`, `macos-latest`, `windows-latest` + `msys2/setup-msys2` action installing tmux. Every PR runs all three.
- **Semver lockstep on every change** (patch=fix, minor=feature, major=breaking) across `CHANGELOG.md`, the `-ldflags`-injected version in both binaries, and this docs vault. One cohesive feature = one minor. v0.1.0 at Phase 1 exit; v1.0.0 at Phase 6.
- **Docs in lockstep**: README.md and docs/ updated in the same commit as behavior changes.
- **Releases**: goreleaser — `windows/{amd64,arm64}`, `darwin/{arm64,amd64}`, `linux/{amd64,arm64}`.

---

## 8. Phases

### Phase 0 — Scaffold (½ day)
Repo `gpty` (MIT + NOTICE), Go module, `cmd/gpty` + `cmd/gmux` hello-world, CI matrix green on all 3 OSes, this doc moved in.
**Exit:** `go build ./...` and a trivial conformance test pass on ubuntu/macos/windows CI.

### Phase 1 — Core merge (3 days)
Port win-pty `go/` → `internal/{tmux,keys,session,panes}` + `cmd/gpty`; port wmux → `cmd/gmux`; **unify** win-pty's `tmuxBin/tmuxEnv` with wmux's `buildEnv` into `internal/platform` (they have already drifted apart across the two repos — this merge is the fix). Port the Python test suite to Go.
**Exit:** full ported test suite + §5 conformance green on 3-OS CI; `gmux new/attach/ls/kill` verified by hand on the Windows box. Tag **v0.1.0**.

### Phase 2 — Cross-platform native (2½ days)
Unix platform layer; `install.sh` (brew/apt-aware); `gpty doctor` (tmux present? ≥3.2? MSYS2 sane? prints exact fixes); `gpty setup` on Windows absorbing winmux's installer (MSYS2 via winget, `pacman -S tmux`, conf install, PATH) leaving `install.ps1` a thin bootstrap. PowerShell-7 default pane on Windows.
**Exit:** fresh-machine install is one command on each OS. (Side effect: gpty replaces the Python agent-pty venv on the Mac.) Tag v0.2.0.

### Phase 3 — Local agents / MCP stdio (1½ days)
Carry the existing Go MCP server over under the gpty name (tool names per §4); `claude mcp add gpty -- gpty mcp` documented; port smoke examples (drive-a-REPL, pane-split-visible-to-human).
**Exit (acceptance):** Claude Code on Windows opens a PowerShell tmux window, drives it, snapshots it — once via MCP, once via plain CLI. Tag v0.3.0.

### Phase 4 — Control-mode engine (4 days — the new engineering)
`internal/ctl`: persistent control-mode client (spawn `tmux -C`, reply-block parser with command correlation, event dispatch, octal decoding, `%pause` flow-control handling, reconnect with backoff). Resident server (`mcp`/`serve`) routes through it; event-driven `wait_for`. Every path falls back to exec on ctl failure; `--no-ctl` escape hatch.
**Exit:** §6 benchmark gates pass on the real Windows machine; kill -9 the tmux server mid-run → gpty recovers or fails loud, never hangs. Tag v0.4.0.

### Phase 5 — Cloud agents / MCP HTTP (2½ days)
`gpty serve --addr 127.0.0.1:7683` — streamable HTTP MCP; bearer token from `GPTY_TOKEN` or config; **refuses non-loopback bind without a token**; documented exposure recipes (Tailscale, `ssh -R`, cloudflared). Optional session-name allowlist.
**Exit (the product's reason to exist):** a cloud model connects to the Windows box and opens, drives, and snapshots a PowerShell tmux window; a human `gmux attach`es and watches it happen live. Tag v0.5.0.

### Phase 6 — v1.0 ship (1½ days)
Docs vault complete; goreleaser release; deprecation banners on winmux + win-pty READMEs pointing here; NOTICE lineage (agent-pty concept/API by AakeshF; Go rewrite + Windows port by Sam Dotson).
**Exit:** v1.0.0 released with binaries for all six targets; both old repos point forward.

**Total: ~15–16 focused days.**

---

## 9. Risks & mitigations

| Risk | Reality | Mitigation |
|---|---|---|
| Control-mode parser edge cases (UTF-8 octal escapes, `%pause` flow control, interleaved blocks) | The one genuinely hard component | Exec fallback caps blast radius at "as fast as today, never broken"; `--no-ctl` flag; soak test in Phase 4 exit |
| Windows one-shot CLI latency (~40–80 ms) | Cygwin process-spawn floor — architectural, not fixable (fork emulation; even Microsoft abandoned translation with WSL1) | Resident path avoids it entirely; document for CLI users; do **not** attempt cygwin heroics |
| tmux version variance | Control mode needs ≥ 3.2 | `gpty doctor` enforces; MSYS2/brew ship newer |
| HTTP exposure | The only real security surface | Loopback + token by default; refuse open binds; never default-listen on 0.0.0.0 |
| `capture-pane` fidelity for TUI apps (alt-screen) | Same behavior as today's win-pty | Accept; note in docs |
| Name/launch risk | "go-pty" collision | Repo registered as `gpty` at Phase 0 |

## 10. Deferred (explicitly out of v1.0)

- **Minimal Go mux on raw ConPTY** — the escape hatch if Windows latency ever truly matters beyond §6: replace the ~10 §5 commands natively (weeks), rather than rewriting tmux (years) or cygwin (never). The §5 contract is written so this could slot in beneath gpty without the agent surface changing.
- **`gptyd` daemon for sub-ms one-shot CLI** — only if a real workload demands it.
- **Windows ARM64 MSYS2 quirks** — ship the binary, validate when hardware is available.

## 11. Decision log

| Date | Decision |
|---|---|
| 2026-06-09 | Merge winmux + win-pty into one product, `gpty` + `gmux` commands, one Go module |
| 2026-06-09 | Keep C tmux as engine; no tmux rewrite, no cygwin replacement, no full parity |
| 2026-06-09 | Control-mode resident path for speed; exec one-shots for CLI |
| 2026-06-09 | Three access planes: CLI, stdio MCP (local), HTTP MCP (cloud, token + loopback default) |
| 2026-06-09 | Compat locks: `pty_*`/`pane_*` tool names, `agent-pty-<name>` prefix, key grammar |
| 2026-06-09 | Repo name `gpty` (go-pty taken); MIT + NOTICE lineage to AakeshF/agent-pty |
