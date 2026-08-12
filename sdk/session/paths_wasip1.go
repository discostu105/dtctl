//go:build wasip1

package session

import (
	"os"
	"path/filepath"
)

// github.com/adrg/xdg does not compile on wasip1 (no home-directory
// resolution), so these helpers resolve the XDG base directories directly
// from the environment, falling back to the spec defaults under $HOME. In a
// wasm host the environment is fully synthetic anyway: the host decides what
// HOME and XDG_CONFIG_HOME are inside the instance.

func xdgDir(envVar, homeSuffix string) string {
	if dir := os.Getenv(envVar); dir != "" {
		return dir
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, homeSuffix)
}

// DefaultConfigPath returns the default config file path following XDG Base Directory spec
// Returns: XDG_CONFIG_HOME/dtctl/config (typically ~/.config/dtctl/config)
func DefaultConfigPath() string {
	return filepath.Join(xdgDir("XDG_CONFIG_HOME", ".config"), "dtctl", "config")
}

// ConfigDir returns the config directory path following XDG Base Directory spec
func ConfigDir() string {
	return filepath.Join(xdgDir("XDG_CONFIG_HOME", ".config"), "dtctl")
}

// CacheDir returns the cache directory path following XDG Base Directory spec
func CacheDir() string {
	return filepath.Join(xdgDir("XDG_CACHE_HOME", ".cache"), "dtctl")
}

// DataDir returns the data directory path following XDG Base Directory spec
func DataDir() string {
	return filepath.Join(xdgDir("XDG_DATA_HOME", filepath.Join(".local", "share")), "dtctl")
}

// StateDir returns the state directory path following XDG Base Directory spec
// (persistent but disposable data: history, logs). Typically ~/.local/state/dtctl.
func StateDir() string {
	return filepath.Join(xdgDir("XDG_STATE_HOME", filepath.Join(".local", "state")), "dtctl")
}
