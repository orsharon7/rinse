package onboarding

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

// CycleRequest is the request body sent to POST /cycles.
type CycleRequest struct {
	Name     string        `json:"name"`
	Settings CycleSettings `json:"settings"`
}

// CycleSettings maps to the API settings shape from RIN-25#document-defaults-config.
type CycleSettings struct {
	RemindOnComplete bool `json:"remindOnComplete"`
	AutoAdvance      bool `json:"autoAdvance"`
	SaveHistory      bool `json:"saveHistory"`
}

// Cycle is the response returned by POST /cycles.
type Cycle struct {
	ID          string        `json:"id"`
	Name        string        `json:"name"`
	Status      string        `json:"status"`
	CreatedAt   time.Time     `json:"createdAt"`
	StartedAt   *time.Time    `json:"startedAt"`
	CompletedAt *time.Time    `json:"completedAt"`
	Settings    CycleSettings `json:"settings"`
}

// APIBase returns the base URL for the cycles backend.
//
// Transport decision (open item resolved): the TUI communicates with the rinse
// backend via a local HTTP server. The default port is 7433. Override with the
// RINSE_API_URL environment variable (e.g. "http://localhost:9000").
//
// Rationale: local HTTP is the most portable option — no gRPC toolchain
// required, easy to mock in tests, and works across all future platform targets.
func APIBase() string {
	if u := os.Getenv("RINSE_API_URL"); u != "" {
		return u
	}
	return "http://localhost:7433"
}

// httpClient is a package-level client with a conservative timeout.
// 10 s is long enough for a healthy local backend; short enough to surface
// a hung server quickly in the TUI.
var httpClient = &http.Client{Timeout: 10 * time.Second}

// CreateCycle calls POST /cycles and returns the created cycle on success.
func CreateCycle(name string, d Defaults) (*Cycle, error) {
	req := CycleRequest{
		Name: name,
		Settings: CycleSettings{
			RemindOnComplete: d.RemindOnComplete,
			AutoAdvance:      d.AutoAdvance,
			SaveHistory:      d.SaveHistory,
		},
	}

	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("could not build request: %w", err)
	}

	url := strings.TrimRight(APIBase(), "/") + "/cycles"
	resp, err := httpClient.Post(url, "application/json", bytes.NewReader(body)) //nolint:noctx
	if err != nil {
		return nil, fmt.Errorf("could not reach rinse backend at %s: %w", url, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		b, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("server returned %d: %s", resp.StatusCode, string(b))
	}

	var cycle Cycle
	if err := json.NewDecoder(resp.Body).Decode(&cycle); err != nil {
		return nil, fmt.Errorf("parse response: %w", err)
	}

	return &cycle, nil
}
