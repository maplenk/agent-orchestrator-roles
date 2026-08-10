package sqlite

import (
	"database/sql"
	"fmt"
	"io/fs"
	"path"
	"strconv"
	"strings"

	"github.com/pressly/goose/v3"
)

// upstreamMigrationPairing records that one immutable fork migration and one
// immutable upstream migration have the same physical effect. It is separate
// from forkMigration: historical renumber repair and future upstream
// equivalence have different provenance and must not share optional version
// fields or sentinel values.
type upstreamMigrationPairing struct {
	forkVersion     int64
	forkName        string
	upstreamVersion int64
	upstreamName    string
	// sharedSince identifies the upstream merge where the fingerprint stopped
	// being fork-exclusive. It may be empty while the expected upstream file is
	// absent, but the pairing refuses to activate without it.
	sharedSince string
	applied     func(*sql.Tx) (bool, error)
}

// upstreamMigrationPairings is intentionally empty until an approved schema
// extraction has an allocated upstream migration filename. Pairing code lands
// before the upstream PR, but an entry remains inert until that exact filename
// is embedded in the running binary.
func upstreamMigrationPairings() []upstreamMigrationPairing {
	return nil
}

func repairUpstreamMigrationPairings(db *sql.DB) error {
	return repairUpstreamMigrationPairingsWith(db, migrationsFS, upstreamMigrationPairings())
}

