package onboarding

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unicode"
)

// TomlConfigPath returns ~/.config/rinse/config.toml
func TomlConfigPath() string {
	dir, err := os.UserConfigDir()
	if err != nil {
		dir = filepath.Join(os.Getenv("HOME"), ".config")
	}
	return filepath.Join(dir, "rinse", "config.toml")
}

// tomlQuoteString returns a TOML basic string literal (including surrounding
// quotes) for s. Unlike Go's %q, it never emits \xNN escape sequences which
// are not valid in TOML; instead it uses \uXXXX for control characters.
func tomlQuoteString(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range s {
		switch r {
		case '"':
			b.WriteString(`\"`)
		case '\\':
			b.WriteString(`\\`)
		case '\b':
			b.WriteString(`\b`)
		case '\t':
			b.WriteString(`\t`)
		case '\n':
			b.WriteString(`\n`)
		case '\f':
			b.WriteString(`\f`)
		case '\r':
			b.WriteString(`\r`)
		default:
			if unicode.IsControl(r) {
				fmt.Fprintf(&b, `\u%04X`, r)
			} else {
				b.WriteRune(r)
			}
		}
	}
	b.WriteByte('"')
	return b.String()
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
			"# Written by onboarding wizard. Edit this file manually to change settings.\n\n"+
			"[defaults]\n"+
			"remind_on_complete = %v    # Notify when a cycle finishes. (Onboarding Step C toggle 1)\n"+
			"auto_advance       = %v   # Automatically move to next step. (Onboarding Step C toggle 2)\n"+
			"save_history       = %v    # Persist cycle run history to disk. (Onboarding Step C toggle 3)\n\n"+
			"[cycle]\n"+
			"name = %s                    # Set during onboarding Step B. Editable at any time.\n",
		d.RemindOnComplete,
		d.AutoAdvance,
		d.SaveHistory,
		tomlQuoteString(cycleName),
	)

	tmp, err := os.CreateTemp(filepath.Dir(path), "config.toml.*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	if _, err := tmp.WriteString(content); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return err
	}
	if err := os.Rename(tmpName, path); err != nil {
		os.Remove(tmpName)
		return err
	}
	return nil
}
