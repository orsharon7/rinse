package agent

import "strings"

// patternRule maps a set of keywords to a canonical pattern label.
// Rules are evaluated in order; the first match wins.
type patternRule struct {
	label    string
	keywords []string
}

// rules is the ordered keyword-to-label classification table.
// Labels are snake_case to be query-friendly in SQLite.
var rules = []patternRule{
	{"error_handling", []string{"error", "err", "panic", "recover", "exception", "fatal"}},
	{"naming", []string{"naming", "rename", "identifier", "variable name", "function name", "method name", "exported", "unexported", "camel", "snake_case"}},
	{"docs", []string{"comment", "godoc", "doc", "documentation", "missing doc", "undocumented"}},
	{"formatting", []string{"format", "indent", "whitespace", "blank line", "trailing", "gofmt", "lint"}},
	{"performance", []string{"performance", "alloc", "allocation", "memory", "efficient", "optimize", "cache"}},
	{"security", []string{"security", "injection", "xss", "csrf", "sanitize", "escape", "auth", "unauthori"}},
	{"testing", []string{"test", "assertion", "mock", "coverage", "unit test", "table-driven"}},
	{"concurrency", []string{"goroutine", "mutex", "race", "concurrent", "sync", "channel", "deadlock"}},
	{"nil_check", []string{"nil", "null", "nil pointer", "dereference"}},
	{"unused_code", []string{"unused", "dead code", "unreachable", "unnecessary"}},
	{"imports", []string{"import", "dependency", "package"}},
	{"complexity", []string{"complex", "simplif", "refactor", "extract", "too long", "cyclomatic"}},
	{"type_safety", []string{"type assert", "interface", "cast", "conversion", "type mismatch"}},
	{"logging", []string{"log", "logging", "slog", "printf", "fmt.print"}},
}

// ExtractPatterns classifies a slice of comments into pattern labels.
// Each comment body is matched against keyword rules; matched labels are
// deduplicated and returned in encounter order.
//
// Comments with no keyword match are classified as "other".
func ExtractPatterns(comments []Comment) []string {
	seen := map[string]bool{}
	var out []string

	for _, c := range comments {
		if c.InReplyToID != nil {
			continue // only classify top-level Copilot comments
		}
		label := classify(c.Body)
		if !seen[label] {
			seen[label] = true
			out = append(out, label)
		}
	}
	return out
}

// classify returns the best-matching pattern label for a single comment body.
func classify(body string) string {
	lower := strings.ToLower(body)
	for _, rule := range rules {
		for _, kw := range rule.keywords {
			if strings.Contains(lower, kw) {
				return rule.label
			}
		}
	}
	return "other"
}