func repairUpstreamMigrationPairingsWith(
	db *sql.DB,
	migrationFS fs.FS,
	pairings []upstreamMigrationPairing,
) error {
	active, err := activeUpstreamMigrationPairings(migrationFS, pairings)
	if err != nil {
		return err
	}
	if len(active) == 0 {
		return nil
	}

	// A fresh combined fork has no goose table yet. Create only the ledger;
	// reconciliation below records the fork identity so goose can run the
	// canonical upstream migration exactly once.
	if _, err := goose.EnsureDBVersion(db); err != nil {
		return fmt.Errorf("upstream migration pairing: ensure goose ledger: %w", err)
	}

	tx, err := db.Begin()
	if err != nil {
		return fmt.Errorf("upstream migration pairing: begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	for _, pairing := range active {
		forkRecorded, err := versionRecorded(tx, pairing.forkVersion)
		if err != nil {
			return fmt.Errorf("upstream migration pairing: read fork identity %d: %w", pairing.forkVersion, err)
		}
		upstreamRecorded, err := versionRecorded(tx, pairing.upstreamVersion)
		if err != nil {
			return fmt.Errorf("upstream migration pairing: read upstream identity %d: %w", pairing.upstreamVersion, err)
		}
		effectRecorded, err := pairing.applied(tx)
		if err != nil {
			return fmt.Errorf("upstream migration pairing: inspect %s effect: %w", pairing.forkName, err)
		}

		switch {
		case !forkRecorded && !upstreamRecorded && !effectRecorded:
			// Fresh combined install: reserve only the fork-owned identity.
			// The canonical, exact-name upstream migration remains unapplied
			// and will be executed by goose.
			if err := recordAppliedMigration(tx, pairing.forkVersion); err != nil {
				return fmt.Errorf("upstream migration pairing: reserve fork identity %d: %w", pairing.forkVersion, err)
			}
		case forkRecorded && !upstreamRecorded && effectRecorded:
			if err := recordAppliedMigration(tx, pairing.upstreamVersion); err != nil {
				return fmt.Errorf("upstream migration pairing: record upstream identity %d: %w", pairing.upstreamVersion, err)
			}
		case !forkRecorded && upstreamRecorded && effectRecorded:
			if err := recordAppliedMigration(tx, pairing.forkVersion); err != nil {
				return fmt.Errorf("upstream migration pairing: record fork identity %d: %w", pairing.forkVersion, err)
			}
		case forkRecorded && upstreamRecorded && effectRecorded:
			// Already paired.
		case !forkRecorded && !upstreamRecorded && effectRecorded:
			return fmt.Errorf(
				"upstream migration pairing: %s effect exists without fork or upstream identity",
				pairing.forkName,
			)
		default:
			return fmt.Errorf(
				"upstream migration pairing: identity recorded without %s effect (fork=%t upstream=%t)",
				pairing.forkName,
				forkRecorded,
				upstreamRecorded,
			)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("upstream migration pairing: commit: %w", err)
	}
	return nil
}

// activeUpstreamMigrationPairings returns only pairings whose exact canonical
// upstream filename is embedded at the expected version. Historical repair
// consults the same activation result so a recorded canonical upstream
// identity wins over now-ambiguous legacy version numbers.
func activeUpstreamMigrationPairings(
	migrationFS fs.FS,
	pairings []upstreamMigrationPairing,
) ([]upstreamMigrationPairing, error) {
	filesByVersion, err := migrationFilesByVersion(migrationFS)
	if err != nil {
		return nil, fmt.Errorf("upstream migration pairing: inspect embedded migrations: %w", err)
	}

	active := make([]upstreamMigrationPairing, 0, len(pairings))
	for _, pairing := range pairings {
		if err := validateUpstreamMigrationPairing(pairing); err != nil {
			return nil, fmt.Errorf("upstream migration pairing: %w", err)
		}

		upstreamFiles := filesByVersion[pairing.upstreamVersion]
		if !containsMigrationFilename(upstreamFiles, pairing.upstreamName) {
			// Missing, rejected and renumbered upstream migrations all stay
			// inert. A different migration at this version must run normally.
			continue
		}
		if len(upstreamFiles) != 1 {
			return nil, fmt.Errorf(
				"upstream migration pairing: version %d contains multiple files %v",
				pairing.upstreamVersion,
				upstreamFiles,
			)
		}
		if pairing.sharedSince == "" {
			return nil, fmt.Errorf(
				"upstream migration pairing: %s is active without shared-since provenance",
				pairing.upstreamName,
			)
		}

		forkFiles := filesByVersion[pairing.forkVersion]
		if len(forkFiles) != 1 || forkFiles[0] != pairing.forkName {
			return nil, fmt.Errorf(
				"upstream migration pairing: active %s expects fork migration %s at version %d, found %v",
				pairing.upstreamName,
				pairing.forkName,
				pairing.forkVersion,
				forkFiles,
			)
		}
		active = append(active, pairing)
	}
	return active, nil
}

func validateUpstreamMigrationPairing(pairing upstreamMigrationPairing) error {
	if pairing.forkVersion <= 0 || pairing.upstreamVersion <= 0 {
		return fmt.Errorf("pairing versions must be positive: fork=%d upstream=%d", pairing.forkVersion, pairing.upstreamVersion)
	}
	if pairing.forkVersion == pairing.upstreamVersion {
		return fmt.Errorf("pairing versions must differ: %d", pairing.forkVersion)
	}
	if pairing.applied == nil {
		return fmt.Errorf("pairing %d/%d has no physical fingerprint", pairing.forkVersion, pairing.upstreamVersion)
	}
	if err := validateMigrationFilename(pairing.forkVersion, pairing.forkName); err != nil {
		return fmt.Errorf("fork migration: %w", err)
	}
	if err := validateMigrationFilename(pairing.upstreamVersion, pairing.upstreamName); err != nil {
		return fmt.Errorf("upstream migration: %w", err)
	}
	return nil
}

func validateMigrationFilename(version int64, name string) error {
	if name == "" || path.Base(name) != name || !strings.HasSuffix(name, ".sql") {
		return fmt.Errorf("invalid filename %q", name)
	}
	prefix, _, ok := strings.Cut(name, "_")
	if !ok {
		return fmt.Errorf("filename %q has no version prefix", name)
	}
	parsed, err := strconv.ParseInt(prefix, 10, 64)
	if err != nil || parsed != version {
		return fmt.Errorf("filename %q does not encode version %d", name, version)
	}
	return nil
}

func migrationFilesByVersion(migrationFS fs.FS) (map[int64][]string, error) {
	entries, err := fs.ReadDir(migrationFS, "migrations")
	if err != nil {
		return nil, err
	}
	files := make(map[int64][]string)
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".sql") {
			continue
		}
		prefix, _, ok := strings.Cut(entry.Name(), "_")
		if !ok {
			continue
		}
		version, err := strconv.ParseInt(prefix, 10, 64)
		if err != nil {
			continue
		}
		files[version] = append(files[version], entry.Name())
	}
	return files, nil
}

func containsMigrationFilename(files []string, want string) bool {
	for _, file := range files {
		if file == want {
			return true
		}
	}
	return false
}

func recordAppliedMigration(tx *sql.Tx, version int64) error {
	_, err := tx.Exec(
		`INSERT INTO goose_db_version (version_id, is_applied) VALUES (?, 1)`,
		version,
	)
	return err
}
