package store

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"
)

func TestHasNonterminalAgentSwitchBeforeMigration(t *testing.T) {
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "pre-agent-switch.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })

	s := &Store{readDB: db}
	if active, err := s.HasNonterminalAgentSwitch(context.Background()); err != nil || active {
		t.Fatalf("active=%v err=%v, want false nil", active, err)
	}
}
