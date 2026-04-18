package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const copilotInstructionsPath = ".github/copilot-instructions.md"

// detectedLanguage represents a primary language detected in the repo.
type detectedLanguage string

const (
	langGo         detectedLanguage = "go"
	langJavaScript detectedLanguage = "javascript"
	langTypeScript  detectedLanguage = "typescript"
	langPython     detectedLanguage = "python"
	langRust       detectedLanguage = "rust"
	langUnknown    detectedLanguage = "unknown"
)

// detectPrimaryLanguage inspects the current directory for common language
// marker files and returns the most likely primary language.
func detectPrimaryLanguage() detectedLanguage {
	checks := []struct {
		file string
		lang detectedLanguage
	}{
		{"go.mod", langGo},
		{"go.sum", langGo},
		{"tsconfig.json", langTypeScript},
		{"package.json", langJavaScript},
		{"requirements.txt", langPython},
		{"pyproject.toml", langPython},
		{"setup.py", langPython},
		{"Cargo.toml", langRust},
	}

	for _, c := range checks {
		if _, err := os.Stat(c.file); err == nil {
			return c.lang
		}
	}

	return langUnknown
}

// copilotInstructionsTemplate returns a language-appropriate copilot instructions
// template. The repoName is used for personalising the header.
func copilotInstructionsTemplate(lang detectedLanguage) string {
	shared := `
## Focus Areas (what to flag)
- Bugs, logic errors, correctness issues
- Security vulnerabilities (hardcoded secrets, injection, path traversal)
- Missing error handling
- Race conditions and concurrency issues
- Off-by-one errors and boundary conditions

## Skip These (do not comment on)
- Code style, formatting, naming conventions
- Whitespace and line length
- Missing comments or documentation
- TODOs and FIXMEs
- Test file style issues
- Import ordering`

	switch lang {
	case langGo:
		return `# Copilot Code Review Instructions
` + shared + `

## Go-Specific
- Flag: error return values that are explicitly discarded with _
- Flag: goroutines without proper cancellation
- Flag: missing context propagation
- Skip: minor naming style (camelCase vs snake_case debates)
`

	case langTypeScript:
		return `# Copilot Code Review Instructions
` + shared + `

## TypeScript-Specific
- Flag: unsafe type assertions (as any, as unknown casts without guards)
- Flag: unhandled promise rejections and floating promises
- Flag: null/undefined dereferences without proper guards
- Skip: minor import style preferences
`

	case langJavaScript:
		return `# Copilot Code Review Instructions
` + shared + `

## JavaScript-Specific
- Flag: unhandled promise rejections and floating promises
- Flag: null/undefined dereferences without proper guards
- Flag: missing async/await where promises are returned
- Skip: minor import style preferences
`

	case langPython:
		return `# Copilot Code Review Instructions
` + shared + `

## Python-Specific
- Flag: bare except clauses that swallow all exceptions
- Flag: mutable default arguments
- Flag: SQL string formatting (use parameterized queries)
- Skip: PEP 8 style debates already handled by a linter
`

	case langRust:
		return `# Copilot Code Review Instructions
` + shared + `

## Rust-Specific
- Flag: unwrap() / expect() in production paths without justification
- Flag: unsafe blocks without a SAFETY comment
- Flag: integer overflow risks in release builds
- Skip: clippy naming convention suggestions already handled by CI
`

	default:
		return `# Copilot Code Review Instructions
` + shared + `
`
	}
}

// languageName returns a human-readable name for the detected language.
func languageName(lang detectedLanguage) string {
	switch lang {
	case langGo:
		return "Go"
	case langTypeScript:
		return "TypeScript"
	case langJavaScript:
		return "JavaScript"
	case langPython:
		return "Python"
	case langRust:
		return "Rust"
	default:
		return "generic"
	}
}

// RunInitCopilotInstructions checks for .github/copilot-instructions.md and
// offers to generate it if missing. reader is used for prompting; pass nil to
// skip the prompt and always write (useful for tests).
func RunInitCopilotInstructions(reader interface {
	ReadString(byte) (string, error)
}) error {
	// Check if the file already exists.
	if _, err := os.Stat(copilotInstructionsPath); err == nil {
		fmt.Printf("✓ %s already exists — skipping\n", copilotInstructionsPath)
		return nil
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("failed to stat %s: %w", copilotInstructionsPath, err)
	}

	lang := detectPrimaryLanguage()
	fmt.Printf("Add Copilot review instructions to reduce noise? (%s template) [Y/n]: ", languageName(lang))

	line, _ := reader.ReadString('\n')
	line = strings.TrimSpace(strings.ToLower(line))
	if line == "n" || line == "no" {
		fmt.Println("Skipped Copilot instructions.")
		return nil
	}

	template := copilotInstructionsTemplate(lang)

	// Ensure .github/ directory exists.
	if err := os.MkdirAll(filepath.Dir(copilotInstructionsPath), 0o755); err != nil {
		return fmt.Errorf("failed to create .github directory: %w", err)
	}

	// Write atomically via temp file.
	tmpFile, err := os.CreateTemp(filepath.Dir(copilotInstructionsPath), ".copilot-instructions.tmp*")
	if err != nil {
		return fmt.Errorf("failed to create temp file: %w", err)
	}
	tmpName := tmpFile.Name()

	if err := tmpFile.Chmod(0o644); err != nil {
		tmpFile.Close()
		os.Remove(tmpName)
		return fmt.Errorf("failed to set permissions: %w", err)
	}

	if _, err := tmpFile.WriteString(template); err != nil {
		tmpFile.Close()
		os.Remove(tmpName)
		return fmt.Errorf("failed to write instructions: %w", err)
	}

	if err := tmpFile.Close(); err != nil {
		os.Remove(tmpName)
		return fmt.Errorf("failed to close temp file: %w", err)
	}

	if err := os.Rename(tmpName, copilotInstructionsPath); err != nil {
		os.Remove(tmpName)
		return fmt.Errorf("failed to write %s: %w", copilotInstructionsPath, err)
	}

	fmt.Printf("✓ Created %s\n", copilotInstructionsPath)
	fmt.Println("  Tip: commit this file so Copilot uses it for every PR in this repo.")
	return nil
}
