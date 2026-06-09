// Package assets embeds the config files gpty installs, so the binaries stay
// self-contained (build-plan goal #4: single static binaries, zero runtime
// deps). `gpty setup` writes these to the right place with @@GPTY@@ resolved.
package assets

import _ "embed"

// TmuxConf is the gpty tmux configuration (PowerShell-7 default pane on
// Windows, truecolor, bash/pwsh binds). The @@GPTY@@ placeholder is replaced
// with the install dir at setup time.
//
//go:embed tmux.conf
var TmuxConf string

// PaneInit is the PowerShell bootstrap each Windows pane runs to repair the
// cygwin/tmux environment round-trip.
//
//go:embed pane-init.ps1
var PaneInit string
