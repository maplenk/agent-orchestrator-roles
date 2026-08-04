package store

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/gen"
)

// AppendLifecycleLedger inserts one append-only lifecycle event. No updates.
func (s *Store) AppendLifecycleLedger(ctx context.Context, rec domain.LifecycleLedgerRecord) error {
	if strings.TrimSpace(rec.ID) == "" {
		return fmt.Errorf("lifecycle ledger: id required")
	}
	if rec.SessionID == "" || rec.ProjectID == "" {
		return fmt.Errorf("lifecycle ledger: session_id and project_id required")
	}
	if !rec.Kind.Valid() {
		return fmt.Errorf("lifecycle ledger: invalid kind %q", rec.Kind)
	}
	created := rec.CreatedAt
	if created.IsZero() {
		created = time.Now().UTC()
	}
	payload := rec.PayloadJSON
	if strings.TrimSpace(payload) == "" {
		payload = "{}"
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	if err := s.qw.InsertLifecycleLedger(ctx, gen.InsertLifecycleLedgerParams{
		ID:                    rec.ID,
		SessionID:             string(rec.SessionID),
		ProjectID:             string(rec.ProjectID),
		Kind:                  string(rec.Kind),
		Phase:                 string(rec.Phase),
		GenerationID:          rec.GenerationID,
		FromHarness:           string(rec.FromHarness),
		ToHarness:             string(rec.ToHarness),
		FromModel:             rec.FromModel,
		ToModel:               rec.ToModel,
		RoleID:                rec.RoleID,
		SourceNativeSessionID: rec.SourceNativeSessionID,
		TargetNativeSessionID: rec.TargetNativeSessionID,
		PayloadJson:           payload,
		CreatedAt:             created.UTC(),
	}); err != nil {
		return fmt.Errorf("append lifecycle ledger %s: %w", rec.ID, err)
	}
	return nil
}

// ListLifecycleLedger returns events for a session oldest-first.
func (s *Store) ListLifecycleLedger(ctx context.Context, sessionID domain.SessionID) ([]domain.LifecycleLedgerRecord, error) {
	rows, err := s.qr.ListLifecycleLedgerBySession(ctx, string(sessionID))
	if err != nil {
		return nil, fmt.Errorf("list lifecycle ledger %s: %w", sessionID, err)
	}
	out := make([]domain.LifecycleLedgerRecord, 0, len(rows))
	for _, r := range rows {
		out = append(out, domain.LifecycleLedgerRecord{
			ID:                    r.ID,
			SessionID:             domain.SessionID(r.SessionID),
			ProjectID:             domain.ProjectID(r.ProjectID),
			Kind:                  domain.LifecycleLedgerKind(r.Kind),
			Phase:                 domain.LifecycleLedgerPhase(r.Phase),
			GenerationID:          r.GenerationID,
			FromHarness:           domain.AgentHarness(r.FromHarness),
			ToHarness:             domain.AgentHarness(r.ToHarness),
			FromModel:             r.FromModel,
			ToModel:               r.ToModel,
			RoleID:                r.RoleID,
			SourceNativeSessionID: r.SourceNativeSessionID,
			TargetNativeSessionID: r.TargetNativeSessionID,
			PayloadJSON:           r.PayloadJson,
			CreatedAt:             r.CreatedAt.UTC(),
		})
	}
	return out, nil
}
