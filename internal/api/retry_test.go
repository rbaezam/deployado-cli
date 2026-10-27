package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// These tests pin two things that would silently break the watcher
// loop if they regressed:
//
//   1. parseRetryAfter must correctly handle integer-seconds,
//      HTTP-date, missing, and malformed inputs — and ALWAYS return
//      a bounded, positive duration. A negative / huge wait would
//      either hang the CLI or burn its retry budget instantly.
//
//   2. The request loop must actually transparently recover from
//      transient 429s. The watcher's whole UX depends on it.

func TestParseRetryAfter(t *testing.T) {
	t.Run("integer seconds", func(t *testing.T) {
		got := parseRetryAfter("5", 0)
		if got != 5*time.Second {
			t.Errorf("want 5s, got %v", got)
		}
	})

	t.Run("integer seconds with whitespace", func(t *testing.T) {
		// Real-world: some servers add a leading space.
		got := parseRetryAfter("  7 ", 0)
		if got != 7*time.Second {
			t.Errorf("want 7s, got %v", got)
		}
	})

	t.Run("integer seconds gets capped at the maximum", func(t *testing.T) {
		// Defensive: a misbehaving server suggesting "Retry-After: 3600"
		// shouldn't make us sleep an hour. Cap kicks in.
		got := parseRetryAfter("3600", 0)
		if got != time.Duration(maxRetryAfterSecs)*time.Second {
			t.Errorf("want capped to %ds, got %v", maxRetryAfterSecs, got)
		}
	})

	t.Run("HTTP-date in the near future", func(t *testing.T) {
		future := time.Now().Add(8 * time.Second).UTC().Format(http.TimeFormat)
		got := parseRetryAfter(future, 0)
		// Parsing+wall-clock drift: allow a small window.
		if got < 5*time.Second || got > 12*time.Second {
			t.Errorf("want ~8s, got %v", got)
		}
	})

	t.Run("HTTP-date in the past becomes 1s", func(t *testing.T) {
		past := time.Now().Add(-5 * time.Minute).UTC().Format(http.TimeFormat)
		got := parseRetryAfter(past, 0)
		if got != time.Second {
			t.Errorf("past date should fall through to 1s, got %v", got)
		}
	})

	t.Run("missing header falls back to exponential per attempt", func(t *testing.T) {
		cases := map[int]time.Duration{
			0: 1 * time.Second,
			1: 2 * time.Second,
			2: 4 * time.Second,
		}
		for attempt, want := range cases {
			got := parseRetryAfter("", attempt)
			if got != want {
				t.Errorf("attempt=%d: want %v, got %v", attempt, want, got)
			}
		}
	})

	t.Run("garbage header falls back to exponential", func(t *testing.T) {
		got := parseRetryAfter("not-a-number", 1)
		if got != 2*time.Second {
			t.Errorf("garbage should fall through to exp(1)=2s, got %v", got)
		}
	})
}

func TestRequestRetriesOn429ThenSucceeds(t *testing.T) {
	// The most important regression to pin: a single transient 429
	// must NOT propagate to the caller. The watcher loop dies on
	// errors, so any 429 reaching ListDeployments etc. would kill
	// the user's `--watch` session.

	var attempts int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt32(&attempts, 1)
		if n == 1 {
			// First hit: throttle for 1 second, then they should retry.
			w.Header().Set("Retry-After", "1")
			http.Error(w, `{"error":"rate_limit"}`, http.StatusTooManyRequests)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok": true}`))
	}))
	defer server.Close()

	client := NewClient(server.URL, "fake-token")

	start := time.Now()
	resp, err := client.get("/api/v1/test")
	elapsed := time.Since(start)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		t.Errorf("want 200 after retry, got %d", resp.StatusCode)
	}
	if got := atomic.LoadInt32(&attempts); got != 2 {
		t.Errorf("expected 2 attempts (1 throttled + 1 success), got %d", got)
	}
	// Must have actually waited the suggested Retry-After before
	// retrying. If this is sub-second, the retry didn't honor the
	// header — Traefik would punish us again immediately.
	if elapsed < 900*time.Millisecond {
		t.Errorf("expected to wait ~1s before retry, only waited %v", elapsed)
	}
}

func TestRequestSurfacesPersistent429AfterMaxRetries(t *testing.T) {
	// Symmetric to the previous test: if the server keeps throttling,
	// we eventually give up and surface the 429 — never hang forever.

	var attempts int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&attempts, 1)
		w.Header().Set("Retry-After", "0") // make retries instant for test speed
		http.Error(w, `{"error":"rate_limit"}`, http.StatusTooManyRequests)
	}))
	defer server.Close()

	client := NewClient(server.URL, "fake-token")

	resp, err := client.get("/api/v1/test")
	if err != nil {
		// Should NOT be a network error — should be a successful HTTP response with 429.
		t.Fatalf("unexpected error: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusTooManyRequests {
		t.Errorf("want final 429, got %d", resp.StatusCode)
	}

	got := atomic.LoadInt32(&attempts)
	// maxRetries = 3 attempts total in the loop + 1 final singleAttempt
	// to materialize a readable body for parseError = 4 server hits.
	if got != 4 {
		t.Errorf("want 4 attempts (3 in retry loop + 1 final), got %d", got)
	}
}

func TestRequestDoesNotRetryOnNon429(t *testing.T) {
	// 500/503/etc must NOT enter the retry loop — those are not
	// rate-limit signals and silently retrying would mask real bugs
	// (a corrupted DB, a crashed downstream) AND potentially cause
	// duplicate-write side effects on non-idempotent endpoints.

	var attempts int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&attempts, 1)
		http.Error(w, `{"error":"boom"}`, http.StatusInternalServerError)
	}))
	defer server.Close()

	client := NewClient(server.URL, "fake-token")
	resp, _ := client.get("/api/v1/test")
	defer resp.Body.Close()

	if got := atomic.LoadInt32(&attempts); got != 1 {
		t.Errorf("500 must NOT trigger retries, got %d attempts", got)
	}
	if resp.StatusCode != 500 {
		t.Errorf("want 500, got %d", resp.StatusCode)
	}
}

func TestRequestRetryRebuildsPOSTBody(t *testing.T) {
	// Subtle bug we'd hit without re-readable bodies: the second
	// attempt of a POST would arrive with empty body because the
	// first attempt consumed bytes.NewReader's cursor. Pin the
	// "every attempt sees the same payload" invariant.

	var attempts int32
	var seenBodies []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt32(&attempts, 1)

		// Drain body to compare across attempts.
		buf := make([]byte, 1024)
		read, _ := r.Body.Read(buf)
		seenBodies = append(seenBodies, string(buf[:read]))

		if n < 2 {
			w.Header().Set("Retry-After", "0")
			http.Error(w, `{"error":"rate_limit"}`, http.StatusTooManyRequests)
			return
		}
		w.WriteHeader(200)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer server.Close()

	client := NewClient(server.URL, "fake-token")
	payload := map[string]string{"name": "reservento"}
	resp, err := client.post("/api/v1/test", payload)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer resp.Body.Close()

	if len(seenBodies) < 2 {
		t.Fatalf("expected at least 2 attempts, got %d", len(seenBodies))
	}
	// Both attempts must see the same payload — if the second is
	// empty, the body wasn't re-marshalled correctly.
	if seenBodies[0] != seenBodies[1] {
		t.Errorf("body mismatch across retries: first=%q second=%q",
			seenBodies[0], seenBodies[1])
	}
	if !strings.Contains(seenBodies[0], "reservento") {
		t.Errorf("body looks corrupted: %q", seenBodies[0])
	}
}
