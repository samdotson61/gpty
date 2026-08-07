# Windows control-mode validation — the one remaining hardware step

**Status: CLOSED — validated on real Windows hardware 2026-08-06 (gpty
0.9.0).** Root cause of the silent pipes, found with `tmux -vv` server logs:
the msys runtime does not deliver SCM_RIGHTS fd-passing over its AF_UNIX
emulation, so a `tmux -C` client identifies `STDIN`/`STDOUT` as `-1` and the
server writes every control line into a bufferevent on fd -1 — on Go pipes
and cygwin shell pipes alike. What does cross the socket is the tty NAME
(the server opens the client terminal by path), which is why interactive
attach always worked. The fix (internal/ctl/dial_windows.go): dial `tmux
-CC` — control mode over the client tty — inside a real cygwin pty allocated
by `script(1)`, and talk to the pty master through script's ordinary pipe
stdio. Validated numbers on the Windows box: dial ~1s, §6 wait_for reaction
median **98-99ms** (vs the 200ms exec poll cadence; Unix direct pipes remain
~1ms), full `TestCtl` conformance green twice consecutively, no leaked
clients (Close now sends `detach-client` first — killing script alone
orphans the -CC client attached server-side).

The original hunt instructions are kept below for archaeology.

Everything below is copy-paste; total time ~10 minutes on the Windows box.

## Step 1 — install / update gpty

```powershell
git clone https://github.com/samdotson61/gpty ; cd gpty    # or git pull
./install.ps1 -Yes
```

## Step 2 — the probe (the interesting part)

```powershell
gpty doctor --ctl-debug
```

- **`✓ control channel up in …`** → the channel WORKS on real Windows and the
  CI failure was runner-specific. Go to step 3.
- **`✗ control channel failed …`** → the raw `ctl>` / `ctl<` lines above the
  failure are the diagnostic. Three patterns mean three different things:
  - **No `ctl<` lines at all** → cygwin never delivers tmux's stdout over the
    pipe (the CI symptom). Paste the output into a Claude session on this
    repo; likely next probes: `MSYS=noglob` on/off for the `-C` client, and
    `stdbuf`/`winpty`-style pipe modes.
  - **`ctl<` startup lines but no handshake reply** → the reader works and
    command writes are lost; different bug, same paste-it-back flow.
  - **Garbled/interleaved lines** → parser assumption broken on cygwin; the
    dump shows exactly where.

## Step 3 — if the probe succeeded: run the full live suite

```powershell
go test -tags live -count=1 -v -run 'TestCtl' ./conformance/
```

(Since 0.8.1 the suite's exit cleanup only kill-servers a tmux server it
started itself, so this is safe on a box with live sessions — before that,
TestMain killed the server unconditionally, `-run` filter or not.)

All green (not skipped) means the channel is validated. Then close the gate:

1. Remove the two Windows `t.Skipf` blocks in `conformance/conformance_test.go`
   (`TestCtlEngine`, `TestCtlEventWaitFor`, `TestCtlWaitReaction`) so CI
   regressions turn red again.
2. Record the measured numbers (`TestCtlWaitReaction` logs the §6 reaction
   median) in README's table alongside the macOS figures.
3. Update CHANGELOG + this file's Status line; bump a patch version.

## Why this can't be done from the Mac

The failure only reproduces under the cygwin runtime — macOS/Linux control
mode works and is CI-green. The CI runner reproduces the failure but can't be
interactively debugged (each experiment = a full push/run cycle at ~2 min,
with no way to poke at pipe modes mid-run). One interactive session on the
real box beats twenty blind CI commits.
