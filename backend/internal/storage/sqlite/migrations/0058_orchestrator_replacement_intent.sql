-- +goose Up
-- +goose StatementBegin
-- Durable replacement intent, so a zero-orchestrator interval is never terminal.
--
-- "Never zero orchestrators" is NOT achievable under retire-first semantics.
-- The orchestrator worktree is canonical per project, and gitworktree.Create
-- adopts an already-registered worktree rather than failing — so the
-- predecessor must release it before the successor can create it. A spawn
-- failure after that release necessarily leaves the project with no
-- coordinator. That interval is unavoidable; being STUCK in it is not.
--
-- The achievable guarantee is therefore: intent is persisted BEFORE retirement
-- begins, and any zero-owner state is automatically recovered — on the next
-- attempt or at the next boot — rather than waiting for a human to notice a
-- project that silently stopped coordinating.
--
-- One row per project. The project ownership gate already serializes
-- replacement, so a second concurrent intent for the same project is
-- unrepresentable rather than merely unlikely; PRIMARY KEY states that.
CREATE TABLE orchestrator_replacement_intent (
    -- No FK to projects: intent must survive a project row rewrite, and its
    -- whole job is to be readable when other state is mid-flight.
    project_id        TEXT PRIMARY KEY,
    -- The session being replaced, when there was one. Empty for a first spawn
    -- that has no predecessor, which still records intent so a crash between
    -- "decided to spawn" and "spawned" is recoverable too.
    retired_session_id TEXT NOT NULL DEFAULT '',
    requested_at      TIMESTAMP NOT NULL,
    -- Recovery bookkeeping. Retained rather than reset so a project that cannot
    -- be recovered is visible as such instead of looking freshly requested on
    -- every boot.
    attempt_count     INTEGER NOT NULL DEFAULT 0 CHECK (attempt_count >= 0),
    last_attempt_at   TIMESTAMP,
    last_error        TEXT NOT NULL DEFAULT ''
);

-- Deliberately NOT seeded from existing state. A pre-existing project with no
-- active orchestrator is not evidence of an interrupted replacement — it is the
-- ordinary state of a project nobody has started one for, and spawning agents
-- for those on the next boot would be a surprising, expensive side effect of a
-- migration. Intent is only ever written by a replacement that actually began.
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS orchestrator_replacement_intent;
-- +goose StatementEnd
