package detect

import (
	"os"
	"path/filepath"
	"testing"
)

func TestHasDotNetHealthEndpoint(t *testing.T) {
	tests := []struct {
		name      string
		fileName  string
		contents  string
		wantFound bool
	}{
		{
			name:     "MapGet with /health",
			fileName: "Program.cs",
			contents: `var app = builder.Build();
app.MapGet("/health", () => Results.Ok("OK"));
app.Run();`,
			wantFound: true,
		},
		{
			name:     "MapGet with health (no leading slash)",
			fileName: "Program.cs",
			contents: `app.MapGet("health", () => "OK");`,
			wantFound: true,
		},
		{
			name:     "MapHealthChecks",
			fileName: "Program.cs",
			contents: `app.MapHealthChecks("/health");`,
			wantFound: true,
		},
		{
			name:     "UseHealthChecks middleware",
			fileName: "Program.cs",
			contents: `app.UseHealthChecks("/health");`,
			wantFound: true,
		},
		{
			name:     "Controller Route attribute",
			fileName: "Controllers/HealthController.cs",
			contents: `using Microsoft.AspNetCore.Mvc;

[ApiController]
[Route("health")]
public class HealthController : ControllerBase {
    [HttpGet]
    public IActionResult Get() => Ok();
}`,
			wantFound: true,
		},
		{
			name:     "HttpGet attribute on action",
			fileName: "Controllers/StatusController.cs",
			contents: `[HttpGet("/health")]
public IActionResult Health() => Ok("OK");`,
			wantFound: true,
		},
		{
			name:     "Verbatim string literal @-prefix",
			fileName: "Program.cs",
			contents: `app.MapGet(@"/health", () => "OK");`,
			wantFound: true,
		},
		{
			name:      "No /health anywhere — typical fresh ASP.NET template",
			fileName:  "Program.cs",
			contents:  `var app = builder.Build(); app.Run();`,
			wantFound: false,
		},
		{
			name:     "Only /healthz (kubernetes-style) — NOT detected",
			fileName: "Program.cs",
			contents: `app.MapGet("/healthz", () => "ok");`,
			// We could also accept this but the platform's default
			// probe path is /health; let the user be explicit.
			wantFound: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, tt.fileName)

			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, []byte(tt.contents), 0o644); err != nil {
				t.Fatal(err)
			}

			found, hint := HasDotNetHealthEndpoint(dir)

			if found != tt.wantFound {
				t.Errorf("HasDotNetHealthEndpoint() = %v, want %v", found, tt.wantFound)
			}

			if !found && hint == "" {
				t.Error("expected a non-empty hint when health endpoint is missing")
			}
		})
	}
}

// Skips bin/obj/.git etc. so we don't trigger on build artifacts that
// might have leftover compiled health route definitions from a previous
// build.
func TestHasDotNetHealthEndpoint_SkipsNoiseDirs(t *testing.T) {
	dir := t.TempDir()

	// Health endpoint exists only inside a "bin" directory — should NOT
	// count.
	binDir := filepath.Join(dir, "bin", "Release", "net9.0")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(binDir, "Compiled.cs"), []byte(`app.MapGet("/health", ...)`), 0o644); err != nil {
		t.Fatal(err)
	}

	// And a real source file with no /health.
	if err := os.WriteFile(filepath.Join(dir, "Program.cs"), []byte(`app.Run();`), 0o644); err != nil {
		t.Fatal(err)
	}

	found, _ := HasDotNetHealthEndpoint(dir)
	if found {
		t.Error("expected NOT found — match was inside bin/, which should be skipped")
	}
}
