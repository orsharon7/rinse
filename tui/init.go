package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

// RepoRinseConfig is the per-repository .rinse.json config file structure.
// It stores shared team settings so everyone reviewing PRs in the same repo
// uses consistent defaults without having to configure them individually.
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
	}

	fmt.Println("Initializing Rinse config for this repo...")
	fmt.Println()

	// Prompt for engine.
	fmt.Println("Select engine:")
	for i, r := range runners {
		fmt.Printf("  [%d] %s — %s\n", i+1, r.name, r.desc)
	}
	fmt.Printf("Engine (1-%d) [1]: ", len(runners))
	engineLine, _ := reader.ReadString('\n')
	engineLine = strings.TrimSpace(engineLine)

	runnerIdx := 0
	if engineLine != "" {
		for i, r := range runners {
			if engineLine == fmt.Sprintf("%d", i+1) || strings.EqualFold(engineLine, r.name) {
				runnerIdx = i
				break
			}
		}
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

	// Write via temp file + rename for atomicity (avoids partial writes on crash).
	tmp, err := os.CreateTemp(".", ".rinse.json.tmp.*")
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: failed to create temp config: %v\n", err)
		os.Exit(1)
	}
	tmpName := tmp.Name()
	defer func() { _ = os.Remove(tmpName) }() // clean up on failure
	if _, err := tmp.Write(append(data, '\n')); err != nil {
		_ = tmp.Close()
		fmt.Fprintf(os.Stderr, "error: failed to write temp config: %v\n", err)
		os.Exit(1)
	}
	if err := tmp.Close(); err != nil {
		fmt.Fprintf(os.Stderr, "error: failed to close temp config: %v\n", err)
		os.Exit(1)
	}
	if err := os.Rename(tmpName, rinseConfigFile); err != nil {
		fmt.Fprintf(os.Stderr, "error: failed to write %s: %v\n", rinseConfigFile, err)
		os.Exit(1)
	}

	fmt.Printf("\n✓ Created %s\n", rinseConfigFile)
	fmt.Println()
	fmt.Println("Tip: commit .rinse.json so your team shares the same settings.")
}
