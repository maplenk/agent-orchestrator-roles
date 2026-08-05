package sqlite

import (
	"database/sql"
	"path/filepath"
	"testing"
	"time"
)

// seedOrchestrator inserts a session row directly, bypassing the store so the
// pre-0046 duplicate state can be reproduced.
func seedSession(t *testing.T, db *sql.DB, id, project string, num int, kind string, terminated bool, createdAt, workspace string) {
	t.Helper()
	term := 0
	if terminated {
		term = 1
	}
	_, err := db.Exec(`
		INSERT INTO sessions (
			id, project_id, num, kind, is_terminated,
			activity_last_at, created_at, updated_at,
			branch, workspace_path, workspace_repo_path, runtime_handle_id, runtime_launch_id
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		id, project, num, kind, term,
		createdAt, createdAt, createdAt,
		"ao/"+project+"-orchestrator", workspace, workspace, "tmux-"+id, "launch-"+id,
	)
	if err != nil {
		t.Fatalf("seed session %s: %v", id, err)
	}
}

// TestMigration0046ReconcilesDuplicateOrchestrators is the data-safety guard for
// the one-active-orchestrator constraint. A pre-2B daemon could already hold two
// active orchestrators per project (Restore and boot RestoreAll take no
// ownership gate), and CREATE UNIQUE INDEX fails outright on that data —
// wedging startup permanently, because goose records its version inside the
// transaction it rolls back and sqlite.Open's error aborts daemon.Run. The
// migration must therefore reconcile first, in the same transaction.
func TestMigration0046ReconcilesDuplicateOrchestrators(t *testing.T) {
	db, err := sql.Open("sqlite", "file:"+filepath.Join(t.TempDir(), "ao.db")+"?_pragma=busy_timeout(5000)")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	// Stop just before 0046: duplicates are still representable.
	upTo(t, db, 45)

	if _, err := db.Exec(`INSERT INTO projects (id, path, registered_at) VALUES ('mer','/repo/mer','2026-01-01'), ('other','/repo/other','2026-01-01')`); err != nil {
		t.Fatalf("seed projects: %v", err)
	}

	const canonical = "/ws/mer/orchestrator/mer-orchestrator"
	// Three active orchestrators in one project, all naming the canonical
	// workspace. Newest created_at must survive.
	seedSession(t, db, "mer-1", "mer", 1, "orchestrator", false, "2026-01-01", canonical)
	seedSession(t, db, "mer-2", "mer", 2, "orchestrator", false, "2026-01-03", canonical) // survivor
	seedSession(t, db, "mer-3", "mer", 3, "orchestrator", false, "2026-01-02", canonical)
	// Must be left alone: a worker, an already-terminated orchestrator, and a
	// different project's sole orchestrator.
	seedSession(t, db, "mer-w", "mer", 4, "worker", false, "2026-01-04", "/ws/mer/mer-w")
	seedSession(t, db, "mer-old", "mer", 5, "orchestrator", true, "2025-12-01", "/ws/mer/stale")
	seedSession(t, db, "other-1", "other", 1, "orchestrator", false, "2026-01-01", "/ws/other/orchestrator")

	// Every duplicate carries a restore marker; the losers' markers must go, or
	// RestoreAll resurrects them onto the canonical worktree.
	for _, id := range []string{"mer-1", "mer-2", "mer-3"} {
		if _, err := db.Exec(`INSERT INTO session_worktrees (session_id, repo_name, branch, base_sha, worktree_path, state)
			VALUES (?, 'root', 'ao/mer-orchestrator', 'sha', ?, 'active')`, id, canonical); err != nil {
			t.Fatalf("seed marker %s: %v", id, err)
		}
	}

	upTo(t, db, 46)

	// 1. Exactly one active orchestrator survives per project, and it is the
	//    deterministic newest — matching newestOrchestratorRecord in Go.
	var survivor string
	if err := db.QueryRow(`SELECT id FROM sessions WHERE project_id='mer' AND kind='orchestrator' AND is_terminated=0`).Scan(&survivor); err != nil {
		t.Fatalf("survivor query: %v", err)
	}
	if survivor != "mer-2" {
		t.Fatalf("survivor = %q, want mer-2 (newest created_at)", survivor)
	}

	// 2. Losers are terminated, not deleted (sessions is widely referenced).
	for _, id := range []string{"mer-1", "mer-3"} {
		var terminated bool
		if err := db.QueryRow(`SELECT is_terminated FROM sessions WHERE id=?`, id).Scan(&terminated); err != nil {
			t.Fatalf("loser %s must still exist: %v", id, err)
		}
		if !terminated {
			t.Errorf("loser %s must be terminated", id)
		}
	}

	// 3. Losers released their workspace claim, or they would alias the
	//    survivor's canonical worktree and a later Kill/Cleanup would destroy it.
	for _, id := range []string{"mer-1", "mer-3"} {
		var path, repoPath, branch, handle, launch string
		if err := db.QueryRow(`SELECT workspace_path, workspace_repo_path, branch, runtime_handle_id, runtime_launch_id FROM sessions WHERE id=?`, id).
			Scan(&path, &repoPath, &branch, &handle, &launch); err != nil {
			t.Fatalf("loser %s claim query: %v", id, err)
		}
		if path != "" || repoPath != "" || branch != "" || handle != "" || launch != "" {
			t.Errorf("loser %s still claims ownership: path=%q repo=%q branch=%q handle=%q launch=%q",
				id, path, repoPath, branch, handle, launch)
		}
	}

	// 4. Losers' restore markers are gone; the survivor keeps its own.
	for _, tc := range []struct {
		id   string
		want int
	}{{"mer-1", 0}, {"mer-3", 0}, {"mer-2", 1}} {
		var n int
		if err := db.QueryRow(`SELECT COUNT(*) FROM session_worktrees WHERE session_id=?`, tc.id).Scan(&n); err != nil {
			t.Fatalf("marker count %s: %v", tc.id, err)
		}
		if n != tc.want {
			t.Errorf("%s restore markers = %d, want %d", tc.id, n, tc.want)
		}
	}

	// 5. The losers' EXACT execution identity is preserved for the fail-closed
	//    boot reaper. These duplicates own external processes, so clearing the
	//    handles without capturing them first would leave nothing authoritative
	//    to probe.
	for _, id := range []string{"mer-1", "mer-3"} {
		var project, handle, launch, workspace string
		var queuedAt time.Time
		var attempts int
		if err := db.QueryRow(`SELECT project_id, runtime_handle_id, runtime_launch_id, workspace_path, queued_at, attempt_count
			FROM orchestrator_reap_queue WHERE session_id=?`, id).
			Scan(&project, &handle, &launch, &workspace, &queuedAt, &attempts); err != nil {
			t.Fatalf("loser %s must be queued for reaping: %v", id, err)
		}
		if project != "mer" {
			t.Errorf("%s queued project = %q, want mer", id, project)
		}
		if handle != "tmux-"+id || launch != "launch-"+id {
			t.Errorf("%s queued with handle=%q launch=%q, want the ORIGINAL pre-reconciliation identity", id, handle, launch)
		}
		if workspace != canonical {
			t.Errorf("%s queued workspace = %q, want %q", id, workspace, canonical)
		}
		if queuedAt.IsZero() {
			t.Errorf("%s queued_at must be set and scannable as a time", id)
		}
		if attempts != 0 {
			t.Errorf("%s attempt_count = %d, want 0", id, attempts)
		}
	}

	// 6. Only losers are queued: the survivor and untouched rows owe nothing.
	for _, id := range []string{"mer-2", "mer-w", "mer-old", "other-1"} {
		var n int
		if err := db.QueryRow(`SELECT COUNT(*) FROM orchestrator_reap_queue WHERE session_id=?`, id).Scan(&n); err != nil {
			t.Fatalf("queue count %s: %v", id, err)
		}
		if n != 0 {
			t.Errorf("%s must not be queued for reaping", id)
		}
	}

	// 7. Untouched rows stay untouched.
	for _, id := range []string{"mer-w", "other-1"} {
		var terminated bool
		var path string
		if err := db.QueryRow(`SELECT is_terminated, workspace_path FROM sessions WHERE id=?`, id).Scan(&terminated, &path); err != nil {
			t.Fatalf("query %s: %v", id, err)
		}
		if terminated {
			t.Errorf("%s must not be terminated by orchestrator reconciliation", id)
		}
		if path == "" {
			t.Errorf("%s must keep its workspace claim", id)
		}
	}

	// 8. The constraint is live: a second active orchestrator is now rejected.
	_, err = db.Exec(`UPDATE sessions SET is_terminated=0 WHERE id='mer-1'`)
	if err == nil {
		t.Fatal("re-activating a second orchestrator must violate the unique index")
	}
}

// TestMigration0046IsANoOpWithoutDuplicates keeps the reconciliation from
// touching a healthy database.
func TestMigration0046IsANoOpWithoutDuplicates(t *testing.T) {
	db, err := sql.Open("sqlite", "file:"+filepath.Join(t.TempDir(), "ao.db")+"?_pragma=busy_timeout(5000)")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	upTo(t, db, 45)
	if _, err := db.Exec(`INSERT INTO projects (id, path, registered_at) VALUES ('mer','/repo/mer','2026-01-01')`); err != nil {
		t.Fatalf("seed project: %v", err)
	}
	seedSession(t, db, "mer-1", "mer", 1, "orchestrator", false, "2026-01-01", "/ws/mer/orchestrator")
	seedSession(t, db, "mer-2", "mer", 2, "worker", false, "2026-01-02", "/ws/mer/mer-2")

	upTo(t, db, 46)

	for _, id := range []string{"mer-1", "mer-2"} {
		var terminated bool
		var path string
		if err := db.QueryRow(`SELECT is_terminated, workspace_path FROM sessions WHERE id=?`, id).Scan(&terminated, &path); err != nil {
			t.Fatalf("query %s: %v", id, err)
		}
		if terminated || path == "" {
			t.Errorf("%s was modified by a no-op reconciliation: terminated=%v path=%q", id, terminated, path)
		}
	}

	// A healthy database owes no reaping: an entry here would make the boot
	// reaper probe a runtime that was never superseded.
	var queued int
	if err := db.QueryRow(`SELECT COUNT(*) FROM orchestrator_reap_queue`).Scan(&queued); err != nil {
		t.Fatalf("queue count: %v", err)
	}
	if queued != 0 {
		t.Fatalf("reap queue = %d entries, want 0 on a healthy database", queued)
	}
}
