package onboarding

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strings"
	"syscall"
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
// The provided context can be used to cancel the request (e.g. when the user
// aborts the wizard with ctrl+c).
func CreateCycle(ctx context.Context, name string, d Defaults) (*Cycle, error) {
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
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("could not build request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	resp, err := httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("could not reach rinse backend at %s: %w", url, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		const maxErrBodyBytes = 1 << 16 // 64 KiB — cap to avoid large allocs from untrusted servers
		b, _ := io.ReadAll(io.LimitReader(resp.Body, maxErrBodyBytes))
		return nil, fmt.Errorf("server returned %d: %s", resp.StatusCode, string(b))
	}

	var cycle Cycle
	if err := json.NewDecoder(resp.Body).Decode(&cycle); err != nil {
		return nil, fmt.Errorf("parse response: %w", err)
	}

	return &cycle, nil
}

// IsOfflineErr reports whether err looks like a transport-level failure that
// indicates no backend is reachable at the configured URL — connection
// refused, no such host, network timeout, or context deadline. Used by the
// onboarding wizard to treat the OSS default (no backend shipped) as
// best-effort: the wizard advances without showing a fatal error.
//
// Non-transport failures (e.g. HTTP 4xx/5xx returned by a reachable server,
// or a JSON parse error) are NOT classified as offline — those still surface
// to the user, since they indicate a misconfigured or broken backend rather
// than the absence of one.
func IsOfflineErr(err error) bool {
	if err == nil {
		return false
	}

	// Context cancellation / deadline reached before any response.
	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}

	// Common syscall errors when nothing is listening or the host is gone.
	if errors.Is(err, syscall.ECONNREFUSED) ||
		errors.Is(err, syscall.EHOSTUNREACH) ||
		errors.Is(err, syscall.ENETUNREACH) {
		return true
	}

	// Network operation errors (DNS, dial, timeout) wrap *net.OpError /
	// *net.DNSError. Use errors.As so wrapped errors are caught too.
	var dnsErr *net.DNSError
	if errors.As(err, &dnsErr) {
		return true
	}
	var opErr *net.OpError
	if errors.As(err, &opErr) {
		return true
	}
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return true
	}

	// Fall-back string match for transport-level errors that do not surface
	// a typed sentinel (e.g. http.Client's "connection refused" wrapped
	// through url.Error on some platforms). Keep the list narrow — these
	// are last-resort signals, not stylistic checks.
	msg := err.Error()
	switch {
	case strings.Contains(msg, "connection refused"),
		strings.Contains(msg, "no such host"),
		strings.Contains(msg, "i/o timeout"),
		strings.Contains(msg, "network is unreachable"),
		strings.Contains(msg, "no route to host"):
		return true
	}

	return false
}
