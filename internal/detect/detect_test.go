package detect

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDetectStack_StaticFrontendIsNotClassifiedAsNode(t *testing.T) {
	dir := t.TempDir()

	packageJSON := `{
  "name": "frontend-app",
  "dependencies": {
    "vue": "^3.4.0"
  },
  "devDependencies": {
    "vite": "^5.0.0"
  }
}`

	if err := os.WriteFile(filepath.Join(dir, "package.json"), []byte(packageJSON), 0o644); err != nil {
		t.Fatalf("failed to write package.json: %v", err)
	}

	info := DetectStack(dir)
	if info == nil {
		t.Fatal("expected stack info, got nil")
	}

	if info.Stack != StackStatic {
		t.Fatalf("expected stack %q, got %q", StackStatic, info.Stack)
	}
}

func TestDetectStack_NodeBackendRemainsNode(t *testing.T) {
	dir := t.TempDir()

	packageJSON := `{
  "name": "node-api",
  "main": "index.js",
  "scripts": {
    "start": "node index.js"
  },
  "dependencies": {
    "express": "^4.19.0"
  }
}`

	if err := os.WriteFile(filepath.Join(dir, "package.json"), []byte(packageJSON), 0o644); err != nil {
		t.Fatalf("failed to write package.json: %v", err)
	}

	info := DetectStack(dir)
	if info == nil {
		t.Fatal("expected stack info, got nil")
	}

	if info.Stack != StackNode {
		t.Fatalf("expected stack %q, got %q", StackNode, info.Stack)
	}
}
