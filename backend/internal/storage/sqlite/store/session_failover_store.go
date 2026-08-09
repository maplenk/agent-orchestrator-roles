package store

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/gen"
)

// Durable storage for Phase 3B manual-failover attempts (migration 9008,
// PHASE3B_MVP_CONTRACT sections 5 and 6).

// AppendSessionFailoverAttemptWithLedger writes the failover/requested ledger
// row and the attempt row in ONE SQLite write transaction.
//
// This is contract section 6 rule 1, and the atomicity is the whole point of the
// method existing at all rather than the manager calling AppendLifecycleLedger
// and an attempt-append in sequence. Two separate writes leave a crash window
// with no repair story in either direction: a ledger row with no attempt makes
// the incident look continued when no rung was spent, and an attempt with no
// ledger row spends a rung with nothing in the audit trail saying why. Because
// inTx hands the callback a *gen.Queries that already carries
// InsertLifecycleLedger, closing that window needs no change to
// lifecycle_ledger_store.go.
//
// The ledger row goes in FIRST, keeping the 3A "ledger before effect" ordering
// inside the transaction as well as outside it. That also makes the realistic
// failure -- a UNIQUE(session_id, incident_id, seq) collision from a retry that
// recomputed the same seq -- land on the second insert, where it rolls the
// ledger row back with it.
func (s *Store) AppendSessionFailoverAttemptWithLedger(
	ctx context.Context,
	attempt domain.FailoverAttempt,
	ledger domain.LifecycleLedgerRecord,
) error {
	if err := validateFailoverAttempt(attempt); err != nil {
		return err
	}
	if strings.TrimSpace(ledger.ID) == "" {
		return fmt.Errorf("failover attempt %s: ledger id required", attempt.ID)
	}
	if ledger.SessionID == "" || ledger.ProjectID == "" {
		return fmt.Errorf("failover attempt %s: ledger session_id and project_id required", attempt.ID)
	}
	if !ledger.Kind.Valid() {
		return fmt.Errorf("failover attempt %s: invalid ledger kind %q", attempt.ID, ledger.Kind)
	}

	created := ledger.CreatedAt
	if created.IsZero() {
		created = time.Now().UTC()
	}
	payload := ledger.PayloadJSON
	if strings.TrimSpace(payload) == "" {
		payload = "{}"
	}

	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	return s.inTx(ctx, "append failover attempt", func(q *gen.Queries) error {
		if err := q.InsertLifecycleLedger(ctx, gen.InsertLifecycleLedgerParams{
			ID:                    ledger.ID,
			SessionID:             string(ledger.SessionID),
			ProjectID:             string(ledger.ProjectID),
			Kind:                  string(ledger.Kind),
			Phase:                 string(ledger.Phase),
			GenerationID:          ledger.GenerationID,
			FromHarness:           string(ledger.FromHarness),
			ToHarness:             string(ledger.ToHarness),
			FromModel:             ledger.FromModel,
			ToModel:               ledger.ToModel,
			RoleID:                ledger.RoleID,
			SourceNativeSessionID: ledger.SourceNativeSessionID,
			TargetNativeSessionID: ledger.TargetNativeSessionID,
			PayloadJson:           payload,
			CreatedAt:             created.UTC(),
		}); err != nil {
			return fmt.Errorf("ledger row %s: %w", ledger.ID, err)
		}
		if err := q.InsertSessionFailoverAttempt(ctx, gen.InsertSessionFailoverAttemptParams{
			ID:                 attempt.ID,
			SessionID:          string(attempt.SessionID),
			ProjectID:          string(attempt.ProjectID),
			IncidentID:         attempt.IncidentID,
			Seq:                int64(attempt.Seq),
			RoleID:             attempt.RoleID,
			FromHarness:        string(attempt.FromHarness),
			FromModel:          attempt.FromModel,
			ToHarness:          string(attempt.ToHarness),
			ToModel:            attempt.ToModel,
			RungIndex:          int64(attempt.RungIndex),
			GenerationID:       attempt.GenerationID,
			SourceGenerationID: attempt.SourceGenerationID,
			State:              string(attempt.State),
			CreatedAt:          attempt.CreatedAt.UTC(),
			UpdatedAt:          attempt.UpdatedAt.UTC(),
		}); err != nil {
			return fmt.Errorf("attempt row %s: %w", attempt.ID, err)
		}
		return nil
	})
}

// ListSessionFailoverAttemptsByIncident returns every attempt for one incident,
// oldest first, in ANY state. Callers build domain.NextFailoverRung's `used`
// from all of them: a rung that failed is spent, not retried.
func (s *Store) ListSessionFailoverAttemptsByIncident(
	ctx context.Context,
	sessionID domain.SessionID,
	incidentID string,
) ([]domain.FailoverAttempt, error) {
	rows, err := s.qr.ListSessionFailoverAttemptsByIncident(ctx, gen.ListSessionFailoverAttemptsByIncidentParams{
		SessionID:  string(sessionID),
		IncidentID: incidentID,
	})
	if err != nil {
		return nil, fmt.Errorf("list failover attempts %s/%s: %w", sessionID, incidentID, err)
	}
	return failoverAttemptsToDomain(rows), nil
}

