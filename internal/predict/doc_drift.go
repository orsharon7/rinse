// Package predict — doc_drift.go
//
// DocDrift implements the `rinse predict --doc-drift` LLM documentation drift
// detector (RIN-213 / v0.4 spec from RIN-189).
//
// # What it detects
//
//   - Function signature changes with stale doc comments (godoc says X, code does Y)
//   - Exported types / functions missing godoc comments entirely
//   - README code examples that no longer compile (heuristic: API surface mismatch)
//
// # Design constraints (from spec)
//
//   - Copilot API only — no third-party LLM calls without an explicit --llm flag
//   - Max 10 LLM calls per PR run; each call counted and logged
//   - P95 latency target <3s per call (enforced by per-call timeout)
//   - Opt-in only: requires doc_drift=true in ~/.rinse/config.json OR --doc-drift flag
//   - Pro-gated: non-Pro users see the upgrade prompt instead
//
// # Architecture
//
// RunDocDrift takes a diff string, extracts candidate sites (exported Go
// symbols whose doc comment might be stale), then calls the LLM via the
// LLMClient interface.  The interface is satisfied by CopilotLLMClient for
// production and MockLLMClient for unit tests — keeping latency tests purely
// deterministic.
package predict

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// ─── LLM client abstraction ───────────────────────────────────────────────────

// LLMClient is the minimal interface required by the doc-drift detector.
// It is satisfied by CopilotLLMClient (production) and MockLLMClient (tests).
type LLMClient interface {
	// Complete sends a single prompt and returns the model's text response.
	// The context deadline is used to enforce per-call latency budgets.
	Complete(ctx context.Context, prompt string) (string, error)
}

// ─── DriftItem ────────────────────────────────────────────────────────────────

// DriftItem is a single documentation drift finding.
type DriftItem struct {
	// Kind classifies the drift type.
	Kind string // "stale_godoc" | "missing_godoc" | "readme_example" | "cap_reached"

	// File and Line locate the drift site.
	File string
	Line int

	// Symbol is the Go identifier (function, type, method, …) that drifted.
	Symbol string

	// Detail is a one-sentence description of what drifted and what to fix.
	Detail string

	// SuggestedDoc is the LLM-suggested replacement doc comment, if available.
	SuggestedDoc string
}

// ─── DocDriftReport ───────────────────────────────────────────────────────────

// DocDriftReport is the output of RunDocDrift.
type DocDriftReport struct {
	// Items is the ordered list of drift findings.
	Items []DriftItem

	// LLMCallCount is the number of LLM calls consumed.
	LLMCallCount int

	// Source mirrors the outer Report.Source.
	Source string
}

// ─── Options ─────────────────────────────────────────────────────────────────

// DocDriftOptions controls the detector's behaviour.
type DocDriftOptions struct {
	// MaxLLMCalls caps the number of LLM calls.  Default: 10 (spec max).
	MaxLLMCalls int

	// PerCallTimeout is the per-LLM-call deadline.  Default: 3s.
	PerCallTimeout time.Duration

	// Client is the LLM backend.  If nil, a no-op client is used (no calls).
	Client LLMClient
}

func (o *DocDriftOptions) maxCalls() int {
	if o.MaxLLMCalls <= 0 {
		return 10
	}
	return o.MaxLLMCalls
}

func (o *DocDriftOptions) timeout() time.Duration {
	if o.PerCallTimeout <= 0 {
		return 3 * time.Second
	}
	return o.PerCallTimeout
}

func (o *DocDriftOptions) client() LLMClient {
	if o.Client == nil {
		return &noopLLMClient{}
	}
	return o.Client
}

// ─── RunDocDrift ──────────────────────────────────────────────────────────────

