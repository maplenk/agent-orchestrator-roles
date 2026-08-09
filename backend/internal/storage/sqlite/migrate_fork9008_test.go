package sqlite

import (
	"crypto/sha256"
	"database/sql"
	"fmt"
	"io/fs"
	"path/filepath"
	"reflect"
	"testing"
	"testing/fstest"
	"time"

	"github.com/pressly/goose/v3"
)

func TestMigration0085ContentIsImmutable(t *testing.T) {
	body, err := migrationsFS.ReadFile("migrations/0085_agent_switching.sql")
	if err != nil {
		t.Fatalf("read 0085: %v", err)
	}
	if got, want := fmt.Sprintf("%x", sha256.Sum256(body)), "b3871aaf81c982886f3385f276aed543abdce029a62057f1a6708a3b5c643bc3"; got != want {
		t.Fatalf("0085 SHA-256 = %s, want immutable upstream %s", got, want)
	}
}

func TestCopiedFork9008DatabaseAppliesAgentSwitchingAndIsIdempotent(t *testing.T) {
	db, err := sql.Open("sqlite", "file:"+filepath.Join(t.TempDir(), "ao.db")+pragmas)
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	applyHistoricalFork9008(t, db)
	var applied85 int
	if err := db.QueryRow(`SELECT COUNT(*) FROM goose_db_version WHERE version_id = 85 AND is_applied = 1`).Scan(&applied85); err != nil {
		t.Fatalf("read pre-sync 0085 ledger: %v", err)
	}
	if applied85 != 0 {
		t.Fatalf("historical fork unexpectedly contains 0085: rows=%d", applied85)
	}

	now := time.Date(2026, time.August, 10, 9, 0, 0, 0, time.UTC)
	if _, err := db.Exec(`
INSERT INTO projects (id, path, registered_at, config)
VALUES ('fork-copy', '/repos/fork-copy', ?, '{}');
INSERT INTO sessions (
    id, project_id, num, kind, harness,
    role_id, spawn_capability_hash, switch_pending_json, pause_json,
    activity_last_at, created_at, updated_at
) VALUES (
    'fork-copy-1', 'fork-copy', 1, 'worker', 'claude-code',
    'implementor', 'spawn-capability', '', '',
    ?, ?, ?
);`, now, now, now, now); err != nil {
		t.Fatalf("seed copied fork data: %v", err)
	}

	if err := migrate(db); err != nil {
		t.Fatalf("migrate copied fork-9008 database: %v", err)
	}
	for _, version := range []int64{85, 9000, 9001, 9002, 9003, 9004, 9005, 9006, 9007, 9008, 9009, 9010} {
		var applied int
		if err := db.QueryRow(`
SELECT COALESCE((
    SELECT is_applied FROM goose_db_version
    WHERE version_id = ? ORDER BY id DESC LIMIT 1
), 0)`, version).Scan(&applied); err != nil {
			t.Fatalf("read migration %d: %v", version, err)
		}
		if applied != 1 {
			t.Errorf("migration %d applied = %d, want 1", version, applied)
		}
	}

	var roleID, capability string
	if err := db.QueryRow(`
SELECT role_id, spawn_capability_hash
FROM sessions WHERE id = 'fork-copy-1'`).Scan(&roleID, &capability); err != nil {
		t.Fatalf("read preserved fork session: %v", err)
	}
	if roleID != "implementor" || capability != "spawn-capability" {
		t.Fatalf("fork session changed: role=%q capability=%q", roleID, capability)
	}
	for _, table := range []string{"agent_native_sessions", "agent_switches"} {
		var count int
		if err := db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = ?`, table).Scan(&count); err != nil {
			t.Fatalf("inspect %s: %v", table, err)
		}
		if count != 1 {
			t.Errorf("table %s count = %d, want 1", table, count)
		}
	}

	firstBoot := exactLedgerRows(t, db)
	if err := migrate(db); err != nil {
		t.Fatalf("second copied-fork boot: %v", err)
	}
	secondBoot := exactLedgerRows(t, db)
	if !reflect.DeepEqual(secondBoot, firstBoot) {
		t.Fatalf("second boot changed migration ledger:\nfirst: %#v\nsecond: %#v", firstBoot, secondBoot)
	}
}

func applyHistoricalFork9008(t *testing.T, db *sql.DB) {
	t.Helper()
	omit := map[int64]bool{
		48: true, 49: true, 54: true,
		80: true, 81: true, 82: true, 83: true, 84: true, 85: true,
		9009: true,
		9010: true,
	}
	paths, err := fs.Glob(migrationsFS, "migrations/*.sql")
	if err != nil {
		t.Fatalf("list migrations: %v", err)
	}
	historical := fstest.MapFS{}
	for _, path := range paths {
		version, err := goose.NumericComponent(filepath.Base(path))
		if err != nil {
			t.Fatalf("parse migration %s: %v", path, err)
		}
		if omit[version] {
			continue
		}
		body, err := migrationsFS.ReadFile(path)
		if err != nil {
			t.Fatalf("read migration %s: %v", path, err)
		}
		historical[path] = &fstest.MapFile{Data: body}
	}

	gooseMu.Lock()
	defer gooseMu.Unlock()
	goose.SetBaseFS(historical)
	goose.SetLogger(goose.NopLogger())
	if err := goose.SetDialect("sqlite3"); err != nil {
		t.Fatalf("set goose dialect: %v", err)
	}
	if err := goose.Up(db, "migrations"); err != nil {
		t.Fatalf("apply historical fork migrations: %v", err)
	}
}
