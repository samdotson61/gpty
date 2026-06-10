//go:build !windows

package platform

import (
	"os"
	"path/filepath"
)

const listSep = ":"

// defaultBin is the last resort when tmux isn't on PATH (PATH lookup already
// failed). Returning the bare name yields a clear "executable file not found"
// rather than a wrong absolute guess. `gpty doctor` gives the real fix.
func defaultBin() string { return "tmux" }

// executableIn returns dir/name if it is an executable regular file.
func executableIn(dir, name string) string {
	p := filepath.Join(dir, name)
	if fi, err := os.Stat(p); err == nil && fi.Mode().IsRegular() && fi.Mode().Perm()&0111 != 0 {
		return p
	}
	return ""
}

// osEnv on Unix: the inherited environment with a guaranteed UTF-8 locale and
// the executable's own directory on PATH. There is no cygwin pseudo-console, so
// the interactive flag is irrelevant.
func osEnv(interactive bool) []string {
	env := os.Environ()
	env = ensureUTF8(env)
	env = pathWith(env, exeDir(), filepath.Dir(Bin()))
	return env
}