// RunDocDrift analyses diff and the on-disk Go files it touches for documentation
// drift.  It returns a DocDriftReport containing all findings and the LLM call
// count consumed.
//
// The function enforces the following invariants:
//   - At most opts.MaxLLMCalls (default 10) LLM calls are made; remaining
//     candidates are skipped and a note is added to the last DriftItem.Detail.
//   - Each LLM call is bounded by opts.PerCallTimeout (default 3s).
//   - No files in the working tree are created or modified.
func RunDocDrift(diff, source string, opts DocDriftOptions) (*DocDriftReport, error) {
	rep := &DocDriftReport{Source: source}

	chunks := parseDiff(diff)
	if len(chunks) == 0 {
		return rep, nil
	}

	maxCalls := opts.maxCalls()
	timeout := opts.timeout()
	client := opts.client()

	callCount := 0
	capReached := false

outer:
	for i := range chunks {
		chunk := &chunks[i]

		// README drift is a heuristic check (no LLM call needed).
		readmeItems := detectReadmeDrift(chunk)
		rep.Items = append(rep.Items, readmeItems...)

		if !chunk.isGo {
			continue
		}

		candidates := extractDocDriftCandidates(chunk)
		for _, cand := range candidates {
			if callCount >= maxCalls {
				if !capReached {
					rep.Items = append(rep.Items, DriftItem{
						Kind:   "cap_reached",
						File:   chunk.file,
						Symbol: cand.symbol,
						Detail: fmt.Sprintf("LLM call cap (%d) reached; remaining candidates not analysed.", maxCalls),
					})
					capReached = true
				}
				break outer
			}

			item, err := analyseCandidate(client, timeout, chunk.file, cand)
			callCount++
			if err != nil {
				// Non-fatal: record the error and continue.
				rep.Items = append(rep.Items, DriftItem{
					Kind:   cand.kind,
					File:   chunk.file,
					Line:   cand.line,
					Symbol: cand.symbol,
					Detail: fmt.Sprintf("LLM call failed: %v", err),
				})
				continue
			}
			if item != nil {
				rep.Items = append(rep.Items, *item)
			}
		}
	}

	rep.LLMCallCount = callCount
	return rep, nil
}

// ─── Candidate extraction ─────────────────────────────────────────────────────

// driftCandidate is an internal representation of a potential drift site.
type driftCandidate struct {
	kind    string // "stale_godoc" | "missing_godoc"
	symbol  string
	line    int
	docText string // existing doc comment, if any
	sig     string // function/type signature text
}

// extractDocDriftCandidates parses the added Go lines in chunk and returns
// candidate symbols for LLM analysis.
func extractDocDriftCandidates(chunk *diffChunk) []driftCandidate {
	src := strings.Join(chunk.lines, "\n")
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, chunk.file, src, parser.ParseComments)
	if err != nil {
		// Partial diffs won't parse cleanly; fall back to text scanning.
		return extractDocDriftCandidatesText(chunk)
	}

	var candidates []driftCandidate

	ast.Inspect(f, func(n ast.Node) bool {
		switch decl := n.(type) {
		case *ast.FuncDecl:
			if !decl.Name.IsExported() {
				return true
			}
			pos := fset.Position(decl.Pos())
			cand := driftCandidate{
				symbol: decl.Name.Name,
				line:   pos.Line,
				sig:    funcDeclSig(decl),
			}
			if decl.Doc != nil && decl.Doc.Text() != "" {
				cand.kind = "stale_godoc"
				cand.docText = strings.TrimSpace(decl.Doc.Text())
			} else {
				cand.kind = "missing_godoc"
			}
			candidates = append(candidates, cand)

		case *ast.GenDecl:
			for _, spec := range decl.Specs {
				ts, ok := spec.(*ast.TypeSpec)
				if !ok || !ts.Name.IsExported() {
					continue
				}
				pos := fset.Position(ts.Pos())
				cand := driftCandidate{
					symbol: ts.Name.Name,
					line:   pos.Line,
					sig:    ts.Name.Name + " (type)",
				}
				if decl.Doc != nil && decl.Doc.Text() != "" {
					cand.kind = "stale_godoc"
					cand.docText = strings.TrimSpace(decl.Doc.Text())
				} else {
					cand.kind = "missing_godoc"
				}
				candidates = append(candidates, cand)
			}
		}
		return true
	})

	return candidates
}

// extractDocDriftCandidatesText is the text-only fallback when AST parsing fails.
var (
	reExportedFunc = regexp.MustCompile(`^func\s+([A-Z][A-Za-z0-9_]*)`)
	reExportedType = regexp.MustCompile(`^type\s+([A-Z][A-Za-z0-9_]*)`)
)

