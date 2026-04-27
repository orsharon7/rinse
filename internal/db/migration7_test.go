package db

import (
	"os"
	"testing"
	"time"
)

func TestMigration7RulesExtracted(t *testing.T) {
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
		StartedAt: time.Now(), RulesExtracted: 5,
	}
	if err := d.InsertSession(s); err != nil {
		t.Fatalf("InsertSession: %v", err)
	}

	rows, err := d.LoadSessions()
	if err != nil {
		t.Fatalf("LoadSessions: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(rows))
	}
	if rows[0].RulesExtracted != 5 {
		t.Fatalf("expected RulesExtracted=5, got %d", rows[0].RulesExtracted)
	}
	t.Logf("OK: RulesExtracted=%d", rows[0].RulesExtracted)
}
