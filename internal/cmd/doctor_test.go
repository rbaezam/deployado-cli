package cmd

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/deployado/cli/internal/api"
)

// TestErrorLogPattern locks down the substrings doctor uses to flag
// "this log line looks like a runtime failure". A regression here would
// silently make the recent-logs check stop catching things — exactly the
// kind of slow drift that's hard to spot in production, so we pin it.
func TestErrorLogPattern(t *testing.T) {
	hits := []string{
		"ERROR: something blew up",
		"Unhandled exception in worker",
		"panic: runtime error: invalid memory address",
		"FATAL: connection refused",
		"Traceback (most recent call last):",
		"TypeError: undefined is not a function",
		"could not connect to database: cannot connect",
		"goroutine stack trace:",
	}
	for _, line := range hits {
		if !errorLogPattern.MatchString(line) {
			t.Errorf("expected pattern to match %q, didn't", line)
		}
	}

	misses := []string{
		"GET /healthz 200 4ms",
		"[info] booting application",
		"User logged in successfully",
		"",
	}
	for _, line := range misses {
		if errorLogPattern.MatchString(line) {
			t.Errorf("expected pattern NOT to match %q, but it did", line)
		}
	}
}

// TestHasFailures: any single fail trips the exit-1 path. Pass/info/warn
// alone must not. This is what gates the CLI's exit code, so the precise
// boolean matters for CI scripts that call `deployado doctor`.
func TestHasFailures(t *testing.T) {
	cases := []struct {
		name string
		in   []checkResult
		want bool
	}{
		{"empty", nil, false},
		{"all pass", []checkResult{
			{Status: statusPass},
			{Status: statusPass},
		}, false},
		{"warn only", []checkResult{
			{Status: statusWarn},
			{Status: statusInfo},
		}, false},
		{"one fail among passes", []checkResult{
			{Status: statusPass},
			{Status: statusFail},
			{Status: statusPass},
		}, true},
		{"only fail", []checkResult{
			{Status: statusFail},
		}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := hasFailures(tc.in); got != tc.want {
				t.Errorf("hasFailures = %v, want %v", got, tc.want)
			}
		})
	}
}

// TestAsFloat pins the JSON-number coercion used by the live-metrics
// check. encoding/json gives back float64 for all numeric JSON, but
// the helper handles int/int64/float32 too as belt-and-suspenders.
// A regression here would be silent (zero metrics displayed as 0%)
// AND would hide real high-CPU situations behind a green check.
func TestAsFloat(t *testing.T) {
	cases := map[string]struct {
		in   any
		want float64
	}{
		"float64 passthrough":   {float64(42.5), 42.5},
		"float32 widening":      {float32(3.14), 3.140000104904175}, // float32 imprecision is expected
		"int":                   {7, 7.0},
		"int64":                 {int64(99), 99.0},
		"nil yields zero":       {nil, 0.0},
		"string yields zero":    {"oops", 0.0},
		"map yields zero":       {map[string]any{}, 0.0},
		"missing JSON key path": {(any)(nil), 0.0},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			got := asFloat(tc.in)
			if got != tc.want {
				t.Errorf("asFloat(%v) = %v, want %v", tc.in, got, tc.want)
			}
		})
	}
}

// TestMetricsStale: the "is this snapshot fresh enough to trust"
// gate. >2 min means the MetricsCollectorWorker missed at least one
// tick — the values shouldn't be displayed as authoritative.
// Malformed input must degrade to "not stale" so we don't false-
// alarm and downgrade a healthy app's reading.
func TestMetricsStale(t *testing.T) {
	now := time.Now()

	cases := map[string]struct {
		ts   string
		want bool
	}{
		"fresh (just now)":          {now.Format(time.RFC3339Nano), false},
		"fresh (30s ago)":           {now.Add(-30 * time.Second).Format(time.RFC3339Nano), false},
		"fresh (just under 2m)":     {now.Add(-119 * time.Second).Format(time.RFC3339Nano), false},
		"stale (just over 2m)":      {now.Add(-121 * time.Second).Format(time.RFC3339Nano), true},
		"stale (10m ago)":           {now.Add(-10 * time.Minute).Format(time.RFC3339Nano), true},
		"malformed degrades to not stale": {"not-a-timestamp", false},
		"empty degrades to not stale":     {"", false},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			got := metricsStale(tc.ts)
			if got != tc.want {
				t.Errorf("metricsStale(%q) = %v, want %v", tc.ts, got, tc.want)
			}
		})
	}
}

