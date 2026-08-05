package store

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"
)

// isActiveOrchestratorConflict has to identify migration 0046's index from an
// error message alone: SQLite reports "UNIQUE constraint failed:
// sessions.project_id (2067)" and never names the index. The predicate is
// therefore keyed on the COLUMN, which is only unambiguous while
// idx_sessions_one_active_orchestrator remains the sole unique index on
// sessions.
//
// This exercises it against errors produced by the real driver rather than
// hand-built ones (modernc.org/sqlite.Error has unexported fields, so it cannot
// be constructed anyway). Going through the public store API could not prove
// the scoping today — no other unique index on sessions exists to collide
// with — which is exactly why the guard is worth pinning here: the day one is
// added, this fails instead of silently mislabelling it.
func TestIsActiveOrchestratorConflict_IsScopedToTheSessionsIndex(t *testing.T) {
	ctx := context.Background()
	db, err := sql.Open("sqlite", "file:"+filepath.Join(t.TempDir(), "probe.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	mustExec := func(query string) {
		t.Helper()
		if _, err := db.ExecContext(ctx, query); err != nil {
			t.Fatalf("exec %q: %v", query, err)
		}
	}
	// Mirror the shape 0046 creates.
	mustExec(`CREATE TABLE sessions (
		id TEXT PRIMARY KEY, project_id TEXT NOT NULL, kind TEXT NOT NULL, is_terminated INTEGER NOT NULL)`)
	mustExec(`CREATE UNIQUE INDEX idx_sessions_one_active_orchestrator
		ON sessions (project_id) WHERE kind = 'orchestrator' AND is_terminated = 0`)
	// And a second table with its own unique index, standing in for review_run.
	mustExec(`CREATE TABLE review_run (id TEXT PRIMARY KEY, session_id TEXT NOT NULL)`)
	mustExec(`CREATE UNIQUE INDEX idx_review_run_session ON review_run (session_id)`)

	insertOrchestrator := func(id string) error {
		_, err := db.ExecContext(ctx,
			`INSERT INTO sessions (id, project_id, kind, is_terminated) VALUES (?, 'mer', 'orchestrator', 0)`, id)
		return err
	}
	if err := insertOrchestrator("mer-1"); err != nil {
		t.Fatalf("first orchestrator: %v", err)
	}
	orchestratorErr := insertOrchestrator("mer-2")
	if orchestratorErr == nil {
		t.Fatal("second active orchestrator was accepted; the fixture index is wrong")
	}
	if !isActiveOrchestratorConflict(orchestratorErr) {
		t.Errorf("isActiveOrchestratorConflict(%v) = false, want true", orchestratorErr)
	}

	// A unique violation on a DIFFERENT table must not be claimed.
	if _, err := db.ExecContext(ctx, `INSERT INTO review_run VALUES ('run-1', 'mer-1')`); err != nil {
		t.Fatalf("first review run: %v", err)
	}
	_, reviewErr := db.ExecContext(ctx, `INSERT INTO review_run VALUES ('run-2', 'mer-1')`)
	if reviewErr == nil {
		t.Fatal("duplicate review run was accepted; the fixture index is wrong")
	}
	if !isSQLiteUnique(reviewErr) {
		t.Fatalf("fixture did not produce a unique violation: %v", reviewErr)
	}
	if isActiveOrchestratorConflict(reviewErr) {
		t.Errorf("a review_run collision was reported as an orchestrator conflict: %v", reviewErr)
	}

	// A constraint failure that is not a UNIQUE violation must not be claimed
	// either, however the message reads.
	_, nullErr := db.ExecContext(ctx,
		`INSERT INTO sessions (id, project_id, kind, is_terminated) VALUES ('mer-3', NULL, 'worker', 0)`)
	if nullErr == nil {
		t.Fatal("NOT NULL violation was accepted; the fixture is wrong")
	}
	if isActiveOrchestratorConflict(nullErr) {
		t.Errorf("a NOT NULL violation was reported as an orchestrator conflict: %v", nullErr)
	}

	// And a nil error is never a conflict.
	if isActiveOrchestratorConflict(nil) {
		t.Error("isActiveOrchestratorConflict(nil) = true")
	}
}
