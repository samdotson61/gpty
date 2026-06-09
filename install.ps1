#requires -version 5
<#
.SYNOPSIS
  Install gpty + gmux on Windows: a native MSYS2 tmux with PowerShell-7 panes,
  driven by two single-file Go binaries.
.DESCRIPTION
  Installs MSYS2 (winget) + tmux (pacman), ensures Go, builds gpty.exe and
  gmux.exe, installs the tmux.conf (via `gpty setup`), and puts this folder on
  PATH. No WSL. Idempotent: re-running is safe.
#>
[CmdletBinding()]
param(
    [string]$Msys2Root = 'C:\msys64',
    [switch]$Yes   # answer yes to all install offers (non-interactive)
)

$ErrorActionPreference = 'Stop'
function Info($m) { Write-Host "==> $m" -ForegroundColor Cyan }

# Confirm-Install: yes/no offer with default yes. -Yes (or a non-interactive
# host) assumes yes, matching install.sh's behavior.
function Confirm-Install($question) {
    if ($Yes) { return $true }
    if (-not [Environment]::UserInteractive) { Info "(non-interactive - assuming yes: $question)"; return $true }
    $ans = Read-Host "$question [Y/n]"
    return ([string]::IsNullOrEmpty($ans) -or $ans -match '^[Yy]')
}

# 1. MSYS2
if (-not (Test-Path "$Msys2Root\usr\bin\bash.exe")) {
    Info 'Installing MSYS2 via winget...'
    winget install --id MSYS2.MSYS2 --accept-source-agreements --accept-package-agreements --disable-interactivity
} else {
    Info "MSYS2 already present at $Msys2Root"
}
$bash = "$Msys2Root\usr\bin\bash.exe"
if (-not (Test-Path $bash)) { throw "MSYS2 bash not found at $bash after install." }

# 2. tmux
Info 'Installing tmux via pacman...'
$env:MSYSTEM = 'MSYS'
& $bash -lc 'pacman -S --noconfirm --needed tmux'

# 3. Go, then build the two single-file binaries.
function Resolve-Go {
    $c = Get-Command go -ErrorAction SilentlyContinue
    if ($c) { return $c.Source }
    $p = Join-Path $env:ProgramFiles 'Go\bin\go.exe'
    if (Test-Path $p) { return $p }
    return $null
}
$go = Resolve-Go
if (-not $go) {
    if (-not (Confirm-Install "Go not found - install it via winget (GoLang.Go)?")) {
        throw 'Go is required to build gpty. Install it from https://go.dev/dl and re-run.'
    }
    Info 'Installing Go via winget (GoLang.Go)...'
    winget install --id GoLang.Go --accept-source-agreements --accept-package-agreements --disable-interactivity
    $go = Resolve-Go
    if (-not $go) { throw 'Go install did not produce go.exe. Install Go from https://go.dev/dl and re-run.' }
}

$version = (& git -C $PSScriptRoot describe --tags --always 2>$null); if (-not $version) { $version = 'dev' }
$ldflags = "-s -w -X github.com/samdotson61/gpty/internal/buildinfo.Version=$version"
Info "Building gpty.exe + gmux.exe ($version) with $(& $go version)"
Push-Location $PSScriptRoot
try {
    & $go build -ldflags $ldflags -o (Join-Path $PSScriptRoot 'gpty.exe') ./cmd/gpty
    if ($LASTEXITCODE -ne 0) { throw 'go build (gpty) failed.' }
    & $go build -ldflags $ldflags -o (Join-Path $PSScriptRoot 'gmux.exe') ./cmd/gmux
    if ($LASTEXITCODE -ne 0) { throw 'go build (gmux) failed.' }
} finally { Pop-Location }

# 4. Put this folder on the user PATH (so `gpty` / `gmux` resolve).
$userPath = [Environment]::GetEnvironmentVariable('PATH', 'User')
if (($userPath -split ';') -notcontains $PSScriptRoot) {
    Info "Adding $PSScriptRoot to your user PATH..."
    $newPath = if ([string]::IsNullOrEmpty($userPath)) { $PSScriptRoot } else { "$PSScriptRoot;$userPath" }
    [Environment]::SetEnvironmentVariable('PATH', $newPath, 'User')
    $env:PATH = "$PSScriptRoot;$env:PATH"
    $pathChanged = $true
} else {
    Info "gpty already on PATH ($PSScriptRoot)"
}

# 5. Install the tmux.conf (gpty embeds it; setup writes it to the tmux homes).
Info 'Installing tmux.conf (PowerShell-7 default panes)...'
& (Join-Path $PSScriptRoot 'gpty.exe') setup | Out-Host

# 6. Report
& (Join-Path $PSScriptRoot 'gpty.exe') doctor | Out-Host
Write-Host ""
Write-Host "Installed gpty + gmux. Try:" -ForegroundColor Green
Write-Host "    gmux new -t test                  # new PowerShell-7 session, attached"
Write-Host "    claude mcp add gpty -- gpty mcp    # register with Claude Code (local)"
if ($pathChanged) {
    Write-Host ""
    Write-Host "NOTE: PATH was updated - open a NEW terminal for 'gpty'/'gmux' to resolve." -ForegroundColor Yellow
}
