# Changelog

All notable changes to gpty are documented here. Versioning is semver in
lockstep with `internal/buildinfo.Version` and the docs vault (patch=fix,
minor=feature, major=breaking; one cohesive feature = one minor).

## [0.5.1] — 2026-06-12

Fixes for the two findings of an external review of 0.3.0–0.5.0.

### Fixed
- **The tmux alias could still exec itself through the final fallback.**
  0.4.0's recursion guard protected the PATH lookup but not `defaultBin()`:
  on Unix that fallback is the bare name "tmux", which exec.Command resolves
  through PATH — straight back to the alias when no real tmux exists anywhere
  else (real tmux uninstalled later, or the alias symlink clobbering
  Homebrew's tmux in /usr/local/bin on Intel Macs). Reproduced live by the
  reviewer: one re-entry per `-u` flag until OOM. Layered fix:
  - every resolution layer (env override, PATH lookup, rescan, fallback) now
    also rejects candidates that ARE the running executable (`os.SameFile`,
    so a Unix symlink is caught wherever it lives on PATH);
  - when the only tmux anywhere is our own alias, `Bin()` warns once on
    stderr and returns a poison name that fails loud and clean
    ("tmux-not-found: executable file not found") instead of forking;
  - `install.sh --tmux-alias` refuses to overwrite a `$BIN/tmux` that isn't
    already gpty's own symlink — the Intel-Mac Homebrew clobber;
  - defense-in-depth: every tmux invocation bumps `GPTY_EXEC_DEPTH`, and
    gpty/gmux exit loudly at 10 nested executions (legitimate
    gmux-inside-a-pane nesting adds one level per human step and never
    approaches the cap).
- **gmux's passthrough now routes attaching commands through the interactive
  environment.** `tmux attach-session -t x` / `new-session` typed by their
  full names fell to gmux's default case and ran with the non-interactive
  env (`MSYS=noglob`); gmux now uses the same `Passthrough` routing as the
  gpty CLI. Note: live testing on the current MSYS2 runtime (tmux 3.6a)
  showed attach actually works under noglob — cold start and warm attach
  both verified against the wmux-era "open terminal failed" lore — so this
  is consistency plus insurance for older runtimes, not a reproduced
  failure. windows.go's comment now says so.

## [0.5.0] — 2026-06-10

### Changed
- **MCP tool definitions slimmed ~30%** — they ride in the context window of
  every agent session that registers gpty, so wording is a token budget, not
  documentation. `tools/list` payload: 6,215 → 4,518 chars (~1,554 → ~1,130
  tokens). Same 15 tools, same frozen names, same behavior; the
  anti-hallucination guard rails survive in compressed form (pty_spawn still
  says a session is invisible to gmux watchers, pane_split still reports the
  attached-client count). `TestToolBudget` pins the ceiling (5,000 chars
  total, 200 per description) over an in-memory MCP client, so the
  definitions can't creep back toward documentation.

### Added
- **`--tools` filter on `gpty mcp` and `gpty serve`** for a much bigger cut
  when an agent only needs part of the surface: `all` (default), `session`
  (pty_* only — 1,713 chars, ~430 tokens), `panes` (pane_* only), or a
  comma-separated list of exact tool names. Unknown names are an error so a
  typo can't silently drop a tool.

## [0.4.0] — 2026-06-10

### Added
- **Type `tmux`, get gmux.** The installers register gmux under the name
  `tmux` (busybox-style: one binary, dispatched on the name it was invoked
  under), so `tmux new -t sesh` opens a gmux session named `sesh`, `tmux ls` /
  `tmux attach -t sesh` / `tmux kill -t sesh` carry gmux's semantics, a bare
  `tmux` opens a new attached session, and **every other tmux command still
  reaches the real tmux** through gmux's passthrough (`tmux -V`,
  `tmux list-windows`, …). Windows installs the alias by default (there is no
  native tmux on PATH to shadow); on macOS/Linux it's opt-in via
  `install.sh --tmux-alias` / `GPTY_TMUX_ALIAS=1` since it can shadow the
  system tmux depending on PATH order.
