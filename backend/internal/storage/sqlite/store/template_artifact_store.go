package store

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/gen"
)

// ErrTemplateArtifactConflict is returned when PutTemplateArtifact targets an
// existing id whose stored sha256 or content differs from the caller's bytes.
// Idempotent re-puts of the exact same artifact succeed; content mutation fails closed.
var ErrTemplateArtifactConflict = errors.New("template artifact content conflict")

// PutTemplateArtifact stores immutable template bytes. Insert is idempotent
// (ON CONFLICT DO NOTHING). After insert, the row is re-read and must match
// the caller's sha256 and content exactly — so a colliding id with different
// payload cannot be reported as success.
func (s *Store) PutTemplateArtifact(ctx context.Context, id, sha256 string, content []byte, createdAt time.Time) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	cp := append([]byte(nil), content...)
	if err := s.qw.UpsertTemplateArtifact(ctx, gen.UpsertTemplateArtifactParams{
		ID:        id,
		Sha256:    sha256,
		Content:   cp,
		CreatedAt: createdAt,
	}); err != nil {
		return err
	}
	// Verify under the same write lock so concurrent puts cannot race past us
	// before we check stored content. Use the writer-bound queries for read-your-writes.
	row, err := s.qw.GetTemplateArtifact(ctx, id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("put template artifact %q: row missing after insert", id)
		}
		return fmt.Errorf("put template artifact %q: verify: %w", id, err)
	}
	if row.Sha256 != sha256 || !bytes.Equal(row.Content, cp) {
		return fmt.Errorf("%w: id %q stored sha=%q want=%q content_equal=%v",
			ErrTemplateArtifactConflict, id, row.Sha256, sha256, bytes.Equal(row.Content, cp))
	}
	return nil
}

// GetTemplateArtifact loads the immutable role template bytes pinned on a
// session at spawn. ok=false means the artifact is absent, which restore must
// treat as fatal rather than falling back to a current template: the whole
// point of the CAS is that a restored session runs the exact bytes it started
// with.
func (s *Store) GetTemplateArtifact(ctx context.Context, id string) (content []byte, sha string, ok bool, err error) {
	row, err := s.qr.GetTemplateArtifact(ctx, id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, "", false, nil
		}
		return nil, "", false, err
	}
	return append([]byte(nil), row.Content...), row.Sha256, true, nil
}
