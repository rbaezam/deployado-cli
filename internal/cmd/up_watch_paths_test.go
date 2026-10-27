package cmd

import "testing"

func TestWatchPathsForServicePath(t *testing.T) {
	tests := []struct {
		name        string
		servicePath string
		want        []string
	}{
		{name: "empty path", servicePath: "", want: []string{}},
		{name: "root dot", servicePath: ".", want: []string{}},
		{name: "root dot slash", servicePath: "./", want: []string{}},
		{name: "relative subdir", servicePath: "apps/frontend", want: []string{"apps/frontend/**"}},
		{name: "relative with dot prefix", servicePath: "./apps/api", want: []string{"apps/api/**"}},
		{name: "trims slashes", servicePath: "/apps/web/", want: []string{"apps/web/**"}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := watchPathsForServicePath(tc.servicePath)
			if len(got) != len(tc.want) {
				t.Fatalf("watchPathsForServicePath(%q) len=%d want=%d", tc.servicePath, len(got), len(tc.want))
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Fatalf("watchPathsForServicePath(%q)[%d]=%q want=%q", tc.servicePath, i, got[i], tc.want[i])
				}
			}
		})
	}
}
