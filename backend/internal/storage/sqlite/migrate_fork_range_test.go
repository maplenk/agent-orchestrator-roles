package sqlite

import (
	"database/sql"
	"path/filepath"
	"reflect"
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
	seedForkSchemaWithMuse(t, db, n, false)
}

func seedForkSchemaWithMuse(t *testing.T, db *sql.DB, n int, muse bool) {
	t.Helper()
	sessionsSQL := `CREATE TABLE sessions (id TEXT PRIMARY KEY)`
	if muse {
		sessionsSQL = `CREATE TABLE sessions (
			id TEXT PRIMARY KEY,
			harness TEXT NOT NULL DEFAULT '' CHECK (harness IN ('', 'muse'))
		)`
	}
	if _, err := db.Exec(sessionsSQL); err != nil {
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

type gooseLedgerRow struct {
	ID        int64
	Version   int64
	Applied   bool
	Timestamp string
}

func exactLedgerRows(t *testing.T, db *sql.DB) []gooseLedgerRow {
	t.Helper()
	rows, err := db.Query(`
		SELECT id, version_id, is_applied, COALESCE(CAST(tstamp AS TEXT), '')
		FROM goose_db_version
		ORDER BY id`)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	var out []gooseLedgerRow
	for rows.Next() {
		var row gooseLedgerRow
		if err := rows.Scan(&row.ID, &row.Version, &row.Applied, &row.Timestamp); err != nil {
			t.Fatal(err)
		}
		out = append(out, row)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

func exactVersionRows(t *testing.T, db *sql.DB, version int64) []gooseLedgerRow {
	t.Helper()
	var out []gooseLedgerRow
	for _, row := range exactLedgerRows(t, db) {
		if row.Version == version {
			out = append(out, row)
		}
	}
	return out
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
	// The complete stale 53-60 block wins even when the table also contains
	// Muse text; otherwise the old fork identities would remain in upstream's
	// range and cause silent skips.
	seedForkSchemaWithMuse(t, db, 8, true)

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

// The fork originally shipped at 0042-0049 before moving to 0053-0060. Those
// first numbers now belong to upstream, so repair must preserve them while
// recording only the fork migrations whose physical effects are present.
func TestForkRepairOriginal42HistoryPreservesUpstreamNumbers(t *testing.T) {
	db := openRaw(t)
	seedGooseLedger(t, db, 42, 43, 44, 45, 47)
	seedForkSchema(t, db, 4)

	if err := repairForkMigrationVersions(db); err != nil {
		t.Fatalf("repair: %v", err)
	}

	got := ledger(t, db)
	for _, v := range []int64{42, 43, 44, 45, 47} {
		if !got[v] {
			t.Errorf("upstream-reclaimed version %d was removed", v)
		}
	}
	for _, v := range []int64{9000, 9001, 9002, 9003} {
		if !got[v] {
			t.Errorf("physically applied migration %d was not repaired", v)
		}
	}
	for _, v := range []int64{9004, 9005, 9006, 9007} {
		if got[v] {
			t.Errorf("migration %d was recorded without its physical effect", v)
		}
	}
}

// Once the original 42-49 range is recorded, it is the provenance for the
// fork's physical schema. A simultaneous 53-60 block can therefore be genuine
// upstream history and must remain byte-for-byte intact while 9000-9007 are
// added from the original rows.
func TestForkRepairOriginalHistoryPreservesCompleteUpstreamBlock(t *testing.T) {
	db := openRaw(t)
	seedGooseLedger(t, db,
		42, 43, 44, 45, 46, 47, 48, 49,
		52, 53, 54, 55, 56, 57, 58, 59, 60,
	)
	seedForkSchemaWithMuse(t, db, 8, true)
	upstreamRows := make(map[int64][]gooseLedgerRow, 8)
	for v := int64(53); v <= 60; v++ {
		upstreamRows[v] = exactVersionRows(t, db, v)
	}

	if err := repairForkMigrationVersions(db); err != nil {
		t.Fatalf("repair: %v", err)
	}

	got := ledger(t, db)
	for v := int64(42); v <= 49; v++ {
		if !got[v] {
			t.Errorf("upstream-reclaimed version %d was removed", v)
		}
	}
	for v := int64(53); v <= 60; v++ {
		if !got[v] {
			t.Errorf("genuine upstream version %d was removed", v)
		}
		if rows := exactVersionRows(t, db, v); !reflect.DeepEqual(rows, upstreamRows[v]) {
			t.Errorf("upstream version %d changed:\noriginal: %#v\nrepaired: %#v", v, upstreamRows[v], rows)
		}
	}
	for v := int64(9000); v <= 9007; v++ {
		if !got[v] {
			t.Errorf("version %d not recorded", v)
		}
		var count int
		if err := db.QueryRow(
			`SELECT COUNT(*) FROM goose_db_version WHERE version_id = ? AND is_applied = 1`,
			v,
		).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != 1 {
			t.Errorf("version %d recorded %d times, want exactly once", v, count)
		}
	}

	firstRepair := exactLedgerRows(t, db)
	if err := repairForkMigrationVersions(db); err != nil {
		t.Fatalf("second repair: %v", err)
	}
	if got := exactLedgerRows(t, db); !reflect.DeepEqual(got, firstRepair) {
		t.Fatalf("complete upstream-block repair changed on second pass:\nfirst: %#v\nsecond: %#v", firstRepair, got)
	}
}

// The complete original fork range can coexist with upstream's genuine Muse
// migration at 53. Classification must happen from the full on-entry block:
// inserting 9000 from 42 must not turn the later Muse row into stale fork 53.
func TestForkRepairOriginal42HistoryRetainsLoneMuseRow(t *testing.T) {
	db := openRaw(t)
	seedGooseLedger(t, db, 42, 43, 44, 45, 46, 47, 48, 49, 53)
	seedForkSchemaWithMuse(t, db, 8, true)
	originalMuse := exactVersionRows(t, db, 53)
	if len(originalMuse) != 1 {
		t.Fatalf("seeded Muse rows = %#v, want exactly one", originalMuse)
	}

	for pass := 1; pass <= 3; pass++ {
		if err := repairForkMigrationVersions(db); err != nil {
			t.Fatalf("repair pass %d: %v", pass, err)
		}
		if got := exactVersionRows(t, db, 53); !reflect.DeepEqual(got, originalMuse) {
			t.Fatalf("repair pass %d replaced genuine Muse row:\noriginal: %#v\ngot: %#v", pass, originalMuse, got)
		}
	}

	got := ledger(t, db)
	for v := int64(42); v <= 49; v++ {
		if !got[v] {
			t.Errorf("upstream-reclaimed version %d was removed", v)
		}
	}
	for v := int64(54); v <= 60; v++ {
		if got[v] {
			t.Errorf("unexpected legacy version %d was invented", v)
		}
	}
	for v := int64(9000); v <= 9007; v++ {
		if !got[v] {
			t.Errorf("role migration %d was not repaired", v)
		}
		if rows := exactVersionRows(t, db, v); len(rows) != 1 {
			t.Errorf("role migration %d rows = %#v, want exactly one", v, rows)
		}
	}
}

func TestForkRepairOriginalHistoryPreservesPartialUpstreamBlock(t *testing.T) {
	db := openRaw(t)
	seedGooseLedger(t, db, 42, 43, 44, 45, 46, 47, 48, 49, 53, 54)
	seedForkSchemaWithMuse(t, db, 8, true)
	upstream53 := exactVersionRows(t, db, 53)
	upstream54 := exactVersionRows(t, db, 54)

	if err := repairForkMigrationVersions(db); err != nil {
		t.Fatalf("repair: %v", err)
	}
	if got := exactVersionRows(t, db, 53); !reflect.DeepEqual(got, upstream53) {
		t.Fatalf("upstream 53 changed:\noriginal: %#v\nafter: %#v", upstream53, got)
	}
	if got := exactVersionRows(t, db, 54); !reflect.DeepEqual(got, upstream54) {
		t.Fatalf("upstream 54 changed:\noriginal: %#v\nafter: %#v", upstream54, got)
	}
	for v := int64(9000); v <= 9007; v++ {
		if rows := exactVersionRows(t, db, v); len(rows) != 1 {
			t.Errorf("fork version %d rows = %#v, want one", v, rows)
		}
	}
}

func TestForkRepairOriginalHistoryDoesNotRequireUpstreamPhysicalEffect(t *testing.T) {
	db := openRaw(t)
	seedGooseLedger(t, db, 42, 43, 44, 45, 46, 47, 48, 49, 53)
	seedForkSchema(t, db, 8)
	upstream53 := exactVersionRows(t, db, 53)

	if err := repairForkMigrationVersions(db); err != nil {
		t.Fatalf("repair: %v", err)
	}
	if got := exactVersionRows(t, db, 53); !reflect.DeepEqual(got, upstream53) {
		t.Fatalf("upstream 53 changed:\noriginal: %#v\nafter: %#v", upstream53, got)
	}
	for v := int64(9000); v <= 9007; v++ {
		if rows := exactVersionRows(t, db, v); len(rows) != 1 {
			t.Errorf("fork version %d rows = %#v, want one", v, rows)
		}
	}
}

func TestForkRepairOriginalHistoryRepairsOnlyPresentEffects(t *testing.T) {
	db := openRaw(t)
	seedGooseLedger(t, db, 42, 43, 44, 45, 46, 47, 48, 49, 53)
	// Seven physical effects are present, but pause_json is missing. The first
	// seven may be repaired while goose remains free to apply 9007.
	seedForkSchemaWithMuse(t, db, 7, true)
	upstream53 := exactVersionRows(t, db, 53)

	if err := repairForkMigrationVersions(db); err != nil {
		t.Fatalf("repair: %v", err)
	}
	if got := exactVersionRows(t, db, 53); !reflect.DeepEqual(got, upstream53) {
		t.Fatalf("upstream 53 changed:\noriginal: %#v\nafter: %#v", upstream53, got)
	}
	for v := int64(9000); v <= 9006; v++ {
		if rows := exactVersionRows(t, db, v); len(rows) != 1 {
			t.Errorf("fork version %d rows = %#v, want one", v, rows)
		}
	}
	if rows := exactVersionRows(t, db, 9007); len(rows) != 0 {
		t.Fatalf("fork version 9007 recorded without pause effect: %#v", rows)
	}
}

func TestForkRepairOriginalHistoryCompletesPartialNewBlock(t *testing.T) {
	db := openRaw(t)
	seedGooseLedger(t, db,
		42, 43, 44, 45, 46, 47, 48, 49,
		53, 54, 55, 56, 57, 58, 59, 60,
		9000,
	)
	seedForkSchemaWithMuse(t, db, 8, true)
	upstreamRows := make(map[int64][]gooseLedgerRow, 8)
	for v := int64(53); v <= 60; v++ {
		upstreamRows[v] = exactVersionRows(t, db, v)
	}

	if err := repairForkMigrationVersions(db); err != nil {
		t.Fatalf("repair: %v", err)
	}
	for v := int64(53); v <= 60; v++ {
		if got := exactVersionRows(t, db, v); !reflect.DeepEqual(got, upstreamRows[v]) {
			t.Errorf("upstream version %d changed:\noriginal: %#v\nafter: %#v", v, upstreamRows[v], got)
		}
	}
	for v := int64(9000); v <= 9007; v++ {
		if rows := exactVersionRows(t, db, v); len(rows) != 1 {
			t.Errorf("fork version %d rows = %#v, want one", v, rows)
		}
	}
}

func TestForkRepairRollsBackInsertWhenDeleteFails(t *testing.T) {
	db := openRaw(t)
	seedGooseLedger(t, db, 53, 54, 55, 56, 57, 58, 59, 60)
	seedForkSchema(t, db, 8)
	if _, err := db.Exec(`
		CREATE TRIGGER refuse_fork_53_delete
		BEFORE DELETE ON goose_db_version
		WHEN OLD.version_id = 53
		BEGIN
			SELECT RAISE(ABORT, 'forced fork-ledger delete failure');
		END`); err != nil {
		t.Fatalf("create delete trigger: %v", err)
	}
	before := exactLedgerRows(t, db)

	if err := repairForkMigrationVersions(db); err == nil {
		t.Fatal("repair succeeded despite forced DELETE failure")
	}
	if got := exactLedgerRows(t, db); !reflect.DeepEqual(got, before) {
		t.Fatalf("failed repair did not roll back prior insert:\nbefore: %#v\nafter: %#v", before, got)
	}
	if rows := exactVersionRows(t, db, 9000); len(rows) != 0 {
		t.Fatalf("failed repair leaked inserted 9000 row: %#v", rows)
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
