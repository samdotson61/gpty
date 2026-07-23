package main

import (
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"runtime"
	"strconv"
	"time"

	"github.com/samdotson61/gpty/internal/buildinfo"
	"github.com/samdotson61/gpty/internal/ctl"
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

// runDoctor checks the environment and prints actionable fixes. --ctl-debug
// adds a raw protocol dump to the control-mode probe.
func runDoctor(args []string) error {
	opts, _ := parseOpts(args, map[string]bool{})
	debug := boolFlag(opts, "ctl-debug")
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

	probeCtl(debug)
	fmt.Println("done.")
	return nil
}

// probeCtl dials the control-mode channel and reports the result. This is the
// diagnostic for the open Windows question (cygwin tmux -C never answering the
// handshake): with debug=true every raw protocol line is echoed, so a failing
// host shows exactly what — if anything — came back before the timeout.
func probeCtl(debug bool) {
	fmt.Println("control mode (resident-server accelerator):")
	if debug {
		ctl.Debug = os.Stdout
		defer func() { ctl.Debug = nil }()
		fmt.Println("  (raw protocol dump on — lines prefixed ctl>/ctl<)")
	}
	fmt.Println("  probing... (a broken channel takes ~20s to declare itself)")
	start := time.Now()
	c, err := ctl.Dial()
	if err != nil {
		fmt.Printf("  ✗ control channel failed after %s: %v\n", time.Since(start).Round(time.Millisecond), err)
		fmt.Println("    gpty still works — the resident server falls back to the exec engine.")
		if !debug {
			fmt.Println("    re-run `gpty doctor --ctl-debug` and share the ctl>/ctl< lines to diagnose.")
		}
		return
	}
	defer c.Close()
	fmt.Printf("  ✓ control channel up in %s (hot reads/sends take the fast path)\n", time.Since(start).Round(time.Millisecond))
}
