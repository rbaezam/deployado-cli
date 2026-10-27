package config

import (
	"os"
	"path/filepath"
	"testing"
)

// withTempConfig points config storage at an isolated dir for the test.
func withTempConfig(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	t.Setenv(ProfileEnvVar, "")
	return filepath.Join(dir, "deployado", "config.json")
}

func writeRaw(t *testing.T, path, contents string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte(contents), 0600); err != nil {
		t.Fatalf("write: %v", err)
	}
}

func TestLoadMigratesLegacyConfig(t *testing.T) {
	path := withTempConfig(t)
	writeRaw(t, path, `{"host":"https://example.com","access_token":"legacy-token"}`)

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if cfg.Active != DefaultProfileName {
		t.Errorf("active = %q, want %q", cfg.Active, DefaultProfileName)
	}
	p := cfg.Profiles[DefaultProfileName]
	if p == nil {
		t.Fatalf("default profile missing after migration")
	}
	if p.Host != "https://example.com" || p.AccessToken != "legacy-token" {
		t.Errorf("migrated profile = %+v", p)
	}

	// Second load reads the new shape directly — no second migration needed.
	cfg2, err := Load()
	if err != nil {
		t.Fatalf("re-Load: %v", err)
	}
	if cfg2.Profiles[DefaultProfileName].AccessToken != "legacy-token" {
		t.Errorf("re-Load lost token: %+v", cfg2.Profiles)
	}
}

func TestLegacyMigrationFillsDefaultHost(t *testing.T) {
	path := withTempConfig(t)
	writeRaw(t, path, `{"access_token":"t"}`)

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got := cfg.Profiles[DefaultProfileName].Host; got != DefaultHost {
		t.Errorf("host = %q, want %q", got, DefaultHost)
	}
}

func TestActivePrecedence(t *testing.T) {
	withTempConfig(t)
	if err := PutProfile("alpha", &Profile{Host: "h1", AccessToken: "a"}, true); err != nil {
		t.Fatalf("put alpha: %v", err)
	}
	if err := PutProfile("beta", &Profile{Host: "h2", AccessToken: "b"}, false); err != nil {
		t.Fatalf("put beta: %v", err)
	}

	// 1. No override → uses active (alpha).
	_, name, err := Active("")
	if err != nil || name != "alpha" {
		t.Fatalf("active=%q err=%v, want alpha", name, err)
	}

	// 2. Env var override.
	t.Setenv(ProfileEnvVar, "beta")
	_, name, err = Active("")
	if err != nil || name != "beta" {
		t.Fatalf("env override: name=%q err=%v, want beta", name, err)
	}

	// 3. Explicit flag beats env var.
	_, name, err = Active("alpha")
	if err != nil || name != "alpha" {
		t.Fatalf("flag override: name=%q err=%v, want alpha", name, err)
	}
}

func TestRemoveActiveFallsBack(t *testing.T) {
	withTempConfig(t)
	_ = PutProfile("alpha", &Profile{Host: "h", AccessToken: "a"}, true)
	_ = PutProfile("beta", &Profile{Host: "h", AccessToken: "b"}, false)

	if err := RemoveProfile("alpha"); err != nil {
		t.Fatalf("RemoveProfile: %v", err)
	}
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Active != "beta" {
		t.Errorf("active = %q, want beta", cfg.Active)
	}
}

func TestRemoveLastProfileDeletesFile(t *testing.T) {
	path := withTempConfig(t)
	_ = PutProfile("only", &Profile{Host: "h", AccessToken: "a"}, true)

	if err := RemoveProfile("only"); err != nil {
		t.Fatalf("RemoveProfile: %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("config file still exists: %v", err)
	}

	_, _, err := Active("")
	if err != ErrNoProfile {
		t.Errorf("Active err = %v, want ErrNoProfile", err)
	}
}

func TestRenameProfile(t *testing.T) {
	withTempConfig(t)
	_ = PutProfile("default", &Profile{Host: "h", AccessToken: "a"}, true)
	_ = PutProfile("other", &Profile{Host: "h", AccessToken: "b"}, false)

	// Active profile follows the rename.
	if err := RenameProfile("default", "twin-software"); err != nil {
		t.Fatalf("rename: %v", err)
	}
	cfg, _ := Load()
	if cfg.Active != "twin-software" {
		t.Errorf("active = %q, want twin-software", cfg.Active)
	}
	if cfg.Profiles["twin-software"] == nil || cfg.Profiles["default"] != nil {
		t.Errorf("profiles = %+v", cfg.Profiles)
	}

	// Renaming to an existing name fails.
	if err := RenameProfile("twin-software", "other"); err == nil {
		t.Error("expected collision error")
	}

	// Renaming an unknown profile fails.
	if err := RenameProfile("nope", "x"); err == nil {
		t.Error("expected not-found error")
	}

	// Same-name rename is a no-op.
	if err := RenameProfile("other", "other"); err != nil {
		t.Errorf("same-name rename should be a no-op: %v", err)
	}
}

func TestActiveUnknownProfile(t *testing.T) {
	withTempConfig(t)
	_ = PutProfile("alpha", &Profile{Host: "h", AccessToken: "a"}, true)

	if _, _, err := Active("does-not-exist"); err == nil {
		t.Error("expected error for unknown profile")
	}
}
