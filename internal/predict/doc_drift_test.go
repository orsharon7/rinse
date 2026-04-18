package predict

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"
)

// ─── extractDocDriftCandidates ────────────────────────────────────────────────

func TestExtractDocDriftCandidates_DetectsMissingGodoc(t *testing.T) {
	chunk := &diffChunk{
		file: "api.go",
		isGo: true,
		lines: []string{
			"package api",
			"",
			"func NewClient(addr string) *Client {",
			"	return &Client{addr: addr}",
			"}",
		},
	}
	cands := extractDocDriftCandidates(chunk)
	if len(cands) == 0 {
		t.Fatal("expected at least one candidate for exported func without godoc")
	}
	found := false
	for _, c := range cands {
		if c.symbol == "NewClient" && c.kind == "missing_godoc" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected missing_godoc candidate for NewClient, got: %+v", cands)
	}
}

func TestExtractDocDriftCandidates_DetectsStaleGodoc(t *testing.T) {
	chunk := &diffChunk{
		file: "api.go",
		isGo: true,
		lines: []string{
			"package api",
			"",
			"// Connect establishes a UDP connection.",
			"func Connect(host string, port int) error {",
			"	return nil",
			"}",
		},
	}
	cands := extractDocDriftCandidates(chunk)
	if len(cands) == 0 {
		t.Fatal("expected at least one candidate for exported func with godoc")
	}
	found := false
	for _, c := range cands {
		if c.symbol == "Connect" && c.kind == "stale_godoc" {
			found = true
			if !strings.Contains(c.docText, "UDP") {
				t.Errorf("expected doc text to be captured, got: %q", c.docText)
			}
		}
	}
	if !found {
		t.Errorf("expected stale_godoc candidate for Connect, got: %+v", cands)
	}
}

func TestExtractDocDriftCandidates_SkipsUnexported(t *testing.T) {
	chunk := &diffChunk{
		file: "internal.go",
		isGo: true,
		lines: []string{
			"package foo",
			"func helper() {}",
			"type internalType struct{}",
		},
	}
	cands := extractDocDriftCandidates(chunk)
	for _, c := range cands {
		if c.symbol == "helper" || c.symbol == "internalType" {
			t.Errorf("unexported symbol %q should not be a candidate", c.symbol)
		}
	}
}

func TestExtractDocDriftCandidates_ExportedType(t *testing.T) {
	chunk := &diffChunk{
		file: "types.go",
		isGo: true,
		lines: []string{
			"package types",
			"",
			"type Config struct {",
			"	Host string",
			"}",
		},
	}
	cands := extractDocDriftCandidates(chunk)
	found := false
	for _, c := range cands {
		if c.symbol == "Config" && c.kind == "missing_godoc" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected missing_godoc candidate for exported type Config, got: %+v", cands)
	}
}

// ─── parseDocDriftResponse ────────────────────────────────────────────────────

func TestParseDocDriftResponse_DriftDetected(t *testing.T) {
	resp := `{"drifted":true,"reason":"Doc says TCP but code uses UDP.","suggested_doc":"// Connect establishes a UDP connection."}`
	cand := driftCandidate{kind: "stale_godoc", symbol: "Connect", line: 5}
	item := parseDocDriftResponse(resp, "api.go", cand)
	if item == nil {
		t.Fatal("expected a DriftItem when drifted=true")
	}
	if item.Symbol != "Connect" {
		t.Errorf("expected symbol=Connect, got %q", item.Symbol)
	}
	if item.Kind != "stale_godoc" {
		t.Errorf("expected kind=stale_godoc, got %q", item.Kind)
	}
	if !strings.Contains(item.Detail, "UDP") {
		t.Errorf("expected detail to contain reason, got %q", item.Detail)
	}
	if item.SuggestedDoc == "" {
		t.Error("expected suggested_doc to be populated")
	}
}

func TestParseDocDriftResponse_NoDrift(t *testing.T) {
	resp := `{"drifted":false,"reason":"","suggested_doc":""}`
	cand := driftCandidate{kind: "stale_godoc", symbol: "Foo", line: 1}
	item := parseDocDriftResponse(resp, "foo.go", cand)
	if item != nil {
		t.Errorf("expected nil item when drifted=false, got: %+v", item)
	}
}

func TestParseDocDriftResponse_MarkdownFences(t *testing.T) {
	resp := "```json\n{\"drifted\":true,\"reason\":\"stale\",\"suggested_doc\":\"// Foo does bar.\"}\n```"
	cand := driftCandidate{kind: "stale_godoc", symbol: "Foo", line: 1}
	item := parseDocDriftResponse(resp, "foo.go", cand)
	if item == nil {
		t.Fatal("expected DriftItem even when response is wrapped in markdown fences")
	}
}

