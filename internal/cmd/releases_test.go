package cmd

import "testing"

// TestLastNLines pins the trailing-N-lines helper that --tail relies on.
// The walk-backwards implementation is more efficient than splitting on
// newlines but it's easy to get the boundary conditions wrong (empty
// input, fewer lines than requested, missing trailing newline) — exactly
// the cases that show up in real build transcripts.
func TestLastNLines(t *testing.T) {
	cases := []struct {
		name string
		in   string
		n    int
		want string
	}{
		{"empty input", "", 5, ""},
		{"n=0 returns full input", "a\nb\nc\n", 0, "a\nb\nc\n"},
		{"n negative returns full input (defensive)", "a\nb\nc\n", -3, "a\nb\nc\n"},
		{
			"fewer lines than requested returns full input",
			"only one line\n", 5,
			"only one line\n",
		},
		{
			"exact line count returns full input",
			"a\nb\nc\n", 3,
			"a\nb\nc\n",
		},
		{
			"trims to last n with trailing newline",
			"a\nb\nc\nd\ne\n", 2,
			"d\ne\n",
		},
		{
			"trims correctly when last line has no trailing newline",
			"a\nb\nc\nd", 2,
			"c\nd",
		},
		{
			"single line no newline",
			"the whole thing", 10,
			"the whole thing",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := lastNLines(tc.in, tc.n)
			if got != tc.want {
				t.Errorf("lastNLines(%q, %d) = %q, want %q", tc.in, tc.n, got, tc.want)
			}
		})
	}
}
