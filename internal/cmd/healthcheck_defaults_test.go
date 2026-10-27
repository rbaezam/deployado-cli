package cmd

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/deployado/cli/internal/detect"
)

func TestDefaultHealthCheckPath(t *testing.T) {
	t.Run("static runtime uses root path", func(t *testing.T) {
		if got := defaultHealthCheckPath("static", detect.StackUnknown, ""); got != "/" {
			t.Fatalf("expected '/', got %q", got)
		}
	})

	t.Run("static stack uses root path", func(t *testing.T) {
		if got := defaultHealthCheckPath("node", detect.StackStatic, ""); got != "/" {
			t.Fatalf("expected '/', got %q", got)
		}
	})

	t.Run("legacy node frontend falls back to root path", func(t *testing.T) {
		dir := t.TempDir()
		packageJSON := `{
  "name": "frontend-app",
  "dependencies": { "vue": "^3.4.0" },
  "devDependencies": { "vite": "^5.0.0" }
}`
		if err := os.WriteFile(filepath.Join(dir, "package.json"), []byte(packageJSON), 0o644); err != nil {
			t.Fatalf("failed to write package.json: %v", err)
		}

		if got := defaultHealthCheckPath("node", detect.StackUnknown, dir); got != "/" {
			t.Fatalf("expected '/', got %q", got)
		}
	})

	t.Run("regular node runtime keeps /health", func(t *testing.T) {
		dir := t.TempDir()
		packageJSON := `{
  "name": "node-api",
  "dependencies": { "express": "^4.19.0" }
}`
		if err := os.WriteFile(filepath.Join(dir, "package.json"), []byte(packageJSON), 0o644); err != nil {
			t.Fatalf("failed to write package.json: %v", err)
		}

		if got := defaultHealthCheckPath("node", detect.StackUnknown, dir); got != "/health" {
			t.Fatalf("expected '/health', got %q", got)
		}
	})
}