func TestParseDocDriftResponse_InvalidJSON(t *testing.T) {
	cand := driftCandidate{kind: "stale_godoc", symbol: "Foo", line: 1}
	item := parseDocDriftResponse("not json at all", "foo.go", cand)
	if item != nil {
		t.Errorf("expected nil for unparseable response, got: %+v", item)
	}
}

// ─── MockLLMClient ────────────────────────────────────────────────────────────

func TestMockLLMClient_RecordsCallsAndReturnsResponses(t *testing.T) {
	mock := &MockLLMClient{
		Responses: []string{
			`{"drifted":true,"reason":"outdated","suggested_doc":"// New doc."}`,
			`{"drifted":false,"reason":"","suggested_doc":""}`,
		},
	}

	r1, err := mock.Complete(context.Background(), "prompt1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	r2, err := mock.Complete(context.Background(), "prompt2")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if mock.CallCount != 2 {
		t.Errorf("expected CallCount=2, got %d", mock.CallCount)
	}
	if len(mock.Prompts) != 2 {
		t.Errorf("expected 2 recorded prompts, got %d", len(mock.Prompts))
	}
	if !strings.Contains(r1, "outdated") {
		t.Errorf("unexpected first response: %q", r1)
	}
	if !strings.Contains(r2, "false") {
		t.Errorf("unexpected second response: %q", r2)
	}
}

