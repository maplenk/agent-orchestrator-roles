package sqlite

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
)

// errForkMigrationHistoryAmbiguous stops startup before goose can silently
// skip an upstream migration or replace a genuine upstream ledger row.
var errForkMigrationHistoryAmbiguous = errors.New("ambiguous fork migration history")

// This fork's migrations live at 9000+, deliberately far above upstream's
// counter. They used to sit at 0053–0060, in the sequence upstream was still
// filling, and upstream duly shipped its own 0053 (Muse harness) into the same
// slot. goose tracks applied migrations by NUMBER, so on any database that had
// run this fork's 0053 the upstream one was recorded as already applied and
// silently skipped — the widened harness CHECK never ran, and inserting a Muse
// session then failed a constraint with no migration error to point at.
//
// Renumbering alone does not fix an EXISTING database: it still has 53–60 in
// its ledger, so the renamed files look unapplied and re-run
// "ALTER TABLE sessions ADD COLUMN role_id" against a table that already has
// it. So the ledger is rewritten first, in one transaction, before goose runs.
//
// A fork range does not make future collisions impossible, but it makes them
// deliberate: upstream would have to reach 9000.

// forkMigration pairs a legacy version number with its 9000-series replacement
// and a PHYSICAL fingerprint of the migration having run.
//
// The fingerprint is the load-bearing part. Version 53 alone is ambiguous — on
// an upstream database it means the Muse harness migration, and on a fork
// database it means the role columns. Rewriting the ledger on the version
// number alone would delete a legitimate upstream entry and re-run Muse.
type forkMigration struct {
	// firstVersion is the migration's original fork-local number. Those numbers
	// were later claimed by upstream, so a repair may copy their timestamp into
	// the 9000 ledger but must never delete them.
	firstVersion int64
	oldVersion   int64
	newVersion   int64
	name         string
	// applied reports whether this migration's effect is physically present.
	applied func(*sql.Tx) (bool, error)
}

type forkMigrationOnEntry struct {
	migration      forkMigration
	firstRecorded  bool
	oldRecorded    bool
	newRecorded    bool
	effectRecorded bool
}

type forkHistorySnapshot struct {
	migrations []forkMigrationOnEntry
	museEffect bool
}

type forkHistoryDisposition uint8

const (
	forkHistoryPairwise forkHistoryDisposition = iota
	forkHistoryPreserveLoneMuse
	forkHistoryCleanupCompleteStaleBlock
)

func forkMigrations() []forkMigration {
	return []forkMigration{
		{42, 53, 9000, "session_role_fields", hasSessionsColumnTx("role_id")},
		{43, 54, 9001, "session_spawn_capability_hash", hasSessionsColumnTx("spawn_capability_hash")},
		{44, 55, 9002, "lifecycle_ledger", hasTableTx("lifecycle_ledger")},
		{45, 56, 9003, "session_switch_pending", hasSessionsColumnTx("switch_pending_json")},
		{46, 57, 9004, "one_active_orchestrator", hasIndexTx("idx_sessions_one_active_orchestrator")},
		{47, 58, 9005, "orchestrator_replacement_intent", hasTableTx("orchestrator_replacement_intent")},
		// 0059 rebuilt the ledger to admit a new kind; the rebuilt table's SQL
		// text is the only trace it left.
		{48, 59, 9006, "lifecycle_ledger_orchestrator_fresh", tableSQLContainsTx("lifecycle_ledger", "orchestrator_fresh_conversation")},
		{49, 60, 9007, "session_pause", hasSessionsColumnTx("pause_json")},
	}
}

