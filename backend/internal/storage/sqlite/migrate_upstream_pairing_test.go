package sqlite

import (
	"database/sql"
	"fmt"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/pressly/goose/v3"
)

const (
	testForkMigrationName     = "9008_session_failover_attempts.sql"
	testUpstreamMigrationName = "0086_session_failover_attempts.sql"
)

func TestUpstreamMigrationPairingStaysInertUntilExactFileExists(t *testing.T) {
	tests := []struct {
		name string
		fs   fstest.MapFS
	}{
		{
			name: "upstream file missing",
			fs: fstest.MapFS{
				"migrations/" + testForkMigrationName: migrationFile(),
			},
		},
		{
			name: "expected version occupied by another migration",
			fs: fstest.MapFS{
				"migrations/" + testForkMigrationName:           migrationFile(),
				"migrations/0086_unrelated_upstream_change.sql": migrationFile(),
			},
		},
		{
			name: "upstream PR renumbered",
			fs: fstest.MapFS{
				"migrations/" + testForkMigrationName:           migrationFile(),
				"migrations/0087_session_failover_attempts.sql": migrationFile(),
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db := openRaw(t)
			if err := runPairingRepair(db, tt.fs, testPairing()); err != nil {
				t.Fatalf("repair inactive pairing: %v", err)
			}
			if ok, err := hasGooseTable(db); err != nil {
				t.Fatal(err)
			} else if ok {
				t.Fatal("inactive pairing created a goose ledger")
			}
		})
	}
}

func TestUpstreamMigrationPairingFreshCombinedDatabaseRunsUpstreamIdentity(t *testing.T) {
	db := openRaw(t)
	pairing := testPairing()

	if err := runPairingRepair(db, activePairingFS(), pairing); err != nil {
		t.Fatalf("repair fresh pairing: %v", err)
	}
	assertVersionRecorded(t, db, pairing.forkVersion, true)
	assertVersionRecorded(t, db, pairing.upstreamVersion, false)
}

func TestUpstreamMigrationPairingFreshCombinedDatabaseAppliesOnlyCanonicalUpstreamSQL(t *testing.T) {
	db := openRaw(t)
	pairing := testPairing()
	pairedFS := fstest.MapFS{
		"migrations/" + testUpstreamMigrationName: createPairingEffectMigration(),
		"migrations/" + testForkMigrationName:     createPairingEffectMigration(),
	}

	gooseMu.Lock()
	defer gooseMu.Unlock()
	defer goose.SetBaseFS(migrationsFS)
	if err := goose.SetDialect("sqlite3"); err != nil {
		t.Fatal(err)
	}
	if err := repairUpstreamMigrationPairingsWith(db, pairedFS, []upstreamMigrationPairing{pairing}); err != nil {
		t.Fatalf("repair fresh pairing: %v", err)
	}
	goose.SetBaseFS(pairedFS)
	if err := goose.Up(db, "migrations", goose.WithAllowMissing()); err != nil {
		t.Fatalf("apply paired migrations: %v", err)
	}

	assertVersionRecorded(t, db, pairing.forkVersion, true)
	assertVersionRecorded(t, db, pairing.upstreamVersion, true)
	var tables int
	if err := db.QueryRow(
		`SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = 'session_failover_attempts'`,
	).Scan(&tables); err != nil {
		t.Fatal(err)
	}
	if tables != 1 {
		t.Fatalf("session_failover_attempts tables = %d, want 1", tables)
	}
}

func TestUpstreamMigrationPairingExistingForkRecordsUpstreamIdentity(t *testing.T) {
	db := openRaw(t)
	seedGooseLedger(t, db, 9008)
	createPairingEffect(t, db)

	if err := runPairingRepair(db, activePairingFS(), testPairing()); err != nil {
		t.Fatalf("repair existing fork: %v", err)
	}
	assertVersionRecorded(t, db, 9008, true)
	assertVersionRecorded(t, db, 86, true)
}

func TestUpstreamMigrationPairingExistingUpstreamRecordsPairingOnlyForkIdentity(t *testing.T) {
	db := openRaw(t)
	seedGooseLedger(t, db, 86)
	createPairingEffect(t, db)

	if err := runPairingRepair(db, activePairingFS(), testPairing()); err != nil {
		t.Fatalf("repair existing upstream: %v", err)
	}
	assertVersionRecorded(t, db, 86, true)
	assertVersionRecorded(t, db, 9008, true)
}

func TestUpstreamMigrationPairingIsIdempotent(t *testing.T) {
	db := openRaw(t)
	seedGooseLedger(t, db, 86, 9008)
	createPairingEffect(t, db)

	for i := 0; i < 2; i++ {
		if err := runPairingRepair(db, activePairingFS(), testPairing()); err != nil {
			t.Fatalf("repair pass %d: %v", i+1, err)
		}
	}
	assertAppliedRowCount(t, db, 86, 1)
	assertAppliedRowCount(t, db, 9008, 1)
}

