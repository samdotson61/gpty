# Changelog

All notable changes to gpty are documented here. Versioning is semver in
lockstep with `internal/buildinfo.Version` and the docs vault (patch=fix,
minor=feature, major=breaking; one cohesive feature = one minor).

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
- Deprecation banners on winmux/win-pty; goreleaser multi-target release
  (build-plan Phase 6).
