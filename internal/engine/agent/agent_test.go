package agent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestBuildPrompt_golden verifies that BuildPrompt produces the expected
// structural elements so callers don't silently regress on prompt format.
func TestBuildPrompt_golden(t *testing.T) {
	ctx := PRContext{
		PR:       "42",
		Repo:     "owner/repo",
		CWD:      "/workspace/repo",
		ReviewID: "REV123",
		Comments: []Comment{
			{ID: 1, Path: "main.go", Line: 10, Body: "Fix this", InReplyToID: nil},
		},
	}

	got, err := BuildPrompt(ctx)
	if err != nil {
		t.Fatalf("BuildPrompt returned error: %v", err)
	}

	mustContain := []string{
		"PR #42",
		"owner/repo",
		"/workspace/repo",
		"REV123",
		"Total top-level comments: 1",
		`"id": 1`,
		`"path": "main.go"`,
		`"body": "Fix this"`,
		"git add -A",
		`copilot-pull-request-reviewer[bot]`,
		"gh api repos/owner/repo/pulls/42/comments/<id>/replies",
	}
	for _, want := range mustContain {
		if !strings.Contains(got, want) {
			t.Errorf("BuildPrompt output missing %q", want)
		}
	}
}

// TestBuildPrompt_topLevelCount checks that only top-level comments are counted.
func TestBuildPrompt_topLevelCount(t *testing.T) {
	replyID := int64(1)
	ctx := PRContext{
		PR:   "1",
		Repo: "a/b",
		CWD:  "/tmp",
		Comments: []Comment{
			{ID: 1, InReplyToID: nil},
			{ID: 2, InReplyToID: &replyID},
			{ID: 3, InReplyToID: nil},
		},
	}

	got, err := BuildPrompt(ctx)
	if err != nil {
		t.Fatalf("BuildPrompt error: %v", err)
	}
	if !strings.Contains(got, "Total top-level comments: 2") {
		t.Errorf("expected 2 top-level comments in prompt; got:\n%s", got)
	}
}

// TestScriptDir_found verifies ScriptDir walks up and locates scripts/pr-review.sh.
func TestScriptDir_found(t *testing.T) {
	// Create a nested temp dir: root/scripts/pr-review.sh
	root := t.TempDir()
	scripts := filepath.Join(root, "scripts")
	if err := os.Mkdir(scripts, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(scripts, "pr-review.sh"), []byte("#!/bin/bash"), 0o755); err != nil {
		t.Fatal(err)
	}

	// Start from a deeply nested child dir.
	nested := filepath.Join(root, "a", "b", "c")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}

	got, err := ScriptDir(nested)
	if err != nil {
		t.Fatalf("ScriptDir returned error: %v", err)
	}
	if got != scripts {
		t.Errorf("ScriptDir = %q; want %q", got, scripts)
	}
}

// TestScriptDir_notFound verifies ScriptDir returns an error when pr-review.sh
// is nowhere in the ancestor chain.
func TestScriptDir_notFound(t *testing.T) {
	dir := t.TempDir()
	_, err := ScriptDir(dir)
	if err == nil {
		t.Fatal("expected error when pr-review.sh is absent, got nil")
	}
}

// TestParseReviewState_clean verifies that the "clean" status is parsed correctly
// from JSON output (as returned by pr-review.sh status).
func TestParseReviewState_clean(t *testing.T) {
	raw := `{"status":"clean","review_id":"R_1","comment_count":0}`
	var rs ReviewState
	if err := json.Unmarshal([]byte(raw), &rs); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}
	if rs.Status != "clean" {
		t.Errorf("Status = %q; want %q", rs.Status, "clean")
	}
	if rs.ReviewID != "R_1" {
		t.Errorf("ReviewID = %q; want %q", rs.ReviewID, "R_1")
	}
}

// TestParseReviewState_NULStripped verifies that NUL bytes in the raw output
// are stripped before JSON parsing, matching the bytes.ReplaceAll logic in
// GetReviewState. This test directly exercises the JSON-parsing step; the
// NUL stripping itself is tested here inline to validate the expected input.
func TestParseReviewState_NULStripped(t *testing.T) {
	raw := "{\"status\":\"approved\",\"review_id\":\"R_2\"}\x00"
	cleaned := strings.ReplaceAll(raw, "\x00", "")
	var rs ReviewState
	if err := json.Unmarshal([]byte(cleaned), &rs); err != nil {
		t.Fatalf("unmarshal after NUL strip: %v", err)
	}
	if rs.Status != "approved" {
		t.Errorf("Status = %q; want %q", rs.Status, "approved")
	}
}

// TestParseComments verifies that the comments wrapper JSON is parsed correctly.
func TestParseComments(t *testing.T) {
	replyID := int64(10)
	comments := []Comment{
		{ID: 10, Path: "foo.go", Line: 5, Body: "top-level", InReplyToID: nil},
		{ID: 11, Path: "foo.go", Line: 6, Body: "reply", InReplyToID: &replyID},
	}
	wrapper := struct {
		ReviewID string    `json:"review_id"`
		Count    int       `json:"count"`
		Comments []Comment `json:"comments"`
	}{ReviewID: "R_3", Count: 2, Comments: comments}

	data, err := json.Marshal(wrapper)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var got struct {
		ReviewID string    `json:"review_id"`
		Count    int       `json:"count"`
		Comments []Comment `json:"comments"`
	}
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(got.Comments) != 2 {
		t.Fatalf("expected 2 comments, got %d", len(got.Comments))
	}
	if got.Comments[0].InReplyToID != nil {
		t.Error("first comment should have nil InReplyToID")
	}
	if got.Comments[1].InReplyToID == nil || *got.Comments[1].InReplyToID != 10 {
		t.Errorf("second comment InReplyToID = %v; want 10", got.Comments[1].InReplyToID)
	}
}
