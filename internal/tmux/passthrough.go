// Passthrough makes every tmux command usable as a gpty subcommand: anything
// gpty doesn't recognize itself is checked against the live tmux command table
// (`list-commands`, so the set always matches the installed tmux) and, if it's
// a real command or alias, forwarded as-is. gpty's own names win on collision.
package tmux

import "strings"

// Commands returns the set of command names and aliases the installed tmux
// understands. `list-commands` is answered without a running server, so this
// works even before the first session exists.
func Commands() (map[string]bool, error) {
	out, _, err := RunCapture("list-commands", "-F", "#{command_list_name} #{command_list_alias}")
	if err != nil {
		return nil, err
	}
	return parseCommandList(out), nil
}

// IsCommand reports whether name is a tmux command or alias. A failed table
// query (tmux missing/broken) reports false — the caller's unknown-command
// error is the right message there, and `gpty doctor` diagnoses the rest.
func IsCommand(name string) bool {
	set, err := Commands()
	return err == nil && set[name]
}

// parseCommandList parses `list-commands -F "#{command_list_name}
// #{command_list_alias}"` output: one name per line, alias beside it when the
// command has one.
func parseCommandList(out string) map[string]bool {
	set := map[string]bool{}
	for _, line := range strings.Split(out, "\n") {
		for _, f := range strings.Fields(line) {
			set[f] = true
		}
	}
	return set
}

// Passthrough runs name+args exactly as `tmux <name> <args...>` would, with
// the real console attached so output, prompts, and exit status are native.
// Attaching commands get the interactive environment (Windows: cygwin pcon —
// see platform.osEnv); everything else keeps noglob so format-string args
// survive cygwin. Returns tmux's exit code.
func Passthrough(name string, args []string) int {
	full := append([]string{name}, args...)
	if isAttaching(name, args) {
		return Interactive(full...)
	}
	return Console(full...)
}

// isAttaching reports whether the command will attach a client to this
// terminal: attach-session always; new-session unless detached with -d.
func isAttaching(name string, args []string) bool {
	switch name {
	case "attach-session", "attach":
		return true
	case "new-session", "new":
		for _, a := range args {
			if a == "-d" {
				return false
			}
		}
		return true
	}
	return false
}
