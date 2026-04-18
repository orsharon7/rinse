// waitlist-server is the RINSE Pro waitlist HTTP server.
// It accepts email + GitHub username sign-ups, stores them in Supabase,
// sends a confirmation email via Resend, and exposes an admin CSV export.
//
// Required environment variables:
//
//	SUPABASE_URL          – https://<project>.supabase.co
//	SUPABASE_SERVICE_KEY  – service_role key (never the anon key)
//	RESEND_API_KEY        – Resend API key
//	ADMIN_SECRET          – Bearer token required on GET /admin/export
//
// Optional:
//
//	PORT       – listen port (default: 8080)
//	FROM_EMAIL – sender address (default: waitlist@rinse.dev)
package main

import (
	"fmt"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/orsharon7/rinse/internal/waitlist"
)

func main() {
	cfg, err := configFromEnv()
	if err != nil {
		log.Fatalf("waitlist-server: config error: %v", err)
	}

	srv := waitlist.NewServer(cfg)

	addr := fmt.Sprintf(":%d", cfg.Port)
	log.Printf("waitlist-server listening on %s", addr)
	httpSrv := &http.Server{
		Addr:              addr,
		Handler:           srv,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       120 * time.Second,
	}
	if err := httpSrv.ListenAndServe(); err != nil {
		log.Fatalf("waitlist-server: %v", err)
	}
}

func configFromEnv() (waitlist.Config, error) {
	var missing []string

	require := func(key string) string {
		v := os.Getenv(key)
		if strings.TrimSpace(v) == "" {
			missing = append(missing, key)
		}
		return v
	}

	cfg := waitlist.Config{
		SupabaseURL:        require("SUPABASE_URL"),
		SupabaseServiceKey: require("SUPABASE_SERVICE_KEY"),
		ResendAPIKey:       require("RESEND_API_KEY"),
		AdminSecret:        require("ADMIN_SECRET"),
	}

	if len(missing) > 0 {
		return cfg, fmt.Errorf("missing required env vars: %s", strings.Join(missing, ", "))
	}

	// Optional: PORT
	cfg.Port = 8080
	if p := os.Getenv("PORT"); p != "" {
		n, err := strconv.Atoi(p)
		if err != nil || n < 1 {
			return cfg, fmt.Errorf("PORT must be a positive integer, got %q", p)
		}
		cfg.Port = n
	}

	// Optional: FROM_EMAIL
	cfg.FromEmail = "waitlist@rinse.dev"
	if v := os.Getenv("FROM_EMAIL"); strings.TrimSpace(v) != "" {
		cfg.FromEmail = v
	}

	return cfg, nil
}
