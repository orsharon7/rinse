package waitlist

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

const resendEndpoint = "https://api.resend.com/emails"

// Mailer sends transactional emails via Resend.
type Mailer struct {
	apiKey    string
	fromEmail string
	http      *http.Client
}

// NewMailer returns a configured Mailer.
func NewMailer(apiKey, fromEmail string) *Mailer {
	return &Mailer{
		apiKey:    apiKey,
		fromEmail: fromEmail,
		http:      &http.Client{Timeout: 10 * time.Second},
	}
}

type resendPayload struct {
	From    string   `json:"from"`
	To      []string `json:"to"`
	Subject string   `json:"subject"`
	HTML    string   `json:"html"`
}

// SendConfirmation fires a confirmation email to the new waitlist subscriber.
func (m *Mailer) SendConfirmation(ctx context.Context, e Entry) error {
	html := confirmationHTML(e)

	payload := resendPayload{
		From:    m.fromEmail,
		To:      []string{e.Email},
		Subject: "You're on the RINSE Pro waitlist 🎉",
		HTML:    html,
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("mailer: marshal payload: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, resendEndpoint, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("mailer: build request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+m.apiKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := m.http.Do(req)
	if err != nil {
		return fmt.Errorf("mailer: send: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		raw, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("mailer: resend status %d: %s", resp.StatusCode, raw)
	}
	return nil
}

func confirmationHTML(e Entry) string {
	name := e.GitHubUsername
	if name == "" {
		name = e.Email
	}
	return fmt.Sprintf(`<!DOCTYPE html>
<html>
<head><meta charset="utf-8"></head>
<body style="font-family:sans-serif;max-width:560px;margin:0 auto;padding:24px;color:#111">
  <h1 style="font-size:1.5rem;margin-bottom:8px">You're on the RINSE Pro waitlist</h1>
  <p>Hey <strong>%s</strong> — thanks for signing up.</p>
  <p>
    RINSE Pro automates your entire GitHub Copilot PR review loop — 
    letting AI agents fix, push, and re-request review until Copilot approves.
    We'll reach out as soon as a beta slot opens.
  </p>
  <p>In the meantime, check out the open-source core on
    <a href="https://github.com/orsharon7/rinse">GitHub</a>.
  </p>
  <hr style="border:none;border-top:1px solid #eee;margin:24px 0">
  <p style="font-size:.875rem;color:#666">
    You're receiving this because you signed up at rinse.dev.<br>
    If this was a mistake, just ignore this email — we'll never spam you.
  </p>
</body>
</html>`, name)
}
