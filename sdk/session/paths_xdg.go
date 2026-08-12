//go:build !wasip1

package session

import (
	"path/filepath"

	"github.com/adrg/xdg"
)

// DefaultConfigPath returns the default config file path following XDG Base Directory spec
// Returns: XDG_CONFIG_HOME/dtctl/config (typically ~/.config/dtctl/config)
func DefaultConfigPath() string {
	return filepath.Join(xdg.ConfigHome, "dtctl", "config")
}

// ConfigDir returns the config directory path following XDG Base Directory spec
func ConfigDir() string {
	return filepath.Join(xdg.ConfigHome, "dtctl")
}

// CacheDir returns the cache directory path following XDG Base Directory spec
func CacheDir() string {
	return filepath.Join(xdg.CacheHome, "dtctl")
}

// DataDir returns the data directory path following XDG Base Directory spec
func DataDir() string {
	return filepath.Join(xdg.DataHome, "dtctl")
}

// StateDir returns the state directory path following XDG Base Directory spec
// (persistent but disposable data: history, logs). Typically ~/.local/state/dtctl.
func StateDir() string {
	return filepath.Join(xdg.StateHome, "dtctl")
}
