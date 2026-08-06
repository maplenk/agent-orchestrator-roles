package sqlite

import (
	"database/sql"
	"path/filepath"
	"testing"
)

// The four histories a real database can be in when this build first runs.
// Each is built by hand rather than by running goose, because the point is to
// simulate a database written by an EARLIER build — one whose migration files
// no longer exist in this binary.

func openRaw(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func seedGooseLedger(t *testing.T, db *sql.DB, versions ...int64) {
	t.Helper()
	if _, err := db.Exec(`CREATE TABLE goose_db_version (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		version_id INTEGER NOT NULL,
		is_applied BOOLEAN NOT NULL,
		tstamp TIMESTAMP DEFAULT (datetime('now'))
	)`); err != nil {
		t.Fatal(err)
	}
	for _, v := range append([]int64{0}, versions...) {
		if _, err := db.Exec(`INSERT INTO goose_db_version (version_id, is_applied) VALUES (?, 1)`, v); err != nil {
			t.Fatal(err)
		}
	}
}

// seedForkSchema creates just enough physical schema for the fingerprints of
// the first n fork migrations to match.
func seedForkSchema(t *testing.T, db *sql.DB, n int) {
	t.Helper()
	if _, err := db.Exec(`CREATE TABLE sessions (id TEXT PRIMARY KEY)`); err != nil {
		t.Fatal(err)
	}
	steps := []string{
		`ALTER TABLE sessions ADD COLUMN role_id TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE sessions ADD COLUMN spawn_capability_hash TEXT NOT NULL DEFAULT ''`,
		`CREATE TABLE lifecycle_ledger (id TEXT PRIMARY KEY, kind TEXT)`,
		`ALTER TABLE sessions ADD COLUMN switch_pending_json TEXT NOT NULL DEFAULT ''`,
		`CREATE UNIQUE INDEX idx_sessions_one_active_orchestrator ON sessions(id)`,
		`CREATE TABLE orchestrator_replacement_intent (id TEXT PRIMARY KEY)`,
		``, // 0059 is handled below: it REBUILDS the ledger
		`ALTER TABLE sessions ADD COLUMN pause_json TEXT NOT NULL DEFAULT ''`,
	}
	for i := 0; i < n && i < len(steps); i++ {
		if i == 6 {
			for _, stmt := range []string{
				`DROP TABLE lifecycle_ledger`,
				`CREATE TABLE lifecycle_ledger (id TEXT PRIMARY KEY, kind TEXT CHECK (kind IN ('switch', 'orchestrator_fresh_conversation')))`,
			} {
				if _, err := db.Exec(stmt); err != nil {
					t.Fatal(err)
				}
			}
			continue
		}
		if steps[i] == "" {
			continue
		}
		if _, err := db.Exec(steps[i]); err != nil {
			t.Fatalf("step %d: %v", i, err)
		}
	}
}

func ledger(t *testing.T, db *sql.DB) map[int64]bool {
	t.Helper()
	rows, err := db.Query(`SELECT version_id, is_applied FROM goose_db_version ORDER BY id`)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	out := map[int64]bool{}
	for rows.Next() {
		var v int64
		var applied bool
		if err := rows.Scan(&v, &applied); err != nil {
			t.Fatal(err)
		}
		out[v] = applied
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

// A database that ran the whole fork range. Every 53-60 entry must move, and
// 53 must be FREED — that is the entire point, since upstream's real 0053
// (Muse) can only run if goose no longer believes 53 is applied.
func TestForkRepairFullHistory(t *testing.T) {
	db := openRaw(t)
	seedGooseLedger(t, db, 52, 53, 54, 55, 56, 57, 58, 59, 60)
	seedForkSchema(t, db, 8)

	if err := repairForkMigrationVersions(db); err != nil {
		t.Fatalf("repair: %v", err)
	}

	got := ledger(t, db)
	for v := int64(53); v <= 60; v++ {
		if _, ok := got[v]; ok {
			t.Errorf("version %d still recorded; upstream's migration at that number stays skipped", v)
		}
	}
	for v := int64(9000); v <= 9007; v++ {
		if !got[v] {
			t.Errorf("version %d not recorded; its migration will re-run against a table that already has it", v)
		}
	}
	if !got[52] {
		t.Error("repair disturbed an upstream version it does not own")
	}
}

// Stopped part way — an install that upgraded to a mid-range fork build. The
// applied prefix moves; the rest must NOT be invented, or goose will skip
// migrations this database never ran.
func TestForkRepairPartialHistory(t *testing.T) {
	db := openRaw(t)
	seedGooseLedger(t, db, 52, 53, 54, 55, 56)
	seedForkSchema(t, db, 4)

	if err := repairForkMigrationVersions(db); err != nil {
		t.Fatalf("repair: %v", err)
	}

	got := ledger(t, db)
	for _, v := range []int64{9000, 9001, 9002, 9003} {
		if !got[v] {
			t.Errorf("applied migration %d was not carried over", v)
		}
	}
	for _, v := range []int64{9004, 9005, 9006, 9007} {
		if _, ok := got[v]; ok {
			t.Errorf("version %d was recorded but never ran; goose will now skip it", v)
		}
	}
}

// A database that only ever ran upstream. Version 53 here is the MUSE
// migration, and the fork's fingerprints are absent. Touching it would delete a
// legitimate entry and re-run Muse against an already-widened CHECK.
func TestForkRepairLeavesACleanUpstreamHistoryAlone(t *testing.T) {
	db := openRaw(t)
	seedGooseLedger(t, db, 52, 53)
	if _, err := db.Exec(`CREATE TABLE sessions (id TEXT PRIMARY KEY)`); err != nil {
		t.Fatal(err)
	}

	if err := repairForkMigrationVersions(db); err != nil {
		t.Fatalf("repair: %v", err)
	}

	got := ledger(t, db)
	if !got[53] {
		t.Fatal("repair claimed upstream's version 53; Muse would be re-run and the fork's 9000 skipped")
	}
	if _, ok := got[9000]; ok {
		t.Fatal("repair recorded a fork migration this database never ran")
	}
}

// Every boot runs this. The second pass must be a no-op — and the interesting
// part is that by then version 53 is legitimately recorded again, by Muse,
// while the fork's fingerprints are still present. Keying on the OLD version
// plus the fingerprint alone would eat it.
func TestForkRepairIsIdempotentEvenOnceUpstreamReclaimsTheNumber(t *testing.T) {
	db := openRaw(t)
	seedGooseLedger(t, db, 52, 53, 54, 55, 56, 57, 58, 59, 60)
	seedForkSchema(t, db, 8)

	if err := repairForkMigrationVersions(db); err != nil {
		t.Fatalf("first repair: %v", err)
	}
	first := ledger(t, db)

	// goose now applies upstream's real 0053.
	if _, err := db.Exec(`INSERT INTO goose_db_version (version_id, is_applied) VALUES (53, 1)`); err != nil {
		t.Fatal(err)
	}

	for i := 0; i < 3; i++ {
		if err := repairForkMigrationVersions(db); err != nil {
			t.Fatalf("repair pass %d: %v", i+2, err)
		}
	}

	got := ledger(t, db)
	if !got[53] {
		t.Fatal("a later pass deleted upstream's version 53; Muse would be re-run on every boot")
	}
	for v := int64(9000); v <= 9007; v++ {
		if got[v] != first[v] {
			t.Errorf("version %d changed on a repeat pass", v)
		}
	}
}

// A rolled-back migration leaves is_applied = 0 behind a newer row. Reading any
// row but the latest would treat a deliberately un-applied migration as
// applied and skip it forever.
func TestForkRepairRespectsARolledBackVersion(t *testing.T) {
	db := openRaw(t)
	seedGooseLedger(t, db, 52, 53)
	seedForkSchema(t, db, 1)
	if _, err := db.Exec(`INSERT INTO goose_db_version (version_id, is_applied) VALUES (53, 0)`); err != nil {
		t.Fatal(err)
	}

	if err := repairForkMigrationVersions(db); err != nil {
		t.Fatalf("repair: %v", err)
	}
	if _, ok := ledger(t, db)[9000]; ok {
		t.Fatal("a rolled-back migration was carried over as applied")
	}
}

// No ledger at all: a database this build is creating. goose owns it from here.
func TestForkRepairOnAFreshDatabase(t *testing.T) {
	db := openRaw(t)
	if err := repairForkMigrationVersions(db); err != nil {
		t.Fatalf("repair on a database with no goose ledger: %v", err)
	}
}
