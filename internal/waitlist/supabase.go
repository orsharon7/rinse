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

const supabaseTable = "pro_waitlist"

// Entry is a single waitlist record.
type Entry struct {
	ID             int       `json:"id,omitempty"`
	Email          string    `json:"email"`
	GitHubUsername string    `json:"github_username"`
	CreatedAt      time.Time `json:"created_at,omitempty"`
}

// SupabaseClient talks to the Supabase REST API.
type SupabaseClient struct {
	baseURL    string
	serviceKey string
	http       *http.Client
}

// NewSupabaseClient returns a configured Supabase client.
func NewSupabaseClient(baseURL, serviceKey string) *SupabaseClient {
	return &SupabaseClient{
		baseURL:    baseURL,
		serviceKey: serviceKey,
		http:       &http.Client{Timeout: 10 * time.Second},
	}
}

// Insert stores a new waitlist entry. It returns ErrDuplicate when the email
// already exists (Supabase unique-constraint violation).
func (c *SupabaseClient) Insert(ctx context.Context, e Entry) error {
	body, err := json.Marshal(e)
	if err != nil {
		return fmt.Errorf("supabase: marshal entry: %w", err)
	}

	url := fmt.Sprintf("%s/rest/v1/%s", c.baseURL, supabaseTable)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("supabase: build request: %w", err)
	}
	c.setHeaders(req)
	req.Header.Set("Prefer", "return=minimal")

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("supabase: insert: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusConflict {
		return ErrDuplicate
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		raw, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("supabase: insert status %d: %s", resp.StatusCode, raw)
	}
	return nil
}

// List returns all waitlist entries ordered by created_at ascending.
// It paginates through results using limit/offset to avoid Supabase's
// default max-rows cap (commonly 1000), so the full table is always returned.
func (c *SupabaseClient) List(ctx context.Context) ([]Entry, error) {
	const pageSize = 1000
	var all []Entry
	for offset := 0; ; offset += pageSize {
		page, err := c.listPage(ctx, pageSize, offset)
		if err != nil {
			return nil, err
		}
		all = append(all, page...)
		if len(page) < pageSize {
			break
		}
	}
	return all, nil
}

// ListPage fetches a single page of waitlist entries ordered by created_at ascending.
// limit and offset control pagination.
func (c *SupabaseClient) ListPage(ctx context.Context, limit, offset int) ([]Entry, error) {
	return c.listPage(ctx, limit, offset)
}

// listPage fetches a single page of waitlist entries.
func (c *SupabaseClient) listPage(ctx context.Context, limit, offset int) ([]Entry, error) {
	url := fmt.Sprintf("%s/rest/v1/%s?order=created_at.asc&limit=%d&offset=%d",
		c.baseURL, supabaseTable, limit, offset)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("supabase: build request: %w", err)
	}
	c.setHeaders(req)

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("supabase: list: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		raw, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("supabase: list status %d: %s", resp.StatusCode, raw)
	}

	var entries []Entry
	if err := json.NewDecoder(resp.Body).Decode(&entries); err != nil {
		return nil, fmt.Errorf("supabase: decode list: %w", err)
	}
	return entries, nil
}

func (c *SupabaseClient) setHeaders(r *http.Request) {
	r.Header.Set("apikey", c.serviceKey)
	r.Header.Set("Authorization", "Bearer "+c.serviceKey)
	r.Header.Set("Content-Type", "application/json")
}

// ErrDuplicate is returned when the email is already on the waitlist.
var ErrDuplicate = fmt.Errorf("email already registered")
