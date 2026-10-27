package cmd

import (
	"os/exec"
	"strings"
	"testing"
)

// TestShellQuote covers the round-trip property we actually care about:
// for any input, `sh -c 'echo <quoted>'` must print the original string.
// We assert that empirically by shelling out, and also pin the exact
// output for the common shapes so a regression in the encoding is
// caught by a diff, not just by an integration test.
func TestShellQuote(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"empty string", "", "''"},
		{"safe alnum", "foo", "foo"},
		{"safe path", "/usr/bin/python3", "/usr/bin/python3"},
		{"safe punctuation", "a-b.c_d:e@f+g=h,i%j/k", "a-b.c_d:e@f+g=h,i%j/k"},

		{"space", "hello world", "'hello world'"},
		{"dollar sign", "$HOME", `'$HOME'`},
		{"pipe", "a|b", `'a|b'`},
		{"semicolon", "a;b", `'a;b'`},
		{"ampersand", "a&b", `'a&b'`},
		{"parens", "print(hi)", `'print(hi)'`},
		{"double quote", `say "hi"`, `'say "hi"'`},
		{"backtick", "`whoami`", "'`whoami`'"},
		{"backslash", `a\b`, `'a\b'`},
		{"newline", "a\nb", "'a\nb'"},
		{"glob", "*.go", `'*.go'`},
		{"question mark", "a?", `'a?'`},
		{"redirect", "a>b", `'a>b'`},

		// The hard case: single quotes themselves. The classic POSIX
		// trick is to terminate the quoted string, emit an escaped
		// quote, and re-open. Verified by hand below and round-tripped
		// in TestShellQuoteRoundtrip.
		{"single quote", "'", `''\'''`},
		{"python with single quotes", "print('hi')", `'print('\''hi'\'')'`},
		{"mixed", "it's $5", `'it'\''s $5'`},
		{"only single quotes", "'''", `''\'''\'''\'''`},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := shellQuote(tc.in)
			if got != tc.want {
				t.Errorf("shellQuote(%q)\n  got:  %s\n  want: %s", tc.in, got, tc.want)
			}
		})
	}
}

// TestShellQuoteRoundtrip is the property-based check: for every input,
// `sh -c "printf %s <quoted>"` must produce the original bytes. This is
// the actual invariant the rest of `deployado exec` relies on.
func TestShellQuoteRoundtrip(t *testing.T) {
	inputs := []string{
		"",
		"foo",
		"hello world",
		"print('hi')",
		`echo "with quotes"`,
		"$HOME",
		"a|b;c&d",
		"path/with spaces/and-dashes",
		"unicode: ñ ü 漢字 emoji 🚀",
		"single ' quote",
		"only '''",
		`backslash \ here`,
		"newline\nin\nmiddle",
		"tab\there",
		"*.{go,py}",
		"$(whoami)",
		"`pwd`",
	}

	for _, in := range inputs {
		t.Run(in, func(t *testing.T) {
			quoted := shellQuote(in)
			// printf %s — no escape interpretation, just emit the bytes.
			cmd := exec.Command("sh", "-c", "printf %s "+quoted)
			out, err := cmd.Output()
			if err != nil {
				t.Fatalf("sh failed: %v (quoted=%s)", err, quoted)
			}
			if string(out) != in {
				t.Errorf("roundtrip mismatch\n  in:      %q\n  quoted:  %s\n  shellOut:%q",
					in, quoted, string(out))
			}
		})
	}
}

// TestShellQuoteArgvRoundtrip verifies the multi-arg case end-to-end:
// joining N quoted args with spaces and parsing them back through a
// POSIX shell yields exactly the original argv. This is what `deployado
// exec` actually does on the wire.
func TestShellQuoteArgvRoundtrip(t *testing.T) {
	argvs := [][]string{
		{"python", "-c", "print('hello world')"},
		{"sh", "-c", "env | grep DB"},
		{"echo", "$HOME", "is", "set"},
		{"mix", "ecto.migrate", "--quiet"},
		{"psql", "-c", `SELECT * FROM users WHERE name='O''Brien'`},
		{"node", "-e", `console.log("hello, world")`},
	}

	for _, argv := range argvs {
		t.Run(strings.Join(argv, " "), func(t *testing.T) {
			quoted := make([]string, len(argv))
			for i, a := range argv {
				quoted[i] = shellQuote(a)
			}
			joined := strings.Join(quoted, " ")

			// Use a tiny shell wrapper that prints each arg on its own
			// line with a NUL terminator so we can split unambiguously
			// even when args contain newlines.
			script := `for a in ` + joined + `; do printf %s "$a"; printf '\0'; done`
			cmd := exec.Command("sh", "-c", script)
			out, err := cmd.Output()
			if err != nil {
				t.Fatalf("sh failed: %v (script=%s)", err, script)
			}

			gotParts := strings.Split(string(out), "\x00")
			// Trailing empty element from the final NUL — drop it.
			if len(gotParts) > 0 && gotParts[len(gotParts)-1] == "" {
				gotParts = gotParts[:len(gotParts)-1]
			}

			if len(gotParts) != len(argv) {
				t.Fatalf("argc mismatch\n  wanted %d args: %q\n  got    %d args: %q\n  script: %s",
					len(argv), argv, len(gotParts), gotParts, script)
			}
			for i := range argv {
				if gotParts[i] != argv[i] {
					t.Errorf("arg[%d] mismatch\n  in:    %q\n  out:   %q\n  script:%s",
						i, argv[i], gotParts[i], script)
				}
			}
		})
	}
}

func TestIsShellSafe(t *testing.T) {
	safe := []string{"", "a", "abc", "Foo123", "a-b", "a_b", "a.b", "a/b",
		"a:b", "a@b", "a+b", "a=b", "a,b", "a%b", "/usr/bin/python3"}
	unsafe := []string{" ", "a b", "$x", "'", `"`, "`", "*", "?", "|",
		";", "&", "(", ")", "{", "}", "[", "]", "<", ">", "\\", "!",
		"#", "~", "\n", "\t"}

	for _, s := range safe {
		if !isShellSafe(s) {
			t.Errorf("isShellSafe(%q) = false, want true", s)
		}
	}
	for _, s := range unsafe {
		if isShellSafe(s) {
			t.Errorf("isShellSafe(%q) = true, want false", s)
		}
	}
}
