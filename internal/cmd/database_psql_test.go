package cmd

import (
	"os"
	"testing"
)

// envDefault is the tiny helper that decides whether to provide a
// default value for an env var or pass through whatever the user
// already set. It looks trivial, but a regression here would either
// silently clobber the user's pager/history config (annoying for
// power users) or fail to set a default at all (worse UX for new
// users).
func TestEnvDefault(t *testing.T) {
	t.Run("returns default when key is unset", func(t *testing.T) {
		key := "DEPLOYADO_PSQL_TEST_UNSET"
		_ = os.Unsetenv(key) // belt and suspenders
		got := envDefault(key, "fallback")
		want := key + "=fallback"
		if got != want {
			t.Errorf("got %q, want %q", got, want)
		}
	})

	t.Run("preserves existing value when key is set", func(t *testing.T) {
		key := "DEPLOYADO_PSQL_TEST_SET"
		_ = os.Setenv(key, "user-chose-this")
		t.Cleanup(func() { _ = os.Unsetenv(key) })

		got := envDefault(key, "would-overwrite")
		want := key + "=user-chose-this"
		if got != want {
			t.Errorf("envDefault overwrote a user-set value: got %q, want %q", got, want)
		}
	})

	t.Run("preserves empty-string value when key is explicitly set to empty", func(t *testing.T) {
		// An empty PSQL_PAGER means "no pager" — a deliberate user
		// choice. We must not silently replace it with "less -SRX".
		key := "DEPLOYADO_PSQL_TEST_EMPTY"
		_ = os.Setenv(key, "")
		t.Cleanup(func() { _ = os.Unsetenv(key) })

		got := envDefault(key, "would-overwrite")
		want := key + "="
		if got != want {
			t.Errorf("envDefault overwrote an explicit empty value: got %q, want %q", got, want)
		}
	})
}

// isPrivateHost is the pre-flight check that prevents the 30-second
// "Connection refused" stall when the API returns an unreachable
// private IP. The classifier needs to match RFC 1918 ranges
// correctly — a regression here would either: silently let private
// connections fail with a worse error (false negative), or block
// legitimate public connections (false positive). Both are bad.
func TestIsPrivateHost(t *testing.T) {
	cases := map[string]bool{
		// RFC 1918 — the canonical private ranges:
		"10.0.0.5":     true, // Hetzner private (the actual case that motivated the check)
		"10.255.0.1":   true,
		"172.16.0.1":   true,
		"172.31.255.1": true,
		"192.168.1.1":  true,

		// Boundaries — these are PUBLIC:
		"172.15.0.1": false, // just below 172.16
		"172.32.0.1": false, // just above 172.31
		"9.0.0.1":    false, // just below 10.x
		"11.0.0.1":   false, // just above 10.x

		// Loopback variants:
		"127.0.0.1": true,
		"127.0.0.2": true,
		"localhost": true,

		// Link-local (uncommon but technically unreachable):
		"169.254.1.1": true,

		// Public IPs:
		"8.8.8.8":         false,
		"1.1.1.1":         false,
		"203.0.113.10":    false, // public (documentation range)
		"198.51.100.20":  false, // public (documentation range)
		"203.0.113.1":     false, // TEST-NET-3 but routable-looking

		// Hostnames — pass through as public (we don't DNS-resolve):
		"db.deployado.com":     false,
		"pg.internal":          false, // .internal TLD — psql will fail, that's fine
		"customer-db.acme.com": false,

		// Edge cases:
		"":             false, // empty: treat as public; psql will fail clearly
		"::1":          true,  // IPv6 loopback
		"fd00::1":      true,  // IPv6 ULA (Unique Local Address)
		"2001:db8::1":  false, // public IPv6 (documentation range, but treated as public)
	}

	for host, want := range cases {
		got := isPrivateHost(host)
		if got != want {
			t.Errorf("isPrivateHost(%q) = %v, want %v", host, got, want)
		}
	}
}