func TestUpstreamMigrationPairingRejectsEffectWithoutIdentity(t *testing.T) {
	db := openRaw(t)
	createPairingEffect(t, db)

	err := runPairingRepair(db, activePairingFS(), testPairing())
	if err == nil || !containsAll(err.Error(), "effect exists", "without fork or upstream identity") {
		t.Fatalf("error = %v, want effect-without-identity failure", err)
	}
}

func TestUpstreamMigrationPairingRejectsIdentityWithoutEffect(t *testing.T) {
	for _, version := range []int64{86, 9008} {
		t.Run(versionName(version), func(t *testing.T) {
			db := openRaw(t)
			seedGooseLedger(t, db, version)

			err := runPairingRepair(db, activePairingFS(), testPairing())
			if err == nil || !containsAll(err.Error(), "identity recorded", "without", "effect") {
				t.Fatalf("error = %v, want identity-without-effect failure", err)
			}
		})
	}
}

func TestUpstreamMigrationPairingRequiresSharedSinceWhenActive(t *testing.T) {
	db := openRaw(t)
	pairing := testPairing()
	pairing.sharedSince = ""

	err := runPairingRepair(db, activePairingFS(), pairing)
	if err == nil || !containsAll(err.Error(), "active", "without shared-since provenance") {
		t.Fatalf("error = %v, want missing provenance failure", err)
	}
}

func TestUpstreamMigrationPairingRejectsNonEquivalentFingerprint(t *testing.T) {
	db := openRaw(t)
	seedGooseLedger(t, db, 9008)
	if _, err := db.Exec(`CREATE TABLE different_effect (id INTEGER PRIMARY KEY)`); err != nil {
		t.Fatal(err)
	}

	err := runPairingRepair(db, activePairingFS(), testPairing())
	if err == nil || !containsAll(err.Error(), "identity recorded", "without", "effect") {
		t.Fatalf("error = %v, want non-equivalent fingerprint failure", err)
	}
}

func TestUpstreamMigrationPairingRowsAreStructurallyIndependentFromRenumberRepairs(t *testing.T) {
	filesByVersion, err := migrationFilesByVersion(migrationsFS)
	if err != nil {
		t.Fatal(err)
	}
	seenFork := map[int64]struct{}{}
	seenUpstream := map[int64]struct{}{}
	for _, pairing := range upstreamMigrationPairings() {
		if err := validateUpstreamMigrationPairing(pairing); err != nil {
			t.Errorf("invalid pairing %+v: %v", pairing, err)
			continue
		}
		if _, duplicate := seenFork[pairing.forkVersion]; duplicate {
			t.Errorf("duplicate fork pairing version %d", pairing.forkVersion)
		}
		seenFork[pairing.forkVersion] = struct{}{}
		if _, duplicate := seenUpstream[pairing.upstreamVersion]; duplicate {
			t.Errorf("duplicate upstream pairing version %d", pairing.upstreamVersion)
		}
		seenUpstream[pairing.upstreamVersion] = struct{}{}

		if got := filesByVersion[pairing.forkVersion]; len(got) != 1 || got[0] != pairing.forkName {
			t.Errorf("fork pairing %d expects %q, embedded files are %v", pairing.forkVersion, pairing.forkName, got)
		}
		if shipped, ok := shippedMigrations[pairing.forkVersion]; !ok || shipped != pairing.forkName {
			t.Errorf("fork pairing %d/%q is absent from shipped migration ledger", pairing.forkVersion, pairing.forkName)
		}

		upstreamFiles := filesByVersion[pairing.upstreamVersion]
		if containsMigrationFilename(upstreamFiles, pairing.upstreamName) {
			if pairing.sharedSince == "" {
				t.Errorf("active upstream pairing %d/%q has no shared-since provenance", pairing.upstreamVersion, pairing.upstreamName)
			}
			if shipped, ok := shippedMigrations[pairing.upstreamVersion]; !ok || shipped != pairing.upstreamName {
				t.Errorf("active upstream pairing %d/%q is absent from shipped migration ledger", pairing.upstreamVersion, pairing.upstreamName)
			}
		}
	}
}

