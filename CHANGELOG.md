# Changelog

All notable changes to gpty are documented here. Versioning is semver in
lockstep with `internal/buildinfo.Version` and the docs vault (patch=fix,
minor=feature, major=breaking; one cohesive feature = one minor).

## [0.9.0] — 2026-08-06

### Added
- **Control mode works on Windows — the build-plan §8 Phase 4 exit gate is
  closed.** Root cause of the never-answering channel: the msys runtime does
  not deliver SCM_RIGHTS fd-passing over its AF_UNIX emulation, so a plain
  `tmux -C` client identifies STDIN/STDOUT as -1 and the server writes every
  control line into the void (diagnosed live with `tmux -vv` server logs; Go
  pipes and cygwin shell pipes fail identically). The tty NAME does cross the
  socket, so the new Windows dial (internal/ctl/dial_windows.go) runs `tmux
  -CC` — control mode over the client tty — inside a cygwin pty allocated by
  `script(1)`, talking to the pty master through ordinary pipes. The protocol
  reader normalizes the -CC framing (CRLF, the P1000p DCS intro, the ST
  terminator), and Unix keeps the direct `-C` dial. Validated on hardware:
  dial ~1s, §6 wait_for reaction median 98-99ms (vs the 200ms exec poll it
  replaces; Unix stays ~1ms), TestCtl conformance green twice consecutively.
- **`--tools` specs now combine presets and names** — e.g. `--tools
  panes,crew` exposes the pane suite plus the whole orchestration layer.

### Fixed
- `ctl.Client.Close` now sends `detach-client` before killing the dialed
  process. On Windows the process is script(1) and the tmux -CC client is its
  child on a pty — killing script alone orphaned the client, which stayed
  attached server-side and dragged %output delivery for every later client
  (nine such zombies accumulated during validation; the flaky 240ms wait
  reactions disappeared with the cleanup).
