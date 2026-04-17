package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

// RepoRinseConfig is the per-repository .rinse.json config file structure.
// It stores shared repository settings that `rinse init` writes to disk for
// teams that want to check in defaults for a repo.
type RepoRinseConfig struct {
	Engine       string `json:"engine"`        // "opencode" or "claude"
	Model        string `json:"model"`         // model override (empty = engine default)
	Reflect      bool   `json:"reflect"`       // enable reflection agent
	ReflectBranch string `json:"reflect_branch,omitempty"` // branch to push rules to (empty = default)
	AutoMerge    bool   `json:"auto_merge"`    // auto-merge after approval
}

const rinseConfigFile = ".rinse.json"

// RunInit implements the `rinse init` subcommand.
// It scaffolds a .rinse.json config in the current directory with sensible
// defaults, prompting the user to choose engine and reflection settings.
func RunInit() {
	reader := bufio.NewReader(os.Stdin)

	// Check if config already exists.
	if _, err := os.Stat(rinseConfigFile); err == nil {
		fmt.Printf("Config already exists. Overwrite? (y/N) ")
		line, _ := reader.ReadString('\n')
		line = strings.TrimSpace(strings.ToLower(line))
		if line != "y" && line != "yes" {
			fmt.Println("Aborted.")
			os.Exit(0)
		}
	} else if !os.IsNotExist(err) {
		fmt.Fprintf(os.Stderr, "error: failed to stat %s: %v\n", rinseConfigFile, err)
		os.Exit(1)
	}

	fmt.Println("Initializing Rinse config for this repo...")
	fmt.Println()

	// Prompt for engine.
	fmt.Println("Select engine:")
	for i, r := range runners {
		fmt.Printf("  [%d] %s — %s\n", i+1, r.name, r.desc)
	}
	runnerIdx := 0
	for {
		fmt.Printf("Engine (1-%d) [1]: ", len(runners))
		engineLine, _ := reader.ReadString('\n')
		engineLine = strings.TrimSpace(engineLine)

		if engineLine == "" {
			break
		}

		matched := false
		for i, r := range runners {
			if engineLine == fmt.Sprintf("%d", i+1) || strings.EqualFold(engineLine, r.name) {
				runnerIdx = i
				matched = true
				break
			}
		}

		if matched {
			break
		}

		fmt.Fprintf(os.Stderr, "error: invalid engine selection %q; enter a number between 1 and %d or a valid engine name\n", engineLine, len(runners))
	}
	selectedRunner := runners[runnerIdx]
	fmt.Printf("→ Using: %s\n\n", selectedRunner.name)

	// Prompt for model override.
	fmt.Printf("Model override (leave blank for default: %s): ", selectedRunner.defaultModel)
	modelLine, _ := reader.ReadString('\n')
	modelOverride := strings.TrimSpace(modelLine)

	// Prompt for reflection.
	fmt.Print("Enable reflection agent? (y/N): ")
	reflectLine, _ := reader.ReadString('\n')
	reflectLine = strings.TrimSpace(strings.ToLower(reflectLine))
	reflect := reflectLine == "y" || reflectLine == "yes"

	reflectBranch := ""
	if reflect {
		fmt.Print("Reflection branch (leave blank to use the repo default branch): ")
		branchLine, _ := reader.ReadString('\n')
		reflectBranch = strings.TrimSpace(branchLine)
	}

	// Prompt for auto-merge.
	fmt.Print("Auto-merge after approval? (y/N): ")
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
		fmt.Fprintf(os.Stderr, "error: failed to encode config: %v\n", err)
		os.Exit(1)
	}

	// Write atomically via temp file + rename to avoid a partial write corrupting .rinse.json.
	tmp, err := os.CreateTemp(".", ".rinse.json.tmp*")
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: failed to create temp file: %v\n", err)
		os.Exit(1)
	}
	tmpName := tmp.Name()
	if cherr := os.Chmod(tmpName, 0o644); cherr != nil {
		tmp.Close()
		os.Remove(tmpName)
		fmt.Fprintf(os.Stderr, "error: failed to set temp file permissions: %v\n", cherr)
		os.Exit(1)
	}
	if _, werr := tmp.Write(append(data, '\n')); werr != nil {
		tmp.Close()
		os.Remove(tmpName)
		fmt.Fprintf(os.Stderr, "error: failed to write temp file: %v\n", werr)
		os.Exit(1)
	}
	if cerr := tmp.Close(); cerr != nil {
		os.Remove(tmpName)
		fmt.Fprintf(os.Stderr, "error: failed to close temp file: %v\n", cerr)
		os.Exit(1)
	}
	// Preserve any existing target until the new file has been moved into place.
	// This avoids losing the original config if the final rename fails.
	backupName := rinseConfigFile + ".bak"
	hadExisting := false
	if _, serr := os.Stat(rinseConfigFile); serr == nil {
		hadExisting = true
		if rerr := os.Remove(backupName); rerr != nil && !os.IsNotExist(rerr) {
			os.Remove(tmpName)
			fmt.Fprintf(os.Stderr, "error: failed to remove backup %s: %v\n", backupName, rerr)
			os.Exit(1)
		}
		if rerr := os.Rename(rinseConfigFile, backupName); rerr != nil {
			os.Remove(tmpName)
			fmt.Fprintf(os.Stderr, "error: failed to back up existing %s: %v\n", rinseConfigFile, rerr)
			os.Exit(1)
		}
	} else if !os.IsNotExist(serr) {
		os.Remove(tmpName)
		fmt.Fprintf(os.Stderr, "error: failed to stat %s: %v\n", rinseConfigFile, serr)
		os.Exit(1)
	}
	if rerr := os.Rename(tmpName, rinseConfigFile); rerr != nil {
		if hadExisting {
			if rerr2 := os.Rename(backupName, rinseConfigFile); rerr2 != nil {
				fmt.Fprintf(os.Stderr, "error: failed to write %s: %v (also failed to restore backup: %v)\n", rinseConfigFile, rerr, rerr2)
				os.Remove(tmpName)
				os.Exit(1)
			}
		}
		os.Remove(tmpName)
		fmt.Fprintf(os.Stderr, "error: failed to write %s: %v\n", rinseConfigFile, rerr)
		os.Exit(1)
	}
	if hadExisting {
		if rerr := os.Remove(backupName); rerr != nil && !os.IsNotExist(rerr) {
			fmt.Fprintf(os.Stderr, "warning: created %s but failed to remove backup %s: %v\n", rinseConfigFile, backupName, rerr)
		}
	}

	fmt.Printf("\n✓ Created %s\n", rinseConfigFile)
	fmt.Println()
	fmt.Println("Tip: commit .rinse.json to give your team a starting reference for settings.")
}