// TestTallyDeploymentStatuses: only current deployments count.
// Easy to regress if someone "simplifies" the loop and drops the
// IsCurrent gate — then doctor would start reporting historical
// failures as live instance health, which is exactly the noise
// users complained about in C1 design discussions.
func TestTallyDeploymentStatuses(t *testing.T) {
	deps := []api.Deployment{
		{IsCurrent: true, Status: "healthy"},
		{IsCurrent: true, Status: "degraded"},
		// Historical failure must NOT inflate the failed count.
		{IsCurrent: false, Status: "failed"},
		{IsCurrent: true, Status: "failed"},
		// Old healthy rollouts also don't count.
		{IsCurrent: false, Status: "healthy"},
	}

	current, healthy, degraded, failed := tallyDeploymentStatuses(deps)

	if current != 3 {
		t.Errorf("current: want 3, got %d", current)
	}
	if healthy != 1 {
		t.Errorf("healthy: want 1, got %d", healthy)
	}
	if degraded != 1 {
		t.Errorf("degraded: want 1, got %d", degraded)
	}
	if failed != 1 {
		t.Errorf("failed: want 1, got %d", failed)
	}
}

// TestClassifyInstanceHealth: the priority order matters here.
// "Zero current" must beat "all healthy" (trivially can't both be
// true) and "all down" must beat the generic "mixed" branch so the
// fail status is reachable when every instance is dead. The order
// in the switch is load-bearing — pin it.
func TestClassifyInstanceHealth(t *testing.T) {
	cases := []struct {
		name                              string
		current, healthy, degraded, failed int
		wantStatus                        checkStatus
		wantMsgContains                   string
	}{
		{"no current deployments", 0, 0, 0, 0, statusFail, "not serving traffic"},
		{"all healthy", 3, 3, 0, 0, statusPass, "3/3 healthy"},
		{"single healthy", 1, 1, 0, 0, statusPass, "1/1 healthy"},
		{"all degraded (zero healthy)", 2, 0, 2, 0, statusFail, "0/2 healthy"},
		{"all failed (zero healthy)", 1, 0, 0, 1, statusFail, "0/1 healthy"},
		{"mixed: one healthy one degraded", 2, 1, 1, 0, statusWarn, "1/2 healthy"},
		{"mixed: 2 of 3 healthy", 3, 2, 1, 0, statusWarn, "2/3 healthy"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gotStatus, gotMsg, _ := classifyInstanceHealth(
				tc.current, tc.healthy, tc.degraded, tc.failed, "app-id")
			if gotStatus != tc.wantStatus {
				t.Errorf("status: want %v, got %v", tc.wantStatus, gotStatus)
			}
			if !strings.Contains(gotMsg, tc.wantMsgContains) {
				t.Errorf("message %q does not contain %q", gotMsg, tc.wantMsgContains)
			}
		})
	}
}

// TestCheckStatusJSON pins down the JSON contract — third-party scripts
// will key off these strings, so reordering the iota constants must not
// change the output. If you add a new status, add it here AND extend
// MarshalJSON; otherwise the new status leaks through as "unknown".
func TestCheckStatusJSON(t *testing.T) {
	cases := map[checkStatus]string{
		statusPass: `"pass"`,
		statusInfo: `"info"`,
		statusWarn: `"warn"`,
		statusFail: `"fail"`,
	}
	for s, want := range cases {
		got, err := json.Marshal(s)
		if err != nil {
			t.Fatalf("marshal %v: %v", s, err)
		}
		if string(got) != want {
			t.Errorf("status %v: marshalled to %s, want %s", s, got, want)
		}
	}
}

// TestSinceStr: this is purely cosmetic, but the bucketing is what
// makes the doctor's output readable ("11m ago" vs "0.183 hours ago").
// Unparseable input must fall through to "recently" — never empty,
// never the raw timestamp.
func TestSinceStr(t *testing.T) {
	now := time.Now()

	cases := []struct {
		name     string
		input    string
		contains string
	}{
		{"30 seconds ago", now.Add(-30 * time.Second).Format(time.RFC3339Nano), "s ago"},
		{"5 minutes ago", now.Add(-5 * time.Minute).Format(time.RFC3339Nano), "min ago"},
		{"3 hours ago", now.Add(-3 * time.Hour).Format(time.RFC3339Nano), "h ago"},
		{"2 days ago", now.Add(-48 * time.Hour).Format(time.RFC3339Nano), "d ago"},
		{"garbage", "not-a-timestamp", "recently"},
		{"empty", "", "recently"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := sinceStr(tc.input)
			if !strings.Contains(got, tc.contains) {
				t.Errorf("sinceStr(%q) = %q, expected to contain %q",
					tc.input, got, tc.contains)
			}
		})
	}
}