func extractDocDriftCandidatesText(chunk *diffChunk) []driftCandidate {
	var candidates []driftCandidate
	lines := chunk.lines
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		var m []string
		if m = reExportedFunc.FindStringSubmatch(trimmed); m == nil {
			m = reExportedType.FindStringSubmatch(trimmed)
		}
		if m == nil {
			continue
		}
		hasDoc := i > 0 && strings.HasPrefix(strings.TrimSpace(lines[i-1]), "//")
		kind := "missing_godoc"
		var docText string
		if hasDoc {
			kind = "stale_godoc"
			docText = strings.TrimSpace(lines[i-1])
		}
		candidates = append(candidates, driftCandidate{
			kind:    kind,
			symbol:  m[1],
			line:    i + 1,
			sig:     trimmed,
			docText: docText,
		})
	}
	return candidates
}

// funcDeclSig returns a compact one-line representation of a FuncDecl signature.
func funcDeclSig(fn *ast.FuncDecl) string {
	var b strings.Builder
	b.WriteString("func ")
	if fn.Recv != nil && len(fn.Recv.List) > 0 {
		b.WriteString("(<receiver>) ")
	}
	b.WriteString(fn.Name.Name)
	b.WriteString("(...)")
	if fn.Type.Results != nil && len(fn.Type.Results.List) > 0 {
		b.WriteString(" <returns>")
	}
	return b.String()
}

// ─── LLM call ─────────────────────────────────────────────────────────────────

func analyseCandidate(client LLMClient, timeout time.Duration, file string, cand driftCandidate) (*DriftItem, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	var prompt string
	if cand.kind == "missing_godoc" {
		prompt = buildMissingGodocPrompt(cand)
	} else {
		prompt = buildStaleGodocPrompt(cand)
	}

	resp, err := client.Complete(ctx, prompt)
	if err != nil {
		return nil, err
	}

	return parseDocDriftResponse(resp, file, cand), nil
}

func buildStaleGodocPrompt(cand driftCandidate) string {
	return fmt.Sprintf(`You are a Go code reviewer checking for documentation drift.

Given the following Go function/type signature and its doc comment, determine if the doc comment is accurate and up to date.

Symbol: %s
Signature: %s
Current doc comment:
%s

Respond with a JSON object:
{
  "drifted": true/false,
  "reason": "one-sentence reason if drifted, empty string if not",
  "suggested_doc": "suggested replacement doc comment if drifted, empty string if not"
}

Respond with valid JSON only. No markdown. No explanation outside the JSON.`,
		cand.symbol, cand.sig, cand.docText)
}

func buildMissingGodocPrompt(cand driftCandidate) string {
	return fmt.Sprintf(`You are a Go code reviewer enforcing godoc standards.

The following exported Go symbol is missing a doc comment, which violates Go best practices and will be flagged by Copilot.

Symbol: %s
Signature: %s

Respond with a JSON object:
{
  "drifted": true,
  "reason": "Missing godoc comment on exported symbol.",
  "suggested_doc": "// %s ..."
}

Respond with valid JSON only. No markdown.`,
		cand.symbol, cand.sig, cand.symbol)
}

// llmDocDriftResponse is the expected JSON shape from the LLM.
type llmDocDriftResponse struct {
	Drifted      bool   `json:"drifted"`
	Reason       string `json:"reason"`
	SuggestedDoc string `json:"suggested_doc"`
}

func parseDocDriftResponse(resp, file string, cand driftCandidate) *DriftItem {
	resp = strings.TrimSpace(resp)
	resp = strings.TrimPrefix(resp, "```json")
	resp = strings.TrimPrefix(resp, "```")
	resp = strings.TrimSuffix(resp, "```")
	resp = strings.TrimSpace(resp)

	var parsed llmDocDriftResponse
	if err := json.Unmarshal([]byte(resp), &parsed); err != nil {
		return nil
	}
	if !parsed.Drifted {
		return nil
	}

	detail := parsed.Reason
	if detail == "" {
		detail = "Documentation may not match implementation."
	}

	return &DriftItem{
		Kind:         cand.kind,
		File:         file,
		Line:         cand.line,
		Symbol:       cand.symbol,
		Detail:       detail,
		SuggestedDoc: parsed.SuggestedDoc,
	}
}

// ─── README drift (heuristic, no LLM call) ───────────────────────────────────

