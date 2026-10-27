package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNginxHardeningWarnings(t *testing.T) {
	// Each subtest builds a tiny directory tree representing a service's
	// build context and asserts the warnings the check produces. The
	// goal is to catch the regression that hit booky-web (Dockerfile +
	// unhardened nginx.conf shipping to prod silently).
	tests := []struct {
		name        string
		dockerfile  string
		nginxConf   string
		nginxFile   string // filename: nginx.conf or default.conf, "" = don't write
		wantWarning bool
		wantSubstr  string // optional fragment that must appear in the warning
	}{
		{
			name:        "no Dockerfile — autogen path, agent handles hardening",
			dockerfile:  "",
			wantWarning: false,
		},
		{
			name:        "Dockerfile without nginx — non-nginx runtime",
			dockerfile:  "FROM node:20-alpine\nRUN npm install\n",
			wantWarning: false,
		},
		{
			name:       "nginx Dockerfile + unhardened nginx.conf — the booky-web case",
			dockerfile: "FROM nginx:alpine\nCOPY nginx.conf /etc/nginx/conf.d/default.conf\nCOPY dist /usr/share/nginx/html\n",
			nginxConf: `server {
    listen 8080;
    location / { try_files $uri /index.html; }
}`,
			nginxFile:   "nginx.conf",
			wantWarning: true,
			wantSubstr:  "scanner-path protection",
		},
		{
			name:       "nginx Dockerfile + hardened nginx.conf (dotfile deny) — silent",
			dockerfile: "FROM nginx:alpine\nCOPY nginx.conf /etc/nginx/conf.d/default.conf\n",
			nginxConf: `server {
    listen 8080;
    location ~ /\.(?!well-known) { deny all; return 404; }
    location / { try_files $uri /index.html; }
}`,
			nginxFile:   "nginx.conf",
			wantWarning: false,
		},
		{
			name:       "nginx Dockerfile + hardened default.conf (wp-admin block) — silent",
			dockerfile: "FROM nginx:1.25\nCOPY default.conf /etc/nginx/conf.d/default.conf\n",
			nginxConf: `server {
    location ~* /(wp-admin|xmlrpc) { return 404; }
    location / { try_files $uri /index.html; }
}`,
			nginxFile:   "default.conf",
			wantWarning: false,
		},
		{
			name:       "nginx Dockerfile COPYing config we can't find — heads-up warning",
			dockerfile: "FROM nginx:alpine\nCOPY conf/nginx.conf /etc/nginx/nginx.conf\nCOPY dist /usr/share/nginx/html\n",
			// no nginx.conf or default.conf at root (it's in conf/)
			wantWarning: true,
			wantSubstr:  "couldn't locate",
		},
		{
			name:       "case-insensitive — FROM Nginx is still nginx",
			dockerfile: "FROM Nginx:alpine\nCOPY nginx.conf /etc/nginx/conf.d/default.conf\n",
			nginxConf:  "server { location / { try_files $uri /index.html; } }",
			nginxFile:  "nginx.conf",
			wantWarning: true,
		},
		{
			name:       "comment mentioning nginx — should NOT flip the detector",
			dockerfile: "# Used to be nginx, switched to caddy\nFROM caddy:alpine\n",
			wantWarning: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()

			if tc.dockerfile != "" {
				mustWrite(t, filepath.Join(dir, "Dockerfile"), tc.dockerfile)
			}
			if tc.nginxFile != "" {
				mustWrite(t, filepath.Join(dir, tc.nginxFile), tc.nginxConf)
			}

			warnings := nginxHardeningWarnings(dir)
			gotWarning := len(warnings) > 0

			if gotWarning != tc.wantWarning {
				t.Errorf("got warning=%v (warnings=%v), want %v",
					gotWarning, warnings, tc.wantWarning)
				return
			}

			if tc.wantSubstr != "" {
				found := false
				for _, w := range warnings {
					if strings.Contains(w, tc.wantSubstr) {
						found = true
						break
					}
				}
				if !found {
					t.Errorf("none of the warnings contained %q; got %v",
						tc.wantSubstr, warnings)
				}
			}
		})
	}
}

// nginxConfigHasHardening / dockerfileUsesNginx are exercised
// transitively by the parent test, but a couple of edge cases for
// the predicate functions on their own — cheaper to debug from a
// small focused failure than tracing through the file-building.

func TestDockerfileUsesNginx(t *testing.T) {
	cases := map[string]bool{
		"FROM nginx:alpine\n":                     true,
		"FROM nginx\n":                            true,
		"FROM mycompany/nginx:latest\n":           true,
		"FROM gcr.io/distroless/nginx-1.25\n":     true,
		"FROM Nginx:alpine\n":                     true, // case insensitive
		"# nginx is great\nFROM node:20\n":        false,
		"FROM node:20\n# COPY nginx.conf ...\n":   false,
		"FROM nginx:alpine AS web\nFROM node:20\n": true, // any stage
		"":                                        false,
	}
	for input, want := range cases {
		got := dockerfileUsesNginx([]byte(input))
		if got != want {
			t.Errorf("dockerfileUsesNginx(%q) = %v, want %v", input, got, want)
		}
	}
}

func TestNginxConfigHasHardening(t *testing.T) {
	hardenedSamples := []string{
		`location ~ /\. { deny all; }`,
		`location ~* /(wp-admin|xmlrpc) { return 404; }`,
		`# we also explicitly deny all in this server { deny all; }`,
	}
	for _, s := range hardenedSamples {
		if !nginxConfigHasHardening([]byte(s)) {
			t.Errorf("nginxConfigHasHardening(%q) = false, want true", s)
		}
	}

	plain := `server {
    listen 80;
    location / { try_files $uri $uri/ /index.html; }
}`
	if nginxConfigHasHardening([]byte(plain)) {
		t.Error("nginxConfigHasHardening on plain SPA config returned true, want false")
	}
}

func mustWrite(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}
