package db

import (
	"os"
	"testing"
	"time"
)

// TestMigrations verifies that all migrations apply cleanly on a fresh DB
// and that key fields introduced by migrations round-trip correctly.
func TestMigrations(t *testing.T) {
	tmp, err := os.CreateTemp("", "rinse-test-*.db")
	if err != nil {
		t.Fatal(err)
	}
	tmp.Close()
	defer os.Remove(tmp.Name())

	d, err := Open(tmp.Name())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer d.Close()

	// Verify all 8 migrations recorded
	rows, err := d.sql.Query(`SELECT version, name FROM schema_migrations ORDER BY version`)
	if err != nil {
		t.Fatalf("query schema_migrations: %v", err)
	}
	defer rows.Close()
	var versions []int
	for rows.Next() {
		var v int
		var name string
		if err := rows.Scan(&v, &name); err != nil {
			t.Fatal(err)
		}
		versions = append(versions, v)
	}
	// Fresh DB gets baseline schema (no migrations 1-3 in slice) then 4-8
	for _, want := range []int{4, 5, 6, 7, 8} {
		found := false
		for _, v := range versions {
			if v == want {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("migration %d not recorded in schema_migrations; got %v", want, versions)
		}
	}
}

// TestRulesExtractedRoundTrip verifies migration 007: rules_extracted persists.
func TestRulesExtractedRoundTrip(t *testing.T) {
	tmp, err := os.CreateTemp("", "rinse-test-*.db")
	if err != nil {
		t.Fatal(err)
	}
	tmp.Close()
	defer os.Remove(tmp.Name())

	d, err := Open(tmp.Name())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer d.Close()

	s := SessionRow{
		ID: "test-001", Repo: "test/repo", PRNumber: 1,
		StartedAt: time.Now(), RulesExtracted: 7,
	}
	if err := d.InsertSession(s); err != nil {
		t.Fatalf("InsertSession: %v", err)
	}

	loaded, err := d.LoadSessions()
	if err != nil {
		t.Fatalf("LoadSessions: %v", err)
	}
	if len(loaded) != 1 {
		t.Fatalf("expected 1 row, got %d", len(loaded))
	}
	if loaded[0].RulesExtracted != 7 {
		t.Fatalf("RulesExtracted: want 7, got %d", loaded[0].RulesExtracted)
	}
}

// TestOutcomeCleanDryRunAllowed verifies migration 008: clean and dry_run
// are accepted by a fresh install's CHECK constraint.
func TestOutcomeCleanDryRunAllowed(t *testing.T) {
	tmp, err := os.CreateTemp("", "rinse-test-*.db")
	if err != nil {
		t.Fatal(err)
	}
	tmp.Close()
	defer os.Remove(tmp.Name())

	d, err := Open(tmp.Name())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer d.Close()

	for _, outcome := range []string{"clean", "dry_run", "error", "aborted", "max_iterations", "approved", "merged", "closed", "open", "failed"} {
		s := SessionRow{
			ID: "test-" + outcome, Repo: "test/repo", PRNumber: 1,
			StartedAt: time.Now(), Outcome: outcome,
		}
		if err := d.InsertSession(s); err != nil {
			t.Errorf("InsertSession outcome=%q: %v", outcome, err)
		}
	}
}
