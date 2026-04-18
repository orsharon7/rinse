package runner

import (
	"encoding/json"
	"io"
	"time"
)

// Monitor is called by Run at key lifecycle points so callers can observe
// progress without coupling to the TUI or a specific output format.
//
// All methods are safe to call with a nil Monitor — each implementation must
// guard against a nil receiver, or callers use nilSafeMonitor().
type Monitor interface {
	// OnIterationStart is called before the agent is invoked for an iteration.
	OnIterationStart(iteration, maxIterations int)

	// OnWaitPoll is called each time the runner sleeps waiting for an
	// actionable Copilot review.
	OnWaitPoll(iteration, pollCount int, phase string)

	// OnPhase is called whenever the runner transitions to a new named phase.
	OnPhase(phase string, iteration int)

	// OnIterationComplete is called after a successful agent iteration.
	OnIterationComplete(iteration, commentsAddressed, totalComments int)

	// OnDone is called with the final result just before Run returns.
	OnDone(result Result, exitCode int, elapsedSeconds int)

	// OnError is called when a fatal error occurs.
	OnError(errCode, message string)
}

// noopMonitor satisfies Monitor with empty methods.
type noopMonitor struct{}

func (noopMonitor) OnIterationStart(_, _ int)              {}
func (noopMonitor) OnWaitPoll(_, _ int, _ string)          {}
func (noopMonitor) OnPhase(_ string, _ int)                {}
func (noopMonitor) OnIterationComplete(_, _, _ int)        {}
func (noopMonitor) OnDone(_ Result, _, _ int)              {}
func (noopMonitor) OnError(_, _ string)                    {}

// safeMonitor returns m if non-nil, otherwise a noopMonitor.
func safeMonitor(m Monitor) Monitor {
	if m == nil {
		return noopMonitor{}
	}
	return m
}

// ── JSON / NDJSON monitor ─────────────────────────────────────────────────────

// JSONMonitor emits NDJSON (newline-delimited JSON) events to w.
// All errors to stderr are handled by the caller; this monitor writes only to
// the supplied io.Writer (stdout in practice) so the stream stays parseable.
type JSONMonitor struct {
	w   io.Writer
	enc *json.Encoder
}

// NewJSONMonitor constructs a JSONMonitor that writes to w.
func NewJSONMonitor(w io.Writer) *JSONMonitor {
	enc := json.NewEncoder(w)
	// No indent — NDJSON must be one JSON object per line.
	return &JSONMonitor{w: w, enc: enc}
}

func (m *JSONMonitor) emit(v any) {
	// Errors from enc.Encode are intentionally ignored here — we cannot
	// write an error back to the same stream without corrupting it.
	_ = m.enc.Encode(v)
}

func (m *JSONMonitor) OnPhase(phase string, iteration int) {
	m.emit(struct {
		Event     string `json:"event"`
		Phase     string `json:"phase"`
		Iteration int    `json:"iteration"`
		Timestamp string `json:"timestamp"`
	}{
		Event:     "phase",
		Phase:     phase,
		Iteration: iteration,
		Timestamp: time.Now().UTC().Format(time.RFC3339),
	})
}

func (m *JSONMonitor) OnIterationStart(iteration, maxIterations int) {
	m.emit(struct {
		Event         string `json:"event"`
		Iteration     int    `json:"iteration"`
		MaxIterations int    `json:"max_iterations"`
		Timestamp     string `json:"timestamp"`
	}{
		Event:         "iteration_start",
		Iteration:     iteration,
		MaxIterations: maxIterations,
		Timestamp:     time.Now().UTC().Format(time.RFC3339),
	})
}

func (m *JSONMonitor) OnWaitPoll(iteration, pollCount int, phase string) {
	m.emit(struct {
		Event     string `json:"event"`
		Iteration int    `json:"iteration"`
		PollCount int    `json:"poll_count"`
		Phase     string `json:"phase"`
		Timestamp string `json:"timestamp"`
	}{
		Event:     "poll",
		Iteration: iteration,
		PollCount: pollCount,
		Phase:     phase,
		Timestamp: time.Now().UTC().Format(time.RFC3339),
	})
}

func (m *JSONMonitor) OnIterationComplete(iteration, commentsAddressed, totalComments int) {
	m.emit(struct {
		Event                 string `json:"event"`
		Iteration             int    `json:"iteration"`
		CommentsAddressed     int    `json:"comments_addressed"`
		TotalCommentsAddressed int   `json:"total_comments_addressed"`
		Timestamp             string `json:"timestamp"`
	}{
		Event:                 "iteration_complete",
		Iteration:             iteration,
		CommentsAddressed:     commentsAddressed,
		TotalCommentsAddressed: totalComments,
		Timestamp:             time.Now().UTC().Format(time.RFC3339),
	})
}

func (m *JSONMonitor) OnDone(result Result, exitCode int, elapsedSeconds int) {
	type doneEvent struct {
		Event                  string `json:"event"`
		Approved               bool   `json:"approved"`
		Iterations             int    `json:"iterations"`
		TotalCommentsAddressed int    `json:"total_comments_addressed"`
		ElapsedSeconds         int    `json:"elapsed_seconds"`
		ExitCode               int    `json:"exit_code"`
		Timestamp              string `json:"timestamp"`
		// Only present when not approved (max iterations).
		RemainingComments *int `json:"remaining_comments,omitempty"`
	}
	ev := doneEvent{
		Event:                  "done",
		Approved:               result.Approved,
		Iterations:             result.Iterations,
		TotalCommentsAddressed: result.TotalComments,
		ElapsedSeconds:         elapsedSeconds,
		ExitCode:               exitCode,
		Timestamp:              time.Now().UTC().Format(time.RFC3339),
	}
	m.emit(ev)
}

func (m *JSONMonitor) OnError(errCode, message string) {
	m.emit(struct {
		Event    string `json:"event"`
		Error    string `json:"error"`
		Message  string `json:"message"`
		ExitCode int    `json:"exit_code"`
	}{
		Event:    "error",
		Error:    errCode,
		Message:  message,
		ExitCode: 2,
	})
}
