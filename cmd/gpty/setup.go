package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/samdotson61/gpty/internal/assets"
)

// runSetup installs the tmux.conf (and, on Windows, pane-init.ps1) so panes
// default to PowerShell 7 and an agent in a pane can run gpty/gmux with no extra
// setup. It does NOT install tmux itself — that's install.ps1 / install.sh /
// your package manager (see `gpty doctor`).
func runSetup(args []string) error {
	opts, _ := parseOpts(args, map[string]bool{"dir": true})

	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	dir := opts["dir"]
	if dir == "" {
		dir = filepath.Join(home, ".gpty")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}

	// pane-init.ps1 (used by the conf's default-command on Windows).
	paneInitPath := filepath.Join(dir, "pane-init.ps1")
	if err := os.WriteFile(paneInitPath, []byte(assets.PaneInit), 0o644); err != nil {
		return err
	}

	// Resolve the conf: @@GPTY@@ -> dir (forward slashes, as tmux wants). On a
	// host without pwsh, comment out the PowerShell default-command so panes use
	// the login shell instead of failing.
	conf := strings.ReplaceAll(assets.TmuxConf, "@@GPTY@@", filepath.ToSlash(dir))
	if _, err := exec.LookPath("pwsh"); err != nil {
		conf = strings.Replace(conf,
			`set -g default-command "pwsh`,
			`# (pwsh not found — using login shell) set -g default-command "pwsh`, 1)
	}

	dests := confDestinations(home)
	written := []string{}
	for _, dest := range dests {
		if _, err := os.Stat(dest); err == nil {
			_ = copyFile(dest, dest+".bak")
		}
		if err := os.WriteFile(dest, []byte(conf), 0o644); err != nil {
			fmt.Fprintf(os.Stderr, "  ! could not write %s: %v\n", dest, err)
			continue
		}
		written = append(written, dest)
	}
	if len(written) == 0 {
		return fmt.Errorf("could not install tmux.conf to any of: %v", dests)
	}

	fmt.Println("gpty setup complete:")
	fmt.Printf("  pane-init: %s\n", paneInitPath)
	for _, w := range written {
		fmt.Printf("  tmux.conf: %s\n", w)
	}
	fmt.Println()
	fmt.Println("Next:")
	fmt.Println("  gpty doctor                         # verify tmux >= 3.2")
	fmt.Println("  claude mcp add gpty -- gpty mcp     # register with Claude Code (local)")
	fmt.Println("  gmux new -t test                    # open a session and attach")
	return nil
}

// confDestinations returns every plausible tmux config path for this OS. A
// cygwin tmux's HOME varies by launch context, so on Windows we write to all of
// them.
func confDestinations(home string) []string {
	dests := []string{filepath.Join(home, ".tmux.conf")}
	if runtime.GOOS == "windows" {
		if up := os.Getenv("USERPROFILE"); up != "" && up != home {
			dests = append(dests, filepath.Join(up, ".tmux.conf"))
		}
		root := os.Getenv("MSYS2_ROOT")
		if root == "" {
			root = `C:\msys64`
		}
		if user := os.Getenv("USERNAME"); user != "" {
			dests = append(dests, filepath.Join(root, "home", user, ".tmux.conf"))
		}
	}
	return dedupe(dests)
}

func dedupe(in []string) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, s := range in {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}

func copyFile(src, dst string) error {
	b, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	return os.WriteFile(dst, b, 0o644)
}