func TestMockLLMClient_RespectsContextDeadline(t *testing.T) {
	mock := &MockLLMClient{
		Latency: 100 * time.Millisecond,
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()

	_, err := mock.Complete(ctx, "hello")
	if err == nil {
		t.Fatal("expected context deadline error")
	}
	if !strings.Contains(err.Error(), "context") {
		t.Errorf("expected context error, got: %v", err)
	}
}

func TestMockLLMClient_ExhaustedResponsesReturnLast(t *testing.T) {
	mock := &MockLLMClient{
		Responses: []string{`{"drifted":true,"reason":"x","suggested_doc":""}`},
	}
	for i := 0; i < 5; i++ {
		r, err := mock.Complete(context.Background(), "p")
		if err != nil {
			t.Fatalf("call %d: unexpected error: %v", i, err)
		}
		if !strings.Contains(r, "drifted") {
			t.Errorf("call %d: unexpected response: %q", i, r)
		}
	}
	if mock.CallCount != 5 {
		t.Errorf("expected 5 calls, got %d", mock.CallCount)
	}
}

// ─── RunDocDrift ─────────────────────────────────────────────────────────────

// TestRunDocDrift_DetectsStaleGodoc verifies that a known stale godoc comment
// is detected and reported as a drift item (acceptance criterion: ≥1 case).
func TestRunDocDrift_DetectsStaleGodoc(t *testing.T) {
	diff := `+++ b/api.go
+package api
+
+// Dial creates a TCP connection.
+func Dial(host string, port int) (*Conn, error) {
+	return nil, nil
+}
`
	mock := &MockLLMClient{
		Responses: []string{
			`{"drifted":true,"reason":"Doc says TCP but implementation may use UDP.","suggested_doc":"// Dial establishes a network connection."}`,
		},
	}
	opts := DocDriftOptions{
		MaxLLMCalls:    10,
		PerCallTimeout: 3 * time.Second,
		Client:         mock,
	}

	rep, err := RunDocDrift(diff, "staged changes", opts)
	if err != nil {
		t.Fatalf("RunDocDrift error: %v", err)
	}
	if len(rep.Items) == 0 {
		t.Fatal("expected at least one drift item for stale godoc case")
	}

	found := false
	for _, item := range rep.Items {
		if item.Kind == "stale_godoc" && item.Symbol == "Dial" {
			found = true
			if item.SuggestedDoc == "" {
				t.Error("expected SuggestedDoc to be populated")
			}
		}
	}
	if !found {
		t.Errorf("expected stale_godoc item for Dial, got: %+v", rep.Items)
	}

	if rep.LLMCallCount == 0 {
		t.Error("expected at least 1 LLM call")
	}
}

// TestRunDocDrift_DetectsMissingGodoc verifies detection of missing godoc.
func TestRunDocDrift_DetectsMissingGodoc(t *testing.T) {
	diff := `+++ b/client.go
+package client
+
+func NewClient(addr string) *Client {
+	return &Client{addr: addr}
+}
`
	mock := &MockLLMClient{
		Responses: []string{
			`{"drifted":true,"reason":"Missing godoc comment on exported symbol.","suggested_doc":"// NewClient creates a new Client connected to addr."}`,
		},
	}
	opts := DocDriftOptions{Client: mock}

	rep, err := RunDocDrift(diff, "staged changes", opts)
	if err != nil {
		t.Fatalf("RunDocDrift error: %v", err)
	}

	found := false
	for _, item := range rep.Items {
		if item.Kind == "missing_godoc" && item.Symbol == "NewClient" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected missing_godoc item for NewClient, got: %+v", rep.Items)
	}
}

// TestRunDocDrift_RespectsCallCap verifies that the 10-call cap is enforced.
func TestRunDocDrift_RespectsCallCap(t *testing.T) {
	// Build a diff with many exported functions to exceed the cap.
	var diffLines []string
	diffLines = append(diffLines, "+++ b/big.go", "+package big", "")
	for i := 0; i < 15; i++ {
		diffLines = append(diffLines,
			fmt.Sprintf("+func Func%d() {}", i),
		)
	}
	diff := strings.Join(diffLines, "\n")

	// Mock returns "drifted" for every call so all candidates produce items.
	mock := &MockLLMClient{
		Responses: []string{`{"drifted":true,"reason":"test","suggested_doc":""}`},
	}

	const cap = 5
	opts := DocDriftOptions{
		MaxLLMCalls: cap,
		Client:      mock,
	}

	rep, err := RunDocDrift(diff, "test", opts)
	if err != nil {
		t.Fatalf("RunDocDrift error: %v", err)
	}

	if rep.LLMCallCount > cap {
		t.Errorf("LLMCallCount %d exceeded cap %d", rep.LLMCallCount, cap)
	}
	if mock.CallCount > cap {
		t.Errorf("mock called %d times, expected ≤ %d", mock.CallCount, cap)
	}

	// Verify cap_reached item is present.
	found := false
	for _, item := range rep.Items {
		if item.Kind == "cap_reached" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected a cap_reached item when LLM call cap is hit, got: %+v", rep.Items)
	}
}

// TestRunDocDrift_PerCallLatency verifies that per-call latency enforcement
// works correctly: a mock with 0 latency completes within 1s.
func TestRunDocDrift_PerCallLatency(t *testing.T) {
	diff := `+++ b/fast.go
+package fast
+
+func Fast() {}
`
	mock := &MockLLMClient{
		Latency:   0, // no artificial delay
		Responses: []string{`{"drifted":false,"reason":"","suggested_doc":""}`},
	}

	start := time.Now()
	opts := DocDriftOptions{
		PerCallTimeout: 3 * time.Second,
		Client:         mock,
	}
	_, err := RunDocDrift(diff, "test", opts)
	elapsed := time.Since(start)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if elapsed > 1*time.Second {
		t.Errorf("RunDocDrift took %v; expected < 1s for mock with no latency", elapsed)
	}
}

// TestRunDocDrift_PerCallTimeout verifies that a slow LLM call is cancelled
// when the per-call timeout fires.
func TestRunDocDrift_PerCallTimeout(t *testing.T) {
	diff := `+++ b/slow.go
+package slow
+
+func Slow() {}
`
	// Mock sleeps longer than the per-call timeout.
	mock := &MockLLMClient{
		Latency: 200 * time.Millisecond,
	}
	opts := DocDriftOptions{
		PerCallTimeout: 50 * time.Millisecond,
		Client:         mock,
	}

	rep, err := RunDocDrift(diff, "test", opts)
	if err != nil {
		t.Fatalf("RunDocDrift should not return an error on per-call timeout (non-fatal): %v", err)
	}

	// The item should record the LLM failure.
	if len(rep.Items) == 0 {
		t.Fatal("expected at least one item (error record) for timed-out call")
	}
	found := false
	for _, item := range rep.Items {
		if strings.Contains(item.Detail, "LLM call failed") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected 'LLM call failed' in item detail for timeout case, got: %+v", rep.Items)
	}
}

// TestRunDocDrift_EmptyDiff verifies graceful handling of an empty diff.
func TestRunDocDrift_EmptyDiff(t *testing.T) {
	rep, err := RunDocDrift("", "staged changes", DocDriftOptions{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(rep.Items) != 0 {
		t.Errorf("expected 0 items for empty diff, got %d", len(rep.Items))
	}
}

// TestRunDocDrift_ReadmeExample verifies detection of Go code blocks in README diffs.
func TestRunDocDrift_ReadmeExample(t *testing.T) {
	diff := `+++ b/README.md
+# Usage
+
+` + "```go" + `
+import "fmt"
+
+func main() {
+    fmt.Println(Connect("localhost", 8080))
+}
+` + "```" + `
`
	opts := DocDriftOptions{Client: &noopLLMClient{}}
	rep, err := RunDocDrift(diff, "staged changes", opts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	found := false
	for _, item := range rep.Items {
		if item.Kind == "readme_example" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected readme_example drift item for README.md Go block, got: %+v", rep.Items)
	}
}

// ─── DriftItemsAsPredictions ─────────────────────────────────────────────────

func TestDriftItemsAsPredictions_ConvertsCorrectly(t *testing.T) {
	rep := &DocDriftReport{
		Items: []DriftItem{
			{Kind: "stale_godoc", File: "api.go", Line: 10, Symbol: "Run", Detail: "stale"},
			{Kind: "missing_godoc", File: "types.go", Line: 5, Symbol: "Config", Detail: "missing"},
			{Kind: "readme_example", File: "README.md", Line: 20, Symbol: "README example", Detail: "stale example"},
			{Kind: "cap_reached", File: "big.go", Symbol: "Foo", Detail: "cap"}, // should be excluded
		},
	}

	preds := DriftItemsAsPredictions(rep)

	if len(preds) != 3 {
		t.Errorf("expected 3 predictions (cap_reached excluded), got %d", len(preds))
	}

	for _, p := range preds {
		if p.Pattern == "" {
			t.Error("prediction pattern should not be empty")
		}
		if p.Confidence <= 0 || p.Confidence > 1 {
			t.Errorf("confidence out of range: %.2f", p.Confidence)
		}
	}
}

func TestDriftItemsAsPredictions_SuggestedDocAppended(t *testing.T) {
	rep := &DocDriftReport{
		Items: []DriftItem{
			{Kind: "stale_godoc", File: "api.go", Line: 1, Symbol: "Foo",
				Detail: "stale", SuggestedDoc: "// Foo does bar."},
		},
	}
	preds := DriftItemsAsPredictions(rep)
	if len(preds) != 1 {
		t.Fatalf("expected 1 prediction, got %d", len(preds))
	}
	if !strings.Contains(preds[0].Detail, "Suggested:") {
		t.Errorf("expected 'Suggested:' in detail, got: %q", preds[0].Detail)
	}
}

// ─── IsDocDriftEnabled ────────────────────────────────────────────────────────

func TestIsDocDriftEnabled_FlagOverride(t *testing.T) {
	// When flag=true, always enabled regardless of config.
	if !IsDocDriftEnabled(true) {
		t.Error("IsDocDriftEnabled(true) should return true")
	}
}

func TestIsDocDriftEnabled_ConfigFile(t *testing.T) {
	tmp := t.TempDir()
	orig := os.Getenv("HOME")
	os.Setenv("HOME", tmp)
	defer os.Setenv("HOME", orig)

	rinseDir := tmp + "/.rinse"
	if err := os.MkdirAll(rinseDir, 0o755); err != nil {
		t.Fatal(err)
	}

	// Write config with doc_drift=true.
	cfg := map[string]interface{}{"doc_drift": true}
	data, _ := json.Marshal(cfg)
	if err := os.WriteFile(rinseDir+"/config.json", data, 0o644); err != nil {
		t.Fatal(err)
	}

	if !IsDocDriftEnabled(false) {
		t.Error("IsDocDriftEnabled(false) should return true when config has doc_drift=true")
	}
}

func TestIsDocDriftEnabled_NotEnabled(t *testing.T) {
	tmp := t.TempDir()
	orig := os.Getenv("HOME")
	os.Setenv("HOME", tmp)
	defer os.Setenv("HOME", orig)

	if IsDocDriftEnabled(false) {
		t.Error("IsDocDriftEnabled(false) should return false when no config exists")
	}
}

// ─── detectReadmeDrift ────────────────────────────────────────────────────────

func TestDetectReadmeDrift_SkipsNonReadme(t *testing.T) {
	chunk := &diffChunk{
		file:  "internal.go",
		lines: []string{"```go", "fmt.Println(x)", "```"},
	}
	items := detectReadmeDrift(chunk)
	if len(items) != 0 {
		t.Errorf("expected 0 items for non-README file, got %d", len(items))
	}
}

func TestDetectReadmeDrift_SkipsNonGoBlocks(t *testing.T) {
	chunk := &diffChunk{
		file:  "README.md",
		lines: []string{"```bash", "rinse start 42", "```"},
	}
	items := detectReadmeDrift(chunk)
	if len(items) != 0 {
		t.Errorf("expected 0 items for bash block, got %d", len(items))
	}
}

// ─── looksLikeGoExample ──────────────────────────────────────────────────────

func TestLooksLikeGoExample(t *testing.T) {
	cases := []struct {
		lines []string
		want  bool
	}{
		{[]string{`import "fmt"`, `fmt.Println("hi")`}, true},
		{[]string{`func main() {`, `}`}, true},
		{[]string{`rinse start 42`}, false},
		{[]string{`const x = 1`}, false},
	}
	for _, tc := range cases {
		got := looksLikeGoExample(tc.lines)
		if got != tc.want {
			t.Errorf("looksLikeGoExample(%v) = %v, want %v", tc.lines, got, tc.want)
		}
	}
}
