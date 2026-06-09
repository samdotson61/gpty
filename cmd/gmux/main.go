// Command gmux is the human surface of gpty: create / attach / list / kill the
// same tmux sessions an agent drives via `gpty`. It supersedes wmux — a thin,
// fast, single-binary CLI over tmux that sets the environment tmux needs and
// hands interactive commands the real console (on Windows, via cygwin's
// pseudo-console, the fix for "open terminal failed: not a terminal").
//
//	gmux new -t <name> [-c <cmd>] [-d]   create a session (attaches unless -d)
//	gmux ls                              list sessions
//	gmux attach -t <name>                attach to a session
//	gmux kill -t <name>                  kill a session
//	gmux <tmux args...>                  passed straight through to tmux
package main

import (
	"fmt"
	"os"

	"github.com/samdotson61/gpty/internal/buildinfo"
	"github.com/samdotson61/gpty/internal/tmux"
)

const usage = `gmux — native tmux for humans (PowerShell-7 panes on Windows).
  gmux new -t <name> [-c <cmd>] [-d]   create a session (attaches unless -d)
  gmux ls                              list sessions
  gmux attach -t <name>                attach to a session
  gmux kill -t <name>                  kill a session
  gmux version
  gmux <tmux args...>                  anything else is passed to tmux
Env: GPTY_TMUX (tmux path), MSYS2_ROOT (Windows, default C:\msys64).`

func main() {
	if len(os.Args) < 2 {
		fmt.Println(usage)
		os.Exit(0)
	}
	cmd := os.Args[1]
	rest := os.Args[2:]
	var code int

	switch cmd {
	case "new", "create":
		name := opt(rest, "-t", "-s", "--target", "--name")
		if name == "" {
			fmt.Fprintln(os.Stderr, "usage: gmux new -t <name> [-c <cmd>] [-d]")
			os.Exit(2)
		}
		run := opt(rest, "-c", "--cmd")
		if hasFlag(rest, "-d") {
			a := []string{"new-session", "-d", "-s", name}
			if run != "" {
				a = append(a, run)
			}
			if code = tmux.RunQuietCode(a...); code == 0 {
				fmt.Printf("created session '%s' (detached). attach: gmux attach -t %s\n", name, name)
			}
		} else {
			a := []string{"new-session", "-s", name}
			if run != "" {
				a = append(a, run)
			}
			code = tmux.Interactive(a...)
		}
	case "ls", "list":
		code = tmux.Console("list-sessions")
	case "attach":
		if name := opt(rest, "-t", "--target"); name != "" {
			code = tmux.Interactive("attach", "-t", name)
		} else {
			code = tmux.Interactive("attach")
		}
	case "kill":
		name := opt(rest, "-t", "--target")
		if name == "" {
			fmt.Fprintln(os.Stderr, "usage: gmux kill -t <name>")
			os.Exit(2)
		}
		code = tmux.Console("kill-session", "-t", name)
	case "version", "--version", "-v":
		fmt.Printf("gmux %s\n", buildinfo.Version)
	case "-h", "--help", "help", "":
		fmt.Println(usage)
	default:
		code = tmux.Console(append([]string{cmd}, rest...)...)
	}
	os.Exit(code)
}

// opt finds the value for any of the given flag names.
func opt(args []string, names ...string) string {
	for i := 0; i < len(args); i++ {
		for _, n := range names {
			if args[i] == n && i+1 < len(args) {
				return args[i+1]
			}
		}
	}
	return ""
}

func hasFlag(args []string, f string) bool {
	for _, a := range args {
		if a == f {
			return true
		}
	}
	return false
}
