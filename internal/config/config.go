// Package config manages the launcher's on-disk configuration: install
// location, server addresses, and the list of saved account usernames.
// Passwords are never stored here; see internal/creds.
package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
)

const (
	DefaultLoginServer = "login.us.wizard101.com:12000"
	DefaultPatchServer = "patch.us.wizard101.com:12500"
	DefaultLocale      = "en-US"
)

// Well-known install locations, probed in order when no dir is configured,
// before falling back to every Steam library found on the machine.
var defaultInstallDirs = []string{
	`C:\ProgramData\KingsIsle Entertainment\Wizard101`,
	`C:\Program Files (x86)\Steam\steamapps\common\Wizard101`,
}

type Config struct {
	InstallDir     string   `json:"installDir"`
	LoginServer    string   `json:"loginServer"`
	PatchServer    string   `json:"patchServer"`
	Locale         string   `json:"locale"`
	Accounts       []string `json:"accounts"`
	DefaultAccount string   `json:"defaultAccount"`
	// UI preferences, persisted so toggles keep their state across restarts.
	SkipPatchCheck bool  `json:"skipPatchCheck"`
	RememberMe     *bool `json:"rememberMe,omitempty"`
	// Theme is "dark" (default) or "light".
	Theme string `json:"theme,omitempty"`
	// MinimizeOnLaunch minimizes the launcher once the game starts.
	MinimizeOnLaunch bool `json:"minimizeOnLaunch,omitempty"`
}

// RememberMeOrDefault returns the Remember me preference (default: on).
func (c *Config) RememberMeOrDefault() bool {
	if c.RememberMe == nil {
		return true
	}
	return *c.RememberMe
}

func defaults() Config {
	return Config{
		LoginServer: DefaultLoginServer,
		PatchServer: DefaultPatchServer,
		Locale:      DefaultLocale,
	}
}

// Path returns the config file location. The directory keeps the original
// "w101client" name so existing installs keep their settings after the
// rebrand to Sigil.
func Path() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "w101client", "config.json"), nil
}

func Load() (*Config, error) {
	path, err := Path()
	if err != nil {
		return nil, err
	}

	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		cfg := defaults()
		cfg.InstallDir = detectInstallDir()
		return &cfg, nil
	}
	if err != nil {
		return nil, err
	}

	cfg := defaults()
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parsing %s: %w", path, err)
	}
	if cfg.InstallDir == "" {
		cfg.InstallDir = detectInstallDir()
	}
	return &cfg, nil
}

func (c *Config) Save() error {
	path, err := Path()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o600)
}

func detectInstallDir() string {
	for _, dir := range slices.Concat(defaultInstallDirs, steamInstallDirs()) {
		if _, err := os.Stat(filepath.Join(dir, "Bin", "WizardGraphicalClient.exe")); err == nil {
			return dir
		}
	}
	return ""
}

// AddAccount registers a username, making it the default if it is the first.
func (c *Config) AddAccount(username string) {
	if !slices.Contains(c.Accounts, username) {
		c.Accounts = append(c.Accounts, username)
	}
	if c.DefaultAccount == "" {
		c.DefaultAccount = username
	}
}

// RemoveAccount unregisters a username.
func (c *Config) RemoveAccount(username string) {
	c.Accounts = slices.DeleteFunc(c.Accounts, func(u string) bool { return u == username })
	if c.DefaultAccount == username {
		c.DefaultAccount = ""
		if len(c.Accounts) > 0 {
			c.DefaultAccount = c.Accounts[0]
		}
	}
}

// ResolveAccount picks the account to use: the explicit argument if given,
// otherwise the configured default.
func (c *Config) ResolveAccount(explicit string) (string, error) {
	if explicit != "" {
		return explicit, nil
	}
	if c.DefaultAccount != "" {
		return c.DefaultAccount, nil
	}
	return "", errors.New("no account specified and no default configured; run 'sigil accounts add <username>' first")
}
