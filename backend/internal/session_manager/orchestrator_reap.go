package sessionmanager

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// ErrReapUnconfirmed means at least one superseded orchestrator's execution
// death could not be authoritatively confirmed. It is fatal at boot: the
// obligations stay queued and the daemon must not serve.
var ErrReapUnconfirmed = errors.New("orchestrator reap: execution death not confirmed")

// DrainOrchestratorReapQueue discharges the obligations migration 0057 created
// when it reconciled duplicate orchestrators.
//
// This is FAIL-CLOSED and must run before the generic reconcile/reap passes and
// before RestoreAll. Its failure must abort startup — unlike Reconcile, whose
// errors the daemon logs and continues past. A superseded orchestrator whose
// process is still running holds a live agent inside the canonical workspace
// the survivor now owns; serving in that state is exactly what the constraint
// exists to prevent.
//
// Contract, in order of importance:
//
//   - A missing queue table aborts boot. "The schema is not what I expect" must
//     never be read as "nothing is owed".
//   - An entry is deleted ONLY after death is authoritatively confirmed.
//     Anything else — alive, uncertain probe, unclosable shell — keeps the row,
//     stamps an attempt, and fails.
//   - The canonical workspace is NEVER removed. It belongs to the survivor;
//     only execution surfaces are reaped.
//   - Re-running after a partial drain is safe: discharged entries are gone,
//     the rest are retried.
func (m *Manager) DrainOrchestratorReapQueue(ctx context.Context) error {
	entries, err := m.store.ListOrchestratorReapQueue(ctx)
	if err != nil {
		// Deliberately not tolerated: see contract above.
		return fmt.Errorf("orchestrator reap: read queue: %w", err)
	}
	if len(entries) == 0 {
		return nil
	}

	m.logger.Info("orchestrator reap: draining superseded orchestrators", "count", len(entries))

	var unconfirmed []string
	for _, entry := range entries {
		if reapErr := m.reapSupersededOrchestrator(ctx, entry); reapErr != nil {
			unconfirmed = append(unconfirmed, fmt.Sprintf("%s: %v", entry.SessionID, reapErr))
			// Stamp the attempt so repeated inability to confirm is visible.
			// A stamp failure must not mask the real reason we are failing.
			if stampErr := m.store.RecordOrchestratorReapAttempt(ctx, entry.SessionID, m.clock()); stampErr != nil {
				m.logger.Warn("orchestrator reap: record attempt failed",
					"session", entry.SessionID, "err", stampErr)
			}
			continue
		}
		if delErr := m.store.DeleteOrchestratorReapEntry(ctx, entry.SessionID); delErr != nil {
			// Death was confirmed but the obligation could not be discharged.
			// Keep it: a retained entry costs one redundant probe next boot,
			// whereas proceeding would drop a durable record we cannot rebuild.
			unconfirmed = append(unconfirmed,
				fmt.Sprintf("%s: confirmed dead but queue entry not cleared: %v", entry.SessionID, delErr))
			continue
		}
		m.logger.Info("orchestrator reap: superseded orchestrator confirmed dead",
			"session", entry.SessionID, "project", entry.ProjectID)
	}

	if len(unconfirmed) > 0 {
		return fmt.Errorf("%w: %d of %d outstanding: %s",
			ErrReapUnconfirmed, len(unconfirmed), len(entries), strings.Join(unconfirmed, "; "))
	}
	return nil
}

// reapSupersededOrchestrator confirms that one superseded orchestrator's agent
// runtime and scoped shells are dead. It returns nil only when both are
// authoritatively confirmed, and never touches the workspace.
func (m *Manager) reapSupersededOrchestrator(ctx context.Context, entry domain.OrchestratorReapEntry) error {
	handle, err := m.reapProbeHandle(entry)
	if err != nil {
		return fmt.Errorf("resolve probe handle: %w", err)
	}

	dead, err := m.destroyRuntimeProbed(ctx, handle.ID)
	if err != nil {
		return fmt.Errorf("runtime probe inconclusive: %w", err)
	}
	if !dead {
		return fmt.Errorf("runtime %q still alive", handle.ID)
	}

	// Scoped shells run inside the canonical workspace the survivor now owns,
	// so an unclosable shell is as disqualifying as a live runtime. Unlike
	// drainScopedShells — best-effort by design for interactive cleanup — a
	// failure here is propagated, and unlike ordinary teardown an unwired
	// closer is itself a failure (see requireShellTerminalTeardown).
	release, shellErr := m.requireShellTerminalTeardown(ctx, entry.SessionID)
	if shellErr != nil {
		return fmt.Errorf("scoped shells not confirmed closed: %w", shellErr)
	}
	if release != nil {
		// Nothing is being removed, so the gate is released immediately; the
		// shells themselves are already closed.
		release()
	}
	return nil
}

// reapProbeHandle answers which handle to probe for one queued obligation.
//
// A recorded handle always wins. When the row has none, the raw session id is
// NOT a safe substitute: destroyRuntimeProbed short-circuits an empty handle to
// "confirmed dead" without probing at all (safe inside the switch saga, where
// the handle was just read off a live row; wrong here, since a runtime named
// after the session can outlive a cleared handle field), and the adapters do
// not key runtimes by the raw id — tmux sanitizes ids that are too long or
// contain characters it rejects, and its IsAlive validates whatever handle it
// is given. Probing the raw id would therefore either miss a live runtime or be
// rejected outright, wedging boot on a handle that never existed.
//
// So the adapter is asked. A runtime that cannot answer fails the obligation
// rather than guessing: both shipped adapters implement the capability (asserted
// at compile time in runtimeselect), so this is a wiring error, and the whole
// point of this path is that unconfirmed means unconfirmed.
func (m *Manager) reapProbeHandle(entry domain.OrchestratorReapEntry) (ports.RuntimeHandle, error) {
	if h := strings.TrimSpace(entry.RuntimeHandleID); h != "" {
		return ports.RuntimeHandle{ID: h}, nil
	}
	resolver, ok := m.runtime.(ports.RuntimeSessionHandleResolver)
	if !ok {
		return ports.RuntimeHandle{}, fmt.Errorf(
			"no recorded handle for %s and this runtime cannot derive one from a session id", entry.SessionID)
	}
	handle, err := resolver.SessionHandle(entry.SessionID)
	if err != nil {
		return ports.RuntimeHandle{}, fmt.Errorf("derive handle for %s: %w", entry.SessionID, err)
	}
	if strings.TrimSpace(handle.ID) == "" {
		return ports.RuntimeHandle{}, fmt.Errorf("runtime derived an empty handle for %s", entry.SessionID)
	}
	m.logger.Warn("orchestrator reap: no recorded handle; probing adapter-derived handle",
		"session", entry.SessionID, "handle", handle.ID)
	return handle, nil
}
