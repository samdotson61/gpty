package keys

import (
	"errors"
	"reflect"
	"testing"
)

// These cases are a direct port of win-pty's tests/test_keys.py (the pure-parse
// subset). The shipped Go parser is now the tested code, per build-plan §7.

func txt(s string) Seg { return Seg{Kind: Text, Val: s} }
func key(s string) Seg { return Seg{Kind: Key, Val: s} }

func TestParseOK(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want []Seg
	}{
		{"plain text", "hello", []Seg{txt("hello")}},
		{"named key", "<Enter>", []Seg{key("Enter")}},
		{"mixed", "hi<Enter>bye", []Seg{txt("hi"), key("Enter"), txt("bye")}},
		{"double lt is literal", "a<<b", []Seg{txt("a<b")}},
		{"ctrl c", "<C-c>", []Seg{key("C-c")}},
		{"shift tab nested", "<S-Tab>", []Seg{key("S-Tab")}},
		{"alt x", "<M-x>", []Seg{key("M-x")}},
		{"f1", "<F1>", []Seg{key("F1")}},
		{"f12", "<F12>", []Seg{key("F12")}},
		{"alias cr", "<CR>", []Seg{key("Enter")}},
		{"alias bs", "<BS>", []Seg{key("BSpace")}},
		{"alias pgup", "<PgUp>", []Seg{key("PageUp")}},
		{"alias del", "<Del>", []Seg{key("DC")}},
		{"nested ctrl enter", "<C-Enter>", []Seg{key("C-Enter")}},
		{"real command", "echo hi<Enter>", []Seg{txt("echo hi"), key("Enter")}},
		{"trailing double lt", "x<<", []Seg{txt("x<")}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := Parse(c.in)
			if err != nil {
				t.Fatalf("Parse(%q) unexpected error: %v", c.in, err)
			}
			if !reflect.DeepEqual(got, c.want) {
				t.Fatalf("Parse(%q) = %+v, want %+v", c.in, got, c.want)
			}
		})
	}
}

func TestParseErrors(t *testing.T) {
	cases := []struct{ name, in string }{
		{"unknown token", "<NotARealKey>"},
		{"empty token", "<>"},
		{"unterminated", "hello<Enter"},
		{"bad modifier rest", "<C-nope>"}, // multi-char non-key after modifier
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := Parse(c.in)
			if err == nil {
				t.Fatalf("Parse(%q) expected error, got nil", c.in)
			}
			var pe *ParseError
			if !errors.As(err, &pe) {
				t.Fatalf("Parse(%q) error = %T, want *ParseError", c.in, err)
			}
		})
	}
}
