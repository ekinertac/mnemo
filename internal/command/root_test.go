package command

import (
	"path/filepath"
	"testing"
)

// resetConfigCache clears the package-level config cache so tests that exercise config loading
// don't leak a prior test's config (the CLI loads once per process, but tests share the binary).
func resetConfigCache() { cachedConfig = nil }

// Guards the macOS regression: the config dir must be ~/.config (XDG), NOT os.UserConfigDir()
// which on macOS is ~/Library/Application Support. DESIGN §6.1 specifies ~/.config/mnemo.
func TestConfigFilePathHonorsXDG(t *testing.T) {
	resetConfigCache()
	t.Setenv("MNEMO_CONFIG", "")
	t.Setenv("XDG_CONFIG_HOME", "/xdg")
	got := configFilePath()
	want := filepath.Join("/xdg", "mnemo", "config.json")
	if got != want {
		t.Errorf("configFilePath = %q, want %q", got, want)
	}
}

// $MNEMO_CONFIG takes precedence over the default location.
func TestConfigFilePathEnvOverride(t *testing.T) {
	t.Setenv("MNEMO_CONFIG", "/custom/c.json")
	if got := configFilePath(); got != "/custom/c.json" {
		t.Errorf("MNEMO_CONFIG override = %q, want /custom/c.json", got)
	}
}
