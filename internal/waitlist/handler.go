package waitlist

import (
	"context"
	"crypto/subtle"
	"encoding/csv"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// emailRE is a permissive but practical email validator.
var emailRE = regexp.MustCompile(`^[^@\s]+@[^@\s]+\.[^@\s]+$`)

// githubUsernameRE matches valid GitHub usernames (alphanumeric + hyphens, 1-39 chars).
var githubUsernameRE = regexp.MustCompile(`^[a-zA-Z0-9]([a-zA-Z0-9\-]{0,37}[a-zA-Z0-9])?$`)

// Handler holds the dependencies for all waitlist HTTP handlers.
type Handler struct {
	db          *SupabaseClient
	mailer      *Mailer
	adminSecret string
}

// NewHandler constructs a Handler.
func NewHandler(db *SupabaseClient, mailer *Mailer, adminSecret string) *Handler {
	return &Handler{db: db, mailer: mailer, adminSecret: adminSecret}
}

// joinRequest is the JSON body accepted by POST /waitlist.
type joinRequest struct {
	Email          string `json:"email"`
	GitHubUsername string `json:"github_username"`
}

// Join handles POST /waitlist.
//
// It validates the input, stores the entry in Supabase, and fires a
// confirmation email. Duplicate emails return 409.
func (h *Handler) Join(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20) // 1 MB limit
	var req joinRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, "invalid JSON body", http.StatusBadRequest)
		return
	}

	req.Email = strings.TrimSpace(strings.ToLower(req.Email))
	req.GitHubUsername = strings.TrimSpace(req.GitHubUsername)

	if !emailRE.MatchString(req.Email) {
		jsonError(w, "invalid email address", http.StatusUnprocessableEntity)
		return
	}
	if req.GitHubUsername != "" && !githubUsernameRE.MatchString(req.GitHubUsername) {
		jsonError(w, "invalid GitHub username", http.StatusUnprocessableEntity)
		return
	}

	entry := Entry{
		Email:          req.Email,
		GitHubUsername: req.GitHubUsername,
	}

	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()

	if err := h.db.Insert(ctx, entry); err != nil {
		if errors.Is(err, ErrDuplicate) {
			jsonError(w, "email already registered", http.StatusConflict)
			return
		}
		log.Printf("waitlist: insert error: %v", err)
		jsonError(w, "internal error", http.StatusInternalServerError)
		return
	}

	// Fire confirmation email. Non-fatal: log and continue so the user is
	// still registered even if email delivery is temporarily unavailable.
	go func() {
		mailCtx, mailCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer mailCancel()
		if err := h.mailer.SendConfirmation(mailCtx, entry); err != nil {
			log.Printf("waitlist: confirmation email failed for %s: %v", entry.Email, err)
		}
	}()

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(map[string]string{
		"status":  "ok",
		"message": "You're on the list — check your inbox for a confirmation.",
	})
}

// Export handles GET /admin/export.
//
// It requires the Authorization header to be "Bearer <ADMIN_SECRET>".
// It streams all waitlist entries as a CSV file.
func (h *Handler) Export(w http.ResponseWriter, r *http.Request) {
	if !h.checkAdminAuth(r) {
		jsonError(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()

	entries, err := h.db.List(ctx)
	if err != nil {
		log.Printf("waitlist: list error: %v", err)
		jsonError(w, "internal error", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="rinse-pro-waitlist.csv"`)

	cw := csv.NewWriter(w)
	if err := cw.Write([]string{"id", "email", "github_username", "created_at"}); err != nil {
		log.Printf("waitlist: csv write header: %v", err)
		return
	}
	for _, e := range entries {
		if err := cw.Write([]string{
			intStr(e.ID),
			e.Email,
			e.GitHubUsername,
			e.CreatedAt.UTC().Format(time.RFC3339),
		}); err != nil {
			log.Printf("waitlist: csv write row: %v", err)
			return
		}
	}
	cw.Flush()
	if err := cw.Error(); err != nil {
		log.Printf("waitlist: csv flush: %v", err)
	}
}

// checkAdminAuth validates the Bearer token in the Authorization header
// using a constant-time comparison to prevent timing attacks.
func (h *Handler) checkAdminAuth(r *http.Request) bool {
	auth := r.Header.Get("Authorization")
	const prefix = "Bearer "
	if !strings.HasPrefix(auth, prefix) {
		return false
	}
	token := strings.TrimPrefix(auth, prefix)
	if token == "" || h.adminSecret == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(token), []byte(h.adminSecret)) == 1
}

func jsonError(w http.ResponseWriter, msg string, status int) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": msg})
}

func intStr(n int) string {
	return strconv.Itoa(n)
}
