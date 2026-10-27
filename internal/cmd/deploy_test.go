package cmd

import (
	"testing"

	"github.com/deployado/cli/internal/api"
)

// TestNextBuildLogChunk walks through the watcher's tail loop without
// any network I/O. The cursor logic is the bit most likely to regress
// (off-by-one, double-printing, panic on truncation) and it's also
// the bit that produces the most user-visible damage when wrong —
// either a wall of duplicated output or a frozen-looking watcher
// that's silently dropping lines.
func TestNextBuildLogChunk(t *testing.T) {
	t.Run("empty buffer, zero cursor", func(t *testing.T) {
		c, s := nextBuildLogChunk("", 0)
		if c != 0 || s != "" {
			t.Fatalf("want (0, \"\"), got (%d, %q)", c, s)
		}
	})

	t.Run("first chunk, fresh cursor", func(t *testing.T) {
		c, s := nextBuildLogChunk("Step 1/4\n", 0)
		if c != 9 {
			t.Errorf("cursor: want 9, got %d", c)
		}
		if s != "Step 1/4\n" {
			t.Errorf("suffix: want full buffer, got %q", s)
		}
	})

	t.Run("appended chunk, cursor advances", func(t *testing.T) {
		// Simulates: first tick saw "Step 1/4\n", second tick has
		// "Step 1/4\nStep 2/4\n" — we should print only "Step 2/4\n".
		buffer := "Step 1/4\nStep 2/4\n"
		c, s := nextBuildLogChunk(buffer, 9)
		if c != 18 {
			t.Errorf("cursor: want 18, got %d", c)
		}
		if s != "Step 2/4\n" {
			t.Errorf("suffix: want only the new line, got %q", s)
		}
	})

	t.Run("nothing new since last tick", func(t *testing.T) {
		// Critical: the polling loop calls this every 2s even when no
		// new output arrived. Must NOT re-print anything and must NOT
		// shift the cursor.
		buffer := "Step 1/4\n"
		c, s := nextBuildLogChunk(buffer, 9)
		if c != 9 {
			t.Errorf("cursor must hold steady: want 9, got %d", c)
		}
		if s != "" {
			t.Errorf("suffix must be empty when nothing new: got %q", s)
		}
	})

	t.Run("cursor beyond buffer (truncation defense)", func(t *testing.T) {
		// Shouldn't happen with a correct API but cheap to be safe:
		// if the buffer shrinks (different release, stale cache, bug)
		// we reset to the full new buffer instead of slicing into
		// negative space and panicking.
		c, s := nextBuildLogChunk("short", 999)
		if c != 5 {
			t.Errorf("cursor: want 5 (full buffer length), got %d", c)
		}
		if s != "short" {
			t.Errorf("suffix: want full buffer, got %q", s)
		}
	})

	t.Run("negative cursor (defense)", func(t *testing.T) {
		c, s := nextBuildLogChunk("hello", -1)
		if c != 5 {
			t.Errorf("cursor: want 5, got %d", c)
		}
		if s != "hello" {
			t.Errorf("suffix: want full buffer, got %q", s)
		}
	})

	t.Run("partial line at end (no trailing newline)", func(t *testing.T) {
		// Docker build output is line-buffered upstream but the last
		// chunk arriving mid-line is realistic. We print the partial
		// line; the next tick will pick up the rest as a fresh suffix.
		c, s := nextBuildLogChunk("RUN apk add cur", 0)
		if c != 15 || s != "RUN apk add cur" {
			t.Errorf("want (15, full), got (%d, %q)", c, s)
		}

		// Next tick — line completes.
		c2, s2 := nextBuildLogChunk("RUN apk add curl\n", c)
		if c2 != 17 {
			t.Errorf("cursor: want 17, got %d", c2)
		}
		if s2 != "l\n" {
			t.Errorf("suffix: want \"l\\n\", got %q", s2)
		}
	})
}