- **Recursion guard in tmux resolution.** With a `tmux` alias on PATH,
  `platform.Bin()`'s PATH lookup would have found the alias and made
  gpty/gmux exec themselves forever. Resolution now skips any candidate in
  the running executable's own directory (where the alias lives by
  contract) and scans the rest of PATH, then falls back to the OS default.
  `$GPTY_TMUX` still overrides everything. Covered by a unit test that
  plants a fake alias next to the test binary.

## [0.3.0] — 2026-06-10

### Added
- **Every tmux command and alias now works as a gpty subcommand.** A subcommand
  gpty doesn't recognize itself is checked against the installed tmux's live
  command table (`list-commands`, answered without a running server) and, if
  it's a real command or alias, forwarded as-is: `gpty list-windows`,
  `gpty send-keys -t s ls Enter`, `gpty splitw -h`, `gpty kill-server`, … all
  behave exactly like the bare `tmux` invocation, exit code included.
  Attaching commands (`attach`/`attach-session`, and `new`/`new-session`
  without `-d`) get the interactive environment so Windows attach works
  (cygwin pcon); everything else keeps `MSYS=noglob` so format-string
  arguments survive cygwin. Typos still get gpty's own unknown-command error,
  not a tmux one. gpty's own names win on collision — `send`, `ls`, and
  `wait`/`wait-for` stay gpty commands; use the full tmux names
  (`send-keys`, `list-sessions`) to pass through. `gmux` already forwarded
  unknown commands blindly; `gpty` now matches it, with validation.

## [0.2.1] — 2026-06-09

### Fixed
- **Server startup no longer blocks on the control-mode dial.** `NewAccel` now
  dials in the background: `gpty mcp`/`serve` start instantly on the exec
  engine and upgrade to the control channel when it connects. On hosts where
  the channel never comes up (cygwin tmux today), the first 0.2.0 CI run showed
  startup would have stalled for the full 20s handshake deadline; it now costs
  nothing, and after 3 failed dial attempts gpty logs once and stays on exec
  for the life of the process.

## [0.2.0] — 2026-06-09

