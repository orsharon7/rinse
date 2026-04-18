// Package predict implements the `rinse predict` command — Report Mode (v0.3).
//
// It reads the staged diff (or PR diff) and applies a set of AST-based pattern
// detectors to predict which issues GitHub Copilot is likely to comment on.
// Each prediction carries a confidence score in [0,1].
//
// # Design notes
//
//   - No ML in v0.3: all patterns are deterministic AST / text heuristics.
//   - ML-backed patterns are gated behind the sessions DB (v0.4 dependency).
//   - Exit 0 even when predictions exist — the command is non-blocking.
//   - Prediction events are logged for hit-rate tracking (target ≥70%).
package predict

import (
	"bufio"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// ─── Public types ─────────────────────────────────────────────────────────────

// Prediction is a single predicted Copilot comment pattern.
type Prediction struct {
	// Pattern is the human-readable pattern name, e.g. "Missing error handling".
	Pattern string

	// Confidence is the model's certainty in [0.0, 1.0].
	Confidence float64

	// File is the source file where the issue was detected (may be empty for
	// diff-level patterns).
	File string

	// Line is the approximate line number inside File (0 means unknown).
	Line int

	// Detail is an optional one-sentence explanation.
	Detail string
}

// Report is the full output of a predict run.
type Report struct {
	// Predictions is the ordered list of findings (highest confidence first).
	Predictions []Prediction

	// Source is a human-readable description of what was analysed.
	Source string

	// GeneratedAt is when the report was produced.
	GeneratedAt time.Time
}

// ─── Entry point ──────────────────────────────────────────────────────────────

// Run performs the prediction analysis and returns a Report.
// It reads the staged diff when pr == 0, or the PR diff when pr > 0.
// repo is the "owner/repo" string used by gh; it may be empty for staged-diff mode.
func Run(pr int, repo string) (*Report, error) {
	var diff string
	var source string
	var err error

	if pr > 0 {
		diff, err = prDiff(pr, repo)
		if err != nil {
			return nil, fmt.Errorf("predict: fetch PR diff: %w", err)
		}
		source = fmt.Sprintf("PR #%d (%s)", pr, repo)
	} else {
		diff, err = stagedDiff()
		if err != nil {
			return nil, fmt.Errorf("predict: fetch staged diff: %w", err)
		}
		source = "staged changes"
	}

	if strings.TrimSpace(diff) == "" {
		return &Report{
			Source:      source,
			GeneratedAt: time.Now(),
		}, nil
	}

	chunks := parseDiff(diff)
	predictions := analyseChunks(chunks)
	sortByConfidence(predictions)

	return &Report{
		Predictions: predictions,
		Source:      source,
		GeneratedAt: time.Now(),
	}, nil
}

// ─── Diff fetching ────────────────────────────────────────────────────────────

func stagedDiff() (string, error) {
	out, err := exec.Command("git", "diff", "--cached", "--unified=5").Output()
	if err != nil {
		return "", fmt.Errorf("git diff --cached: %w", err)
	}
	return string(out), nil
}

func prDiff(pr int, repo string) (string, error) {
	args := []string{"pr", "diff", fmt.Sprintf("%d", pr), "--patch"}
	if repo != "" {
		args = append(args, "--repo", repo)
	}
	out, err := exec.Command("gh", args...).Output()
	if err != nil {
		return "", fmt.Errorf("gh pr diff: %w", err)
	}
	return string(out), nil
}

// ─── Diff parsing ─────────────────────────────────────────────────────────────

// diffChunk holds the added lines from a single file in a unified diff.
type diffChunk struct {
	file  string // destination file path
	lines []string
	isGo  bool
}

var (
	reDiffFile = regexp.MustCompile(`^\+\+\+ b/(.+)$`)
	reDiffAdd  = regexp.MustCompile(`^\+([^+]|$)`)
)

func parseDiff(diff string) []diffChunk {
	var chunks []diffChunk
	var cur *diffChunk

	scanner := bufio.NewScanner(strings.NewReader(diff))
	for scanner.Scan() {
		line := scanner.Text()

		if m := reDiffFile.FindStringSubmatch(line); m != nil {
			path := m[1]
			chunks = append(chunks, diffChunk{
				file: path,
				isGo: strings.HasSuffix(path, ".go"),
			})
			cur = &chunks[len(chunks)-1]
			continue
		}
		if cur == nil {
			continue
		}
		if reDiffAdd.MatchString(line) {
			cur.lines = append(cur.lines, line[1:]) // strip leading '+'
		}
	}
	return chunks
}

// ─── Pattern analysis ─────────────────────────────────────────────────────────

func analyseChunks(chunks []diffChunk) []Prediction {
	var preds []Prediction
	for i := range chunks {
		preds = append(preds, detectPatterns(&chunks[i])...)
	}
	return preds
}

// detectPatterns applies every detector to a single diff chunk.
func detectPatterns(chunk *diffChunk) []Prediction {
	var preds []Prediction

	// Text-based detectors run on all file types.
	preds = append(preds, detectTODOsAndHacks(chunk)...)
	preds = append(preds, detectHardcodedSecrets(chunk)...)
	preds = append(preds, detectLongFunctions(chunk)...)

	// Go-specific AST detectors.
	if chunk.isGo {
		src := strings.Join(chunk.lines, "\n")
		preds = append(preds, detectMissingErrorHandling(chunk.file, src)...)
		preds = append(preds, detectUnusedVariables(chunk.file, src)...)
		preds = append(preds, detectNakedReturns(chunk.file, src)...)
	}

	return preds
}

// ─── Pattern 1: Missing error handling ───────────────────────────────────────

var reErrIgnored = regexp.MustCompile(`^[^/\n]*,\s*_\s*:?=.*(?:err|Err|error)`)

func detectMissingErrorHandling(file, src string) []Prediction {
	var preds []Prediction

	// Heuristic 1: blank identifier discards an error-like return.
	scanner := bufio.NewScanner(strings.NewReader(src))
	lineNo := 0
	for scanner.Scan() {
		lineNo++
		line := scanner.Text()
		if reErrIgnored.MatchString(line) {
			preds = append(preds, Prediction{
				Pattern:    "Missing error handling",
				Confidence: 0.88,
				File:       file,
				Line:       lineNo,
				Detail:     "Error return discarded with blank identifier; handle or propagate the error.",
			})
		}
	}

	// Heuristic 2: AST — function calls whose error return is ignored.
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, file, src, 0)
	if err != nil {
		// Partial diff may not parse; fall back to text heuristics already done.
		return preds
	}
	ast.Inspect(f, func(n ast.Node) bool {
		es, ok := n.(*ast.ExprStmt)
		if !ok {
			return true
		}
		call, ok := es.X.(*ast.CallExpr)
		if !ok {
			return true
		}
		// If the function name ends in a common error-returning pattern and
		// it's used as a standalone statement, flag it.
		name := callName(call)
		if isErrorReturningFunc(name) {
			pos := fset.Position(es.Pos())
			preds = append(preds, Prediction{
				Pattern:    "Missing error handling",
				Confidence: 0.75,
				File:       file,
				Line:       pos.Line,
				Detail:     fmt.Sprintf("Return value of %s() not checked; Copilot frequently flags unhandled errors.", name),
			})
		}
		return true
	})

	return preds
}