// repairForkMigrationVersions rewrites this fork's legacy 53–60 ledger entries
// to their 9000-series equivalents. It runs BEFORE goose, in one transaction:
// a half-rewritten ledger is worse than either end state, because the next boot
// would see a mixture and could both re-run an applied migration and skip an
// unapplied one.
//
// It is a no-op on a database that never ran this fork (the fingerprints are
// absent) and on one already repaired (the new version is recorded), so it is
// safe on every boot rather than needing a one-shot flag — a flag is just
// another thing that can be wrong.
func repairForkMigrationVersions(db *sql.DB) error {
	ok, err := hasGooseTable(db)
	if err != nil {
		return fmt.Errorf("fork migration repair: %w", err)
	}
	if !ok {
		// A brand new database. goose will create the ledger and apply the
		// 9000-series files in order; there is nothing to rewrite.
		return nil
	}

	tx, err := db.Begin()
	if err != nil {
		return fmt.Errorf("fork migration repair: begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	snapshot, err := snapshotForkMigrationHistory(tx)
	if err != nil {
		return fmt.Errorf("fork migration repair: snapshot: %w", err)
	}
	disposition, err := classifyForkMigrationHistory(snapshot)
	if err != nil {
		return fmt.Errorf("fork migration repair: %w", err)
	}

	for i, entry := range snapshot.migrations {
		m := entry.migration
		// The fork first shipped these migrations at 0042-0049, before moving
		// them to 0053-0060. Real databases from that first range still carry the
		// physical role schema but no 9000 entries. Upstream has since reclaimed
		// 42-49, so preserve those ledger rows and only add the 9000 identity when
		// the fork-specific physical fingerprint proves the migration ran.
		newRecorded := entry.newRecorded
		if !newRecorded {
			if entry.firstRecorded && entry.effectRecorded {
				if _, err := tx.Exec(
					`INSERT INTO goose_db_version (version_id, is_applied, tstamp)
					 SELECT ?, 1, tstamp FROM goose_db_version WHERE version_id = ? ORDER BY id DESC LIMIT 1`,
					m.newVersion, m.firstVersion,
				); err != nil {
					return fmt.Errorf("fork migration repair: record %d: %w", m.newVersion, err)
				}
				newRecorded = true
			}
		}

		// Already repaired on entry. This is what makes repeat passes
		// idempotent: once 9000 is recorded, a later version 53 row is
		// upstream's Muse migration and must be left alone even though the
		// fork's fingerprint is still (correctly) present. When this pass just
		// recorded 9000 from the first 42-49 range, keep going: the same database
		// may still carry stale 53-60 fork rows that must be freed.
		if entry.newRecorded {
			continue
		}
		if !entry.oldRecorded {
			continue
		}
		if !entry.effectRecorded {
			// The version is recorded but this fork's change is not physically
			// there, so the number belongs to somebody else's migration. Leave
			// it: goose will apply the 9000-series file normally.
			continue
		}
		if disposition == forkHistoryPreserveLoneMuse && i == 0 {
			// The full original 42-49 fork block and all eight physical effects
			// prove where 9000-9007 came from. A lone 53 plus the widened Muse
			// constraint is upstream's row, not the abandoned fork identity.
			continue
		}
		if !newRecorded {
			if _, err := tx.Exec(
				`INSERT INTO goose_db_version (version_id, is_applied, tstamp)
				 SELECT ?, 1, tstamp FROM goose_db_version WHERE version_id = ? ORDER BY id DESC LIMIT 1`,
				m.newVersion, m.oldVersion,
			); err != nil {
				return fmt.Errorf("fork migration repair: record %d: %w", m.newVersion, err)
			}
		}
		if _, err := tx.Exec(`DELETE FROM goose_db_version WHERE version_id = ?`, m.oldVersion); err != nil {
			return fmt.Errorf("fork migration repair: clear %d: %w", m.oldVersion, err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("fork migration repair: commit: %w", err)
	}
	return nil
}

func snapshotForkMigrationHistory(tx *sql.Tx) (forkHistorySnapshot, error) {
	snapshot := forkHistorySnapshot{migrations: make([]forkMigrationOnEntry, 0, len(forkMigrations()))}
	for _, migration := range forkMigrations() {
		firstRecorded, err := versionRecorded(tx, migration.firstVersion)
		if err != nil {
			return forkHistorySnapshot{}, fmt.Errorf("%s: read original version %d: %w", migration.name, migration.firstVersion, err)
		}
		oldRecorded, err := versionRecorded(tx, migration.oldVersion)
		if err != nil {
			return forkHistorySnapshot{}, fmt.Errorf("%s: read abandoned version %d: %w", migration.name, migration.oldVersion, err)
		}
		newRecorded, err := versionRecorded(tx, migration.newVersion)
		if err != nil {
			return forkHistorySnapshot{}, fmt.Errorf("%s: read fork version %d: %w", migration.name, migration.newVersion, err)
		}
		effectRecorded, err := migration.applied(tx)
		if err != nil {
			return forkHistorySnapshot{}, fmt.Errorf("%s: inspect physical effect: %w", migration.name, err)
		}
		snapshot.migrations = append(snapshot.migrations, forkMigrationOnEntry{
			migration:      migration,
			firstRecorded:  firstRecorded,
			oldRecorded:    oldRecorded,
			newRecorded:    newRecorded,
			effectRecorded: effectRecorded,
		})
	}
	museEffect, err := tableSQLContainsTx("sessions", "'muse'")(tx)
	if err != nil {
		return forkHistorySnapshot{}, fmt.Errorf("inspect Muse physical effect: %w", err)
	}
	snapshot.museEffect = museEffect
	return snapshot, nil
}

func classifyForkMigrationHistory(snapshot forkHistorySnapshot) (forkHistoryDisposition, error) {
	if len(snapshot.migrations) != len(forkMigrations()) {
		return forkHistoryPairwise, fmt.Errorf("%w: incomplete fork history snapshot", errForkMigrationHistoryAmbiguous)
	}

	allOriginalRecorded := true
	allEffectsRecorded := true
	var oldMask, newMask, unprotectedOldMask, unprotectedAppliedOldMask uint8
	for i, entry := range snapshot.migrations {
		bit := uint8(1 << i)
		allOriginalRecorded = allOriginalRecorded && entry.firstRecorded
		allEffectsRecorded = allEffectsRecorded && entry.effectRecorded
		if entry.oldRecorded {
			oldMask |= bit
			if !entry.newRecorded {
				unprotectedOldMask |= bit
				if entry.effectRecorded {
					unprotectedAppliedOldMask |= bit
				}
			}
		}
		if entry.newRecorded {
			newMask |= bit
		}
	}
	if !allOriginalRecorded {
		return forkHistoryPairwise, nil
	}
	if unprotectedAppliedOldMask != 0 && !allEffectsRecorded {
		return forkHistoryPairwise, fmt.Errorf(
			"%w: complete original block with physically applied unprotected 53-60 history but incomplete fork schema (old mask %#02x, new mask %#02x, applied old mask %#02x)",
			errForkMigrationHistoryAmbiguous, oldMask, newMask, unprotectedAppliedOldMask,
		)
	}
	if !allEffectsRecorded {
		// A burned upstream ledger may record 42-53 without any fork effects. In
		// that shape the pairwise loop cannot delete an old row; later schema
		// reconciliation repairs the skipped upstream physical effect.
		return forkHistoryPairwise, nil
	}

	switch unprotectedOldMask {
	case 0:
		// Original-only and already-repaired histories are both safe. Any old
		// row with its corresponding 9000 identity already present belongs to
		// upstream and remains protected by the pairwise loop.
		return forkHistoryPairwise, nil
	case 1:
		if snapshot.museEffect {
			return forkHistoryPreserveLoneMuse, nil
		}
		return forkHistoryPairwise, fmt.Errorf(
			"%w: complete original block with lone unprotected 53 but no Muse schema effect (old mask %#02x, new mask %#02x)",
			errForkMigrationHistoryAmbiguous, oldMask, newMask,
		)
	case 0xff:
		// The complete abandoned second block wins even if the schema also
		// contains Muse text: it is the only population whose 53-60 identities
		// are known to be stale and must all be freed for upstream.
		return forkHistoryCleanupCompleteStaleBlock, nil
	default:
		return forkHistoryPairwise, fmt.Errorf(
			"%w: complete original block with partial unprotected 53-60 history (old mask %#02x, new mask %#02x)",
			errForkMigrationHistoryAmbiguous, oldMask, newMask,
		)
	}
}

func hasGooseTable(db *sql.DB) (bool, error) {
	var name string
	err := db.QueryRow(
		`SELECT name FROM sqlite_master WHERE type = 'table' AND name = 'goose_db_version'`,
	).Scan(&name)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

// versionRecorded reports whether the version's LATEST row marks it applied.
// goose appends rows rather than updating them, so a down-then-up history has
// several rows for one version and only the most recent one is the truth.
func versionRecorded(tx *sql.Tx, version int64) (bool, error) {
	var applied bool
	err := tx.QueryRow(
		`SELECT is_applied FROM goose_db_version WHERE version_id = ? ORDER BY id DESC LIMIT 1`,
		version,
	).Scan(&applied)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return applied, nil
}

func hasSessionsColumnTx(column string) func(*sql.Tx) (bool, error) {
	const table = "sessions"
	return func(tx *sql.Tx) (bool, error) {
		rows, err := tx.Query(fmt.Sprintf("PRAGMA table_info(%s)", table))
		if err != nil {
			return false, err
		}
		defer func() { _ = rows.Close() }()
		for rows.Next() {
			var (
				cid        int
				name, typ  string
				notNull    int
				dflt       sql.NullString
				primaryKey int
			)
			if err := rows.Scan(&cid, &name, &typ, &notNull, &dflt, &primaryKey); err != nil {
				return false, err
			}
			if name == column {
				return true, rows.Err()
			}
		}
		return false, rows.Err()
	}
}

func hasTableTx(table string) func(*sql.Tx) (bool, error) {
	return func(tx *sql.Tx) (bool, error) {
		return existsInSchemaTx(tx, `type = 'table' AND name = ?`, table)
	}
}

func hasIndexTx(index string) func(*sql.Tx) (bool, error) {
	return func(tx *sql.Tx) (bool, error) {
		return existsInSchemaTx(tx, `type = 'index' AND name = ?`, index)
	}
}

func tableSQLContainsTx(table, needle string) func(*sql.Tx) (bool, error) {
	return func(tx *sql.Tx) (bool, error) {
		var stored sql.NullString
		err := tx.QueryRow(
			`SELECT sql FROM sqlite_master WHERE type = 'table' AND name = ?`, table,
		).Scan(&stored)
		if errors.Is(err, sql.ErrNoRows) {
			return false, nil
		}
		if err != nil {
			return false, err
		}
		return stored.Valid && strings.Contains(stored.String, needle), nil
	}
}

func existsInSchemaTx(tx *sql.Tx, where, arg string) (bool, error) {
	var name string
	err := tx.QueryRow(`SELECT name FROM sqlite_master WHERE `+where, arg).Scan(&name)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}
