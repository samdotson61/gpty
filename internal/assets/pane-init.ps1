# pane-init.ps1 — bootstrap run by every gmux/gpty PowerShell pane on Windows.
#
# The cygwin/tmux environment round-trip breaks a few Windows variables for
# panes. Repair them here, in pwsh, after the mangling — so it actually sticks
# and an agent in a pane can run `gpty ...` / `gmux ...` with no extra setup.

# 1. USERNAME. The cygwin/tmux env strips USERNAME (and USER). Some tooling
#    keys off it; repopulate from the real Windows account name.
if (-not $env:USERNAME) { $env:USERNAME = [Environment]::UserName }

# 2. PATHEXT. The round-trip collapses the ';'-separated PATHEXT to a single
#    bogus token, so PowerShell stops resolving bare command names (`gpty`,
#    `gmux`) to their launchers. Re-assert a sane value — but only when it's
#    actually broken, so a normal shell keeps its own (richer) value.
if ($env:PATHEXT -notlike '*.CMD*') {
    $env:PATHEXT = '.COM;.EXE;.BAT;.CMD;.VBS;.VBE;.JS;.JSE;.WSF;.WSH;.MSC'
}

# 3. PATH. Re-assert the persistent (Machine + User) PATH so the dirs the
#    installer adds — where gpty.exe / gmux.exe live — are present in the pane
#    even if the env round-trip dropped them. Then guarantee this folder is first.
$persisted = (@(
    [Environment]::GetEnvironmentVariable('PATH', 'Machine')
    [Environment]::GetEnvironmentVariable('PATH', 'User')
) | Where-Object { $_ }) -join ';'
if ($persisted) { $env:PATH = "$persisted;$env:PATH" }

$here = $PSScriptRoot
if ($here -and (($env:PATH -split ';') -notcontains $here)) {
    $env:PATH = "$here;$env:PATH"
}