func callName(call *ast.CallExpr) string {
	switch fn := call.Fun.(type) {
	case *ast.Ident:
		return fn.Name
	case *ast.SelectorExpr:
		return fn.Sel.Name
	}
	return ""
}

var errorReturningFuncs = map[string]bool{
	"Close": true, "Write": true, "Read": true, "Flush": true,
	"Sync": true, "Seek": true, "Chmod": true, "Chown": true,
	"Remove": true, "Rename": true, "MkdirAll": true, "Mkdir": true,
	"WriteFile": true, "ReadFile": true, "Copy": true,
}

func isErrorReturningFunc(name string) bool {
	return errorReturningFuncs[name]
}

// ─── Pattern 2: Unused variables ─────────────────────────────────────────────

func detectUnusedVariables(file, src string) []Prediction {
	var preds []Prediction

	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, file, src, 0)
	if err != nil {
		return preds
	}

	// Collect all ident definitions and usages in each function body.
	ast.Inspect(f, func(n ast.Node) bool {
		fn, ok := n.(*ast.FuncDecl)
		if !ok || fn.Body == nil {
			return true
		}

		defined := map[string]int{}  // name → line
		used := map[string]bool{}

		ast.Inspect(fn.Body, func(inner ast.Node) bool {
			switch v := inner.(type) {
			case *ast.AssignStmt:
				if v.Tok == token.DEFINE {
					for _, lhs := range v.Lhs {
						id, ok := lhs.(*ast.Ident)
						if ok && id.Name != "_" {
							defined[id.Name] = fset.Position(id.Pos()).Line
						}
					}
				}
			case *ast.Ident:
				used[v.Name] = true
			}
			return true
		})

		for name, line := range defined {
			// Only flag if defined but never referenced (usage map includes all idents).
			// This is an over-approximation but mirrors what Copilot typically flags.
			if !used[name] {
				preds = append(preds, Prediction{
					Pattern:    "Unused variable",
					Confidence: 0.82,
					File:       file,
					Line:       line,
					Detail:     fmt.Sprintf("Variable %q is assigned but never used.", name),
				})
			}
		}
		return true
	})

	return preds
}

// ─── Pattern 3: Naked returns ─────────────────────────────────────────────────

