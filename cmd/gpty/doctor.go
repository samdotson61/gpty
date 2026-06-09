package main

import (
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"runtime"
	"strconv"

	"github.com/samdotson61/gpty/internal/buildinfo"
	"github.com/samdotson61/gpty/internal/platform"
)

// minTmuxMajor/minTmuxMinor is the floor for stable control mode + flow control
// (build-plan §5). MSYS2 pacman and Homebrew both ship newer.
const (
	minTmuxMajor = 3
	minTmuxMinor = 2
)

var tmuxVerRe = regexp.MustCompile(`(\d+)\.(\d+)`)

// tmuxVersion runs `tmux -V` and returns the raw string plus parsed major/minor.
func tmuxVersion() (raw string, major, minor int, err error) {
	out, e := exec.Command(platform.Bin(), "-V").Output()
	if e != nil {
		return "", 0, 0, e
	}
	raw = string(out)
	m := tmuxVerRe.FindStringSubmatch(raw)
	if m == nil {
		return raw, 0, 0, fmt.Errorf("could not parse tmux version from %q", raw)
	}
	major, _ = strconv.Atoi(m[1])
	minor, _ = strconv.Atoi(m[2])
	return raw, major, minor, nil
}

func versionString() string {
	v := fmt.Sprintf("gpty %s %s/%s", buildinfo.Version, runtime.GOOS, runtime.GOARCH)
	if raw, _, _, err := tmuxVersion(); err == nil {
		v += " (" + trim(raw) + ")"
	}
	return v
}

func trim(s string) string {
	for len(s) > 0 && (s[len(s)-1] == '\n' || s[len(s)-1] == '\r' || s[len(s)-1] == ' ') {
		s = s[:len(s)-1]
	}
	return s
}

// runDoctor checks the environment and prints actionable fixes.
func runDoctor() error {
	fmt.Printf("gpty %s  (%s/%s)\n", buildinfo.Version, runtime.GOOS, runtime.GOARCH)
	fmt.Printf("tmux binary: %s\n", platform.Bin())

	raw, major, minor, err := tmuxVersion()
	if err != nil {
		fmt.Println("  ✗ tmux not found or not runnable.")
		if runtime.GOOS == "windows" {
			fmt.Println("    fix: run `gpty setup` or install.ps1 (installs MSYS2 + tmux), or set GPTY_TMUX.")
		} else if runtime.GOOS == "darwin" {
			fmt.Println("    fix: brew install tmux")
		} else {
			fmt.Println("    fix: apt install tmux  (or your distro's package manager)")
		}
		return fmt.Errorf("tmux unavailable")
	}
	fmt.Printf("  ✓ %s\n", trim(raw))

	if major < minTmuxMajor || (major == minTmuxMajor && minor < minTmuxMinor) {
		fmt.Printf("  ✗ tmux %d.%d is below the required %d.%d (control mode needs it).\n",
			major, minor, minTmuxMajor, minTmuxMinor)
		return fmt.Errorf("tmux too old")
	}
	fmt.Printf("  ✓ control mode supported (>= %d.%d)\n", minTmuxMajor, minTmuxMinor)

	if runtime.GOOS == "windows" {
		root := os.Getenv("MSYS2_ROOT")
		if root == "" {
			root = `C:\msys64`
		}
		fmt.Printf("MSYS2 root: %s\n", root)
		if _, err := exec.LookPath("pwsh"); err != nil {
			fmt.Println("  ! pwsh (PowerShell 7) not on PATH — panes will fall back to the default shell.")
			fmt.Println("    fix: winget install Microsoft.PowerShell")
		} else {
			fmt.Println("  ✓ pwsh (PowerShell 7) found")
		}
	}

	if os.Getenv("TMUX") != "" {
		fmt.Println("  ✓ running inside tmux (pane ops can target the current window)")
	} else {
		fmt.Println("  i not inside tmux — pane_split needs $TMUX_PANE; run the agent inside a gmux session for live panes.")
	}
	fmt.Println("all good.")
	return nil
}
