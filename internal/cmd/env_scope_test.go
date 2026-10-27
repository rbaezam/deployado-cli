package cmd

import "testing"

func TestEnvScopeForKey(t *testing.T) {
	tests := []struct {
		key      string
		expected string
	}{
		{key: "VITE_API_URL", expected: "build"},
		{key: "next_public_api_url", expected: "build"},
		{key: "REACT_APP_API_URL", expected: "build"},
		{key: "DATABASE_URL", expected: "runtime"},
	}

	for _, tt := range tests {
		if got := envScopeForKey(tt.key); got != tt.expected {
			t.Fatalf("envScopeForKey(%q) = %q, want %q", tt.key, got, tt.expected)
		}
	}
}

func TestSplitEnvByScope(t *testing.T) {
	env := map[string]string{
		"VITE_API_URL": "https://api.example.com",
		"DATABASE_URL": "postgres://db",
		"PORT":         "3000",
	}

	runtimeEnv, buildEnv := splitEnvByScope(env)

	if len(buildEnv) != 1 || buildEnv["VITE_API_URL"] == "" {
		t.Fatalf("expected VITE_API_URL in build scope, got: %#v", buildEnv)
	}

	if len(runtimeEnv) != 2 || runtimeEnv["DATABASE_URL"] == "" || runtimeEnv["PORT"] == "" {
		t.Fatalf("expected DATABASE_URL and PORT in runtime scope, got: %#v", runtimeEnv)
	}
}