func detectNakedReturns(file, src string) []Prediction {
	var preds []Prediction

	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, file, src, 0)
	if err != nil {
		return preds
	}

	ast.Inspect(f, func(n ast.Node) bool {
		fn, ok := n.(*ast.FuncDecl)
		if !ok || fn.Type.Results == nil || fn.Body == nil {
			return true
		}

		// Only flag functions with named return values.
		hasNamed := false
		for _, field := range fn.Type.Results.List {
			if len(field.Names) > 0 {
				hasNamed = true
				break
			}
		}
		if !hasNamed {
			return true
		}

		ast.Inspect(fn.Body, func(inner ast.Node) bool {
			ret, ok := inner.(*ast.ReturnStmt)
			if !ok || len(ret.Results) > 0 {
				return true
			}
			pos := fset.Position(ret.Pos())
			preds = append(preds, Prediction{
				Pattern:    "Naked return in long function",
				Confidence: 0.72,
				File:       file,
				Line:       pos.Line,
				Detail:     "Naked return reduces readability; Copilot frequently suggests explicit returns.",
			})
			return true
		})
		return true
	})

	return preds
}

// ─── Pattern 4: TODO / HACK / FIXME comments ─────────────────────────────────

var reTODO = regexp.MustCompile(`(?i)\b(TODO|FIXME|HACK|XXX)\b`)

func detectTODOsAndHacks(chunk *diffChunk) []Prediction {
	var preds []Prediction
	for i, line := range chunk.lines {
		if reTODO.MatchString(line) && isComment(line) {
			preds = append(preds, Prediction{
				Pattern:    "TODO/FIXME left in code",
				Confidence: 0.65,
				File:       chunk.file,
				Line:       i + 1,
				Detail:     "Copilot often flags TODO/FIXME markers in newly added code.",
			})
		}
	}
	return preds
}

func isComment(line string) bool {
	t := strings.TrimSpace(line)
	return strings.HasPrefix(t, "//") ||
		strings.HasPrefix(t, "#") ||
		strings.HasPrefix(t, "/*") ||
		strings.HasPrefix(t, "*")
}

// ─── Pattern 5: Hardcoded secrets / tokens ────────────────────────────────────

var reSecret = regexp.MustCompile(
	`(?i)(password|secret|token|api[_\-]?key|apikey|private[_\-]?key|access[_\-]?key)\s*[:=]+\s*["'][^"']{6,}["']`,
)

func detectHardcodedSecrets(chunk *diffChunk) []Prediction {
	var preds []Prediction
	for i, line := range chunk.lines {
		t := strings.TrimSpace(line)
		if strings.HasPrefix(t, "//") {
			continue // skip comments
		}
		if reSecret.MatchString(line) {
			preds = append(preds, Prediction{
				Pattern:    "Hardcoded secret / credential",
				Confidence: 0.93,
				File:       chunk.file,
				Line:       i + 1,
				Detail:     "Copilot always flags hardcoded secrets; move to env vars or a secrets manager.",
			})
		}
	}
	return preds
}

// ─── Pattern 6: Overly long functions (line-count proxy) ─────────────────────

// detectLongFunctions flags new additions to files where the added diff is very
// dense — a proxy for a function that will exceed the complexity threshold
// Copilot typically flags.
func detectLongFunctions(chunk *diffChunk) []Prediction {
	const threshold = 60 // added lines in a single diff chunk
	if len(chunk.lines) >= threshold {
		return []Prediction{{
			Pattern:    "Overly long function / block",
			Confidence: 0.60,
			File:       chunk.file,
			Detail: fmt.Sprintf(
				"This diff adds %d lines in one chunk; Copilot frequently suggests splitting functions over ~50 lines.",
				len(chunk.lines),
			),
		}}
	}
	return nil
}

// ─── Helpers ──────────────────────────────────────────────────────────────────

func sortByConfidence(preds []Prediction) {
	for i := 1; i < len(preds); i++ {
		for j := i; j > 0 && preds[j].Confidence > preds[j-1].Confidence; j-- {
			preds[j], preds[j-1] = preds[j-1], preds[j]
		}
	}
}

// ─── Event logging ────────────────────────────────────────────────────────────

// EventLogger is a function type for recording prediction events.
// It receives the report for side-effect logging and must not block.
// A nil logger is a no-op.
type EventLogger func(r *Report) error

// LogEvent records prediction events to ~/.rinse/predict-events.log.
// Each line is JSON: {"ts":"…","source":"…","pattern":"…","confidence":0.9}
func LogEvent(r *Report) error {
	if r == nil || len(r.Predictions) == 0 {
		return nil
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	dir := filepath.Join(home, ".rinse")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}

	logPath := filepath.Join(dir, "predict-events.log")
	f, err := os.OpenFile(logPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()

	ts := r.GeneratedAt.UTC().Format(time.RFC3339)
	for _, p := range r.Predictions {
		line := fmt.Sprintf(
			`{"ts":%q,"source":%q,"pattern":%q,"confidence":%.2f,"file":%q,"line":%d}`+"\n",
			ts, r.Source, p.Pattern, p.Confidence, p.File, p.Line,
		)
		if _, err := f.WriteString(line); err != nil {
			return err
		}
	}
	return nil
}
