package wait

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestMatchReviewEvent(t *testing.T) {
	tests := []struct {
		name     string
		payload  map[string]any
		pr       int
		bot      string
		want     bool
		wantErr  bool
	}{
		{
			name: "copilot bot submitted on target PR",
			payload: map[string]any{
				"action": "submitted",
				"review": map[string]any{
					"state": "commented",
					"user":  map[string]any{"login": "copilot-pull-request-reviewer[bot]", "type": "Bot"},
				},
				"pull_request": map[string]any{"number": 268},
			},
			pr: 268, bot: "copilot", want: true,
		},
		{
			name: "wrong PR number",
			payload: map[string]any{
				"action":       "submitted",
				"review":       map[string]any{"state": "approved", "user": map[string]any{"login": "Copilot"}},
				"pull_request": map[string]any{"number": 999},
			},
			pr: 268, bot: "copilot", want: false,
		},
		{
			name: "non-bot user",
			payload: map[string]any{
				"action":       "submitted",
				"review":       map[string]any{"state": "commented", "user": map[string]any{"login": "human"}},
				"pull_request": map[string]any{"number": 268},
			},
			pr: 268, bot: "copilot", want: false,
		},
		{
			name: "edited action (not submitted)",
			payload: map[string]any{
				"action":       "edited",
				"review":       map[string]any{"state": "commented", "user": map[string]any{"login": "Copilot"}},
				"pull_request": map[string]any{"number": 268},
			},
			pr: 268, bot: "copilot", want: false,
		},
		{
			name: "case-insensitive bot match",
			payload: map[string]any{
				"action":       "submitted",
				"review":       map[string]any{"state": "commented", "user": map[string]any{"login": "COPILOT-Bot"}},
				"pull_request": map[string]any{"number": 268},
			},
			pr: 268, bot: "copilot", want: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			body, _ := json.Marshal(tt.payload)
			got, err := MatchReviewEvent(body, tt.pr, tt.bot)
			if (err != nil) != tt.wantErr {
				t.Fatalf("err = %v, wantErr = %v", err, tt.wantErr)
			}
			if got != tt.want {
				t.Errorf("MatchReviewEvent = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestMatchReviewEvent_InvalidJSON(t *testing.T) {
	if _, err := MatchReviewEvent([]byte("not json"), 1, "copilot"); err == nil {
		t.Errorf("expected error on bad JSON")
	}
}

// TestForCopilotReview_PollFinds verifies the poll strategy returns OutcomeArrived
// when the GitHub-emulating server reports a copilot review after the first poll.
// Webhook detection is bypassed by forcing Strategy="poll".
func TestForCopilotReview_PollFinds(t *testing.T) {
	state := &pollServerState{}
	srv := newPollTestServer(t, state)
	t.Cleanup(srv.Close)
	t.Setenv("RINSE_TEST_GH_TOKEN_OVERRIDE", "test-token")
	t.Setenv("RINSE_TEST_GH_API_BASE", srv.URL)

	state.reviews = []map[string]any{
		{"id": 1, "state": "PENDING", "user": map[string]string{"login": "Copilot"}},
	}
	go func() {
		time.Sleep(150 * time.Millisecond)
		state.mu.Lock()
		state.reviews = append(state.reviews, map[string]any{
			"id":    2,
			"state": "COMMENTED",
			"user":  map[string]string{"login": "copilot-pull-request-reviewer[bot]"},
		})
		state.etag = "etag2"
		state.mu.Unlock()
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	var progress bytes.Buffer
	res, err := ForCopilotReview(ctx, Opts{
		Repo: "octo/hello", PR: 1,
		Timeout: 1500 * time.Millisecond, PollInterval: 50 * time.Millisecond,
		Strategy: "poll", ProgressOut: &progress,
	})
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if res.Outcome != OutcomeArrived {
		t.Errorf("outcome = %v, want arrived", res.Outcome)
	}
	if !strings.Contains(progress.String(), "Copilot reviewing") {
		t.Errorf("progress missing tick: %q", progress.String())
	}
}

func TestForCopilotReview_PollTimeout(t *testing.T) {
	state := &pollServerState{
		reviews: []map[string]any{
			{"id": 1, "state": "PENDING", "user": map[string]string{"login": "Copilot"}},
		},
	}
	srv := newPollTestServer(t, state)
	t.Cleanup(srv.Close)
	t.Setenv("RINSE_TEST_GH_TOKEN_OVERRIDE", "test-token")
	t.Setenv("RINSE_TEST_GH_API_BASE", srv.URL)

	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()
	res, err := ForCopilotReview(ctx, Opts{
		Repo: "octo/hello", PR: 1,
		Timeout: 300 * time.Millisecond, PollInterval: 50 * time.Millisecond,
		Strategy: "poll",
	})
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if res.Outcome != OutcomeTimeout {
		t.Errorf("outcome = %v, want timeout", res.Outcome)
	}
}

// ── Test scaffolding ──────────────────────────────────────────────────────────

type pollServerState struct {
	mu      sync.RWMutex
	reviews []map[string]any
	etag    string
}

func newPollTestServer(t *testing.T, state *pollServerState) *httptest.Server {
	t.Helper()
	if state.etag == "" {
		state.etag = "etag1"
	}
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		state.mu.RLock()
		defer state.mu.RUnlock()
		if got := r.Header.Get("If-None-Match"); got != "" && got == state.etag {
			w.WriteHeader(http.StatusNotModified)
			return
		}
		w.Header().Set("ETag", state.etag)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(state.reviews)
	}))
}
