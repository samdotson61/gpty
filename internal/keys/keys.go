// Package keys splits a send payload into literal-text and named-key segments,
// mapping named keys to tmux send-keys key names.
//
// Grammar (compat lock §4.3 of the build plan — frozen so existing win-pty
// muscle memory keeps working):
//
//	literal text            -> typed verbatim
//	<<                      -> a literal "<"
//	<Enter> <CR>            -> Enter        <Esc>/<Escape> -> Escape
//	<Tab>                   -> Tab          <Space>        -> Space
//	<BS>/<Backspace>        -> BSpace       <Del>/<Delete> -> DC
//	<Up> <Down> <Left> <Right> <Home> <End> <PgUp> <PgDn>
//	<F1>..<F12>             -> F1..F12
//	<C-x> <S-x> <M-x>       -> Ctrl/Shift/Alt + x (single char or nested named key)
//
// This is a faithful Go port of win-pty's agent_pty/keys.py (the canonical
// behavior — the two earlier implementations had drifted; this is the merge).
package keys

import (
	"fmt"
	"strings"
)

// Kind distinguishes a literal-text segment from a named-key segment.
type Kind int

const (
	Text Kind = iota // literal characters, typed verbatim
	Key              // a tmux key name, e.g. "Enter" or "C-c"
)

// Seg is one parsed segment of a send payload.
type Seg struct {
	Kind Kind
	Val  string // literal text, or a tmux key name
}

// ParseError is returned for malformed payloads (unknown/empty/unterminated
// tokens). It mirrors win-pty's KeyParseError.
type ParseError struct{ Msg string }

func (e *ParseError) Error() string { return e.Msg }

func perr(format string, a ...any) error { return &ParseError{Msg: fmt.Sprintf(format, a...)} }

// tokenMap maps our lowercase token names to tmux send-keys key names.
var tokenMap = map[string]string{
	"enter": "Enter", "cr": "Enter",
	"esc": "Escape", "escape": "Escape",
	"tab": "Tab", "space": "Space",
	"bs": "BSpace", "bspace": "BSpace", "backspace": "BSpace",
	"up": "Up", "down": "Down", "left": "Left", "right": "Right",
	"home": "Home", "end": "End",
	"pgup": "PageUp", "pageup": "PageUp",
	"pgdn": "PageDown", "pagedown": "PageDown",
	"del": "DC", "delete": "DC",
}

func init() {
	for i := 1; i <= 12; i++ {
		tokenMap[fmt.Sprintf("f%d", i)] = fmt.Sprintf("F%d", i)
	}
}

// resolve turns a token (the text between < and >) into a tmux key name.
func resolve(token string) (string, error) {
	if v, ok := tokenMap[strings.ToLower(token)]; ok {
		return v, nil
	}
	// Modifier combo: ^([CSM])-(.+)$ (case-insensitive on the modifier).
	if len(token) >= 3 && token[1] == '-' {
		var mod byte
		switch token[0] {
		case 'c', 'C':
			mod = 'C'
		case 's', 'S':
			mod = 'S'
		case 'm', 'M':
			mod = 'M'
		}
		if mod != 0 {
			rest := token[2:]
			// Allow a nested named key after the modifier (e.g. <C-Enter>),
			// else a single literal char (e.g. <C-c>).
			if v, ok := tokenMap[strings.ToLower(rest)]; ok {
				return string(mod) + "-" + v, nil
			}
			if len([]rune(rest)) == 1 {
				return string(mod) + "-" + rest, nil
			}
		}
	}
	return "", perr("unknown key token: <%s>", token)
}

// Parse splits text into ordered Text/Key segments. "<<" escapes a literal "<".
func Parse(text string) ([]Seg, error) {
	var segs []Seg
	var buf strings.Builder
	flush := func() {
		if buf.Len() > 0 {
			segs = append(segs, Seg{Kind: Text, Val: buf.String()})
			buf.Reset()
		}
	}
	i, n := 0, len(text)
	for i < n {
		c := text[i]
		if c == '<' {
			if i+1 < n && text[i+1] == '<' { // "<<" -> literal "<"
				buf.WriteByte('<')
				i += 2
				continue
			}
			rel := strings.IndexByte(text[i+1:], '>')
			if rel == -1 {
				return nil, perr("unterminated `<` at position %d", i)
			}
			end := i + 1 + rel
			token := text[i+1 : end]
			if token == "" {
				return nil, perr("empty token `<>` at position %d", i)
			}
			name, err := resolve(token)
			if err != nil {
				return nil, err
			}
			flush()
			segs = append(segs, Seg{Kind: Key, Val: name})
			i = end + 1
			continue
		}
		buf.WriteByte(c)
		i++
	}
	flush()
	return segs, nil
}
