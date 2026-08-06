package sqlite

import (
	"database/sql"
	"path/filepath"
	"strings"
	"testing"

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
// The second one is the safety net: re-running `ALTER TABLE ... ADD COLUMN` on
// a column that already exists fails loudly. This test pins that it DOES fail,
// because it is the only thing standing between an old database and a daemon
// that boots onto a schema nobody verified. If a future migration edit made the
// roles set idempotent — an `IF NOT EXISTS` here, a guard there — the loud
// failure would disappear and the silent skip would be all that was left.
func TestOldRolesDatabaseCannotMigrateSilently(t *testing.T) {
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
	for newV, oldV := range map[int64]int64{53: 42, 54: 43, 55: 44, 56: 45, 57: 46, 58: 47, 59: 48, 60: 49} {
		if _, err := db.Exec(`UPDATE goose_db_version SET version_id = ? WHERE version_id = ?`, oldV, newV); err != nil {
			t.Fatalf("rewrite version %d -> %d: %v", newV, oldV, err)
		}
	}

	// Now migrate as a daemon would on that machine.
	err = migrate(db)
	if err == nil {
		t.Fatal("an old roles database migrated cleanly. That means the roles migrations became " +
			"idempotent, so the loud failure is gone — and the SILENT half remains: goose still " +
			"believes upstream's 0042/0043/0044/0047 are applied and will never run them, so the " +
			"daemon boots onto a schema missing sessions.pinned and agent_model_catalog while the " +
			"code expects both. If this behaviour is intentional it needs a real repair step " +
			"(see UPSTREAM_SYNC_PLAN.md and upstream #3598), not silence.")
	}
	// The failure must be about the schema already being there, not some
	// unrelated breakage that would mask a real regression later.
	if !strings.Contains(strings.ToLower(err.Error()), "duplicate column") {
		t.Fatalf("migrate failed, but not with the expected duplicate-column error: %v", err)
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

// The renumbered set must start above upstream's highest, or the collision this
// whole exercise removes comes straight back on the next sync.
func TestRolesMigrationsStartAboveUpstreamRange(t *testing.T) {
	entries, err := migrationsFS.ReadDir("migrations")
	if err != nil {
		t.Fatalf("read migrations: %v", err)
	}
	// Upstream's highest at the pinned merge target (4efd8a10) is 0052.
	const upstreamHighest = 52
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
		// The fork's own migrations are the ones above the upstream range.
		if v > upstreamHighest && v < rolesLowest {
			rolesLowest = v
		}
	}
	if rolesLowest != 53 {
		t.Fatalf("lowest fork migration = %d, want 53 (immediately above upstream's %d)", rolesLowest, upstreamHighest)
	}
	if maxSeen != 60 {
		t.Fatalf("highest migration = %d, want 60; update this test and UPSTREAM_SYNC_PLAN.md together", maxSeen)
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
