package sqlite

import (
	"database/sql"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestMigration9009EnforcesAgentSwitchStateAndRecoveryTuples(t *testing.T) {
	db, err := sql.Open("sqlite", "file:"+filepath.Join(t.TempDir(), "ao.db")+pragmas)
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := migrate(db); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	now := time.Date(2026, time.August, 10, 8, 0, 0, 0, time.UTC)
	if _, err := db.Exec(`
INSERT INTO projects (id, path, registered_at)
VALUES ('switch-contract', '/repos/switch-contract', ?);
INSERT INTO sessions (
    id, project_id, num, harness, runtime_launch_id,
    activity_last_at, created_at, updated_at
) VALUES (
    'switch-contract-1', 'switch-contract', 1, 'claude-code', 'source-generation',
    ?, ?, ?
);`, now, now, now, now); err != nil {
		t.Fatalf("seed parents: %v", err)
	}

	insert := func(id, state string) error {
		_, err := db.Exec(`
INSERT INTO agent_switches (
    id, session_id, idempotency_key, request_fingerprint,
    from_harness, target_harness, state, agent_handoff_status,
    source_generation_id, target_generation_id, role_snapshot_json,
    requested_at, updated_at
) VALUES (?, 'switch-contract-1', ?, ?, 'claude-code', 'codex', ?,
          'not_attempted', 'source-generation', 'target-generation',
          '{"roleId":"test-worker","resolvedHarness":"codex"}', ?, ?);`,
			id, id, "v1:"+strings.Repeat("a", 64), state, now, now)
		return err
	}
	if err := insert("invalid-initial", "starting_target"); err == nil {
		t.Fatal("noninitial switch tuple was inserted")
	}
	if err := insert("switch-contract", "preparing_handoff"); err != nil {
		t.Fatalf("insert initial switch: %v", err)
	}

	if _, err := db.Exec(`
UPDATE agent_switches SET state = 'source_stopped', updated_at = ?
WHERE id = 'switch-contract';`, now.Add(time.Second)); err == nil {
		t.Fatal("invalid preparing_handoff -> source_stopped transition succeeded")
	}
	if _, err := db.Exec(`
UPDATE agent_switches
SET state = 'stopping_source', updated_at = ?
WHERE id = 'switch-contract';`, now.Add(time.Second)); err == nil {
		t.Fatal("stopping_source without target plan succeeded")
	}
	if _, err := db.Exec(`
UPDATE agent_switches
SET state = 'stopping_source', target_start_mode = 'fresh',
    updated_at = ?
WHERE id = 'switch-contract';`, now.Add(time.Second)); err != nil {
		t.Fatalf("record target plan: %v", err)
	}
	if _, err := db.Exec(`
UPDATE agent_switches
SET target_generation_id = 'replacement-generation', updated_at = ?
WHERE id = 'switch-contract';`, now.Add(2*time.Second)); err == nil {
		t.Fatal("target generation was replaced")
	}
	if _, err := db.Exec(`
UPDATE agent_switches
SET request_fingerprint = ?, updated_at = ?
WHERE id = 'switch-contract';`, "v1:"+strings.Repeat("b", 64), now.Add(2*time.Second)); err == nil {
		t.Fatal("request fingerprint was mutated")
	}
	if _, err := db.Exec(`
UPDATE agent_switches SET state = 'source_stopped', updated_at = ?
WHERE id = 'switch-contract';`, now.Add(2*time.Second)); err != nil {
		t.Fatalf("record source stop: %v", err)
	}
	if _, err := db.Exec(`
UPDATE agent_switches SET state = 'starting_target', updated_at = ?
WHERE id = 'switch-contract';`, now.Add(3*time.Second)); err != nil {
		t.Fatalf("begin target start: %v", err)
	}
	if _, err := db.Exec(`
UPDATE agent_switches SET state = 'target_ready', updated_at = ?
WHERE id = 'switch-contract';`, now.Add(4*time.Second)); err == nil {
		t.Fatal("target_ready without native ownership tuple succeeded")
	}

	if _, err := db.Exec(`
INSERT INTO agent_native_sessions (
    id, ao_session_id, harness, native_session_id,
    last_generation_id, created_at, last_used_at
) VALUES (
    'target-native', 'switch-contract-1', 'codex', 'codex-thread',
    'target-generation', ?, ?
);`, now, now); err != nil {
		t.Fatalf("insert target native session: %v", err)
	}
	if _, err := db.Exec(`
UPDATE agent_switches
SET state = 'target_ready', target_native_session_ref = 'target-native',
    target_runtime_handle_id = 'target-handle', updated_at = ?
WHERE id = 'switch-contract';`, now.Add(4*time.Second)); err != nil {
		t.Fatalf("record target ownership: %v", err)
	}
	if _, err := db.Exec(`
UPDATE agent_switches SET target_acknowledged_at = ?, updated_at = ?
WHERE id = 'switch-contract';`, now.Add(5*time.Second), now.Add(5*time.Second)); err == nil {
		t.Fatal("target acknowledgement outside delivery succeeded")
	}
	if _, err := db.Exec(`
UPDATE agent_switches SET state = 'delivering_context', updated_at = ?
WHERE id = 'switch-contract';`, now.Add(5*time.Second)); err != nil {
		t.Fatalf("begin delivery: %v", err)
	}
	if _, err := db.Exec(`
UPDATE agent_switches SET state = 'completed', updated_at = ?
WHERE id = 'switch-contract';`, now.Add(6*time.Second)); err == nil {
		t.Fatal("completed switch without acknowledgement succeeded")
	}
	if _, err := db.Exec(`
UPDATE agent_switches SET target_acknowledged_at = ?, updated_at = ?
WHERE id = 'switch-contract';`, now.Add(6*time.Second), now.Add(6*time.Second)); err != nil {
		t.Fatalf("record target acknowledgement: %v", err)
	}
	if _, err := db.Exec(`
UPDATE agent_switches SET state = 'completed', updated_at = ?
WHERE id = 'switch-contract';`, now.Add(7*time.Second)); err != nil {
		t.Fatalf("complete acknowledged switch: %v", err)
	}
	if _, err := db.Exec(`
UPDATE agent_switches SET state = 'failed', error_code = 'switch_failed', updated_at = ?
WHERE id = 'switch-contract';`, now.Add(8*time.Second)); err == nil {
		t.Fatal("terminal switch state was rewritten")
	}
}
