package onboarding

import (
	"fmt"
	"os"
	"path/filepath"
)

// TomlConfigPath returns ~/.config/rinse/config.toml
func TomlConfigPath() string {
	dir, err := os.UserConfigDir()
	if err != nil {
		dir = os.Getenv("HOME")
	}
	return filepath.Join(dir, "rinse", "config.toml")
}

// WriteTomlConfig writes the TOML config atomically.
// Called during Step C after the user confirms defaults.
func WriteTomlConfig(cycleName string, d Defaults) error {
	path := TomlConfigPath()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}

	// Manual TOML generation — avoids adding a TOML library dependency.
	// Structure matches the spec in RIN-25#document-defaults-config.
	content := fmt.Sprintf(
		"# ~/.config/rinse/config.toml\n"+
			"# Written by onboarding wizard. Edit manually or via `rinse config set <key> <value>`.\n\n"+
			"[defaults]\n"+
			"remind_on_complete = %v    # Notify when a cycle finishes. (Onboarding Step C toggle 1)\n"+
			"auto_advance       = %v   # Automatically move to next step. (Onboarding Step C toggle 2)\n"+
			"save_history       = %v    # Persist cycle run history to disk. (Onboarding Step C toggle 3)\n\n"+
			"[cycle]\n"+
			"name = %q                    # Set during onboarding Step B. Editable at any time.\n",
		d.RemindOnComplete,
		d.AutoAdvance,
		d.SaveHistory,
		cycleName,
	)

	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(content), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
