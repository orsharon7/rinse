// Package waitlist implements the RINSE Pro waitlist HTTP server.
package waitlist

import (
	"net/http"
)

// Config holds all runtime configuration for the waitlist server.
type Config struct {
	SupabaseURL        string
	SupabaseServiceKey string
	ResendAPIKey       string
	AdminSecret        string
	FromEmail          string
	Port               int
}

// Server is the root HTTP handler for the waitlist service.
type Server struct {
	mux     *http.ServeMux
	handler *Handler
}

// NewServer wires up all routes and returns a ready-to-use HTTP handler.
func NewServer(cfg Config) *Server {
	sb := NewSupabaseClient(cfg.SupabaseURL, cfg.SupabaseServiceKey)
	mailer := NewMailer(cfg.ResendAPIKey, cfg.FromEmail)
	h := NewHandler(sb, mailer, cfg.AdminSecret)

	mux := http.NewServeMux()

	// Public endpoints
	mux.HandleFunc("POST /waitlist", h.Join)

	// Admin endpoint – requires Bearer $ADMIN_SECRET
	mux.HandleFunc("GET /admin/export", h.Export)

	// Health check
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})

	return &Server{mux: mux, handler: h}
}

// ServeHTTP implements http.Handler.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mux.ServeHTTP(w, r)
}