// ListSessionFailoverAttemptsBySession returns every attempt for a session
// across incidents, oldest first. Reconciliation reads this rather than one
// incident's slice, because boot has no incident id in hand to ask about.
func (s *Store) ListSessionFailoverAttemptsBySession(
	ctx context.Context,
	sessionID domain.SessionID,
) ([]domain.FailoverAttempt, error) {
	rows, err := s.qr.ListSessionFailoverAttemptsBySession(ctx, string(sessionID))
	if err != nil {
		return nil, fmt.Errorf("list failover attempts %s: %w", sessionID, err)
	}
	return failoverAttemptsToDomain(rows), nil
}

// UpdateSessionFailoverAttemptState moves an attempt from the state the caller
// observed to a new one. ok=false means the compare-and-set found a different
// state and wrote nothing -- an answer, not an error.
//
// That distinction is load-bearing for post_stop, which has two legitimate
// completers (an operator Continue that adopts it, and boot's post_stop
// recovery). Both attempt post_stop -> acked on the same generation; the loser
// sees ok=false and re-reads rather than failing the operation, so a race
// between them still ends in exactly one ack.
//
// The generation is deliberately not writable here. It is durable from the
// first insert (contract section 6b), so no code path can rewrite it.
func (s *Store) UpdateSessionFailoverAttemptState(
	ctx context.Context,
	attemptID string,
	from, to domain.FailoverAttemptState,
	updatedAt time.Time,
) (bool, error) {
	if strings.TrimSpace(attemptID) == "" {
		return false, fmt.Errorf("failover attempt: id required")
	}
	if !from.Valid() {
		return false, fmt.Errorf("failover attempt %s: invalid expected state %q", attemptID, from)
	}
	if !to.Valid() {
		return false, fmt.Errorf("failover attempt %s: invalid target state %q", attemptID, to)
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	rows, err := s.qw.UpdateSessionFailoverAttemptState(ctx, gen.UpdateSessionFailoverAttemptStateParams{
		State:     string(to),
		UpdatedAt: updatedAt.UTC(),
		ID:        attemptID,
		State_2:   string(from),
	})
	if err != nil {
		return false, fmt.Errorf("update failover attempt %s: %w", attemptID, err)
	}
	return rows > 0, nil
}

// validateFailoverAttempt rejects a row SQLite's CHECK would reject anyway, so
// the error names the field instead of surfacing a constraint string.
func validateFailoverAttempt(a domain.FailoverAttempt) error {
	if strings.TrimSpace(a.ID) == "" {
		return fmt.Errorf("failover attempt: id required")
	}
	if a.SessionID == "" || a.ProjectID == "" {
		return fmt.Errorf("failover attempt %s: session_id and project_id required", a.ID)
	}
	if strings.TrimSpace(a.IncidentID) == "" {
		return fmt.Errorf("failover attempt %s: incident_id required", a.ID)
	}
	if !a.State.Valid() {
		return fmt.Errorf("failover attempt %s: invalid state %q", a.ID, a.State)
	}
	// Contract section 6b: the generation is minted before this row is written.
	// An empty one here means a caller reintroduced the stamp-afterwards shape
	// that made crash recovery a guess, so it is refused at the boundary rather
	// than stored and puzzled over later.
	if strings.TrimSpace(a.GenerationID) == "" {
		return fmt.Errorf("failover attempt %s: generation_id required", a.ID)
	}
	if strings.TrimSpace(a.SourceGenerationID) == "" {
		return fmt.Errorf("failover attempt %s: source_generation_id required", a.ID)
	}
	return nil
}

func failoverAttemptsToDomain(rows []gen.SessionFailoverAttempt) []domain.FailoverAttempt {
	out := make([]domain.FailoverAttempt, 0, len(rows))
	for _, r := range rows {
		out = append(out, domain.FailoverAttempt{
			ID:                 r.ID,
			SessionID:          domain.SessionID(r.SessionID),
			ProjectID:          domain.ProjectID(r.ProjectID),
			IncidentID:         r.IncidentID,
			Seq:                int(r.Seq),
			RoleID:             r.RoleID,
			FromHarness:        domain.AgentHarness(r.FromHarness),
			FromModel:          r.FromModel,
			ToHarness:          domain.AgentHarness(r.ToHarness),
			ToModel:            r.ToModel,
			RungIndex:          int(r.RungIndex),
			GenerationID:       r.GenerationID,
			SourceGenerationID: r.SourceGenerationID,
			State:              domain.FailoverAttemptState(r.State),
			CreatedAt:          r.CreatedAt.UTC(),
			UpdatedAt:          r.UpdatedAt.UTC(),
		})
	}
	return out
}