func TestHistoricalForkRepairUsesCanonicalPairingIdentityForSharedEffects(t *testing.T) {
	pairing := upstreamMigrationPairing{
		forkVersion:     9000,
		forkName:        "9000_session_role_fields.sql",
		upstreamVersion: 86,
		upstreamName:    "0086_session_role_fields.sql",
		sharedSince:     "upstream merge fixture",
		applied:         hasSessionsColumnTx("role_id"),
	}
	pairingFS := fstest.MapFS{
		"migrations/" + pairing.forkName:     migrationFile(),
		"migrations/" + pairing.upstreamName: migrationFile(),
	}
	active, err := activeUpstreamMigrationPairings(pairingFS, []upstreamMigrationPairing{pairing})
	if err != nil {
		t.Fatal(err)
	}

	t.Run("recorded canonical upstream identity owns shared effect provenance", func(t *testing.T) {
		db := openRaw(t)
		seedGooseLedger(t, db, 42, pairing.upstreamVersion)
		createSessionRoleEffect(t, db)

		if err := repairForkMigrationVersionsWithPairings(db, active); err != nil {
			t.Fatalf("historical repair: %v", err)
		}
		assertVersionRecorded(t, db, pairing.forkVersion, false)

		if err := runPairingRepair(db, pairingFS, pairing); err != nil {
			t.Fatalf("pairing repair: %v", err)
		}
		assertVersionRecorded(t, db, pairing.forkVersion, true)
		assertVersionRecorded(t, db, pairing.upstreamVersion, true)
	})

	t.Run("legacy fork identity is normalized before pairing", func(t *testing.T) {
		db := openRaw(t)
		seedGooseLedger(t, db, 42)
		createSessionRoleEffect(t, db)

		if err := repairForkMigrationVersionsWithPairings(db, active); err != nil {
			t.Fatalf("historical repair: %v", err)
		}
		assertVersionRecorded(t, db, pairing.forkVersion, true)
		assertVersionRecorded(t, db, pairing.upstreamVersion, false)

		if err := runPairingRepair(db, pairingFS, pairing); err != nil {
			t.Fatalf("pairing repair: %v", err)
		}
		assertVersionRecorded(t, db, pairing.forkVersion, true)
		assertVersionRecorded(t, db, pairing.upstreamVersion, true)
	})
}

func testPairing() upstreamMigrationPairing {
	return upstreamMigrationPairing{
		forkVersion:     9008,
		forkName:        testForkMigrationName,
		upstreamVersion: 86,
		upstreamName:    testUpstreamMigrationName,
		sharedSince:     "upstream merge fixture",
		applied:         hasTableTx("session_failover_attempts"),
	}
}

func runPairingRepair(db *sql.DB, migrationFS fstest.MapFS, pairings ...upstreamMigrationPairing) error {
	gooseMu.Lock()
	defer gooseMu.Unlock()
	if err := goose.SetDialect("sqlite3"); err != nil {
		return err
	}
	return repairUpstreamMigrationPairingsWith(db, migrationFS, pairings)
}

func activePairingFS() fstest.MapFS {
	return fstest.MapFS{
		"migrations/" + testForkMigrationName:     migrationFile(),
		"migrations/" + testUpstreamMigrationName: migrationFile(),
	}
}

func migrationFile() *fstest.MapFile {
	return &fstest.MapFile{Data: []byte("-- +goose Up\nSELECT 1;\n")}
}

func createPairingEffectMigration() *fstest.MapFile {
	return &fstest.MapFile{Data: []byte(`-- +goose Up
CREATE TABLE session_failover_attempts (id INTEGER PRIMARY KEY);
-- +goose Down
DROP TABLE session_failover_attempts;
`)}
}

func createPairingEffect(t *testing.T, db *sql.DB) {
	t.Helper()
	if _, err := db.Exec(`CREATE TABLE session_failover_attempts (id INTEGER PRIMARY KEY)`); err != nil {
		t.Fatal(err)
	}
}

func createSessionRoleEffect(t *testing.T, db *sql.DB) {
	t.Helper()
	if _, err := db.Exec(`CREATE TABLE sessions (id TEXT PRIMARY KEY, role_id TEXT)`); err != nil {
		t.Fatal(err)
	}
}

func assertVersionRecorded(t *testing.T, db *sql.DB, version int64, want bool) {
	t.Helper()
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback() }()
	got, err := versionRecorded(tx, version)
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("version %d recorded = %t, want %t", version, got, want)
	}
}

func assertAppliedRowCount(t *testing.T, db *sql.DB, version int64, want int) {
	t.Helper()
	var got int
	if err := db.QueryRow(
		`SELECT COUNT(*) FROM goose_db_version WHERE version_id = ? AND is_applied = 1`,
		version,
	).Scan(&got); err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("version %d applied rows = %d, want %d", version, got, want)
	}
}

func containsAll(got string, wants ...string) bool {
	for _, want := range wants {
		if !strings.Contains(got, want) {
			return false
		}
	}
	return true
}

func versionName(version int64) string {
	return fmt.Sprintf("version_%d", version)
}
