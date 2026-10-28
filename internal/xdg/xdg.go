// Package xdg resolves lazypg's directories using the XDG Base Directory
// convention on every platform, including macOS (where os.UserConfigDir would
// otherwise return ~/Library/Application Support).
package xdg

import (
	"os"
	"path/filepath"

	"github.com/daviddwlee84/lazypg/internal/config"
)

func base(envVar, fallback string) string {
	if v := os.Getenv(envVar); v != "" && filepath.IsAbs(v) {
		return v
	}
	home, err := os.UserHomeDir()
	if err != nil {
		home = "."
	}
	return filepath.Join(home, fallback)
}

// ConfigDir holds user preferences, org registrations, and credentials.
func ConfigDir() string { return filepath.Join(base("XDG_CONFIG_HOME", ".config"), config.AppName) }

// DataDir holds generated compose files, org checkouts, and downloaded tools.
func DataDir() string { return filepath.Join(base("XDG_DATA_HOME", ".local/share"), config.AppName) }

// StateDir holds per-project runtime state and the audit log.
func StateDir() string { return filepath.Join(base("XDG_STATE_HOME", ".local/state"), config.AppName) }

// CacheDir holds disposable data such as git dependency checkouts.
func CacheDir() string { return filepath.Join(base("XDG_CACHE_HOME", ".cache"), config.AppName) }
