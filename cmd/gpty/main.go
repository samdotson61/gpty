// Command gpty is the agent surface of gpty: spawn / drive / snapshot / wait on
// name-addressable terminal sessions and panes, exposed as CLI one-shots, an
// MCP stdio server (local agents), and an MCP HTTP server (cloud agents).
//
// One-shot CLI calls use the exec engine (build-plan §2, decision 1). The mcp
// and serve modes hold a resident control-mode client for speed, falling back
// to exec automatically (decision 2). Pair with `gmux` for the human surface.
package main

import (
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/samdotson61/gpty/internal/ctl"
	"github.com/samdotson61/gpty/internal/engine"
	"github.com/samdotson61/gpty/internal/mcpserv"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, usage)
		os.Exit(2)
	}
	sub := os.Args[1]
	args := os.Args[2:]
	eng := engine.Exec{} // one-shot CLI path is always exec

	var err error
	switch sub {
	case "mcp", "mcp-serve":
		err = runMCP(args)
	case "serve":
		err = runServe(args)
	case "spawn":
		err = cliSpawn(eng, args)
	case "send":
		err = needN(args, 2, "send <name> <text>", func() error { return eng.Send(args[0], args[1]) })
	case "snapshot":
		err = needN(args, 1, "snapshot <name>", func() error {
			s, e := eng.Snapshot(args[0])
			if e == nil {
				fmt.Println(s)
			}
			return e
		})
	case "wait-for", "wait":
		err = cliWaitFor(eng, args)
	case "list", "ls":
		var names []string
		if names, err = eng.List(); err == nil {
			for _, n := range names {
				fmt.Println(n)
			}
		}
	case "kill":
		err = needN(args, 1, "kill <name>", func() error { return eng.Kill(args[0]) })
	case "split":
		err = cliSplit(eng, args)
	case "panes", "pane-list":
		var out string
		if out, err = eng.PaneList(firstOr(args)); err == nil {
			fmt.Println(out)
		}
	case "pane-send", "psend":
		err = needN(args, 2, "pane-send <pane> <text>", func() error { return eng.PaneSend(args[0], args[1]) })
	case "pane-capture", "pcap":
		err = needN(args, 1, "pane-capture <pane>", func() error {
			s, e := eng.PaneCapture(args[0])
			if e == nil {
				fmt.Println(s)
			}
			return e
		})
	case "pane-kill", "pkill":
		err = needN(args, 1, "pane-kill <pane>", func() error { return eng.PaneKill(args[0]) })
	case "pane-select", "pselect":
		err = needN(args, 1, "pane-select <pane>", func() error { return eng.PaneSelect(args[0]) })
	case "pane-resize", "presize":
		err = needN(args, 2, "pane-resize <pane> <U|D|L|R> [amount]", func() error {
			amt := 5
			if len(args) >= 3 {
				fmt.Sscanf(args[2], "%d", &amt)
			}
			return eng.PaneResize(args[0], args[1], amt)
		})
	case "pane-layout", "playout":
		layout := ""
		if len(args) >= 1 {
			layout = args[0]
		}
		err = eng.PaneLayout("", layout)
	case "pane-info", "pinfo":
		err = needN(args, 1, "pane-info <pane>", func() error {
			info := eng.PaneInfo(args[0])
			if info == "" {
				return fmt.Errorf("no such pane %q", args[0])
			}
			fmt.Println(info)
			return nil
		})
	case "doctor":
		err = runDoctor()
	case "setup":
		err = runSetup(args)
	case "version", "--version", "-v":
		fmt.Println(versionString())
	case "-h", "--help", "help":
		fmt.Println(usage)
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\n%s\n", sub, usage)
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

const usage = `gpty — agent terminal control over tmux (Windows / macOS / Linux)

 Servers (for agents):
  gpty mcp [--no-ctl]                  MCP server over stdio (local agents)
  gpty serve [--addr H:P] [--token T]  MCP server over streamable HTTP (cloud agents)

 Sessions (background, addressable by name):
  gpty spawn <name> [--cmd C] [--cwd D] [--cols N] [--rows N]
  gpty send <name> <text>              keys: <Enter> <Esc> <Tab> <C-c> <Up> ... ; << = literal <
  gpty snapshot <name>
  gpty wait-for <name> <pattern> [--timeout S]
  gpty list
  gpty kill <name>

 Panes (split & drive the agent's CURRENT gmux window; the human sees it live):
  gpty split [h|v] [--target P] [--cmd C] [--cwd D] [--percent N]   -> new pane id
  gpty panes [all]                     list panes
  gpty pane-info <pane>                session/window, pane count, attached clients
  gpty pane-send <pane> <text>         send keys to a pane
  gpty pane-capture <pane>             rendered text of a pane
  gpty pane-select <pane>              focus a pane
  gpty pane-resize <pane> <U|D|L|R> [amount]
  gpty pane-layout [even-horizontal|even-vertical|main-vertical|tiled]
  gpty pane-kill <pane>

 Setup / diagnostics:
  gpty doctor                          check tmux is present and >= 3.2
  gpty setup [--dir D]                 install the tmux.conf (PowerShell-7 panes on Windows)
  gpty version
Env: GPTY_TMUX (tmux path), MSYS2_ROOT (Windows, default C:\msys64), GPTY_TOKEN (serve auth).`

// --- resident server modes --------------------------------------------------

// buildEngine returns the resident engine and a cleanup func. With control mode
// enabled (default) it wraps exec in a control-mode accelerator; on dial
// failure it logs and falls back to plain exec so the server still starts.
func buildEngine(noCtl bool) (engine.Engine, func()) {
	base := engine.Exec{}
	if noCtl {
		return base, func() {}
	}
	acc, err := ctl.NewAccel(base)
	if err != nil {
		fmt.Fprintf(os.Stderr, "gpty: control mode unavailable (%v); using exec fallback\n", err)
		return base, func() {}
	}
	return acc, acc.Close
}

func runMCP(args []string) error {
	opts, _ := parseOpts(args, map[string]bool{})
	eng, cleanup := buildEngine(boolFlag(opts, "no-ctl"))
	defer cleanup()
	return mcpserv.ServeStdio(eng)
}

func runServe(args []string) error {
	opts, _ := parseOpts(args, map[string]bool{"addr": true, "token": true})
	addr := opts["addr"]
	if addr == "" {
		addr = "127.0.0.1:7683"
	}
	token := opts["token"]
	if token == "" {
		token = os.Getenv("GPTY_TOKEN")
	}
	eng, cleanup := buildEngine(boolFlag(opts, "no-ctl"))
	defer cleanup()
	fmt.Fprintf(os.Stderr, "gpty: serving MCP over http on %s (token %s)\n", addr, tokenState(token))
	return mcpserv.ServeHTTP(eng, mcpserv.HTTPConfig{Addr: addr, Token: token})
}

func tokenState(t string) string {
	if t == "" {
		return "disabled — loopback only"
	}
	return "required"
}

// --- helpers ----------------------------------------------------------------

func needN(args []string, n int, sig string, fn func() error) error {
	if len(args) < n {
		return fmt.Errorf("usage: gpty %s", sig)
	}
	return fn()
}

func firstOr(args []string) string {
	if len(args) > 0 {
		return args[0]
	}
	return ""
}

// parseOpts pulls --flag value pairs out of args (order-independent, so flags
// may follow positionals). Bare flags (e.g. --no-ctl) land in the map with an
// empty value. Returns the option map and the leftover positionals.
func parseOpts(args []string, valueFlags map[string]bool) (map[string]string, []string) {
	opts := map[string]string{}
	var pos []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		key := strings.TrimLeft(a, "-")
		if strings.HasPrefix(a, "-") {
			if valueFlags[key] && i+1 < len(args) {
				opts[key] = args[i+1]
				i++
			} else {
				opts[key] = ""
			}
			continue
		}
		pos = append(pos, a)
	}
	return opts, pos
}