### Added
- **Installers now offer to install Go (and tmux) from the relevant package
  manager.** `install.sh` detects brew (macOS) or apt/dnf/pacman/zypper/apk
  (Linux), prompts with a default-yes offer (reads `/dev/tty`, so it works
  under `curl | bash`; assumes yes with a notice when there is no tty), and
  validates Go ≥ 1.21 (older Go can't auto-fetch the toolchain go.mod needs).
  `install.ps1` now *asks* before installing Go via winget instead of doing it
  silently. Non-interactive acceptance: `--yes`/`GPTY_YES=1` (sh), `-Yes` (ps1).
- `install.sh` carries its executable bit.

### Changed
- **First 3-OS CI results** (ubuntu/macos/windows + MSYS2): Linux and macOS run
  the full live conformance suite green, including control mode. Windows passes
  the exec-engine suite (sessions, panes, literal-send) against MSYS2 tmux; the
  control-mode dial times out on the CI runner. Dial's handshake now gets a
  patient 20s deadline (cygwin cold-start on loaded runners), and a dial
  failure on Windows is a tracked test skip rather than a red build — matching
  the runtime behavior, which falls back to the exec engine automatically.
  Validating the channel on real Windows hardware remains the Phase 4 gate.

## [0.1.1] — 2026-06-09

Post-release review pass (staticcheck + fresh-eyes audit of the control-mode
client, CLI plumbing, HTTP guard, and CI).

### Fixed
- **Control-mode reply-correlation shift on write failure.** If writing a
  command to the `tmux -C` stdin failed, the already-enqueued reply stayed in
  the FIFO queue, so the next command's `%begin` would pair with the orphan and
  every later reply would correlate off by one. A broken stdin now tears the
  channel down (readLoop fails all pending replies; callers fall back to exec).
- **`gpty serve` logged "serving…" before the loopback guard ran**, printing a
  misleading line when the bind was about to be refused. The log now comes from
  `ServeHTTP` after validation passes.
- **`gpty wait-for --timeout <garbage>` produced a 0s timeout** (instant
  failure) instead of keeping the 10s default.
- **Windows CI silently skipped the live conformance suite**: `setup-msys2`
  installs under `RUNNER_TEMP` (not `C:\msys64`) and plain steps don't get the
  msys2 PATH. The workflow now exports `GPTY_TMUX` from the action's location,
  and the suite resolves tmux the way the product does (`platform.Bin()`)
  instead of a bare PATH lookup.
- `pasteBuf` moved next to its only consumer (the Windows literal-send path) —
  flagged by staticcheck as dead code on Unix builds.

## [0.1.0] — 2026-06-09

First public cut: the merge of [winmux](https://github.com/samdotson61/winmux)
and [win-pty](https://github.com/samdotson61/win-pty) into one cross-platform Go
module over C tmux, per [docs/build-plan.md](docs/build-plan.md).

### Added
- **Two commands, one module.** `gpty` (agent surface) and `gmux` (human
  surface, supersedes `wmux`), built as single static binaries.
- **Three access planes.** CLI one-shots; MCP over **stdio** (`gpty mcp`) for
  local agents; MCP over **streamable HTTP** (`gpty serve`) for cloud agents,
  with bearer-token auth and a loopback-only-without-token guard.
- **Resident control-mode engine** (`internal/ctl`). A persistent `tmux -C`
  client makes the hot reads/sends pipe round-trips instead of process spawns —
  measured **~100× faster** than exec on macOS (snapshot/send ~2.7 ms → ~27 µs),
  well under the build-plan §6 ≤5 ms resident budget. Falls back to exec on any
  channel error, with background re-dial.
- **Unified platform layer** (`internal/platform`) merging win-pty's `tmuxEnv`
  and wmux's `buildEnv`, including wmux's conditional-`MSYS=noglob` logic for
  Windows interactive attach.
- **15 MCP tools** with names frozen from win-pty (`pty_*`, `pane_*`) plus a new
  `pane_info`, so existing agent registrations upgrade by changing only the
  command path. `agent-pty-<name>` session prefix preserved.
- **`gpty doctor`** (tmux presence + ≥3.2 version gate) and **`gpty setup`**
  (installs the embedded tmux.conf with PowerShell-7 default panes).
- **Go test suite** ported from win-pty's Python tests (`internal/keys`), plus a
  **live conformance suite** and **benchmarks** (`conformance/`, `-tags live`)
  that exercise the §5 tmux contract against a real tmux.
- 3-OS CI (ubuntu/macos/windows + MSYS2).

### Fixed
- **Server cold-start race** surfaced by live MCP testing: the MCP SDK
  dispatches buffered tool calls concurrently, and several tmux clients could
  race the server's first start and get "no server running." `EnsureServer` now
  serializes cold-start and verifies connectivity, with a lock-free fast path
  after warmup (no hot-path cost) and recovery if the server is killed.

### Verified
- macOS (tmux 3.6a): `go test ./...`, `go test -tags live ./conformance/...`,
  and an end-to-end sequential MCP drive (spawn → send → wait → pane_split →
  list → kill) over the control-mode engine — all green, repeatably.

### Pending (tracked for later phases)
- **Hand-validation on a Windows box.** The Windows path is implemented (the
  merged winmux/win-pty code) and runs in CI via MSYS2, but has not been
  re-driven by hand on Windows. Build-plan §6's "resident gates on real Windows
  hardware" remains open.
- **Event-driven `wait_for`** (`%output` push, ≤10 ms). Shipped `wait_for` uses
  a fast capture-poll over the open channel (~25 ms) — far better than the exec
  200 ms poll, but not yet the push-based path. `%output` framing is parsed and
  consumed; wiring it to per-session waits is future work.
- Deprecation banners on winmux/win-pty *(done 2026-06-10: both repos carry a
  README deprecation banner pointing here and are archived on GitHub)*;
  goreleaser multi-target release (build-plan Phase 6).