func detectReadmeDrift(chunk *diffChunk) []DriftItem {
	if !strings.Contains(strings.ToLower(chunk.file), "readme") {
		return nil
	}

	var items []DriftItem
	inCodeBlock := false
	blockStart := 0
	var blockLines []string

	for i, line := range chunk.lines {
		if strings.HasPrefix(strings.TrimSpace(line), "```") {
			if !inCodeBlock {
				inCodeBlock = true
				blockStart = i + 1
				blockLines = nil
			} else {
				if looksLikeGoExample(blockLines) {
					items = append(items, DriftItem{
						Kind:   "readme_example",
						File:   chunk.file,
						Line:   blockStart,
						Symbol: "README example",
						Detail: "README code example may reference stale API; verify it compiles against the current package surface.",
					})
				}
				inCodeBlock = false
			}
			continue
		}
		if inCodeBlock {
			blockLines = append(blockLines, line)
		}
	}
	return items
}

var reGoImport = regexp.MustCompile(`(?m)(import\s*"|\bfmt\.|\bos\.|\berr\b|func\s+\w+\()`)

func looksLikeGoExample(lines []string) bool {
	src := strings.Join(lines, "\n")
	return reGoImport.MatchString(src)
}

// ─── Copilot LLM client ───────────────────────────────────────────────────────

// CopilotLLMClient implements LLMClient against the GitHub Copilot completion
// endpoint.  It uses the token from `gh auth token` for authentication.
type CopilotLLMClient struct {
	// HTTPClient allows injection of a custom transport for testing.
	// If nil, http.DefaultClient is used.
	HTTPClient *http.Client
}

const copilotChatEndpoint = "https://api.githubcopilot.com/chat/completions"

