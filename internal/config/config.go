package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
)

// DefaultHost is the default Deployado API host.
const DefaultHost = "https://deployado.com"

// DefaultProfileName is the profile name used when migrating a legacy
// single-account config and when no other name can be inferred.
const DefaultProfileName = "default"

// ProfileEnvVar is the environment variable that overrides the active profile.
const ProfileEnvVar = "DEPLOYADO_PROFILE"

// ErrNoProfile is returned when no profile is configured or selected.
var ErrNoProfile = errors.New("not logged in")

// Profile holds credentials and cached metadata for one account.
type Profile struct {
	Host        string `json:"host"`
	AccessToken string `json:"access_token"`
	Email       string `json:"email,omitempty"`
	AccountSlug string `json:"account_slug,omitempty"`
	AccountName string `json:"account_name,omitempty"`
}

// Config is the on-disk shape: a set of named profiles plus the active one.
type Config struct {
	Active   string              `json:"active"`
	Profiles map[string]*Profile `json:"profiles"`
}

// legacyConfig is the pre-multi-account schema we auto-migrate from.
type legacyConfig struct {
	Host        string `json:"host"`
	AccessToken string `json:"access_token"`
}

func configPath() (string, error) {
	configDir := os.Getenv("XDG_CONFIG_HOME")
	if configDir == "" {
		homeDir, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		configDir = filepath.Join(homeDir, ".config")
	}
	return filepath.Join(configDir, "deployado", "config.json"), nil
}

// Load reads the config from disk, migrating legacy single-account configs in place.
func Load() (*Config, error) {
	path, err := configPath()
	if err != nil {
		return nil, err
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("config is corrupt: %w", err)
	}

	if len(cfg.Profiles) == 0 {
		var legacy legacyConfig
		if uerr := json.Unmarshal(data, &legacy); uerr == nil && legacy.AccessToken != "" {
			host := legacy.Host
			if host == "" {
				host = DefaultHost
			}
			cfg = Config{
				Active: DefaultProfileName,
				Profiles: map[string]*Profile{
					DefaultProfileName: {Host: host, AccessToken: legacy.AccessToken},
				},
			}
			_ = save(&cfg)
		}
	}

	if cfg.Profiles == nil {
		cfg.Profiles = map[string]*Profile{}
	}

	return &cfg, nil
}

// LoadOrEmpty returns the config, or an empty (non-nil) one if missing.
func LoadOrEmpty() *Config {
	cfg, err := Load()
	if err != nil {
		return &Config{Profiles: map[string]*Profile{}}
	}
	return cfg
}

// Save persists the config to disk.
func Save(cfg *Config) error {
	return save(cfg)
}

func save(cfg *Config) error {
	path, err := configPath()
	if err != nil {
		return err
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0600)
}

// Delete removes the config file entirely.
func Delete() error {
	path, err := configPath()
	if err != nil {
		return err
	}
	return os.Remove(path)
}

// ResolveProfileName returns the effective profile name.
// Precedence: explicit override > DEPLOYADO_PROFILE env var > cfg.Active.
func ResolveProfileName(cfg *Config, override string) string {
	if override != "" {
		return override
	}
	if env := os.Getenv(ProfileEnvVar); env != "" {
		return env
	}
	return cfg.Active
}

// Active resolves and returns the profile to use, honoring override flag and env var.
// Returns ErrNoProfile if there is no usable profile.
func Active(override string) (*Profile, string, error) {
	cfg, err := Load()
	if err != nil {
		if os.IsNotExist(err) {
			return nil, "", ErrNoProfile
		}
		return nil, "", err
	}
	name := ResolveProfileName(cfg, override)
	if name == "" {
		return nil, "", ErrNoProfile
	}
	p, ok := cfg.Profiles[name]
	if !ok {
		return nil, name, fmt.Errorf("profile %q not found (run 'deployado auth list' to see profiles)", name)
	}
	return p, name, nil
}

// PutProfile creates or replaces a profile. If setActive is true (or no profile
// is active yet), it becomes the active profile.
func PutProfile(name string, p *Profile, setActive bool) error {
	cfg := LoadOrEmpty()
	cfg.Profiles[name] = p
	if setActive || cfg.Active == "" {
		cfg.Active = name
	}
	return save(cfg)
}

// SetActive switches the active profile to name.
func SetActive(name string) error {
	cfg, err := Load()
	if err != nil {
		return err
	}
	if _, ok := cfg.Profiles[name]; !ok {
		return fmt.Errorf("profile %q not found", name)
	}
	cfg.Active = name
	return save(cfg)
}

// RemoveProfile removes a profile. If it was the active one, falls back to any
// remaining profile, or removes the config file entirely if none remain.
func RemoveProfile(name string) error {
	cfg, err := Load()
	if err != nil {
		return err
	}
	if _, ok := cfg.Profiles[name]; !ok {
		return fmt.Errorf("profile %q not found", name)
	}
	delete(cfg.Profiles, name)
	if cfg.Active == name {
		cfg.Active = ""
		for k := range cfg.Profiles {
			cfg.Active = k
			break
		}
	}
	if len(cfg.Profiles) == 0 {
		return Delete()
	}
	return save(cfg)
}

// RenameProfile renames a profile from oldName to newName. If the renamed
// profile was active, the active pointer follows.
func RenameProfile(oldName, newName string) error {
	if oldName == newName {
		return nil
	}
	if newName == "" {
		return errors.New("new profile name is empty")
	}
	cfg, err := Load()
	if err != nil {
		return err
	}
	p, ok := cfg.Profiles[oldName]
	if !ok {
		return fmt.Errorf("profile %q not found", oldName)
	}
	if _, exists := cfg.Profiles[newName]; exists {
		return fmt.Errorf("profile %q already exists", newName)
	}
	cfg.Profiles[newName] = p
	delete(cfg.Profiles, oldName)
	if cfg.Active == oldName {
		cfg.Active = newName
	}
	return save(cfg)
}

// SortedProfileNames returns the profile names in alphabetical order.
func SortedProfileNames(cfg *Config) []string {
	names := make([]string, 0, len(cfg.Profiles))
	for n := range cfg.Profiles {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}