func boolFlag(opts map[string]string, name string) bool {
	_, ok := opts[name]
	return ok
}

func cliSpawn(eng engine.Engine, args []string) error {
	opts, pos := parseOpts(args, map[string]bool{"cmd": true, "cwd": true, "cols": true, "rows": true})
	if len(pos) < 1 {
		return fmt.Errorf("usage: gpty spawn <name> [--cmd C] [--cwd D] [--cols N] [--rows N]")
	}
	cols, rows := 80, 24
	if v := opts["cols"]; v != "" {
		fmt.Sscanf(v, "%d", &cols)
	}
	if v := opts["rows"]; v != "" {
		fmt.Sscanf(v, "%d", &rows)
	}
	if err := eng.Spawn(pos[0], opts["cmd"], opts["cwd"], cols, rows); err != nil {
		return err
	}
	fmt.Println(pos[0])
	return nil
}

func cliSplit(eng engine.Engine, args []string) error {
	opts, pos := parseOpts(args, map[string]bool{"dir": true, "target": true, "cmd": true, "cwd": true, "percent": true})
	dir := opts["dir"]
	if dir == "" && len(pos) >= 1 {
		dir = pos[0]
	}
	pct := 0
	if v := opts["percent"]; v != "" {
		fmt.Sscanf(v, "%d", &pct)
	}
	id, err := eng.Split(opts["target"], dir, opts["cmd"], opts["cwd"], pct)
	if err != nil {
		return err
	}
	fmt.Println(id)
	return nil
}

func cliWaitFor(eng engine.Engine, args []string) error {
	opts, pos := parseOpts(args, map[string]bool{"timeout": true})
	if len(pos) < 2 {
		return fmt.Errorf("usage: gpty wait-for <name> <pattern> [--timeout S]")
	}
	timeout := 10.0
	if v := opts["timeout"]; v != "" {
		timeout, _ = strconv.ParseFloat(v, 64)
	}
	snap, err := eng.WaitFor(pos[0], pos[1], timeout)
	if err != nil {
		return err
	}
	fmt.Println(snap)
	return nil
}
