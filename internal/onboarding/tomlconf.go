package onboarding

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// TomlConfigPath returns the path to the TOML config file:
// <user config dir>/rinse/config.toml
// where <user config dir> is os.UserConfigDir() (e.g. ~/.config on Linux,
// ~/Library/Application Support on macOS).
func TomlConfigPath() string {
	dir, err := os.UserConfigDir()
	if err != nil {
		// os.UserHomeDir() is safer than os.Getenv("HOME") — it never returns "".
		home, herr := os.UserHomeDir()
		if herr != nil {
			dir = os.TempDir()
		} else {
			dir = filepath.Join(home, ".config")
		}
	}
	return filepath.Join(dir, "rinse", "config.toml")
}

// WriteTomlConfig writes the TOML config atomically.
// Called during Step C after the user confirms defaults.
func WriteTomlConfig(cycleName string, d Defaults) error {
	path := TomlConfigPath()
	targetDir := filepath.Dir(path)
	if err := os.MkdirAll(targetDir, 0o755); err != nil {
		return err
	}

	// Manual TOML generation — avoids adding a TOML library dependency.
	// Structure matches the spec in RIN-25#document-defaults-config.
	content := fmt.Sprintf(
		"# %s\n"+
			"# Written by onboarding wizard. Edit this file manually to change defaults.\n\n"+
			"[defaults]\n"+
			"remind_on_complete = %v    # Notify when a cycle finishes. (Onboarding Step C toggle 1)\n"+
			"auto_advance       = %v   # Automatically move to next step. (Onboarding Step C toggle 2)\n"+
			"save_history       = %v    # Persist cycle run history to disk. (Onboarding Step C toggle 3)\n\n"+
			"[cycle]\n"+
			"name = %q                    # Set during onboarding Step B. Editable at any time.\n",
		path,
		d.RemindOnComplete,
		d.AutoAdvance,
		d.SaveHistory,
		cycleName,
	)

	// Use os.CreateTemp in the target dir to avoid fixed-name temp file collisions.
	f, err := os.CreateTemp(targetDir, filepath.Base(path)+".*.tmp")
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer os.Remove(tmp)

	if err := f.Chmod(0o644); err != nil {
		f.Close()
		return err
	}
	if _, err := io.WriteString(f, content); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return replaceFile(tmp, path)
}
