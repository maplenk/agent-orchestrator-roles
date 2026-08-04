-- name: InsertLifecycleLedger :exec
INSERT INTO lifecycle_ledger (
    id, session_id, project_id, kind, phase, generation_id,
    from_harness, to_harness, from_model, to_model, role_id,
    source_native_session_id, target_native_session_id, payload_json, created_at
) VALUES (
    ?, ?, ?, ?, ?, ?,
    ?, ?, ?, ?, ?,
    ?, ?, ?, ?
);

-- name: ListLifecycleLedgerBySession :many
SELECT
    id, session_id, project_id, kind, phase, generation_id,
    from_harness, to_harness, from_model, to_model, role_id,
    source_native_session_id, target_native_session_id, payload_json, created_at
FROM lifecycle_ledger
WHERE session_id = ?
ORDER BY created_at ASC, id ASC;