// Complete sends prompt to the Copilot chat completion endpoint and returns the
// response text.  The context deadline is forwarded as the request deadline.
func (c *CopilotLLMClient) Complete(ctx context.Context, prompt string) (string, error) {
	token, err := getCopilotToken()
	if err != nil {
		return "", fmt.Errorf("doc-drift: copilot auth: %w", err)
	}

	body := map[string]interface{}{
		"model": "gpt-4o",
		"messages": []map[string]string{
			{"role": "user", "content": prompt},
		},
		"max_tokens": 512,
	}
	bodyBytes, err := json.Marshal(body)
	if err != nil {
		return "", fmt.Errorf("doc-drift: marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, copilotChatEndpoint,
		bytes.NewReader(bodyBytes))
	if err != nil {
		return "", fmt.Errorf("doc-drift: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Copilot-Integration-Id", "rinse-doc-drift")
	req.Header.Set("Editor-Version", "rinse/0.4")
	req.Header.Set("User-Agent", "rinse/0.4")

	hc := c.HTTPClient
	if hc == nil {
		hc = http.DefaultClient
	}

	resp, err := hc.Do(req)
	if err != nil {
		return "", fmt.Errorf("doc-drift: copilot request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("doc-drift: copilot returned HTTP %d", resp.StatusCode)
	}

	var result struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("doc-drift: decode copilot response: %w", err)
	}
	if len(result.Choices) == 0 {
		return "", fmt.Errorf("doc-drift: copilot returned no choices")
	}
	return result.Choices[0].Message.Content, nil
}

// getCopilotToken retrieves the Copilot auth token.
// It first checks RINSE_COPILOT_TOKEN env var (CI/test override), then calls gh.
func getCopilotToken() (string, error) {
	if tok := os.Getenv("RINSE_COPILOT_TOKEN"); tok != "" {
		return tok, nil
	}
	out, err := exec.Command("gh", "auth", "token").Output()
	if err != nil {
		return "", fmt.Errorf("gh auth token: %w", err)
	}
	tok := strings.TrimSpace(string(out))
	if tok == "" {
		return "", fmt.Errorf("gh auth token returned empty token")
	}
	return tok, nil
}

// ─── noopLLMClient ────────────────────────────────────────────────────────────

// noopLLMClient is used when opts.Client is nil. It makes no LLM calls.
type noopLLMClient struct{}

func (n *noopLLMClient) Complete(_ context.Context, _ string) (string, error) {
	return `{"drifted":false,"reason":"","suggested_doc":""}`, nil
}

// ─── MockLLMClient ────────────────────────────────────────────────────────────

// MockLLMClient is a test double for LLMClient.  It records prompts and
// returns a fixed sequence of responses, making latency tests deterministic.
type MockLLMClient struct {
	// Responses is the queue of responses to return, one per Complete call.
	// When exhausted, subsequent calls return the last response.
	Responses []string

	// Latency is an optional artificial delay per call for latency testing.
	Latency time.Duration

	// Prompts is populated by each Complete call for inspection in tests.
	Prompts []string

	// CallCount tracks the number of times Complete was called.
	CallCount int
}

// Complete returns the next queued response (or last if exhausted), after
// sleeping Latency if set.  It respects the context deadline.
func (m *MockLLMClient) Complete(ctx context.Context, prompt string) (string, error) {
	m.Prompts = append(m.Prompts, prompt)
	m.CallCount++

	if m.Latency > 0 {
		select {
		case <-time.After(m.Latency):
		case <-ctx.Done():
			return "", ctx.Err()
		}
	}

	if len(m.Responses) == 0 {
		return `{"drifted":false,"reason":"","suggested_doc":""}`, nil
	}
	idx := m.CallCount - 1
	if idx >= len(m.Responses) {
		idx = len(m.Responses) - 1
	}
	return m.Responses[idx], nil
}

// ─── Config helpers ───────────────────────────────────────────────────────────

// DocDriftConfig holds the doc-drift section of ~/.rinse/config.json.
type DocDriftConfig struct {
	// Enabled is true when the user has opted in via {"doc_drift": true}.
	Enabled bool
}

// LoadDocDriftConfig reads the doc_drift flag from ~/.rinse/config.json.
// Returns DocDriftConfig{} (disabled) on any read or parse error.
func LoadDocDriftConfig() DocDriftConfig {
	home, err := os.UserHomeDir()
	if err != nil {
		return DocDriftConfig{}
	}
	path := filepath.Join(home, ".rinse", "config.json")
	data, err := os.ReadFile(path)
	if err != nil {
		return DocDriftConfig{}
	}
	var raw map[string]interface{}
	if err := json.Unmarshal(data, &raw); err != nil {
		return DocDriftConfig{}
	}
	enabled, _ := raw["doc_drift"].(bool)
	return DocDriftConfig{Enabled: enabled}
}

// IsDocDriftEnabled returns true when doc_drift is enabled either via config
// or the explicit opt-in flag.
func IsDocDriftEnabled(flagValue bool) bool {
	if flagValue {
		return true
	}
	return LoadDocDriftConfig().Enabled
}

// ─── Pro gate ────────────────────────────────────────────────────────────────

// IsPro returns true when the user has an active Pro subscription.
// Determined by {"pro": true} in ~/.rinse/config.json.
// Remote entitlement validation is out of scope for v0.4.
func IsPro() bool {
	home, err := os.UserHomeDir()
	if err != nil {
		return false
	}
	path := filepath.Join(home, ".rinse", "config.json")
	data, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	var raw map[string]interface{}
	if err := json.Unmarshal(data, &raw); err != nil {
		return false
	}
	pro, _ := raw["pro"].(bool)
	return pro
}

// ─── DriftItemsAsPredictions ─────────────────────────────────────────────────

// DriftItemsAsPredictions converts a DocDriftReport into Predictions so that
// the existing render pipeline can display doc-drift findings alongside the
// standard AST predictions.
func DriftItemsAsPredictions(rep *DocDriftReport) []Prediction {
	preds := make([]Prediction, 0, len(rep.Items))
	for _, item := range rep.Items {
		if item.Kind == "cap_reached" {
			continue // shown as a footer note, not a prediction row
		}
		pattern := driftKindLabel(item.Kind)
		detail := item.Detail
		if item.SuggestedDoc != "" {
			detail += fmt.Sprintf(" Suggested: %s", driftTruncate(item.SuggestedDoc, 80))
		}
		preds = append(preds, Prediction{
			Pattern:    pattern,
			Confidence: driftKindConfidence(item.Kind),
			File:       item.File,
			Line:       item.Line,
			Detail:     detail,
		})
	}
	return preds
}

func driftKindLabel(kind string) string {
	switch kind {
	case "stale_godoc":
		return "Stale godoc comment"
	case "missing_godoc":
		return "Missing godoc on exported symbol"
	case "readme_example":
		return "README example may be stale"
	default:
		return "Documentation drift"
	}
}

func driftKindConfidence(kind string) float64 {
	switch kind {
	case "stale_godoc":
		return 0.82
	case "missing_godoc":
		return 0.78
	case "readme_example":
		return 0.65
	default:
		return 0.70
	}
}

func driftTruncate(s string, n int) string {
	runes := []rune(s)
	if len(runes) <= n {
		return s
	}
	return string(runes[:n]) + "…"
}