- Conformance latency bounds are now OS-aware: the Windows channel's honest
  push latency (~90-100ms through the script pty) gets a 250ms bound where
  Unix keeps 100ms, and the cross-client round-trip (which deliberately
  includes the trigger's cygwin exec tax) gets 600ms vs 150ms.

## [0.8.2] — 2026-08-06

### Added
- **Test-only:** a live end-to-end crew scenario (`go test -tags live -run
  LiveCrew ./internal/mcpserv`) driving the full Phase 1-2 surface through a
  real MCP client against real tmux panes: a python REPL answered via
  `mesh_send_with_done`, a real y/n prompt detected and auto-approved by the
  permissive Prime Directive, a password prompt escalated (with proof no
  keystroke landed), Red Alert deadlock detection, Bones triage, pane-to-pane
  pipe, subscriptions and lifecycle events. Kill-server-free, safe on a live
  box.

## [0.8.1] — 2026-08-06

### Fixed
- **Test-only:** the 0.8.0 CI run failed on all three OSes because
  `TestServeHTTPEndToEnd` asserted exactly 15 tools over HTTP; the crew port
  registers 33. It now asserts against `mcpserv.AllTools`. Shipped behaviour
  unchanged.
- **The conformance suite's `kill-server` cleanup ran unconditionally on
  exit** — even when `-run` scoped the suite to a single test — destroying
  every live session on a dev box (hit live on this one; the handoff doc's
  "`-run TestCtl` scopes it" advice was wrong about the cleanup). TestMain
  now tears down only a server it started itself: a pre-existing server is
  someone's live work, and per-test Cleanups already remove the suite's own
  sessions.

## [0.8.0] — 2026-08-06

### Added
- **Orchestration layer, phases 1–2 of the upstream agent-pty port** (mesh M6
  plus the Red Alert / Bones / Prime Directive crew modules from M7-M17). 18
  new MCP tools in their own namespaces; the core `pty_*`/`pane_*` surface is
  untouched and can be pinned with the new `--tools core` preset (`mesh` and
  `crew` presets added alongside). New packages `internal/mesh` and
  `internal/crew`, both engine-agnostic: they ride whichever engine (exec or
  control-mode resident) serves the session.
  - **Mesh primitives** — `mesh_send_with_done` (sentinel-bounded
    prompt/reply round-trip for driving another LLM CLI), `mesh_snapshot_since`
    (incremental read past an anchor), `mesh_detect_blocked` (heuristic
    stuck-on-prompt hint), `mesh_pipe` (pane-to-pane content transfer that
    never surfaces in the orchestrator's context), `mesh_subscribe_*`
    (pattern-match push subscriptions), `mesh_lifecycle_*` (born/died/idle/busy
    event streams).
  - **Prime Directive** — policy actuator for blocked panes
    (`prime_directive_resolve`/`_enforce`, conservative and permissive
    policies). Hard-coded invariant: a secrets prompt (password/passphrase/
    2FA/verification) is ALWAYS escalated, never auto-answered.
  - **Red Alert** — fleet escalation to the human (`red_alert_check`,
    `red_alert_notify`, `red_alert_notify_start`/`_stop`): fires a
    notification on a NEW deadlock or dead-pane alert, deduped, read-only on
    panes.
  - **Bones** — read-only health pathology (`bones_examine`, `bones_triage`):
    errors on screen, thrashing loops, hung-mid-task detection, sickest-first
    triage. Never sends a keystroke.
- 55 new unit tests (fake-terminal ports of the upstream acceptance suites)
  plus a live tmux smoke (`go test -tags live -run Live ./internal/mesh`)
  that, unlike the conformance suite, never calls `kill-server`.

### Fixed (relative to the upstream design)
- `mesh_send_with_done` in upstream can return garbage when the sent prompt
  itself contains the done marker (the usual convention — "End your reply
  with <<END>>"): the marker's on-screen ECHO satisfies the wait before the
  sub-agent has produced any output. The gpty port only completes on a marker
  occurrence AFTER the echoed prompt line, falling back to upstream's
  best-effort extraction on timeout. Caught by the live smoke on real tmux.

## [0.7.3] — 2026-07-24

### Fixed
- **Test-only:** the new HTTP conformance test built its `gpty` subprocess
  without an `.exe` suffix on Windows, because it branched on
  `os.Getenv("GOOS")` — `GOOS` is a build-time constant and is normally unset
  in the environment, so the check silently never fired. Now `runtime.GOOS`.
  Shipped behaviour unchanged; the v0.7.1 binaries stand.

## [0.7.2] — 2026-07-24

### Fixed
- **Test-only:** `TestHomebrewLayoutFindsRealTmux` (added in 0.7.1) exercised
  PATH resolution without clearing `$GPTY_TMUX`, so it failed on the Windows CI
  job — which sets that variable to the MSYS2 tmux. `Bin()` was behaving
  correctly (an explicit override wins over PATH); the test's expectation was
  wrong. All three resolution tests now isolate the env first. Shipped
  behaviour is unchanged, so the v0.7.1 binaries stand and no release was cut.

## [0.7.1] — 2026-07-24

### Fixed
- **A Homebrew install could not find tmux at all.** The 0.4.0 recursion guard
  rejected any `tmux` living in the running executable's own directory — a
  location-based proxy for "this is really our own alias." Homebrew breaks that
  assumption: it symlinks gpty *and* the real tmux into the same bin dir, so a
  `brew install`ed gpty rejected the real tmux, fell through to the poison
  name, and reported `tmux binary: tmux-not-found` on every command. Found by
  live-testing the tap the same hour it was published.
  The guard now tests **identity, not location** (`isAliasOfOurs`): the same
  file as the running binary (Unix symlink alias) or byte-identical to a
  sibling `gpty`/`gmux` (the Windows copy alias). Cost is one `Stat` in the
  common case — a real tmux differs in size, so the content hash only runs on
  an exact size tie. Both guarantees re-verified live: the fork-bomb repro
  still spawns **zero** processes, and a real tmux beside gpty now resolves.
  `TestHomebrewLayoutFindsRealTmux` pins the regression.

### Added
- **Homebrew tap:** `brew install samdotson61/tap/gpty`. The formula is
  generated by `scripts/release.sh` from `packaging/homebrew/gpty.rb.tmpl` with
  the release's real checksums and pushed to
  [samdotson61/homebrew-tap](https://github.com/samdotson61/homebrew-tap) on
  every release, so the tap can't drift from the binaries.
- **`install-release.sh`** — `curl … | sh` install from a published release, no
  Go toolchain required: platform detection, checksum verification, `gpty
  setup`, and a tmux-missing hint. `GPTY_BIN` / `GPTY_VERSION` override.
- **Cloud plane covered by tests at last.** `conformance/http_test.go` runs a
  real MCP client against a real `gpty serve` over streamable HTTP with bearer
  auth — 401 without the token, 15 tools listed, then a full remote drive
  (spawn → send → wait_for → snapshot → list → kill) asserting the session is
  genuinely present on the host machine. Previously the HTTP transport was
  only ever checked by hand with curl.

## [0.7.0] — 2026-07-22

Ships build-plan Phase 6: binary releases. With 0.6.0's event-driven wait this
closes every open item that can be closed without the Windows box.

### Added
- **`scripts/release.sh` — one-command release** in the house style: preflight
  (clean tree, buildinfo/CHANGELOG lockstep check, vet + tests), cross-compile
  all six targets (`darwin/{arm64,amd64}`, `linux/{amd64,arm64}`,
  `windows/{amd64,arm64}`, static, trimpath, version stamped), package
  (tar.gz / zip with README+LICENSE+NOTICE, Windows bundles the installers),
  sha256 checksums, tag, and `gh release create` with notes pulled from this
  file's section for the version. First release with binaries: **v0.7.0**.
- **README installs from binaries** as the first option; building from source
  stays documented.
- **[docs/windows-validation.md](windows-validation.md)** — the last open
  item, scripted down to a ~10-minute copy-paste session on the Windows box:
  `gpty doctor --ctl-debug` is the probe, the three failure signatures are
  pre-triaged, and the close-the-gate checklist (remove skips, record §6
  numbers, bump) is written out.

## [0.6.0] — 2026-07-22

Closes the last §6 performance-budget item, pending since 0.1.0.

### Added
- **Event-driven `wait_for` on the resident path.** tmux only delivers
  `%output` notifications for windows in the control client's own session
  (live-verified: unlinked sessions are silent), so WaitFor now `link-window`s
  the target into the hidden ctl session for the wait's duration (refcounted,
  unlinked on return) and wakes on the push, with a 250 ms safety tick behind
  it and the old 25 ms capture-poll as fallback when linking fails. Measured
  reaction to output: **~1.2 ms median (553 µs min)** against the §6 ≤10 ms
  gate — vs the 200 ms poll floor of the exec path. Idle cost drops from 40
  captures/s to 4/s. Two new live tests pin it: cross-client correctness +
  link/unlink mechanics (`TestCtlEventWaitFor`) and the isolated reaction
  number via a `cat` pane driven over the channel (`TestCtlWaitReaction`).
- **`gpty doctor` now probes the control channel** and reports how fast it
  came up — or why it didn't. `--ctl-debug` dumps every raw protocol line
  (`ctl>`/`ctl<`), which is exactly the diagnostic the open Windows question
  needs (cygwin `tmux -C` never answers the handshake; see
  docs/windows-validation.md).

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