// TestShouldStreamRuntime pins the status-based gate that decides
// whether we hit the GetLogs endpoint each tick. The "pending" case
// matters most: the container doesn't exist yet so the agent will
// 404, and we don't want the watcher loop to swallow a wall of
// log-fetch failures every 2s for the first 30s of a deploy.
func TestShouldStreamRuntime(t *testing.T) {
	cases := map[string]bool{
		"":           false, // defensive: zero value should not trigger I/O
		"pending":    false,
		"preparing":  true,
		"running":    true,
		"healthy":    true, // drain pass at end-of-deploy
		"failed":     true, // user wants the crash output
		"degraded":   true,
		"some-other": true, // unknown statuses err on the side of fetching
	}
	for status, want := range cases {
		got := shouldStreamRuntime(status)
		if got != want {
			t.Errorf("shouldStreamRuntime(%q) = %v, want %v", status, got, want)
		}
	}
}

// TestFilterFreshLogs locks the watermark-advances-monotonically
// invariant. The whole point of streaming is that every tick prints
// only new entries — a regression here would either re-print history
// every 2s (annoying) or silently drop new lines (worse).
func TestFilterFreshLogs(t *testing.T) {
	entries := []api.LogEntry{
		{Timestamp: "2026-05-24T12:00:00Z", Message: "boot", Stream: "stdout"},
		{Timestamp: "2026-05-24T12:00:01Z", Message: "ready", Stream: "stdout"},
		{Timestamp: "2026-05-24T12:00:02Z", Message: "crash!", Stream: "stderr"},
	}

	t.Run("empty watermark returns everything", func(t *testing.T) {
		fresh, wm := filterFreshLogs(entries, "")
		if len(fresh) != 3 {
			t.Errorf("fresh: want 3, got %d", len(fresh))
		}
		if wm != "2026-05-24T12:00:02Z" {
			t.Errorf("watermark: want last ts, got %q", wm)
		}
	})

	t.Run("watermark at first entry returns only the later two", func(t *testing.T) {
		fresh, wm := filterFreshLogs(entries, "2026-05-24T12:00:00Z")
		if len(fresh) != 2 {
			t.Errorf("fresh: want 2, got %d", len(fresh))
		}
		if wm != "2026-05-24T12:00:02Z" {
			t.Errorf("watermark: want %q, got %q", "2026-05-24T12:00:02Z", wm)
		}
	})

	t.Run("watermark past last entry returns nothing and holds steady", func(t *testing.T) {
		fresh, wm := filterFreshLogs(entries, "2026-05-24T12:00:99Z")
		if len(fresh) != 0 {
			t.Errorf("fresh: want 0, got %d", len(fresh))
		}
		// CRITICAL: watermark must NOT regress, otherwise next tick
		// would re-print everything we just skipped.
		if wm != "2026-05-24T12:00:99Z" {
			t.Errorf("watermark regressed: want %q, got %q",
				"2026-05-24T12:00:99Z", wm)
		}
	})

	t.Run("equal watermark is treated as already-seen (strict >)", func(t *testing.T) {
		// An entry with the exact same timestamp as the watermark
		// has already been printed. Without strict >, the third
		// entry above would be re-shown every tick where its
		// timestamp happens to be the latest.
		fresh, _ := filterFreshLogs(entries, "2026-05-24T12:00:02Z")
		if len(fresh) != 0 {
			t.Errorf("equal watermark must not produce fresh entries: got %d", len(fresh))
		}
	})

	t.Run("empty input returns nothing and keeps watermark", func(t *testing.T) {
		fresh, wm := filterFreshLogs(nil, "2026-05-24T11:00:00Z")
		if fresh != nil {
			t.Errorf("fresh: want nil, got %v", fresh)
		}
		if wm != "2026-05-24T11:00:00Z" {
			t.Errorf("watermark must hold steady: got %q", wm)
		}
	})

	t.Run("out-of-order entries: watermark = max, not last", func(t *testing.T) {
		// Most APIs return logs in time order, but if anything
		// arrives reordered we still need to advance the watermark
		// to the MAX timestamp seen, not the LAST one in the list —
		// otherwise a later tick would re-fetch the older entries
		// that arrived after their newer siblings.
		mixed := []api.LogEntry{
			{Timestamp: "2026-05-24T12:00:05Z", Message: "newer"},
			{Timestamp: "2026-05-24T12:00:03Z", Message: "older"},
		}
		_, wm := filterFreshLogs(mixed, "")
		if wm != "2026-05-24T12:00:05Z" {
			t.Errorf("watermark: want max timestamp, got %q", wm)
		}
	})
}
