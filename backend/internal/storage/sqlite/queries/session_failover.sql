-- ASCII ONLY IN THIS FILE. This is not a style preference; see the NOTE at the
-- bottom. A single multi-byte character anywhere above a statement silently
-- truncates that statement's generated SQL.

-- name: InsertSessionFailoverAttempt :exec
-- Appends one continuation attempt. Written inside the SAME transaction as the
-- failover/requested lifecycle ledger row (contract section 6 rule 1) -- see
-- Store.AppendSessionFailoverAttemptWithLedger. The
-- UNIQUE(session_id, incident_id, seq) constraint is the last-resort idempotence
-- fence behind that transaction: a retry that recomputed the same seq fails
-- here, and because the ledger row is in the same transaction it is rolled back
-- with it rather than left behind as an orphan.
--
-- generation_id is supplied by the caller and is never empty: Continue mints the
-- switch generation before this insert and pins it into the saga via
-- SwitchRequest.ForceGenerationID (contract section 6b).
INSERT INTO session_failover_attempts (
    id, session_id, project_id, incident_id, seq,
    role_id, from_harness, from_model, to_harness, to_model,
    rung_index, generation_id, state, created_at, updated_at
) VALUES (
    ?, ?, ?, ?, ?,
    ?, ?, ?, ?, ?,
    ?, ?, ?, ?, ?
);

-- name: ListSessionFailoverAttemptsByIncident :many
-- Every attempt for one incident, oldest first. `used` in
-- domain.NextFailoverRung is built from ALL of these regardless of state: a
-- rung that FAILED is spent, not retried. The last row is the latest attempt,
-- which is the one idempotence adopts when it is non-terminal (contract 6a).
SELECT
    id, session_id, project_id, incident_id, seq,
    role_id, from_harness, from_model, to_harness, to_model,
    rung_index, generation_id, state, created_at, updated_at
FROM session_failover_attempts
WHERE session_id = ? AND incident_id = ?
ORDER BY seq ASC;

-- name: ListSessionFailoverAttemptsBySession :many
-- Every attempt for a session across incidents, oldest first. Reconciliation
-- reads this rather than one incident's slice: an attempt left non-terminal by
-- a crash belongs to whichever incident was live at the time, and boot has no
-- incident in hand to ask about.
SELECT
    id, session_id, project_id, incident_id, seq,
    role_id, from_harness, from_model, to_harness, to_model,
    rung_index, generation_id, state, created_at, updated_at
FROM session_failover_attempts
WHERE session_id = ?
ORDER BY created_at ASC, seq ASC;

-- name: UpdateSessionFailoverAttemptState :execrows
-- Moves an attempt to a new state under a compare-and-set on the state the
-- caller observed. The generation is NOT written here: it is durable from the
-- very first insert (contract section 6b), so there is no stamp step and
-- therefore no query that can rewrite a generation -- which is what made crash
-- recovery a guess in the first draft.
--
-- The CAS is what makes the two completers of a post_stop safe against each
-- other: an operator Continue and boot's post_stop recovery both try
-- post_stop -> acked, and the loser gets 0 rows. execrows: 0 means the guard
-- rejected the write, which is an answer, not an error.
UPDATE session_failover_attempts
SET state = ?, updated_at = ?
WHERE id = ? AND state = ?;

-- NOTE (supersedes the diagnosis in queries/sessions.sql:119 and
-- queries/changelog.sql:10): the sqlc 1.31 SQLite-parser truncation is NOT
-- caused by literals or placeholders on the RHS of `=` in DELETE/UPDATE. It is
-- caused by MULTI-BYTE UTF-8 anywhere earlier in the file. The parser reports
-- statement boundaries as rune offsets and sqlc slices the source with them as
-- byte offsets, so every statement after the first non-ASCII character is cut
-- short by the accumulated (bytes - runes) delta, and the severed tail leaks
-- into the NEXT generated const.
--
-- An earlier draft of this file used the section sign and em dashes in these
-- comments and mangled ALL FOUR statements, including the plain INSERT --
-- which has no `=` at all, and so cannot be explained by the older theory.
-- Rewriting the identical SQL with ASCII-only comments generates all four
-- intact, guarded UPDATE included. That is why this file needs no raw
-- ExecContext fallback, and why the rule above is worth obeying literally.
