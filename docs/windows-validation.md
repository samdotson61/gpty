# Windows control-mode validation — the one remaining hardware step

**Status:** open. This is the build-plan §8 Phase 4 exit gate: everything else
about gpty on Windows is CI-verified (the exec engine passes the full live
conformance suite against MSYS2 tmux), but the **control-mode channel** — the
resident accelerator — has never come up on Windows: on the CI runner,
`tmux -C` accepts the spawn and then never answers the handshake (silent
pipes). Until it's validated on real hardware, gpty on Windows quietly runs on
the exec engine (correct, just slower per call).

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
