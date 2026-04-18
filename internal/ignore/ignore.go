// Package ignore implements .rinseignore parsing and path matching.
//
// .rinseignore uses the same syntax as .gitignore: glob patterns, one per
// line, with # comments and blank-line skipping. Patterns are matched against
// file paths using the standard path/filepath.Match semantics extended with
// directory-prefix matching so that "vendor/" matches "vendor/foo/bar.go".
package ignore

import (
	"bufio"
	"io"
	"os"
	"path/filepath"
	"strings"
)

const IgnoreFile = ".rinseignore"

// Matcher holds the compiled pattern list from a .rinseignore file.
// The zero value matches nothing (no patterns loaded).
type Matcher struct {
	patterns []string
}

// Load reads patterns from the .rinseignore file at repoRoot.
// If the file does not exist, an empty Matcher (matches nothing) is returned
// without error — this is the normal case for repos that haven't opted in.
func Load(repoRoot string) (Matcher, error) {
	path := filepath.Join(repoRoot, IgnoreFile)
	f, err := os.Open(path)
	if os.IsNotExist(err) {
		return Matcher{}, nil
	}
	if err != nil {
		return Matcher{}, err
	}
	defer f.Close()
	return parse(f)
}

// ParsePatterns creates a Matcher directly from a slice of raw pattern strings.
// Blank lines and lines beginning with # are ignored, matching .gitignore rules.
// Useful for testing and for passing pre-loaded patterns.
func ParsePatterns(lines []string) Matcher {
	m := Matcher{}
	for _, line := range lines {
		line = strings.TrimRight(line, " \t\r")
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		m.patterns = append(m.patterns, line)
	}
	return m
}

// parse reads patterns from r (one per line).
func parse(r io.Reader) (Matcher, error) {
	var patterns []string
	sc := bufio.NewScanner(r)
	for sc.Scan() {
		line := strings.TrimRight(sc.Text(), " \t\r")
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		patterns = append(patterns, line)
	}
	if err := sc.Err(); err != nil {
		return Matcher{}, err
	}
	return Matcher{patterns: patterns}, nil
}

// Patterns returns the raw pattern strings held by this Matcher.
// The returned slice is a copy; callers may modify it freely.
func (m Matcher) Patterns() []string {
	if len(m.patterns) == 0 {
		return nil
	}
	out := make([]string, len(m.patterns))
	copy(out, m.patterns)
	return out
}

// Matches reports whether filePath should be excluded from RINSE processing.
//
// Matching rules (subset of .gitignore semantics):
//   - Each pattern is tested with filepath.Match (shell glob).
//   - If a pattern ends with "/" it is a directory prefix: any path whose
//     components include that directory prefix is matched.
//   - Patterns without "/" are matched against just the base name of filePath
//     in addition to the full relative path, so "*.gen.go" matches
//     "internal/foo/bar.gen.go".
//   - A leading "!" (negation) is not yet implemented.
func (m Matcher) Matches(filePath string) bool {
	// Normalise to forward slashes for consistent glob matching.
	filePath = filepath.ToSlash(filePath)
	// Strip a leading "./" if present.
	filePath = strings.TrimPrefix(filePath, "./")

	for _, pat := range m.patterns {
		if matchPattern(pat, filePath) {
			return true
		}
	}
	return false
}

// matchPattern tests a single pattern against a file path.
func matchPattern(pat, filePath string) bool {
	pat = filepath.ToSlash(pat)

	// Directory prefix pattern (ends with "/").
	if strings.HasSuffix(pat, "/") {
		// Strip trailing slash for prefix comparison.
		dir := strings.TrimSuffix(pat, "/")
		// Match if the file is inside that directory (prefix match on path
		// components, not just a raw string prefix).
		if isUnderDir(filePath, dir) {
			return true
		}
		// Also try glob against the directory prefix portion.
		parts := strings.SplitN(filePath, "/", -1)
		for i := range parts {
			prefix := strings.Join(parts[:i+1], "/")
			if ok, _ := filepath.Match(dir, prefix); ok {
				return true
			}
		}
		return false
	}

	// Pattern without "/" — try matching the full path first, then just the
	// base name (mimics .gitignore "no slash = match basename anywhere").
	if !strings.Contains(pat, "/") {
		base := filepath.Base(filePath)
		if ok, _ := filepath.Match(pat, base); ok {
			return true
		}
	}

	// Full-path glob match.
	if ok, _ := filepath.Match(pat, filePath); ok {
		return true
	}

	// Handle leading "**/"-style patterns: try matching the suffix of the path.
	if strings.HasPrefix(pat, "**/") {
		suffix := strings.TrimPrefix(pat, "**/")
		if ok, _ := filepath.Match(suffix, filePath); ok {
			return true
		}
		// Also try each sub-path.
		parts := strings.Split(filePath, "/")
		for i := range parts {
			sub := strings.Join(parts[i:], "/")
			if ok, _ := filepath.Match(suffix, sub); ok {
				return true
			}
		}
	}

	return false
}

// isUnderDir returns true if filePath is under the directory named dir.
// Both arguments must use forward slashes.
func isUnderDir(filePath, dir string) bool {
	// Ensure dir ends with "/" for prefix matching on full components.
	prefix := dir + "/"
	return strings.HasPrefix(filePath, prefix) || filePath == dir
}

// DefaultIgnoreContent is the content written to .rinseignore by `rinse init`.
const DefaultIgnoreContent = `# RINSE ignore file
# Add paths/patterns to exclude from AI review cycles.
# Uses .gitignore syntax: glob patterns, one per line, # for comments.

# Common generated files
*.pb.go
*.gen.go
*.pb.gw.go
vendor/

# Auto-generated mocks
internal/mocks/

# Database migrations (auto-generated)
# internal/db/migrations/*.sql

# Minified/compiled assets
# website/dist/
# *.min.js
`
