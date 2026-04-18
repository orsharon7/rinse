package tui

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/orsharon7/rinse/internal/theme"
)

// RepoRinseConfig is the per-repository .rinse.json config file structure.
// It stores shared team settings so everyone reviewing PRs in the same repo
// uses consistent defaults without having to configure them individually.
type RepoRinseConfig struct {
	Engine        string `json:"engine"`                  // "opencode" or "claude"
	Model         string `json:"model,omitempty"`         // model override (empty = engine default)
	Reflect       bool   `json:"reflect"`                 // enable reflection agent
	ReflectBranch string `json:"reflect_branch,omitempty"` // branch to push rules to (empty = default)
	AutoMerge     bool   `json:"auto_merge"`              // auto-merge after approval
}

const rinseConfigFile = ".rinse.json"

// RunInit implements the `rinse init` subcommand.
// It scaffolds a .rinse.json config in the current directory with sensible
// defaults, prompting the user to choose engine and reflection settings.
// It returns an error if the operation fails; the caller is responsible for
// deciding when to exit.
func RunInit() error {
	reader := bufio.NewReader(os.Stdin)

	// Check if config already exists.
	if fi, err := os.Stat(rinseConfigFile); err == nil {
		if !fi.Mode().IsRegular() {
			return fmt.Errorf("%s exists but is not a regular file (mode: %s); remove it manually and re-run", rinseConfigFile, fi.Mode())
		}
		fmt.Print(theme.StyleMuted.Render("Config already exists. Overwrite? (y/N) "))
		line, _ := reader.ReadString('\n')
		line = strings.TrimSpace(strings.ToLower(line))
		if line != "y" && line != "yes" {
			fmt.Println(theme.StyleMuted.Render("Aborted."))
			return nil
		}
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("failed to stat %s: %w", rinseConfigFile, err)
	}

	fmt.Println(theme.StyleStep.Render("Initializing Rinse config for this repo..."))
	fmt.Println()

	// Prompt for engine.
	fmt.Println(theme.StyleStep.Render("Select engine:"))
	for i, r := range runners {
		fmt.Printf("  [%d] %s — %s\n", i+1, theme.StyleVal.Render(r.name), theme.StyleMuted.Render(r.desc))
	}

	runnerIdx := 0
	for {
		fmt.Printf(theme.StyleMuted.Render("Engine (1-%d) [1]: "), len(runners))
		engineLine, _ := reader.ReadString('\n')
		engineLine = strings.TrimSpace(engineLine)

		if engineLine == "" {
			break
		}

		validSelection := false
		for i, r := range runners {
			if engineLine == fmt.Sprintf("%d", i+1) || strings.EqualFold(engineLine, r.name) {
				runnerIdx = i
				validSelection = true
				break
			}
		}

		if validSelection {
			break
		}

		fmt.Printf(theme.StyleErr.Render("Invalid engine selection %q. Please enter a number from 1-%d or a valid engine name.\n"), engineLine, len(runners))
	}

	selectedRunner := runners[runnerIdx]
	fmt.Printf("→ Using: %s\n\n", theme.StyleVal.Render(selectedRunner.name))

	// Prompt for model override.
	fmt.Printf(theme.StyleMuted.Render("Model override (leave blank for default: %s): "), selectedRunner.defaultModel)
	modelLine, _ := reader.ReadString('\n')
	modelOverride := strings.TrimSpace(modelLine)

	// Prompt for reflection.
	fmt.Print(theme.StyleMuted.Render("Enable reflection agent? (y/N): "))
	reflectLine, _ := reader.ReadString('\n')
	reflectLine = strings.TrimSpace(strings.ToLower(reflectLine))
	reflect := reflectLine == "y" || reflectLine == "yes"

	reflectBranch := ""
	if reflect {
		fmt.Print(theme.StyleMuted.Render("Reflection branch (leave blank to use the repo default branch): "))
		branchLine, _ := reader.ReadString('\n')
		reflectBranch = strings.TrimSpace(branchLine)
	}

	// Prompt for auto-merge.
	fmt.Print(theme.StyleMuted.Render("Auto-merge after approval? (y/N): "))
	mergeLine, _ := reader.ReadString('\n')
	mergeLine = strings.TrimSpace(strings.ToLower(mergeLine))
	autoMerge := mergeLine == "y" || mergeLine == "yes"

	// Build config with sensible defaults.
	cfg := RepoRinseConfig{
		Engine:        selectedRunner.name,
		Model:         modelOverride,
		Reflect:       reflect,
		ReflectBranch: reflectBranch,
		AutoMerge:     autoMerge,
	}

	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to encode config: %w", err)
	}

	// Write atomically via temp file + rename to avoid partial writes.
	tmpFile, err := os.CreateTemp(".", ".rinse.json.tmp*")
	if err != nil {
		return fmt.Errorf("failed to create temp file: %w", err)
	}
	tmpName := tmpFile.Name()
	// Set permissions to 0644 so the committed file is world-readable.
	if err := tmpFile.Chmod(0o644); err != nil {
		tmpFile.Close()
		os.Remove(tmpName)
		return fmt.Errorf("failed to set permissions on temp file: %w", err)
	}
	if _, err := tmpFile.Write(append(data, '\n')); err != nil {
		tmpFile.Close()
		os.Remove(tmpName)
		return fmt.Errorf("failed to write temp config: %w", err)
	}
	if err := tmpFile.Close(); err != nil {
		os.Remove(tmpName)
		return fmt.Errorf("failed to close temp config: %w", err)
	}
	// only remove and retry when the rename error explicitly indicates that
	// the destination already exists.
	if err := os.Rename(tmpName, rinseConfigFile); err != nil {
		if !os.IsExist(err) {
			os.Remove(tmpName)
			return fmt.Errorf("failed to write %s: %w", rinseConfigFile, err)
		}
		if removeErr := os.Remove(rinseConfigFile); removeErr != nil {
			os.Remove(tmpName)
			return fmt.Errorf("failed to write %s: %w", rinseConfigFile, removeErr)
		}
		if retryErr := os.Rename(tmpName, rinseConfigFile); retryErr != nil {
			os.Remove(tmpName)
			return fmt.Errorf("failed to write %s: %w", rinseConfigFile, retryErr)
		}
	}

	fmt.Println(theme.StyleLogSuccess.Render(fmt.Sprintf("%s Created %s", theme.IconCheck, rinseConfigFile)))
	fmt.Println()
	fmt.Println(theme.StyleMuted.Render("Tip: ") + theme.StyleTeal.Render("commit .rinse.json") + theme.StyleMuted.Render(" so your team shares the same settings."))
	fmt.Println(theme.StyleMuted.Render("Tip: Add ") + theme.StyleTeal.Render(".rinseignore") + theme.StyleMuted.Render(" at repo root to exclude paths from cycles (gitignore-style)."))

	// Offer to generate .github/copilot-instructions.md to reduce Copilot noise.
	fmt.Println()
	if err := RunInitCopilotInstructions(reader); err != nil {
		// Non-fatal: warn but don't fail the whole init.
		fmt.Printf("Warning: could not generate Copilot instructions: %v\n", err)
	}

	return nil
}
