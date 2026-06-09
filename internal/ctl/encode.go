package ctl

import "strings"

// encodeCommand joins command words into one control-mode line, quoting any
// word that would otherwise confuse tmux's command lexer.
//
// The critical case (verified live) is `#`: it starts a comment, so a format
// string like #{session_name} must be quoted or the preceding -F sees no
// argument. We also quote spaces and the other lexer-significant characters.
// Literal user text never reaches here — Send routes it through send-keys -H —
// so quoting only ever wraps our own fixed flags, targets, and format strings.
func encodeCommand(args []string) string {
	parts := make([]string, len(args))
	for i, a := range args {
		parts[i] = quoteArg(a)
	}
	return strings.Join(parts, " ")
}

// needsQuote reports whether arg contains anything special to tmux's lexer.
func needsQuote(arg string) bool {
	if arg == "" {
		return true
	}
	return strings.ContainsAny(arg, " \t\r\n\"'#;\\{}$")
}

// quoteArg wraps arg in double quotes when needed, escaping " and \. It does
// NOT escape # or $ inside the quotes, so format strings (#{...}) still expand
// for -F — quoting only stops # from being read as a comment.
func quoteArg(arg string) string {
	if !needsQuote(arg) {
		return arg
	}
	var b strings.Builder
	b.WriteByte('"')
	for i := 0; i < len(arg); i++ {
		switch arg[i] {
		case '"', '\\':
			b.WriteByte('\\')
		}
		b.WriteByte(arg[i])
	}
	b.WriteByte('"')
	return b.String()
}
