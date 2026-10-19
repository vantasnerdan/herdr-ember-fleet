// Package paths says where ember-fleet keeps its files.
package paths

import (
	"os"
	"path/filepath"
)

// Root is ~/.config/ember-fleet (or $XDG_CONFIG_HOME/ember-fleet), or
// $EMBER_FLEET_HOME when set (tests, several roles on one account).
func Root() string {
	if h := os.Getenv("EMBER_FLEET_HOME"); h != "" {
		return h
	}
	if x := os.Getenv("XDG_CONFIG_HOME"); x != "" {
		return filepath.Join(x, "ember-fleet")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "ember-fleet")
}

// Role is the directory for one role: relay, hub, dock or cli.
func Role(role string) string { return filepath.Join(Root(), role) }

// Ensure creates dir with mode 0700.
func Ensure(dir string) error { return os.MkdirAll(dir, 0o700) }
