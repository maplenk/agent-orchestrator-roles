package sqlite

import (
	"database/sql"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/pressly/goose/v3"
)

// The fork's migrations were renumbered 0042–0049 → 0053–0060 because upstream
// had taken 0042/0043/0044/0047 and reached 0052. Renumbering is necessary and
// NOT sufficient, and the insufficiency is silent.
//
// goose keys on version NUMBER. A database that ran the roles migrations under
// their old numbers therefore records 42..49 as applied, and after the rename
// goose will:
//
//   - SKIP upstream's 0042/0043/0044/0047, believing them already applied,
//     leaving the schema without sessions.pinned, agent_model_catalog,
//     review-run uniqueness and the batch backfill while the code expects all
//     four — divergence with no error at all; and
//   - RE-RUN the roles migrations at their new numbers.
//
// repairForkMigrationVersions now resolves both halves before goose runs: it
// records the physically present role migrations at 9000+, while retaining the
// upstream-reclaimed 42-49 ledger identities. reconcileSchema independently
// repairs any upstream physical effects skipped by the historical collision.
func TestOldRolesDatabaseRepairsBothMigrationIdentities(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ao.db")
	db, err := sql.Open("sqlite", "file:"+path+"?_pragma=busy_timeout(5000)")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	// A database that has run every migration this branch ships.
	if err := migrate(db); err != nil {
		t.Fatalf("baseline migrate: %v", err)
	}

	// Rewrite history to what an OLD roles database actually looks like: the
	// same schema, recorded under the pre-rename version numbers. This is the
	// exact state on any machine that ran this branch before the sync.
	for newV, oldV := range map[int64]int64{9000: 42, 9001: 43, 9002: 44, 9003: 45, 9004: 46, 9005: 47, 9006: 48, 9007: 49} {
		if _, err := db.Exec(`UPDATE goose_db_version SET version_id = ? WHERE version_id = ?`, oldV, newV); err != nil {
			t.Fatalf("rewrite version %d -> %d: %v", newV, oldV, err)
		}
	}
	originalMuse := exactVersionRows(t, db, 53)
	if len(originalMuse) != 1 {
		t.Fatalf("pre-repair Muse rows = %#v, want exactly one", originalMuse)
	}

	// Now migrate as a daemon would on that machine. It must repair rather than
	// relying on a duplicate-column failure as a safety net.
	if err := migrate(db); err != nil {
		t.Fatalf("migrate original roles history: %v", err)
	}

	got := ledger(t, db)
	for v := int64(42); v <= 49; v++ {
		if !got[v] {
			t.Errorf("upstream-reclaimed version %d was removed", v)
		}
	}
	for v := int64(9000); v <= 9008; v++ {
		if !got[v] {
			t.Errorf("role migration %d was not repaired", v)
		}
	}
	if repairedMuse := exactVersionRows(t, db, 53); !reflect.DeepEqual(repairedMuse, originalMuse) {
		t.Fatalf("migration replaced genuine Muse ledger row:\noriginal: %#v\nrepaired: %#v", originalMuse, repairedMuse)
	}

	var pinnedColumns, catalogTables int
	if err := db.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('sessions') WHERE name = 'is_pinned'`).Scan(&pinnedColumns); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = 'agent_model_catalog'`).Scan(&catalogTables); err != nil {
		t.Fatal(err)
	}
	if pinnedColumns != 1 || catalogTables != 1 {
		t.Fatalf("upstream schema after repair: sessions.is_pinned=%d agent_model_catalog=%d, want 1 each", pinnedColumns, catalogTables)
	}
	if _, err := db.Exec(`
INSERT INTO projects (id, path, registered_at, config)
VALUES ('muse-repair', '/repo/muse-repair', ?, '{}');
INSERT INTO sessions (id, project_id, num, harness, activity_last_at, created_at, updated_at)
VALUES ('muse-repair-1', 'muse-repair', 1, 'muse', ?, ?, ?);
`, time.Unix(100, 0).UTC(), time.Unix(101, 0).UTC(), time.Unix(101, 0).UTC(), time.Unix(101, 0).UTC()); err != nil {
		t.Fatalf("insert Muse session after mixed-history repair: %v", err)
	}
	var integrity string
	if err := db.QueryRow(`PRAGMA integrity_check`).Scan(&integrity); err != nil {
		t.Fatalf("integrity_check: %v", err)
	}
	if integrity != "ok" {
		t.Fatalf("integrity_check = %q, want ok", integrity)
	}
}

// Control: a database whose recorded versions match the shipped filenames
// migrates cleanly and is a no-op the second time. Without this, the test above
// would pass just as well if migrate() were broken for every database.
func TestCurrentDatabaseMigratesAndIsIdempotent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ao.db")
	db, err := sql.Open("sqlite", "file:"+path+"?_pragma=busy_timeout(5000)")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	if err := migrate(db); err != nil {
		t.Fatalf("first migrate: %v", err)
	}
	if err := migrate(db); err != nil {
		t.Fatalf("second migrate must be a no-op: %v", err)
	}
}

// The fork's set must sit in its own range, well clear of upstream's counter.
//
// This test previously demanded the fork start at 53, "immediately above
// upstream's 52" — which is exactly what went wrong. Upstream kept counting,
// shipped its own 0053 (Muse), and every fork database silently skipped it.
// Adjacency is not separation; a fork range is.
func TestRolesMigrationsStartInTheForkRange(t *testing.T) {
	entries, err := migrationsFS.ReadDir("migrations")
	if err != nil {
		t.Fatalf("read migrations: %v", err)
	}
	// Upstream's highest at the immutable Sync 3 pin (6e9dbb1) is 0085.
	const upstreamHighest = 85
	// The fork's own range. Deliberately distant: upstream would have to add
	// ~8900 migrations to reach it.
	const forkRangeStart = 9000
	var rolesLowest int64 = 1 << 30
	var maxSeen int64
	for _, e := range entries {
		name := e.Name()
		var v int64
		if _, err := fmtSscan(name, &v); err != nil {
			continue
		}
		if v > maxSeen {
			maxSeen = v
		}
		// The fork's own migrations are the ones in the fork range.
		if v >= forkRangeStart && v < rolesLowest {
			rolesLowest = v
		}
		// Nothing of ours may sit between upstream's highest and the fork
		// range: that gap is upstream's to fill, and anything parked there is
		// the next collision.
		if v > upstreamHighest && v < forkRangeStart {
			t.Errorf("migration %d sits in upstream's growth path (>%d, <%d)", v, upstreamHighest, forkRangeStart)
		}
	}
	if rolesLowest != forkRangeStart {
		t.Fatalf("lowest fork migration = %d, want %d", rolesLowest, forkRangeStart)
	}
	if maxSeen != 9011 {
		t.Fatalf("highest migration = %d, want 9011; update this test and UPSTREAM_SYNC3_PLAN.md together", maxSeen)
	}
}

// fmtSscan parses the leading zero-padded version from a migration filename.
func fmtSscan(name string, out *int64) (int, error) {
	i := strings.IndexByte(name, '_')
	if i <= 0 {
		return 0, errNotAMigration
	}
	var v int64
	for _, c := range name[:i] {
		if c < '0' || c > '9' {
			return 0, errNotAMigration
		}
		v = v*10 + int64(c-'0')
	}
	*out = v
	return 1, nil
}

var errNotAMigration = goose.ErrNoNextVersion
