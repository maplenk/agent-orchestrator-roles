-- name: UpsertTemplateArtifact :exec
INSERT INTO template_artifacts (id, sha256, content, created_at)
VALUES (?, ?, ?, ?)
ON CONFLICT(id) DO NOTHING;

-- name: GetTemplateArtifact :one
SELECT id, sha256, content, created_at FROM template_artifacts WHERE id = ?;
