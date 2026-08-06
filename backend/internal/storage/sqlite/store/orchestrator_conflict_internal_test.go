package store

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"
)

// isActiveOrchestratorConflict has to identify migration 0057's index from an
// error message alone: SQLite reports "UNIQUE constraint failed:
// sessions.project_id (2067)" and never names the index. The predicate is
// therefore keyed on the reported COLUMN LIST, and it must be an EXACT match —
// sessions has carried UNIQUE(project_id, num) since migration 0001, whose
// violation reports "sessions.project_id, sessions.num" and which a substring
// test silently misclassifies as an orchestrator conflict.
//
// This exercises the predicate against errors produced by the real driver
// rather than hand-built ones (modernc.org/sqlite.Error has unexported fields,
// so it cannot be constructed anyway). The schema below mirrors BOTH sessions
// constraints for that reason: a fixture that omits the composite one does not
// pin the precondition it claims to.
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
	// Both sessions constraints: 0001's composite and 0057's partial index.
	mustExec(`CREATE TABLE sessions (
		id TEXT PRIMARY KEY, project_id TEXT NOT NULL, num INTEGER NOT NULL,
		kind TEXT NOT NULL, is_terminated INTEGER NOT NULL,
		UNIQUE (project_id, num))`)
	mustExec(`CREATE UNIQUE INDEX idx_sessions_one_active_orchestrator
		ON sessions (project_id) WHERE kind = 'orchestrator' AND is_terminated = 0`)
	// And a second table with its own unique index, standing in for review_run.
	mustExec(`CREATE TABLE review_run (id TEXT PRIMARY KEY, session_id TEXT NOT NULL)`)
	mustExec(`CREATE UNIQUE INDEX idx_review_run_session ON review_run (session_id)`)

	insert := func(id string, num int, kind string) error {
		_, err := db.ExecContext(ctx,
			`INSERT INTO sessions (id, project_id, num, kind, is_terminated) VALUES (?, 'mer', ?, ?, 0)`,
			id, num, kind)
		return err
	}
	if err := insert("mer-1", 1, "orchestrator"); err != nil {
		t.Fatalf("first orchestrator: %v", err)
	}

	// 0057's index: a second ACTIVE orchestrator, distinct num.
	orchestratorErr := insert("mer-2", 2, "orchestrator")
	if orchestratorErr == nil {
		t.Fatal("second active orchestrator was accepted; the fixture index is wrong")
	}
	if got := uniqueConstraintColumns(orchestratorErr); got != "sessions.project_id" {
		t.Fatalf("reported columns = %q, want %q (%v)", got, "sessions.project_id", orchestratorErr)
	}
	if !isActiveOrchestratorConflict(orchestratorErr) {
		t.Errorf("isActiveOrchestratorConflict(%v) = false, want true", orchestratorErr)
	}

	// 0001's composite: same (project_id, num), and a kind that keeps it out of
	// the partial index entirely — so this is unambiguously the OTHER constraint.
	compositeErr := insert("mer-1-dup", 1, "worker")
	if compositeErr == nil {
		t.Fatal("duplicate (project_id, num) was accepted; the fixture is wrong")
	}
	if got := uniqueConstraintColumns(compositeErr); got != "sessions.project_id, sessions.num" {
		t.Fatalf("reported columns = %q, want the composite pair (%v)", got, compositeErr)
	}
	if isActiveOrchestratorConflict(compositeErr) {
		t.Errorf("UNIQUE(project_id, num) was misreported as an orchestrator conflict: %v", compositeErr)
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
		`INSERT INTO sessions (id, project_id, num, kind, is_terminated) VALUES ('mer-3', NULL, 3, 'worker', 0)`)
	if nullErr == nil {
		t.Fatal("NOT NULL violation was accepted; the fixture is wrong")
	}
	if isActiveOrchestratorConflict(nullErr) {
		t.Errorf("a NOT NULL violation was reported as an orchestrator conflict: %v", nullErr)
	}
	if got := uniqueConstraintColumns(nullErr); got != "" {
		t.Errorf("uniqueConstraintColumns(non-unique) = %q, want empty", got)
	}

	// And a nil error is never a conflict.
	if isActiveOrchestratorConflict(nil) {
		t.Error("isActiveOrchestratorConflict(nil) = true")
	}
}
