package onboarding

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestIsOfflineErr(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{"deadline exceeded", context.DeadlineExceeded, true},
		{"wrapped deadline", fmt.Errorf("post: %w", context.DeadlineExceeded), true},
		{"econnrefused", syscall.ECONNREFUSED, true},
		{"wrapped econnrefused", fmt.Errorf("dial: %w", syscall.ECONNREFUSED), true},
		{"ehostunreach", syscall.EHOSTUNREACH, true},
		{"enetunreach", syscall.ENETUNREACH, true},
		{"dns error", &net.DNSError{Err: "no such host", Name: "missing.invalid"}, true},
		{"op error wrapped", &net.OpError{Op: "dial", Err: syscall.ECONNREFUSED}, true},
		{"plain http 500-style", errors.New("server returned 500: Internal Server Error"), false},
		{"parse response", errors.New("parse response: unexpected EOF"), false},
		{"context canceled (user abort, not offline)", context.Canceled, false},
		{"unrelated error", errors.New("something else entirely"), false},
		{"string fallback connection refused", errors.New("Post \"http://x\": dial tcp: connection refused"), true},
		{"string fallback no such host", errors.New("Get \"http://x\": dial tcp: lookup x: no such host"), true},
		{"string fallback i/o timeout", errors.New("Get \"http://x\": net/http: request canceled (i/o timeout)"), true},
		{"string fallback no route to host", errors.New("dial tcp 10.0.0.1:7433: connect: no route to host"), true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := IsOfflineErr(tc.err)
			if got != tc.want {
				t.Fatalf("IsOfflineErr(%v) = %v, want %v", tc.err, got, tc.want)
			}
		})
	}
}

// TestIsOfflineErr_RealRefusedDial exercises the live transport path: dial
// to a port that nothing is listening on must classify as offline. This is
// the exact scenario from issue #257 (CreateCycle vs. localhost:7433 with no
// backend running).
func TestIsOfflineErr_RealRefusedDial(t *testing.T) {
	t.Setenv("RINSE_API_URL", "http://127.0.0.1:1")

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	_, err := CreateCycle(ctx, "test", DefaultDefaults())
	if err == nil {
		t.Fatal("expected error from CreateCycle to unreachable port, got nil")
	}
	if !IsOfflineErr(err) {
		t.Fatalf("expected IsOfflineErr to classify dial-refused error as offline, got false. err=%v", err)
	}
}

// TestIsOfflineErr_RealHTTP500NotOffline ensures that a reachable server
// returning a non-2xx status is NOT classified as offline — those errors
// must still surface to the user (issue #257 design: only transport
// failures auto-skip).
func TestIsOfflineErr_RealHTTP500NotOffline(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer srv.Close()
	t.Setenv("RINSE_API_URL", srv.URL)

	_, err := CreateCycle(context.Background(), "test", DefaultDefaults())
	if err == nil {
		t.Fatal("expected error from CreateCycle on 500, got nil")
	}
	if !strings.Contains(err.Error(), "server returned 500") {
		t.Fatalf("expected error to mention 500, got: %v", err)
	}
	if IsOfflineErr(err) {
		t.Fatalf("HTTP 500 from reachable server must NOT be classified offline. err=%v", err)
	}
}
