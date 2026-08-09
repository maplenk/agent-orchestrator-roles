package sessionmanager

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	"github.com/aoagents/agent-orchestrator/backend/internal/roles/capabilities"
)

// failoverFakeStore adds the narrow failover surface to the shared fakeStore by
// EMBEDDING it, so this file needs no edit to manager_test.go (7000 lines, and
// another agent is working in it).
//
// AppendSessionFailoverAttemptWithLedger mirrors the real store's atomicity: on
// any failure it writes NEITHER row. A fake that wrote the ledger row and then
// failed on the attempt would make the manager's tests pass against a shape the
// production store does not have, which is worse than no fake at all.
type failoverFakeStore struct {
	*fakeStore
	attempts []domain.FailoverAttempt
	// appendErr injects a rule-1 transaction failure.
	appendErr error
	// updateErrOnce injects one failure of the attempt state CAS.
	updateAttemptErr error
	appendCalls      int
	// beforeUpdate runs immediately before the state CAS, so a test can move the
	// row underneath an in-flight transition and make the CAS legitimately miss.
	// That race is the only way to reach the miss branch: everywhere else the
	// caller's expected state came from reading this same store.
	beforeUpdate  func()
	getSessionErr error
	// afterAppend runs after both durable fake rows exist but before Continue
	// reaches SwitchWorker. It exposes the crash/race window without weakening
	// the production ordering being tested.
	afterAppend func()
}

type agentSwitchReservedFailoverStore struct {
	*failoverFakeStore
	active    domain.AgentSwitch
	activeErr error
}

func (s *agentSwitchReservedFailoverStore) GetActiveAgentSwitch(
	_ context.Context, sessionID domain.SessionID,
) (domain.AgentSwitch, bool, error) {
	if s.activeErr != nil {
		return domain.AgentSwitch{}, false, s.activeErr
	}
	if s.active.SessionID != sessionID || s.active.State.Terminal() {
		return domain.AgentSwitch{}, false, nil
	}
	return s.active, true, nil
}

func (f *failoverFakeStore) GetSession(
	ctx context.Context, id domain.SessionID,
) (domain.SessionRecord, bool, error) {
	if f.getSessionErr != nil {
		return domain.SessionRecord{}, false, f.getSessionErr
	}
	return f.fakeStore.GetSession(ctx, id)
}

func newFailoverStore() *failoverFakeStore {
	return &failoverFakeStore{fakeStore: newFakeStore()}
}

func (f *failoverFakeStore) AppendSessionFailoverAttemptWithLedger(
	ctx context.Context, a domain.FailoverAttempt, l domain.LifecycleLedgerRecord,
) error {
	f.appendCalls++
	if f.appendErr != nil {
		return f.appendErr
	}
	for _, e := range f.attempts {
		if e.ID == a.ID {
			return fmt.Errorf("duplicate attempt %s", a.ID)
		}
	}
	// Ledger first, and a failure here writes no attempt: one transaction.
	if err := f.AppendLifecycleLedger(ctx, l); err != nil {
		return err
	}
	f.attempts = append(f.attempts, a)
	if f.afterAppend != nil {
		f.afterAppend()
	}
	return nil
}

func (f *failoverFakeStore) ListSessionFailoverAttemptsByIncident(
	_ context.Context, id domain.SessionID, incident string,
) ([]domain.FailoverAttempt, error) {
	var out []domain.FailoverAttempt
	for _, a := range f.attempts {
		if a.SessionID == id && a.IncidentID == incident {
			out = append(out, a)
		}
	}
	return out, nil
}

func (f *failoverFakeStore) ListSessionFailoverAttemptsBySession(
	_ context.Context, id domain.SessionID,
) ([]domain.FailoverAttempt, error) {
	var out []domain.FailoverAttempt
	for _, a := range f.attempts {
		if a.SessionID == id {
			out = append(out, a)
		}
	}
	return out, nil
}

func (f *failoverFakeStore) UpdateSessionFailoverAttemptState(
	_ context.Context, attemptID string, from, to domain.FailoverAttemptState, at time.Time,
) (bool, error) {
	if f.updateAttemptErr != nil {
		return false, f.updateAttemptErr
	}
	if f.beforeUpdate != nil {
		f.beforeUpdate()
	}
	for i, a := range f.attempts {
		if a.ID != attemptID {
			continue
		}
		if a.State != from {
			return false, nil // CAS lost
		}
		f.attempts[i].State = to
		f.attempts[i].UpdatedAt = at
		return true, nil
	}
	return false, nil
}

func (f *failoverFakeStore) only(t *testing.T) domain.FailoverAttempt {
	t.Helper()
	if len(f.attempts) != 1 {
		t.Fatalf("attempts = %d, want exactly 1: %+v", len(f.attempts), f.attempts)
	}
	return f.attempts[0]
}

// failoverLadder installs a role map whose implementor ladder is codex then
// fake, both of which testSwitchCaps marks switch-capable.
func failoverLadder(st *failoverFakeStore, rungs ...domain.FailoverTarget) {
	st.projects["mer"] = domain.ProjectRecord{
		ID: "mer",
		Config: domain.ProjectConfig{
			RoleMap: domain.RoleMap{
				SchemaVersion: domain.RoleMapSchemaVersion,
				Roles: map[string]domain.RoleBinding{
					"implementor": {Template: "implementor", Harness: domain.HarnessClaudeCode},
				},
				Failover: domain.FailoverConfig{
					Roles: map[string][]domain.FailoverTarget{"implementor": rungs},
				},
			},
		},
	}
}

func pauseSessionAt(st *failoverFakeStore, id domain.SessionID, incident string) {
	rec := st.sessions[id]
	rec.Metadata.Pause = &domain.SessionPause{
		IncidentID: incident,
		Reason:     domain.PauseReasonOperator,
		DetectedBy: domain.PauseDetectionOperator,
		Harness:    rec.Harness,
		PausedAt:   time.Date(2026, 8, 7, 9, 0, 0, 0, time.UTC),
	}
	st.sessions[id] = rec
}

func structuredLimitPauseAt(st *failoverFakeStore, id domain.SessionID, incident string) {
	rec := st.sessions[id]
	rec.Metadata.Pause = &domain.SessionPause{
		IncidentID:              incident,
		Reason:                  domain.PauseReasonUsageLimit,
		DetectedBy:              domain.PauseDetectionStructured,
		Harness:                 rec.Harness,
		ObservedRuntimeLaunchID: rec.Metadata.RuntimeLaunchID,
		EvidenceJSON: `{"version":1,"kind":"usage_limit","harness":"claude-code",` +
			`"scope":"account","sourceKey":"window-1"}`,
		PausedAt: time.Date(2026, 8, 9, 9, 0, 0, 0, time.UTC),
	}
	st.sessions[id] = rec
}

func enableAutomaticFailover(t *testing.T, st *failoverFakeStore, m *Manager, id domain.SessionID) {
	t.Helper()
	project := st.projects["mer"]
	project.Config.RoleMap.Failover.Mode = domain.FailoverModeAutomatic
	st.projects["mer"] = project
	structuredLimitPauseAt(st, id, "inc-1")
	m.automaticFailoverCapsOverride = func(h domain.AgentHarness) capabilities.Caps {
		c := testSwitchCaps(h)
		c.LimitDetectionSupported = true
		return c
	}
}

// failoverFixture builds a paused, role-pinned worker with a two-rung ladder.
func failoverFixture(t *testing.T) (*failoverFakeStore, *fakeRuntime, *Manager, domain.SessionID) {
	t.Helper()
	st := newFailoverStore()
	ws := t.TempDir()
	art, sha := pinImplementorTemplate(t, st.fakeStore)
	id := domain.SessionID("mer-1")
	workerSession(st.fakeStore, id, domain.HarnessClaudeCode, ws, art, sha)
	failoverLadder(st,
		domain.FailoverTarget{Harness: domain.HarnessCodex},
		domain.FailoverTarget{Harness: domain.HarnessFake},
	)
	pauseSessionAt(st, id, "inc-1")

	rt := &fakeRuntime{aliveByHandle: map[string]bool{}}
	m := New(Deps{
		Runtime: rt, Agents: singleAgent{agent: &recordingAgent{}}, Workspace: &fakeWorkspace{},
		Store: st, Messenger: &fakeMessenger{}, Lifecycle: &fakeLCM{store: st.fakeStore},
		LookPath: func(string) (string, error) { return "/bin/true", nil },
	})
	m.switchCapsOverride = testSwitchCaps
	return st, rt, m, id
}

type blockingDestroyRuntime struct {
	*fakeRuntime
	entered chan struct{}
	release chan struct{}
}

func (r *blockingDestroyRuntime) Destroy(ctx context.Context, handle ports.RuntimeHandle) error {
	close(r.entered)
	select {
	case <-r.release:
		return r.fakeRuntime.Destroy(ctx, handle)
	case <-ctx.Done():
		return ctx.Err()
	}
}

// --- structural guarantees -------------------------------------------------

// Contract section 3: "A free-form target must be structurally impossible, not
// merely rejected." A validation test would only prove today's validator; this
// proves there is no field to carry one.
func TestContinueFailoverRequest_HasNoTargetField(t *testing.T) {
	typ := reflect.TypeOf(ContinueFailoverRequest{})
	if typ.NumField() != 1 {
		t.Fatalf("ContinueFailoverRequest has %d fields, want exactly 1", typ.NumField())
	}
	if typ.Field(0).Name != "IncidentID" {
		t.Fatalf("field = %q, want IncidentID", typ.Field(0).Name)
	}
}

func TestAutomaticFailoverFenceIsScopedToTheExactIncident(t *testing.T) {
	m := New(Deps{})
	id := domain.SessionID("mer-1")
	if !m.beginAutomaticFailover(id, "inc-a") {
		t.Fatal("first incident did not acquire its automatic fence")
	}
	defer m.endAutomaticFailover(id, "inc-a")
	if m.beginAutomaticFailover(id, "inc-a") {
		t.Fatal("duplicate delivery for the same incident acquired a second fence")
	}
	if !m.beginAutomaticFailover(id, "inc-b") {
		t.Fatal("a newer incident was suppressed by the older incident's still-unwinding fence")
	}
	m.endAutomaticFailover(id, "inc-b")
}

func TestContinueFailover_NonterminalAgentSwitchReservesRungBeforeMutation(t *testing.T) {
	st, rt, m, id := failoverFixture(t)
	reserved := &agentSwitchReservedFailoverStore{
		failoverFakeStore: st,
		active: domain.AgentSwitch{
			ID: "switch-active", SessionID: id,
			State: domain.AgentSwitchDelivering, TargetGenerationID: "agent-switch-gen-1",
		},
	}
	m.store = reserved

	_, err := m.ContinueFailover(context.Background(), id, ContinueFailoverRequest{IncidentID: "inc-1"})
	if !errors.Is(err, ErrFailoverRecoveryRequired) {
		t.Fatalf("error = %v, want ErrFailoverRecoveryRequired", err)
	}
	if reserved.appendCalls != 0 || len(reserved.attempts) != 0 {
		t.Fatalf("reserved switch spent a failover rung: append calls=%d attempts=%+v",
			reserved.appendCalls, reserved.attempts)
	}
	if len(rt.destroyedIDs) != 0 || rt.created != 0 {
		t.Fatalf("reserved switch touched runtimes: creates=%d destroys=%v", rt.created, rt.destroyedIDs)
	}
}

func TestContinueFailover_ActiveAgentSwitchReadFailureIsFailClosed(t *testing.T) {
	st, rt, m, id := failoverFixture(t)
	reserved := &agentSwitchReservedFailoverStore{
		failoverFakeStore: st,
		activeErr:         errors.New("injected active switch read failure"),
	}
	m.store = reserved

	_, err := m.ContinueFailover(context.Background(), id, ContinueFailoverRequest{IncidentID: "inc-1"})
	if err == nil || !strings.Contains(err.Error(), "injected active switch read failure") {
		t.Fatalf("error = %v, want active switch read failure", err)
	}
	if reserved.appendCalls != 0 || len(reserved.attempts) != 0 || rt.created != 0 || len(rt.destroyedIDs) != 0 {
		t.Fatalf("failed reservation read mutated state: append=%d attempts=%d creates=%d destroys=%v",
			reserved.appendCalls, len(reserved.attempts), rt.created, rt.destroyedIDs)
	}
}

func TestContinueAutomaticFailover_OwnershipRefusesRealRestartAcrossFirstAttemptWindow(t *testing.T) {
	st, _, m, id := failoverFixture(t)
	enableAutomaticFailover(t, st, m, id)
	entered := make(chan struct{})
	release := make(chan struct{})
	st.afterAppend = func() {
		close(entered)
		<-release
	}
	type automaticOutcome struct {
		result AutomaticFailoverResult
		err    error
	}
	done := make(chan automaticOutcome, 1)
	go func() {
		result, err := m.ContinueAutomaticFailover(context.Background(), id,
			AutomaticFailoverRequest{IncidentID: "inc-1"})
		done <- automaticOutcome{result: result, err: err}
	}()
	<-entered

	if _, err := m.ResumeAgentWithMode(context.Background(), id); !errors.Is(err, ErrResumeInProgress) {
		close(release)
		t.Fatalf("restart during automatic ownership err = %v, want ErrResumeInProgress", err)
	}
	if rec := st.sessions[id]; rec.Metadata.RuntimeLaunchID != "src-gen" {
		close(release)
		t.Fatalf("refused restart changed source generation to %q", rec.Metadata.RuntimeLaunchID)
	}
	close(release)
	outcome := <-done
	if outcome.err != nil {
		t.Fatalf("automatic continuation after refused restart: %v", outcome.err)
	}
	if !outcome.result.Attempted || st.only(t).State != domain.FailoverAttemptAcked {
		t.Fatalf("automatic continuation did not converge: result=%+v attempt=%+v",
			outcome.result, st.only(t))
	}
}

func TestContinueAutomaticFailover_ManualModeWritesNothingAndKeepsPause(t *testing.T) {
	st, rt, m, id := failoverFixture(t)
	structuredLimitPauseAt(st, id, "inc-1")
	m.automaticFailoverCapsOverride = func(h domain.AgentHarness) capabilities.Caps {
		c := testSwitchCaps(h)
		c.LimitDetectionSupported = true
		return c
	}

	res, err := m.ContinueAutomaticFailover(context.Background(), id,
		AutomaticFailoverRequest{IncidentID: "inc-1"})
	if err != nil {
		t.Fatalf("automatic decision: %v", err)
	}
	if res.Enabled || res.Attempted {
		t.Fatalf("manual mode result = %+v", res)
	}
	assertNothingDurable(t, st, id, "inc-1")
	if rt.created != 0 || rt.destroyed != 0 {
		t.Fatal("manual mode touched the runtime through the automatic entry")
	}
}

func TestContinueAutomaticFailover_OperatorPauseNeverActs(t *testing.T) {
	st, rt, m, id := failoverFixture(t)
	project := st.projects["mer"]
	project.Config.RoleMap.Failover.Mode = domain.FailoverModeAutomatic
	st.projects["mer"] = project
	m.automaticFailoverCapsOverride = func(h domain.AgentHarness) capabilities.Caps {
		c := testSwitchCaps(h)
		c.LimitDetectionSupported = true
		return c
	}

	res, err := m.ContinueAutomaticFailover(context.Background(), id,
		AutomaticFailoverRequest{IncidentID: "inc-1"})
	if err != nil {
		t.Fatalf("automatic decision: %v", err)
	}
	if res.Enabled || res.Attempted {
		t.Fatalf("operator pause result = %+v", res)
	}
	assertNothingDurable(t, st, id, "inc-1")
	if rt.created != 0 || rt.destroyed != 0 {
		t.Fatal("an operator pause triggered automatic runtime work")
	}
}

func TestContinueAutomaticFailover_FirstAttemptRequiresObservedSourceGeneration(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*domain.SessionRecord)
	}{
		{
			name: "legacy structured pin has no generation binding",
			mutate: func(rec *domain.SessionRecord) {
				rec.Metadata.Pause.ObservedRuntimeLaunchID = ""
			},
		},
		{
			name: "restart replaced the observed source generation",
			mutate: func(rec *domain.SessionRecord) {
				rec.Metadata.RuntimeLaunchID = "src-restarted"
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			st, rt, m, id := failoverFixture(t)
			enableAutomaticFailover(t, st, m, id)
			rec := st.sessions[id]
			tc.mutate(&rec)
			st.sessions[id] = rec
			before := rec

			res, err := m.ContinueAutomaticFailover(context.Background(), id,
				AutomaticFailoverRequest{IncidentID: "inc-1"})
			if err != nil {
				t.Fatalf("automatic generation refusal: %v", err)
			}
			if res.Enabled || res.Attempted {
				t.Fatalf("generation refusal result = %+v", res)
			}
			assertNothingDurable(t, st, id, "inc-1")
			if got := st.sessions[id]; !reflect.DeepEqual(got, before) {
				t.Fatalf("generation refusal mutated session:\n got %+v\nwant %+v", got, before)
			}
			if rt.created != 0 || rt.destroyed != 0 {
				t.Fatalf("generation refusal touched runtime: created=%d destroyed=%d", rt.created, rt.destroyed)
			}
		})
	}
}

func TestContinueFailover_ManualContinueAcceptsLegacyOrRestartedStructuredPin(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*domain.SessionRecord)
	}{
		{
			name: "legacy unbound",
			mutate: func(rec *domain.SessionRecord) {
				rec.Metadata.Pause.ObservedRuntimeLaunchID = ""
			},
		},
		{
			name: "human restarted generation",
			mutate: func(rec *domain.SessionRecord) {
				rec.Metadata.RuntimeLaunchID = "src-restarted"
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			st, _, m, id := failoverFixture(t)
			structuredLimitPauseAt(st, id, "inc-1")
			rec := st.sessions[id]
			tc.mutate(&rec)
			st.sessions[id] = rec

			res, err := m.ContinueFailover(context.Background(), id,
				ContinueFailoverRequest{IncidentID: "inc-1"})
			if err != nil {
				t.Fatalf("manual continue: %v", err)
			}
			if res.AttemptSeq != 1 || res.Target.Harness != domain.HarnessCodex {
				t.Fatalf("manual continue result = %+v", res)
			}
			if got := st.only(t); got.State != domain.FailoverAttemptAcked {
				t.Fatalf("manual attempt = %+v, want acked", got)
			}
			if st.sessions[id].Metadata.Pause != nil {
				t.Fatal("manual continue did not clear the exact legacy/restarted pin")
			}
		})
	}
}

func TestContinueAutomaticFailover_UnpromotedCapabilitiesStayDormant(t *testing.T) {
	st, rt, m, id := failoverFixture(t)
	project := st.projects["mer"]
	project.Config.RoleMap.Failover.Mode = domain.FailoverModeAutomatic
	st.projects["mer"] = project
	structuredLimitPauseAt(st, id, "inc-1")

	res, err := m.ContinueAutomaticFailover(context.Background(), id,
		AutomaticFailoverRequest{IncidentID: "inc-1"})
	if err != nil {
		t.Fatalf("automatic decision: %v", err)
	}
	if res.Enabled || res.Attempted {
		t.Fatalf("unpromoted result = %+v", res)
	}
	assertNothingDurable(t, st, id, "inc-1")
	if rt.created != 0 || rt.destroyed != 0 {
		t.Fatal("unpromoted automatic mode touched the runtime")
	}
}

func TestContinueAutomaticFailover_UnpromotedTargetRefusesBeforeAttempt(t *testing.T) {
	st, rt, m, id := failoverFixture(t)
	project := st.projects["mer"]
	project.Config.RoleMap.Failover.Mode = domain.FailoverModeAutomatic
	st.projects["mer"] = project
	structuredLimitPauseAt(st, id, "inc-1")
	m.automaticFailoverCapsOverride = func(h domain.AgentHarness) capabilities.Caps {
		c := testSwitchCaps(h)
		c.LimitDetectionSupported = h == domain.HarnessClaudeCode
		return c
	}

	res, err := m.ContinueAutomaticFailover(context.Background(), id,
		AutomaticFailoverRequest{IncidentID: "inc-1"})
	if err != nil {
		t.Fatalf("automatic decision: %v", err)
	}
	if res.Enabled || res.Attempted {
		t.Fatalf("unpromoted target result = %+v", res)
	}
	assertNothingDurable(t, st, id, "inc-1")
	if rt.created != 0 || rt.destroyed != 0 {
		t.Fatal("unpromoted target was reached before capability refusal")
	}
}

func TestContinueAutomaticFailover_RequiresFullSourceAndTargetRuntimeCapabilities(t *testing.T) {
	tests := []struct {
		name    string
		harness domain.AgentHarness
		mutate  func(*capabilities.Caps)
	}{
		{"source spawn", domain.HarnessClaudeCode, func(c *capabilities.Caps) { c.SpawnSupported = false }},
		{"source switch", domain.HarnessClaudeCode, func(c *capabilities.Caps) { c.SwitchSupported = false }},
		{"source limit", domain.HarnessClaudeCode, func(c *capabilities.Caps) { c.LimitDetectionSupported = false }},
		{"target spawn", domain.HarnessCodex, func(c *capabilities.Caps) { c.SpawnSupported = false }},
		{"target switch", domain.HarnessCodex, func(c *capabilities.Caps) { c.SwitchSupported = false }},
		{"target limit", domain.HarnessCodex, func(c *capabilities.Caps) { c.LimitDetectionSupported = false }},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			st, rt, m, id := failoverFixture(t)
			enableAutomaticFailover(t, st, m, id)
			m.automaticFailoverCapsOverride = func(h domain.AgentHarness) capabilities.Caps {
				c := testSwitchCaps(h)
				c.LimitDetectionSupported = true
				if h == tc.harness {
					tc.mutate(&c)
				}
				return c
			}

			res, err := m.ContinueAutomaticFailover(context.Background(), id,
				AutomaticFailoverRequest{IncidentID: "inc-1"})
			if err != nil {
				t.Fatalf("automatic capability refusal: %v", err)
			}
			if res.Enabled || res.Attempted {
				t.Fatalf("capability refusal result = %+v", res)
			}
			assertNothingDurable(t, st, id, "inc-1")
			if rt.created != 0 || rt.destroyed != 0 {
				t.Fatalf("capability refusal touched runtime: created=%d destroyed=%d", rt.created, rt.destroyed)
			}
		})
	}
}

func TestContinueAutomaticFailover_RejectsMalformedStructuredHarnessProvenance(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*domain.SessionRecord)
	}{
		{
			name: "envelope harness absent",
			mutate: func(rec *domain.SessionRecord) {
				rec.Metadata.Pause.EvidenceJSON = `{"version":1,"kind":"usage_limit","scope":"account","sourceKey":"window-1"}`
			},
		},
		{
			name: "envelope and pause disagree",
			mutate: func(rec *domain.SessionRecord) {
				rec.Metadata.Pause.EvidenceJSON = `{"version":1,"kind":"usage_limit","harness":"codex","scope":"account","sourceKey":"window-1"}`
			},
		},
		{
			name: "pause source and current session disagree",
			mutate: func(rec *domain.SessionRecord) {
				rec.Metadata.Pause.Harness = domain.HarnessCodex
				rec.Metadata.Pause.EvidenceJSON = `{"version":1,"kind":"usage_limit","harness":"codex","scope":"account","sourceKey":"window-1"}`
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			st, rt, m, id := failoverFixture(t)
			enableAutomaticFailover(t, st, m, id)
			rec := st.sessions[id]
			tc.mutate(&rec)
			st.sessions[id] = rec

			_, _ = m.ContinueAutomaticFailover(context.Background(), id,
				AutomaticFailoverRequest{IncidentID: "inc-1"})
			assertNothingDurable(t, st, id, "inc-1")
			if rt.created != 0 || rt.destroyed != 0 {
				t.Fatalf("malformed provenance touched runtime: created=%d destroyed=%d", rt.created, rt.destroyed)
			}
		})
	}
}

func TestContinueAutomaticFailover_RecoveryRechecksFullDurableSourceAndTargetCapabilities(t *testing.T) {
	tests := []struct {
		name    string
		harness domain.AgentHarness
		mutate  func(*capabilities.Caps)
	}{
		{"source spawn", domain.HarnessClaudeCode, func(c *capabilities.Caps) { c.SpawnSupported = false }},
		{"source switch", domain.HarnessClaudeCode, func(c *capabilities.Caps) { c.SwitchSupported = false }},
		{"source limit", domain.HarnessClaudeCode, func(c *capabilities.Caps) { c.LimitDetectionSupported = false }},
		{"target spawn", domain.HarnessCodex, func(c *capabilities.Caps) { c.SpawnSupported = false }},
		{"target switch", domain.HarnessCodex, func(c *capabilities.Caps) { c.SwitchSupported = false }},
		{"target limit", domain.HarnessCodex, func(c *capabilities.Caps) { c.LimitDetectionSupported = false }},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			st, rt, m, id := failoverFixture(t)
			enableAutomaticFailover(t, st, m, id)
			now := time.Date(2026, 8, 9, 10, 0, 0, 0, time.UTC)
			attempt := domain.FailoverAttempt{
				ID: domain.FailoverAttemptID(id, "inc-1", 1), SessionID: id, ProjectID: "mer",
				IncidentID: "inc-1", Seq: 1, RoleID: "implementor",
				FromHarness: domain.HarnessClaudeCode, ToHarness: domain.HarnessCodex,
				RungIndex: 0, GenerationID: "gen-requested", State: domain.FailoverAttemptRequested,
				CreatedAt: now, UpdatedAt: now,
			}
			if err := st.AppendSessionFailoverAttemptWithLedger(context.Background(), attempt,
				m.failoverLedgerRecord(attempt, domain.LifecyclePhaseRequested)); err != nil {
				t.Fatalf("seed attempt: %v", err)
			}
			m.automaticFailoverCapsOverride = func(h domain.AgentHarness) capabilities.Caps {
				c := testSwitchCaps(h)
				c.LimitDetectionSupported = true
				if h == tc.harness {
					tc.mutate(&c)
				}
				return c
			}

			res, err := m.ContinueAutomaticFailover(context.Background(), id,
				AutomaticFailoverRequest{IncidentID: "inc-1"})
			if err != nil {
				t.Fatalf("automatic recovery refusal: %v", err)
			}
			if res.Enabled || res.Attempted {
				t.Fatalf("recovery capability refusal result = %+v", res)
			}
			if got := st.only(t); !reflect.DeepEqual(got, attempt) {
				t.Fatalf("recovery capability refusal mutated attempt:\n got %+v\nwant %+v", got, attempt)
			}
			if st.sessions[id].Metadata.Pause == nil || rt.created != 0 || rt.destroyed != 0 {
				t.Fatalf("recovery refusal state: pause=%+v created=%d destroyed=%d",
					st.sessions[id].Metadata.Pause, rt.created, rt.destroyed)
			}
		})
	}
}

func TestContinueAutomaticFailover_AckedUnpromotedRecoveryRechecksTargetCapabilities(t *testing.T) {
	st, rt, m, id := failoverFixture(t)
	enableAutomaticFailover(t, st, m, id)
	m.lcm.(*fakeLCM).markSpawnedErr = errors.New("database is locked")
	if _, err := m.ContinueAutomaticFailover(context.Background(), id,
		AutomaticFailoverRequest{IncidentID: "inc-1"}); !errors.Is(err, ErrSwitchPostStop) {
		t.Fatalf("seed post-stop attempt: %v", err)
	}
	st.attempts[0].State = domain.FailoverAttemptAcked
	beforeAttempt := st.attempts[0]
	createdBefore := rt.created
	m.lcm.(*fakeLCM).markSpawnedErr = nil
	m.automaticFailoverCapsOverride = func(h domain.AgentHarness) capabilities.Caps {
		c := testSwitchCaps(h)
		c.LimitDetectionSupported = h != domain.HarnessCodex
		return c
	}

	res, err := m.ContinueAutomaticFailover(context.Background(), id,
		AutomaticFailoverRequest{IncidentID: "inc-1"})
	if err != nil {
		t.Fatalf("acked recovery capability refusal: %v", err)
	}
	if res.Enabled || res.Attempted {
		t.Fatalf("acked recovery capability refusal result = %+v", res)
	}
	if got := st.only(t); !reflect.DeepEqual(got, beforeAttempt) {
		t.Fatalf("acked recovery capability refusal mutated attempt: %+v -> %+v", beforeAttempt, got)
	}
	if rt.created != createdBefore || st.sessions[id].Metadata.Pause == nil ||
		st.sessions[id].Metadata.SwitchPending == nil {
		t.Fatalf("acked recovery ran under false target cap: created %d->%d pause=%+v pending=%+v",
			createdBefore, rt.created, st.sessions[id].Metadata.Pause, st.sessions[id].Metadata.SwitchPending)
	}
}

func TestContinueAutomaticFailover_FirstAttemptUsesAcceptedSaga(t *testing.T) {
	st, _, m, id := failoverFixture(t)
	enableAutomaticFailover(t, st, m, id)
	before := st.sessions[id].Metadata.Role

	res, err := m.ContinueAutomaticFailover(context.Background(), id,
		AutomaticFailoverRequest{IncidentID: "inc-1"})
	if err != nil {
		t.Fatalf("automatic failover: %v", err)
	}
	if !res.Enabled || !res.Attempted {
		t.Fatalf("result = %+v, want enabled+attempted", res)
	}
	if res.Continue.Target.Harness != domain.HarnessCodex || res.Continue.RungIndex != 0 ||
		res.Continue.AttemptSeq != 1 {
		t.Fatalf("continue result = %+v", res.Continue)
	}
	att := st.only(t)
	if att.State != domain.FailoverAttemptAcked || att.GenerationID != res.Continue.GenerationID {
		t.Fatalf("attempt/result mismatch: %+v / %+v", att, res.Continue)
	}
	if st.sessions[id].Metadata.Pause != nil {
		t.Fatal("automatic target ack did not clear the exact incident pin")
	}
	var phases []domain.LifecycleLedgerPhase
	for _, event := range st.ledger {
		if event.Kind != domain.LifecycleKindFailover {
			continue
		}
		phases = append(phases, event.Phase)
		if event.GenerationID != att.GenerationID {
			t.Fatalf("automatic ledger generation %q != attempt %q", event.GenerationID, att.GenerationID)
		}
	}
	if len(phases) != 2 || phases[0] != domain.LifecyclePhaseRequested ||
		phases[1] != domain.LifecyclePhaseTargetAck {
		t.Fatalf("automatic failover phases = %v, want [requested target_ack]", phases)
	}
	after := st.sessions[id].Metadata.Role
	if after.RoleID != before.RoleID || after.TemplateArtifactID != before.TemplateArtifactID ||
		after.TemplateSHA256 != before.TemplateSHA256 || after.ResolvedPermissions != before.ResolvedPermissions {
		t.Fatalf("automatic failover changed role authority: %+v -> %+v", before, after)
	}
}

func TestContinueAutomaticFailover_DuplicateAfterPreStopFailureDoesNotSpendSecondRung(t *testing.T) {
	st, rt, m, id := failoverFixture(t)
	failoverLadder(st,
		domain.FailoverTarget{Harness: domain.HarnessCodex},
		domain.FailoverTarget{Harness: domain.HarnessClaudeCode, Model: "fallback-model"},
	)
	enableAutomaticFailover(t, st, m, id)
	rt.aliveByHandle = map[string]bool{"rt-1": true}
	rt.destroyLeavesAlive = true

	first, err := m.ContinueAutomaticFailover(context.Background(), id,
		AutomaticFailoverRequest{IncidentID: "inc-1"})
	if err == nil || !first.Attempted {
		t.Fatalf("first result=%+v err=%v, want failed attempted rung", first, err)
	}
	if st.only(t).State != domain.FailoverAttemptFailed {
		t.Fatal("first automatic pre-stop failure was not terminal")
	}

	duplicate, err := m.ContinueAutomaticFailover(context.Background(), id,
		AutomaticFailoverRequest{IncidentID: "inc-1"})
	if err != nil {
		t.Fatalf("duplicate automatic delivery: %v", err)
	}
	if !duplicate.Enabled || duplicate.Attempted {
		t.Fatalf("duplicate result = %+v, want enabled no-op", duplicate)
	}
	if len(st.attempts) != 1 || rt.created != 0 {
		t.Fatalf("duplicate spent another rung: attempts=%d target launches=%d", len(st.attempts), rt.created)
	}

	// The operator still owns recovery: explicit manual Continue may spend the
	// next authorized rung after the automatic attempt failed safely pre-stop.
	rt.aliveByHandle = map[string]bool{}
	manual, err := m.ContinueFailover(context.Background(), id,
		ContinueFailoverRequest{IncidentID: "inc-1"})
	if err != nil {
		t.Fatalf("manual Continue after automatic failure: %v", err)
	}
	if manual.Target.Harness != domain.HarnessClaudeCode || manual.Target.Model != "fallback-model" ||
		manual.AttemptSeq != 2 {
		t.Fatalf("manual result = %+v, want second rung/seq", manual)
	}
	if len(st.attempts) != 2 || st.sessions[id].Metadata.Pause != nil {
		t.Fatalf("manual recovery state: attempts=%d pause=%+v", len(st.attempts), st.sessions[id].Metadata.Pause)
	}
}

func TestContinueAutomaticFailover_AdoptsPostStopSameGeneration(t *testing.T) {
	st, _, m, id := failoverFixture(t)
	enableAutomaticFailover(t, st, m, id)
	m.lcm.(*fakeLCM).markSpawnedErr = errors.New("database is locked")

	first, err := m.ContinueAutomaticFailover(context.Background(), id,
		AutomaticFailoverRequest{IncidentID: "inc-1"})
	if !errors.Is(err, ErrSwitchPostStop) || !first.Attempted {
		t.Fatalf("first result=%+v err=%v, want post-stop failure", first, err)
	}
	generation := st.only(t).GenerationID
	m.lcm.(*fakeLCM).markSpawnedErr = nil

	second, err := m.ContinueAutomaticFailover(context.Background(), id,
		AutomaticFailoverRequest{IncidentID: "inc-1"})
	if err != nil {
		t.Fatalf("automatic same-attempt recovery: %v", err)
	}
	if !second.Attempted || !second.Continue.Reused || second.Continue.GenerationID != generation {
		t.Fatalf("recovery = %+v, want reused generation %q", second, generation)
	}
	if len(st.attempts) != 1 || st.sessions[id].Metadata.Pause != nil {
		t.Fatalf("recovery spent another rung or kept pause: attempts=%d pause=%+v",
			len(st.attempts), st.sessions[id].Metadata.Pause)
	}
}

func TestContinueAutomaticFailover_ConcurrentDeliveryCreatesOneAttempt(t *testing.T) {
	st, base, m, id := failoverFixture(t)
	blocking := &blockingDestroyRuntime{
		fakeRuntime: base,
		entered:     make(chan struct{}),
		release:     make(chan struct{}),
	}
	m.runtime = blocking
	enableAutomaticFailover(t, st, m, id)

	done := make(chan error, 1)
	go func() {
		_, err := m.ContinueAutomaticFailover(context.Background(), id,
			AutomaticFailoverRequest{IncidentID: "inc-1"})
		done <- err
	}()
	<-blocking.entered

	duplicate, err := m.ContinueAutomaticFailover(context.Background(), id,
		AutomaticFailoverRequest{IncidentID: "inc-1"})
	if err != nil || duplicate.Attempted {
		t.Fatalf("concurrent duplicate = %+v err=%v", duplicate, err)
	}
	close(blocking.release)
	if err := <-done; err != nil {
		t.Fatalf("winning automatic call: %v", err)
	}
	if len(st.attempts) != 1 || base.created != 1 {
		t.Fatalf("concurrent delivery created attempts/runtime = %d/%d, want 1/1", len(st.attempts), base.created)
	}
}

func TestContinueAutomaticFailover_RacingManualAdopterOwnsSharedAttempt(t *testing.T) {
	st, base, m, id := failoverFixture(t)
	enableAutomaticFailover(t, st, m, id)
	appended := make(chan struct{})
	releaseAppend := make(chan struct{})
	st.afterAppend = func() {
		close(appended)
		<-releaseAppend
	}
	blocking := &blockingDestroyRuntime{
		fakeRuntime: base,
		entered:     make(chan struct{}),
		release:     make(chan struct{}),
	}
	m.runtime = blocking

	type outcome struct {
		res ContinueFailoverResult
		err error
	}
	autoDone := make(chan error, 1)
	go func() {
		_, err := m.ContinueAutomaticFailover(context.Background(), id,
			AutomaticFailoverRequest{IncidentID: "inc-1"})
		autoDone <- err
	}()
	select {
	case <-appended:
	case <-time.After(3 * time.Second):
		t.Fatal("automatic owner did not durably append the attempt")
	}

	manualDone := make(chan outcome, 1)
	go func() {
		res, err := m.ContinueFailover(context.Background(), id,
			ContinueFailoverRequest{IncidentID: "inc-1"})
		manualDone <- outcome{res: res, err: err}
	}()
	select {
	case <-blocking.entered:
	case <-time.After(3 * time.Second):
		close(releaseAppend)
		t.Fatal("manual adopter did not acquire switch ownership")
	}
	close(releaseAppend)
	if err := <-autoDone; !errors.Is(err, ErrSwitchOperationInProgress) {
		close(blocking.release)
		t.Fatalf("losing automatic owner error = %v, want ErrSwitchOperationInProgress", err)
	}
	if state := st.only(t).State; state != domain.FailoverAttemptRequested {
		close(blocking.release)
		t.Fatalf("losing owner changed shared attempt to %q", state)
	}
	close(blocking.release)
	manual := <-manualDone
	if manual.err != nil {
		t.Fatalf("manual adopter: %v", manual.err)
	}
	att := st.only(t)
	if att.State != domain.FailoverAttemptAcked || manual.res.GenerationID != att.GenerationID {
		t.Fatalf("manual result/attempt = %+v / %+v", manual.res, att)
	}
	if base.created != 1 || base.destroyed != 1 || st.sessions[id].Metadata.Pause != nil {
		t.Fatalf("runtime/pause = created %d destroyed %d pause %+v", base.created, base.destroyed, st.sessions[id].Metadata.Pause)
	}
}

func TestContinueAutomaticFailover_ManualAdopterCompletesBeforeOriginalResumes(t *testing.T) {
	st, rt, m, id := failoverFixture(t)
	enableAutomaticFailover(t, st, m, id)
	appended := make(chan struct{})
	releaseAppend := make(chan struct{})
	st.afterAppend = func() {
		close(appended)
		<-releaseAppend
	}

	type automaticOutcome struct {
		res AutomaticFailoverResult
		err error
	}
	autoDone := make(chan automaticOutcome, 1)
	go func() {
		res, err := m.ContinueAutomaticFailover(context.Background(), id,
			AutomaticFailoverRequest{IncidentID: "inc-1"})
		autoDone <- automaticOutcome{res: res, err: err}
	}()
	select {
	case <-appended:
	case <-time.After(3 * time.Second):
		t.Fatal("automatic owner did not durably append the attempt")
	}

	manual, err := m.ContinueFailover(context.Background(), id,
		ContinueFailoverRequest{IncidentID: "inc-1"})
	if err != nil {
		close(releaseAppend)
		t.Fatalf("manual adopter: %v", err)
	}
	if !manual.Reused || st.only(t).State != domain.FailoverAttemptAcked ||
		st.sessions[id].Metadata.Pause != nil {
		close(releaseAppend)
		t.Fatalf("manual completion = %+v attempt=%+v pause=%+v",
			manual, st.only(t), st.sessions[id].Metadata.Pause)
	}
	close(releaseAppend)
	auto := <-autoDone
	if auto.err != nil {
		t.Fatalf("original owner did not converge on the completed attempt: %v", auto.err)
	}
	if !auto.res.Continue.Reused || auto.res.Continue.GenerationID != manual.GenerationID {
		t.Fatalf("original owner result = %+v, manual = %+v", auto.res, manual)
	}
	if rt.created != 1 || rt.destroyed != 1 || len(st.attempts) != 1 {
		t.Fatalf("completion race runtime/attempts = %d/%d/%d", rt.created, rt.destroyed, len(st.attempts))
	}
	for _, event := range st.ledger {
		if event.Kind == domain.LifecycleKindFailover && event.Phase == domain.LifecyclePhaseFailed {
			t.Fatalf("completed shared attempt gained a contradictory failed ledger: %+v", event)
		}
	}
}

func TestContinueAutomaticFailover_ResumeAfterAppendRevokesSwitchAuthority(t *testing.T) {
	st, rt, m, id := failoverFixture(t)
	enableAutomaticFailover(t, st, m, id)
	st.afterAppend = func() {
		rec := st.sessions[id]
		rec.Metadata.Pause = nil
		st.sessions[id] = rec
	}

	_, err := m.ContinueAutomaticFailover(context.Background(), id,
		AutomaticFailoverRequest{IncidentID: "inc-1"})
	if !errors.Is(err, ErrSwitchPaused) {
		t.Fatalf("automatic after Resume error = %v, want ErrSwitchPaused", err)
	}
	if rt.created != 0 || rt.destroyed != 0 {
		t.Fatalf("revoked incident touched runtime: created=%d destroyed=%d", rt.created, rt.destroyed)
	}
	if st.sessions[id].Metadata.Pause != nil {
		t.Fatal("automatic failover recreated a pause that Resume cleared")
	}
	if att := st.only(t); att.State != domain.FailoverAttemptFailed {
		t.Fatalf("revoked pre-stop attempt state = %q, want failed", att.State)
	}
	for _, event := range st.ledger {
		if event.Kind == domain.LifecycleKindFailover && event.Phase == domain.LifecyclePhaseTargetAck {
			t.Fatalf("revoked incident wrote a false target ack: %+v", event)
		}
	}
}

func TestContinueAutomaticFailover_ErrorPathSurfacesLatestStateFailure(t *testing.T) {
	t.Run("read error", func(t *testing.T) {
		st, _, m, id := failoverFixture(t)
		enableAutomaticFailover(t, st, m, id)
		st.afterAppend = func() {
			rec := st.sessions[id]
			rec.Metadata.Pause = nil
			st.sessions[id] = rec
		}
		readErr := errors.New("latest state unavailable")
		st.beforeUpdate = func() { st.getSessionErr = readErr }

		_, err := m.ContinueAutomaticFailover(context.Background(), id,
			AutomaticFailoverRequest{IncidentID: "inc-1"})
		if !errors.Is(err, ErrSwitchPaused) || !errors.Is(err, readErr) {
			t.Fatalf("joined latest-state error = %v", err)
		}
	})

	t.Run("missing session", func(t *testing.T) {
		st, _, m, id := failoverFixture(t)
		enableAutomaticFailover(t, st, m, id)
		st.afterAppend = func() {
			rec := st.sessions[id]
			rec.Metadata.Pause = nil
			st.sessions[id] = rec
		}
		st.beforeUpdate = func() { delete(st.sessions, id) }

		_, err := m.ContinueAutomaticFailover(context.Background(), id,
			AutomaticFailoverRequest{IncidentID: "inc-1"})
		if !errors.Is(err, ErrSwitchPaused) || !errors.Is(err, ErrNotFound) {
			t.Fatalf("joined missing-state error = %v", err)
		}
	})
}

func TestContinueAutomaticFailover_FailureCASMissFailsLoudlyWithoutFalseLedger(t *testing.T) {
	assertNoFailedLedger := func(t *testing.T, st *failoverFakeStore) {
		t.Helper()
		for _, event := range st.ledger {
			if event.Kind == domain.LifecycleKindFailover && event.Phase == domain.LifecyclePhaseFailed {
				t.Fatalf("failure CAS loser wrote failed ledger: %+v", event)
			}
		}
	}

	t.Run("attempt disappeared", func(t *testing.T) {
		st, _, m, id := failoverFixture(t)
		enableAutomaticFailover(t, st, m, id)
		st.afterAppend = func() {
			rec := st.sessions[id]
			rec.Metadata.Pause = nil
			st.sessions[id] = rec
		}
		st.beforeUpdate = func() { st.attempts = nil }

		_, err := m.ContinueAutomaticFailover(context.Background(), id,
			AutomaticFailoverRequest{IncidentID: "inc-1"})
		if !errors.Is(err, ErrSwitchPaused) || !errors.Is(err, ErrFailoverRecoveryRequired) {
			t.Fatalf("missing attempt CAS-miss error = %v", err)
		}
		assertNoFailedLedger(t, st)
	})

	t.Run("attempt changed to unexpected non-acked state", func(t *testing.T) {
		st, _, m, id := failoverFixture(t)
		enableAutomaticFailover(t, st, m, id)
		st.afterAppend = func() {
			rec := st.sessions[id]
			rec.Metadata.Pause = nil
			st.sessions[id] = rec
		}
		st.beforeUpdate = func() { st.attempts[0].State = domain.FailoverAttemptPostStop }

		_, err := m.ContinueAutomaticFailover(context.Background(), id,
			AutomaticFailoverRequest{IncidentID: "inc-1"})
		if !errors.Is(err, ErrSwitchPaused) || !errors.Is(err, ErrFailoverRecoveryRequired) {
			t.Fatalf("unexpected-state CAS-miss error = %v", err)
		}
		if got := st.only(t).State; got != domain.FailoverAttemptPostStop {
			t.Fatalf("CAS loser changed unexpected state to %q", got)
		}
		assertNoFailedLedger(t, st)
	})

	t.Run("acked attempt latest read error", func(t *testing.T) {
		st, _, m, id := failoverFixture(t)
		enableAutomaticFailover(t, st, m, id)
		st.afterAppend = func() {
			rec := st.sessions[id]
			rec.Metadata.Pause = nil
			st.sessions[id] = rec
		}
		readErr := errors.New("latest session unavailable")
		st.beforeUpdate = func() {
			st.attempts[0].State = domain.FailoverAttemptAcked
			st.getSessionErr = readErr
		}

		_, err := m.ContinueAutomaticFailover(context.Background(), id,
			AutomaticFailoverRequest{IncidentID: "inc-1"})
		if !errors.Is(err, ErrSwitchPaused) || !errors.Is(err, readErr) {
			t.Fatalf("acked/read-error CAS-miss error = %v", err)
		}
		assertNoFailedLedger(t, st)
	})

	t.Run("acked attempt latest session missing", func(t *testing.T) {
		st, _, m, id := failoverFixture(t)
		enableAutomaticFailover(t, st, m, id)
		st.afterAppend = func() {
			rec := st.sessions[id]
			rec.Metadata.Pause = nil
			st.sessions[id] = rec
		}
		st.beforeUpdate = func() {
			st.attempts[0].State = domain.FailoverAttemptAcked
			delete(st.sessions, id)
		}

		_, err := m.ContinueAutomaticFailover(context.Background(), id,
			AutomaticFailoverRequest{IncidentID: "inc-1"})
		if !errors.Is(err, ErrSwitchPaused) || !errors.Is(err, ErrNotFound) {
			t.Fatalf("acked/missing-session CAS-miss error = %v", err)
		}
		assertNoFailedLedger(t, st)
	})
}

func TestReconcile_AutomaticFailoverClosesCrashAfterPauseBeforeAttempt(t *testing.T) {
	st, rt, m, id := failoverFixture(t)
	enableAutomaticFailover(t, st, m, id)
	// The switch sees the old source as gone after Destroy; the later live pass
	// sees the newly created deterministic fake handle as alive.
	rt.aliveByHandle = map[string]bool{"h1": true}

	if err := m.Reconcile(context.Background()); err != nil {
		t.Fatalf("boot reconcile: %v", err)
	}
	att := st.only(t)
	if att.State != domain.FailoverAttemptAcked || rt.created != 1 {
		t.Fatalf("boot attempt/runtime = %+v/%d", att, rt.created)
	}
	if st.sessions[id].Metadata.Pause != nil {
		t.Fatal("boot did not finish the exact opted-in automatic incident")
	}
}

func TestReconcile_AutomaticFirstAttemptDoesNotReinterpretLegacyOrRestartedPin(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*domain.SessionRecord)
	}{
		{
			name: "legacy structured pin",
			mutate: func(rec *domain.SessionRecord) {
				rec.Metadata.Pause.ObservedRuntimeLaunchID = ""
			},
		},
		{
			name: "restart created a new current generation",
			mutate: func(rec *domain.SessionRecord) {
				rec.Metadata.RuntimeLaunchID = "src-restarted"
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			st, rt, m, id := failoverFixture(t)
			enableAutomaticFailover(t, st, m, id)
			rec := st.sessions[id]
			tc.mutate(&rec)
			st.sessions[id] = rec
			before := rec
			rt.aliveByHandle = map[string]bool{rec.Metadata.RuntimeHandleID: true}

			if err := m.Reconcile(context.Background()); err != nil {
				t.Fatalf("boot reconcile: %v", err)
			}
			assertNothingDurable(t, st, id, "inc-1")
			if got := st.sessions[id]; !reflect.DeepEqual(got, before) {
				t.Fatalf("boot generation refusal mutated session:\n got %+v\nwant %+v", got, before)
			}
			if rt.created != 0 || rt.destroyed != 0 {
				t.Fatalf("boot generation refusal touched runtime: created=%d destroyed=%d", rt.created, rt.destroyed)
			}
		})
	}
}

func TestReconcile_AutomaticBoundPauseDoesNotSwitchHumanRestartGeneration(t *testing.T) {
	st, _, _, id := failoverFixture(t)
	project := st.projects["mer"]
	project.Config.RoleMap.Failover.Mode = domain.FailoverModeAutomatic
	st.projects["mer"] = project
	structuredLimitPauseAt(st, id, "inc-1")
	rec := st.sessions[id]
	rec.Activity = domain.Activity{State: domain.ActivityExited}
	st.sessions[id] = rec

	baseRuntime := &fakeRuntime{aliveByHandle: map[string]bool{"rt-1": true}}
	restartRuntime := &fakeRestartRuntime{fakeRuntime: baseRuntime}
	restartManager := New(Deps{
		Runtime: restartRuntime, Agents: singleAgent{agent: &recordingAgent{}}, Workspace: &fakeWorkspace{},
		Store: st, Messenger: &fakeMessenger{}, Lifecycle: &fakeLCM{store: st.fakeStore},
		DataDir: t.TempDir(), LookPath: func(string) (string, error) { return "/bin/true", nil },
		NewLaunchID: func() string { return "src-restarted" },
	})
	if _, err := restartManager.ResumeAgentWithMode(context.Background(), id); err != nil {
		t.Fatalf("restart agent: %v", err)
	}
	restarted := st.sessions[id]
	if restarted.Metadata.RuntimeLaunchID != "src-restarted" ||
		restarted.Metadata.Pause == nil ||
		restarted.Metadata.Pause.ObservedRuntimeLaunchID != "src-gen" {
		t.Fatalf("restart did not preserve the old pause binding: %+v", restarted.Metadata)
	}

	bootManager := New(Deps{
		Runtime: restartRuntime, Agents: singleAgent{agent: &recordingAgent{}}, Workspace: &fakeWorkspace{},
		Store: st, Messenger: &fakeMessenger{}, Lifecycle: &fakeLCM{store: st.fakeStore},
		DataDir: t.TempDir(), LookPath: func(string) (string, error) { return "/bin/true", nil },
	})
	bootManager.switchCapsOverride = testSwitchCaps
	bootManager.automaticFailoverCapsOverride = func(h domain.AgentHarness) capabilities.Caps {
		c := testSwitchCaps(h)
		c.LimitDetectionSupported = true
		return c
	}
	if err := bootManager.Reconcile(context.Background()); err != nil {
		t.Fatalf("boot reconcile: %v", err)
	}

	got := st.sessions[id]
	if len(st.attempts) != 0 || baseRuntime.created != 0 || baseRuntime.destroyed != 0 {
		t.Fatalf("boot acted on replacement generation: attempts=%d created=%d destroyed=%d",
			len(st.attempts), baseRuntime.created, baseRuntime.destroyed)
	}
	if restartRuntime.restarted != 1 || got.Metadata.RuntimeLaunchID != "src-restarted" ||
		got.Metadata.Pause == nil || got.Metadata.Pause.ObservedRuntimeLaunchID != "src-gen" {
		t.Fatalf("boot changed restarted runtime/pause: restarts=%d session=%+v",
			restartRuntime.restarted, got)
	}
}

func TestReconcile_UnpromotedAutomaticStructuredPauseRemainsPassive(t *testing.T) {
	st, rt, m, id := failoverFixture(t)
	project := st.projects["mer"]
	project.Config.RoleMap.Failover.Mode = domain.FailoverModeAutomatic
	st.projects["mer"] = project
	structuredLimitPauseAt(st, id, "inc-1")
	rt.aliveByHandle = map[string]bool{"rt-1": true}

	if err := m.Reconcile(context.Background()); err != nil {
		t.Fatalf("boot reconcile: %v", err)
	}
	if len(st.attempts) != 0 || rt.created != 0 || rt.destroyed != 0 {
		t.Fatalf("unpromoted boot acted: attempts=%d created=%d destroyed=%d",
			len(st.attempts), rt.created, rt.destroyed)
	}
	if st.sessions[id].Metadata.Pause == nil {
		t.Fatal("unpromoted boot cleared the structured pause")
	}
}

func TestReconcile_AutomaticTerminalFailureNeverAdvancesAnotherRung(t *testing.T) {
	st, rt, m, id := failoverFixture(t)
	enableAutomaticFailover(t, st, m, id)
	rt.aliveByHandle = map[string]bool{"rt-1": true}
	rt.destroyLeavesAlive = true
	if _, err := m.ContinueAutomaticFailover(context.Background(), id,
		AutomaticFailoverRequest{IncidentID: "inc-1"}); err == nil {
		t.Fatal("expected the first pre-stop failure")
	}
	if st.only(t).State != domain.FailoverAttemptFailed {
		t.Fatal("fixture did not reach a terminal failed attempt")
	}

	if err := m.Reconcile(context.Background()); err != nil {
		t.Fatalf("boot reconcile: %v", err)
	}
	if len(st.attempts) != 1 || rt.created != 0 {
		t.Fatalf("boot advanced a terminal automatic incident: attempts=%d target launches=%d",
			len(st.attempts), rt.created)
	}
	if st.sessions[id].Metadata.Pause == nil {
		t.Fatal("boot lifted the pause after a terminal automatic failure")
	}
}

func TestReconcile_AutomaticPostStopRecoversTheSameGeneration(t *testing.T) {
	st, rt, m, id := failoverFixture(t)
	enableAutomaticFailover(t, st, m, id)
	m.lcm.(*fakeLCM).markSpawnedErr = errors.New("database is locked")
	if _, err := m.ContinueAutomaticFailover(context.Background(), id,
		AutomaticFailoverRequest{IncidentID: "inc-1"}); !errors.Is(err, ErrSwitchPostStop) {
		t.Fatalf("first automatic attempt err = %v, want ErrSwitchPostStop", err)
	}
	generation := st.only(t).GenerationID
	m.lcm.(*fakeLCM).markSpawnedErr = nil
	// Once the attempt is durable, recovery follows its target/generation even
	// if this is a legacy pin without the newly added source-generation field.
	rec := st.sessions[id]
	rec.Metadata.Pause.ObservedRuntimeLaunchID = ""
	st.sessions[id] = rec
	rt.aliveByHandle = map[string]bool{"h1": true}

	if err := m.Reconcile(context.Background()); err != nil {
		t.Fatalf("boot reconcile: %v", err)
	}
	att := st.only(t)
	if att.GenerationID != generation || att.State != domain.FailoverAttemptAcked {
		t.Fatalf("boot changed attempt identity/state: %+v, want generation %q acked", att, generation)
	}
	if len(st.attempts) != 1 || st.sessions[id].Metadata.Pause != nil {
		t.Fatalf("boot spent another rung or kept the pin: attempts=%d pause=%+v",
			len(st.attempts), st.sessions[id].Metadata.Pause)
	}
}

func TestReconcile_AutomaticRequestedAttemptRedrivesTheSameGeneration(t *testing.T) {
	st, rt, m, id := failoverFixture(t)
	enableAutomaticFailover(t, st, m, id)
	now := time.Date(2026, 8, 9, 10, 0, 0, 0, time.UTC)
	attempt := domain.FailoverAttempt{
		ID: domain.FailoverAttemptID(id, "inc-1", 1), SessionID: id, ProjectID: "mer",
		IncidentID: "inc-1", Seq: 1, RoleID: "implementor",
		FromHarness: domain.HarnessClaudeCode, FromModel: "claude-sonnet-source",
		ToHarness: domain.HarnessCodex, RungIndex: 0, GenerationID: "gen-requested",
		State: domain.FailoverAttemptRequested, CreatedAt: now, UpdatedAt: now,
	}
	if err := st.AppendSessionFailoverAttemptWithLedger(context.Background(), attempt,
		m.failoverLedgerRecord(attempt, domain.LifecyclePhaseRequested)); err != nil {
		t.Fatalf("seed requested attempt: %v", err)
	}
	// Requested recovery is authorized by the durable attempt identity. A
	// mismatch here must not reinterpret the pin to select a new rung, but it
	// also must not strand the already-selected generation.
	rec := st.sessions[id]
	rec.Metadata.Pause.ObservedRuntimeLaunchID = "older-source-generation"
	st.sessions[id] = rec
	rt.aliveByHandle = map[string]bool{"h1": true}

	if err := m.Reconcile(context.Background()); err != nil {
		t.Fatalf("boot reconcile: %v", err)
	}
	att := st.only(t)
	if att.GenerationID != "gen-requested" || att.State != domain.FailoverAttemptAcked {
		t.Fatalf("boot changed requested attempt identity/state: %+v", att)
	}
	if len(st.attempts) != 1 || st.sessions[id].Metadata.Pause != nil {
		t.Fatalf("boot selected a new rung or kept pause: attempts=%d pause=%+v",
			len(st.attempts), st.sessions[id].Metadata.Pause)
	}
}

func TestReconcile_AutomaticAckedPromotionOnlyClearsExactPause(t *testing.T) {
	st, rt, m, id := failoverFixture(t)
	enableAutomaticFailover(t, st, m, id)
	now := time.Date(2026, 8, 9, 10, 0, 0, 0, time.UTC)
	st.attempts = append(st.attempts, domain.FailoverAttempt{
		ID: domain.FailoverAttemptID(id, "inc-1", 1), SessionID: id, ProjectID: "mer",
		IncidentID: "inc-1", Seq: 1, RoleID: "implementor",
		FromHarness: domain.HarnessClaudeCode, ToHarness: domain.HarnessCodex,
		RungIndex: 0, GenerationID: "gen-acked", State: domain.FailoverAttemptAcked,
		CreatedAt: now, UpdatedAt: now,
	})
	rec := st.sessions[id]
	rec.Harness = domain.HarnessCodex
	rec.Metadata.Role.ResolvedModel = ""
	rec.Metadata.RuntimeLaunchID = "gen-acked"
	rec.Metadata.SwitchPending = nil
	st.sessions[id] = rec
	rt.aliveByHandle = map[string]bool{rec.Metadata.RuntimeHandleID: true}

	if err := m.Reconcile(context.Background()); err != nil {
		t.Fatalf("boot reconcile: %v", err)
	}
	if st.sessions[id].Metadata.Pause != nil {
		t.Fatal("boot did not clear the exact pin after proven target promotion")
	}
	if rt.created != 0 || rt.destroyed != 0 || len(st.attempts) != 1 {
		t.Fatalf("settled ack did runtime/new-rung work: created=%d destroyed=%d attempts=%d",
			rt.created, rt.destroyed, len(st.attempts))
	}
}

func TestReconcile_AutomaticTargetAckBeforePromotionConvergesSameGeneration(t *testing.T) {
	st, rt, m, id := failoverFixture(t)
	enableAutomaticFailover(t, st, m, id)
	// SwitchWorker writes pending, clears the source, rotates credentials,
	// re-pins the live target, then promotes. Fail only that final promotion so
	// target_ack and the target generation are durable while the row still names
	// the source harness.
	st.updateFailAfter = 5
	st.updateErr = errors.New("promotion write failed")
	if _, err := m.ContinueAutomaticFailover(context.Background(), id,
		AutomaticFailoverRequest{IncidentID: "inc-1"}); !errors.Is(err, ErrSwitchPostStop) {
		t.Fatalf("seed ack-before-promotion crash: %v", err)
	}
	before := st.only(t)
	if before.State != domain.FailoverAttemptPostStop {
		t.Fatalf("seed attempt state = %q, want post_stop before ledger reconciliation", before.State)
	}
	rec := st.sessions[id]
	if rec.Metadata.SwitchPending == nil || rec.Metadata.RuntimeLaunchID != before.GenerationID ||
		rec.Harness != domain.HarnessClaudeCode {
		t.Fatalf("seed crash facts = harness %q launch %q pending %+v", rec.Harness, rec.Metadata.RuntimeLaunchID, rec.Metadata.SwitchPending)
	}
	createdBefore := rt.created
	rt.aliveByHandle = map[string]bool{rec.Metadata.RuntimeHandleID: true}
	st.updateFailAfter = 0
	st.updateErr = nil

	if err := m.Reconcile(context.Background()); err != nil {
		t.Fatalf("boot reconcile: %v", err)
	}
	after := st.only(t)
	final := st.sessions[id]
	if after.ID != before.ID || after.GenerationID != before.GenerationID ||
		after.State != domain.FailoverAttemptAcked {
		t.Fatalf("boot changed attempt identity/state: %+v -> %+v", before, after)
	}
	if len(st.attempts) != 1 || rt.created != createdBefore {
		t.Fatalf("boot opened a second rung/runtime: attempts=%d created=%d->%d",
			len(st.attempts), createdBefore, rt.created)
	}
	if final.Harness != domain.HarnessCodex || final.Metadata.RuntimeLaunchID != before.GenerationID ||
		final.Metadata.SwitchPending != nil || final.Metadata.Pause != nil {
		t.Fatalf("boot convergence = harness %q launch %q pending %+v pause %+v",
			final.Harness, final.Metadata.RuntimeLaunchID, final.Metadata.SwitchPending, final.Metadata.Pause)
	}
}

func TestReconcile_AutomaticModeChangedToManualRemainsPaused(t *testing.T) {
	st, rt, m, id := failoverFixture(t)
	enableAutomaticFailover(t, st, m, id)
	project := st.projects["mer"]
	project.Config.RoleMap.Failover.Mode = domain.FailoverModeManual
	st.projects["mer"] = project
	rt.aliveByHandle = map[string]bool{st.sessions[id].Metadata.RuntimeHandleID: true}

	if err := m.Reconcile(context.Background()); err != nil {
		t.Fatalf("boot reconcile: %v", err)
	}
	assertNothingDurable(t, st, id, "inc-1")
	if rt.created != 0 || rt.destroyed != 0 {
		t.Fatalf("manual-at-boot mode touched runtime: created=%d destroyed=%d", rt.created, rt.destroyed)
	}
}

// --- refusals, all before any durable write --------------------------------

func assertNothingDurable(t *testing.T, st *failoverFakeStore, id domain.SessionID, incident string) {
	t.Helper()
	if len(st.attempts) != 0 {
		t.Fatalf("a refused Continue wrote %d attempt rows", len(st.attempts))
	}
	for _, e := range st.ledger {
		if e.Kind == domain.LifecycleKindFailover {
			t.Fatalf("a refused Continue wrote a failover ledger row: %+v", e)
		}
	}
	rec := st.sessions[id]
	if rec.Metadata.Pause == nil || rec.Metadata.Pause.IncidentID != incident {
		t.Fatalf("a refused Continue disturbed the pause pin: %+v", rec.Metadata.Pause)
	}
}

func TestContinueFailover_NoRolePinRefusedBeforeAnyDurableWrite(t *testing.T) {
	st, rt, m, id := failoverFixture(t)
	rec := st.sessions[id]
	rec.Metadata.Role.RoleID = ""
	st.sessions[id] = rec

	_, err := m.ContinueFailover(context.Background(), id, ContinueFailoverRequest{IncidentID: "inc-1"})
	if !errors.Is(err, domain.ErrFailoverRoleRequired) {
		t.Fatalf("err = %v, want ErrFailoverRoleRequired", err)
	}
	assertNothingDurable(t, st, id, "inc-1")
	if rt.destroyed != 0 || rt.created != 0 {
		t.Fatal("a refused Continue touched the runtime")
	}
}

func TestContinueFailover_NoLadderRefusedAndPauseIntact(t *testing.T) {
	st, _, m, id := failoverFixture(t)
	failoverLadder(st) // role exists, ladder empty

	_, err := m.ContinueFailover(context.Background(), id, ContinueFailoverRequest{IncidentID: "inc-1"})
	if !errors.Is(err, domain.ErrFailoverNoTarget) {
		t.Fatalf("err = %v, want ErrFailoverNoTarget", err)
	}
	assertNothingDurable(t, st, id, "inc-1")
}

// The ladder is exhausted rather than absent: every rung has been spent on this
// incident. Same refusal, and the pin survives -- FAILOVER_NO_TARGET is not a
// failure of the pause.
func TestContinueFailover_ExhaustedLadderRefusedAndPauseIntact(t *testing.T) {
	st, rt, m, id := failoverFixture(t)
	for i, h := range []domain.AgentHarness{domain.HarnessCodex, domain.HarnessFake} {
		st.attempts = append(st.attempts, domain.FailoverAttempt{
			ID: domain.FailoverAttemptID(id, "inc-1", i+1), SessionID: id, ProjectID: "mer",
			IncidentID: "inc-1", Seq: i + 1, GenerationID: fmt.Sprintf("g%d", i+1),
			ToHarness: h, State: domain.FailoverAttemptFailed,
		})
	}
	spent := len(st.attempts)

	_, err := m.ContinueFailover(context.Background(), id, ContinueFailoverRequest{IncidentID: "inc-1"})
	if !errors.Is(err, domain.ErrFailoverNoTarget) {
		t.Fatalf("err = %v, want ErrFailoverNoTarget", err)
	}
	if len(st.attempts) != spent {
		t.Fatalf("an exhausted ladder still wrote an attempt row: %d", len(st.attempts))
	}
	if st.sessions[id].Metadata.Pause == nil {
		t.Fatal("an exhausted ladder lifted the pause")
	}
	if rt.created != 0 {
		t.Fatal("an exhausted ladder reached the runtime")
	}
}

// A stale incident must be refused before ANY ledger row, attempt row or
// runtime change: continuing on evidence nobody looked at is the failure this
// check exists for.
func TestContinueFailover_StaleIncidentRefusedBeforeAnything(t *testing.T) {
	st, rt, m, id := failoverFixture(t)
	pauseSessionAt(st, id, "inc-2") // a newer incident now holds the pin

	_, err := m.ContinueFailover(context.Background(), id, ContinueFailoverRequest{IncidentID: "inc-1"})
	if !errors.Is(err, ErrIncidentMismatch) {
		t.Fatalf("err = %v, want ErrIncidentMismatch", err)
	}
	assertNothingDurable(t, st, id, "inc-2")
	if rt.destroyed != 0 || rt.created != 0 {
		t.Fatal("a stale incident reached the runtime")
	}
}

func TestContinueFailover_NotPausedRefused(t *testing.T) {
	st, _, m, id := failoverFixture(t)
	rec := st.sessions[id]
	rec.Metadata.Pause = nil
	st.sessions[id] = rec

	_, err := m.ContinueFailover(context.Background(), id, ContinueFailoverRequest{IncidentID: "inc-1"})
	if !errors.Is(err, ErrNotPaused) {
		t.Fatalf("err = %v, want ErrNotPaused", err)
	}
	if len(st.attempts) != 0 {
		t.Fatal("a not-paused Continue wrote an attempt row")
	}
}

func TestContinueFailover_AttemptBoundReachedStaysPaused(t *testing.T) {
	st, _, m, id := failoverFixture(t)
	for i := 1; i <= domain.MaxFailoversPerIncident; i++ {
		st.attempts = append(st.attempts, domain.FailoverAttempt{
			ID: domain.FailoverAttemptID(id, "inc-1", i), SessionID: id, ProjectID: "mer",
			IncidentID: "inc-1", Seq: i, GenerationID: fmt.Sprintf("g%d", i),
			State: domain.FailoverAttemptFailed, ToHarness: domain.HarnessCodex,
		})
	}
	_, err := m.ContinueFailover(context.Background(), id, ContinueFailoverRequest{IncidentID: "inc-1"})
	if !errors.Is(err, domain.ErrFailoverLimitReached) {
		t.Fatalf("err = %v, want ErrFailoverLimitReached", err)
	}
	if len(st.attempts) != domain.MaxFailoversPerIncident {
		t.Fatalf("the bound wrote another attempt: %d", len(st.attempts))
	}
	if st.sessions[id].Metadata.Pause == nil {
		t.Fatal("reaching the bound lifted the pause; the bound exists to keep it")
	}
}

// The rule-1 transaction failing must leave the session exactly as it was --
// and must not reach the runtime, because both rows are durable BEFORE the
// saga touches it.
func TestContinueFailover_DurableWriteFailureNeverReachesTheRuntime(t *testing.T) {
	st, rt, m, id := failoverFixture(t)
	st.appendErr = errors.New("disk full")

	_, err := m.ContinueFailover(context.Background(), id, ContinueFailoverRequest{IncidentID: "inc-1"})
	if err == nil {
		t.Fatal("expected the durable write failure to surface")
	}
	if len(st.attempts) != 0 {
		t.Fatal("attempt row survived a failed transaction")
	}
	for _, e := range st.ledger {
		if e.Kind == domain.LifecycleKindFailover {
			t.Fatalf("ledger row survived a failed transaction: %+v", e)
		}
	}
	if rt.destroyed != 0 || rt.created != 0 {
		t.Fatal("the saga touched the runtime after the durable write failed")
	}
	if st.sessions[id].Metadata.Pause == nil {
		t.Fatal("pause lifted by a failed continuation")
	}
}

// --- the happy path --------------------------------------------------------

func TestContinueFailover_MovesToNextRungAndLiftsPause(t *testing.T) {
	st, _, m, id := failoverFixture(t)
	before := st.sessions[id].Metadata.Role

	res, err := m.ContinueFailover(context.Background(), id, ContinueFailoverRequest{IncidentID: "inc-1"})
	if err != nil {
		t.Fatalf("continue: %v", err)
	}
	if res.Target.Harness != domain.HarnessCodex || res.RungIndex != 0 || res.AttemptSeq != 1 {
		t.Fatalf("result = %+v, want codex at rung 0, seq 1", res)
	}
	if res.Reused {
		t.Fatal("a first continuation reported Reused")
	}

	att := st.only(t)
	if att.State != domain.FailoverAttemptAcked {
		t.Fatalf("attempt state = %q, want acked", att.State)
	}
	// Section 6b: the generation is the saga's, from the first write.
	if att.GenerationID == "" || att.GenerationID != res.GenerationID {
		t.Fatalf("attempt generation %q != result %q", att.GenerationID, res.GenerationID)
	}
	if got := st.sessions[id].Metadata.RuntimeLaunchID; got != att.GenerationID {
		t.Fatalf("runtime generation %q != attempt generation %q", got, att.GenerationID)
	}
	if st.sessions[id].Metadata.Pause != nil {
		t.Fatal("pause not lifted after target_ack")
	}
	if st.sessions[id].Harness != domain.HarnessCodex {
		t.Fatalf("harness = %q, want codex", st.sessions[id].Harness)
	}

	// Contract section 6 rule 7: role identity is invariant across the move.
	after := st.sessions[id].Metadata.Role
	if after.RoleID != before.RoleID {
		t.Fatalf("role_id changed: %q -> %q", before.RoleID, after.RoleID)
	}
	if after.TemplateArtifactID != before.TemplateArtifactID || after.TemplateSHA256 != before.TemplateSHA256 {
		t.Fatalf("template artifact changed: %+v -> %+v", before, after)
	}
	if after.ResolvedPermissions != before.ResolvedPermissions {
		t.Fatalf("permissions changed: %+v -> %+v", before.ResolvedPermissions, after.ResolvedPermissions)
	}
	if after.ResolvedHarness == before.ResolvedHarness {
		t.Fatal("resolved harness did not move; the continuation did nothing")
	}

	// Both failover ledger phases are on record, keyed per attempt.
	var phases []domain.LifecycleLedgerPhase
	for _, e := range st.ledger {
		if e.Kind == domain.LifecycleKindFailover {
			phases = append(phases, e.Phase)
			if e.GenerationID != att.GenerationID {
				t.Fatalf("failover ledger row carries generation %q, want the switch generation %q",
					e.GenerationID, att.GenerationID)
			}
		}
	}
	if len(phases) != 2 || phases[0] != domain.LifecyclePhaseRequested || phases[1] != domain.LifecyclePhaseTargetAck {
		t.Fatalf("failover ledger phases = %v, want [requested target_ack]", phases)
	}
}

// A paused-DEAD source: the pane is gone, so the destroy probe confirms death
// immediately. Continue must work identically -- acceptance case 2.
func TestContinueFailover_PausedDeadSourceWorks(t *testing.T) {
	st, rt, m, id := failoverFixture(t)
	rt.aliveByHandle = map[string]bool{} // nothing alive: the source is already dead

	res, err := m.ContinueFailover(context.Background(), id, ContinueFailoverRequest{IncidentID: "inc-1"})
	if err != nil {
		t.Fatalf("continue on a paused-dead source: %v", err)
	}
	if res.Target.Harness != domain.HarnessCodex {
		t.Fatalf("target = %+v", res.Target)
	}
	if st.sessions[id].Metadata.Pause != nil {
		t.Fatal("pause not lifted")
	}
	if rt.created != 1 {
		t.Fatalf("target launches = %d, want exactly 1", rt.created)
	}
}

// --- failure classification, contract section 6a ---------------------------

// A PRE-STOP failure: the probe confirms the source is still alive, so the saga
// rolls back and nothing was destroyed. Terminal, rung spent, pause intact.
func TestContinueFailover_PreStopFailureIsTerminalAndKeepsPause(t *testing.T) {
	st, rt, m, id := failoverFixture(t)
	rt.aliveByHandle = map[string]bool{"rt-1": true} // survives destroy
	rt.destroyLeavesAlive = true

	_, err := m.ContinueFailover(context.Background(), id, ContinueFailoverRequest{IncidentID: "inc-1"})
	if err == nil {
		t.Fatal("expected the pre-stop failure to surface")
	}
	att := st.only(t)
	if att.State != domain.FailoverAttemptFailed {
		t.Fatalf("attempt state = %q, want failed for a confirmed-alive source", att.State)
	}
	if st.sessions[id].Metadata.Pause == nil {
		t.Fatal("a failed continuation lifted the pause")
	}
	if rt.created != 0 {
		t.Fatal("a pre-stop failure launched a target")
	}
	// Terminal, so the next Continue starts a NEW attempt rather than adopting.
	if _, ok := domain.ActiveFailoverAttempt(st.attempts); ok {
		t.Fatal("a pre-stop failure left an adoptable attempt")
	}
}

// A POST-STOP failure is the heart of contract section 6a: the source is gone
// and the handoff retained, so the attempt is RECOVERABLE and must never be
// written `failed`.
func TestContinueFailover_PostStopFailureIsNotTerminal(t *testing.T) {
	st, _, m, id := failoverFixture(t)
	m.lcm.(*fakeLCM).markSpawnedErr = errors.New("database is locked")

	_, err := m.ContinueFailover(context.Background(), id, ContinueFailoverRequest{IncidentID: "inc-1"})
	if !errors.Is(err, ErrSwitchPostStop) {
		t.Fatalf("err = %v, want ErrSwitchPostStop", err)
	}
	att := st.only(t)
	if att.State == domain.FailoverAttemptFailed {
		t.Fatal("a post-stop failure was written `failed`; it is recoverable on the same generation")
	}
	if att.State != domain.FailoverAttemptPostStop {
		t.Fatalf("attempt state = %q, want post_stop", att.State)
	}
	if att.State.Terminal() {
		t.Fatal("post_stop reported Terminal(); a duplicate Continue would open a second runtime")
	}
	if st.sessions[id].Metadata.Pause == nil {
		t.Fatal("a post-stop failure lifted the pause")
	}
	// No `failed` ledger row either: the saga is unfinished, not finished badly.
	for _, e := range st.ledger {
		if e.Kind == domain.LifecycleKindFailover && e.Phase == domain.LifecyclePhaseFailed {
			t.Fatalf("post-stop wrote a failover/failed ledger row: %+v", e)
		}
	}
}

// The sequel, and the defect section 6a exists to remove: the next Continue
// must ADOPT the post_stop and finish it on the same generation, not spend a
// second rung and launch a second runtime.
func TestContinueFailover_AdoptsPostStopWithoutSpendingASecondRung(t *testing.T) {
	st, rt, m, id := failoverFixture(t)
	m.lcm.(*fakeLCM).markSpawnedErr = errors.New("database is locked")
	if _, err := m.ContinueFailover(context.Background(), id,
		ContinueFailoverRequest{IncidentID: "inc-1"}); !errors.Is(err, ErrSwitchPostStop) {
		t.Fatalf("setup: err = %v, want ErrSwitchPostStop", err)
	}
	first := st.only(t)
	launchesAfterFirst := rt.created

	// Second Continue: the injected failure is gone, so recovery can complete.
	m.lcm.(*fakeLCM).markSpawnedErr = nil
	res, err := m.ContinueFailover(context.Background(), id, ContinueFailoverRequest{IncidentID: "inc-1"})
	if err != nil {
		t.Fatalf("adopting Continue: %v", err)
	}
	if !res.Reused {
		t.Fatal("adopting an unrecovered post_stop reported Reused=false")
	}
	if res.GenerationID != first.GenerationID {
		t.Fatalf("adoption changed generation: %q -> %q", first.GenerationID, res.GenerationID)
	}
	if res.AttemptSeq != first.Seq {
		t.Fatalf("adoption changed attempt seq: %d -> %d", first.Seq, res.AttemptSeq)
	}
	att := st.only(t) // still exactly one attempt row: no second rung spent
	if att.State != domain.FailoverAttemptAcked {
		t.Fatalf("adopted attempt state = %q, want acked", att.State)
	}
	if got := rt.created - launchesAfterFirst; got != 1 {
		t.Fatalf("recovery launched %d runtimes, want exactly 1", got)
	}
	if st.sessions[id].Metadata.Pause != nil {
		t.Fatal("pause not lifted after the adopted attempt acked")
	}
	if st.sessions[id].Metadata.RuntimeLaunchID != first.GenerationID {
		t.Fatalf("recovered runtime generation = %q, want the original %q",
			st.sessions[id].Metadata.RuntimeLaunchID, first.GenerationID)
	}
}

// A switch fence with no matching durable pending state proves only that some
// transition owns the session right now. It does not prove this failover
// attempt completed, or even that the owner is this attempt. Continue must
// preserve the requested row and surface the conflict rather than turn fence
// occupancy into a false successful result.
func TestContinueFailover_UnrelatedSwitchFenceReturnsConflictWithoutMutation(t *testing.T) {
	st, rt, m, id := failoverFixture(t)
	st.attempts = append(st.attempts, domain.FailoverAttempt{
		ID: domain.FailoverAttemptID(id, "inc-1", 1), SessionID: id, ProjectID: "mer",
		IncidentID: "inc-1", Seq: 1, RoleID: "implementor", GenerationID: "gen-inflight",
		FromHarness: domain.HarnessClaudeCode, ToHarness: domain.HarnessCodex,
		RungIndex: 0, State: domain.FailoverAttemptRequested,
	})
	if !m.beginSwitch(id) {
		t.Fatal("could not take the switch fence")
	}
	defer m.endSwitch(id)

	res, err := m.ContinueFailover(context.Background(), id, ContinueFailoverRequest{IncidentID: "inc-1"})
	if !errors.Is(err, ErrSwitchOperationInProgress) {
		t.Fatalf("continue with unrelated switch fence error = %v, want ErrSwitchOperationInProgress", err)
	}
	if res != (ContinueFailoverResult{}) {
		t.Fatalf("conflicted Continue returned false success: %+v", res)
	}
	if len(st.attempts) != 1 {
		t.Fatalf("conflicted Continue wrote a second attempt row: %d", len(st.attempts))
	}
	if st.attempts[0].State != domain.FailoverAttemptRequested {
		t.Fatalf("conflicted Continue changed attempt state to %q, want requested", st.attempts[0].State)
	}
	if rt.created != 0 {
		t.Fatal("conflicted Continue launched a runtime")
	}
	if st.sessions[id].Metadata.Pause == nil {
		t.Fatal("conflicted Continue lifted the pause before any ack")
	}
}

// A product-real duplicate: the first Continue has persisted switch_pending
// and pre_stop, holds beginSwitch, and is blocked destroying the source. The
// duplicate must report ErrSwitchOperationInProgress without promoting the OUTER
// failover attempt to post_stop. That phase belongs to the switch ledger;
// moving the attempt while the first saga still owns it makes the first
// requested->acked CAS lose after a completely successful target_ack. Returning
// success is also wrong: at this point the first saga has not acknowledged the
// target or cleared the pause.
func TestContinueFailover_DuplicateDuringDestroyDoesNotStealAttemptPromotion(t *testing.T) {
	st, rt, m, id := failoverFixture(t)
	blocking := &blockingDestroyRuntime{
		fakeRuntime: rt,
		entered:     make(chan struct{}),
		release:     make(chan struct{}),
	}
	m.runtime = blocking

	type outcome struct {
		result ContinueFailoverResult
		err    error
	}
	firstCtx, cancelFirst := context.WithCancel(context.Background())
	defer cancelFirst()
	firstDone := make(chan outcome, 1)
	go func() {
		res, err := m.ContinueFailover(firstCtx, id, ContinueFailoverRequest{IncidentID: "inc-1"})
		firstDone <- outcome{result: res, err: err}
	}()

	select {
	case <-blocking.entered:
	case <-time.After(3 * time.Second):
		t.Fatal("first Continue never reached source Destroy")
	}
	beforeDuplicate := st.only(t)
	if beforeDuplicate.State != domain.FailoverAttemptRequested {
		close(blocking.release)
		t.Fatalf("attempt state before duplicate = %q, want requested", beforeDuplicate.State)
	}
	pending := st.sessions[id].Metadata.SwitchPending
	if pending == nil || pending.GenerationID != beforeDuplicate.GenerationID {
		close(blocking.release)
		t.Fatalf("first Continue did not durably pin its generation before Destroy: %+v", pending)
	}

	duplicate, duplicateErr := m.ContinueFailover(
		context.Background(), id, ContinueFailoverRequest{IncidentID: "inc-1"},
	)
	duringDestroy := st.only(t)
	close(blocking.release)
	var first outcome
	select {
	case first = <-firstDone:
	case <-time.After(3 * time.Second):
		t.Fatal("first Continue did not finish after Destroy was released")
	}

	if !errors.Is(duplicateErr, ErrSwitchOperationInProgress) {
		t.Fatalf("duplicate Continue error = %v, want ErrSwitchOperationInProgress", duplicateErr)
	}
	if duplicate != (ContinueFailoverResult{}) {
		t.Fatalf("duplicate returned false success while first saga was pre-ack: %+v", duplicate)
	}
	if duringDestroy.State != domain.FailoverAttemptRequested {
		t.Fatalf("duplicate stole attempt promotion during pre_stop: state=%q, want requested", duringDestroy.State)
	}
	if first.err != nil {
		t.Fatalf("first Continue returned an error after target acknowledgement: %v", first.err)
	}
	if first.result.Reused || first.result.GenerationID != beforeDuplicate.GenerationID {
		t.Fatalf("first result = %+v, want generation %q and Reused=false",
			first.result, beforeDuplicate.GenerationID)
	}

	attempt := st.only(t)
	if attempt.State != domain.FailoverAttemptAcked {
		t.Fatalf("final attempt state = %q, want acked", attempt.State)
	}
	rec := st.sessions[id]
	if rec.Metadata.Pause != nil || rec.Metadata.SwitchPending != nil {
		t.Fatalf("successful first Continue left durable pins: pause=%+v pending=%+v",
			rec.Metadata.Pause, rec.Metadata.SwitchPending)
	}
	if rec.Harness != domain.HarnessCodex || rec.Metadata.RuntimeLaunchID != attempt.GenerationID {
		t.Fatalf("target promotion = harness %q generation %q, want codex/%q",
			rec.Harness, rec.Metadata.RuntimeLaunchID, attempt.GenerationID)
	}
	if rt.destroyed != 1 || rt.created != 1 || rec.Metadata.RuntimeHandleID == "" {
		t.Fatalf("runtime destroy/create/active = %d/%d/%q, want 1/1/non-empty",
			rt.destroyed, rt.created, rec.Metadata.RuntimeHandleID)
	}
	var failoverPhases, switchPhases []domain.LifecycleLedgerPhase
	for _, event := range st.ledger {
		if event.GenerationID != attempt.GenerationID {
			continue
		}
		switch event.Kind {
		case domain.LifecycleKindFailover:
			failoverPhases = append(failoverPhases, event.Phase)
		case domain.LifecycleKindSwitch:
			switchPhases = append(switchPhases, event.Phase)
		}
	}
	wantFailover := []domain.LifecycleLedgerPhase{
		domain.LifecyclePhaseRequested, domain.LifecyclePhaseTargetAck,
	}
	wantSwitch := []domain.LifecycleLedgerPhase{
		domain.LifecyclePhaseRequested, domain.LifecyclePhasePreStop,
		domain.LifecyclePhasePostStop, domain.LifecyclePhaseTargetAck,
	}
	if !reflect.DeepEqual(failoverPhases, wantFailover) || !reflect.DeepEqual(switchPhases, wantSwitch) {
		t.Fatalf("ledger phases failover=%v switch=%v, want %v / %v",
			failoverPhases, switchPhases, wantFailover, wantSwitch)
	}
}

// The same duplicate conflict remains truthful when the first saga later
// fails. In particular, the duplicate may not return success merely because a
// matching pending generation and a live fence existed at observation time.
// Here the source survives Destroy, so the first owner rolls back the pending
// switch and records the one failover attempt as terminally failed.
func TestContinueFailover_DuplicateDuringDestroyFirstLaterFailsTruthfully(t *testing.T) {
	st, rt, m, id := failoverFixture(t)
	blocking := &blockingDestroyRuntime{
		fakeRuntime: rt,
		entered:     make(chan struct{}),
		release:     make(chan struct{}),
	}
	m.runtime = blocking

	type outcome struct {
		result ContinueFailoverResult
		err    error
	}
	firstCtx, cancelFirst := context.WithCancel(context.Background())
	defer cancelFirst()
	firstDone := make(chan outcome, 1)
	go func() {
		res, err := m.ContinueFailover(firstCtx, id, ContinueFailoverRequest{IncidentID: "inc-1"})
		firstDone <- outcome{result: res, err: err}
	}()

	select {
	case <-blocking.entered:
	case <-time.After(3 * time.Second):
		t.Fatal("first Continue never reached source Destroy")
	}
	beforeDuplicate := st.only(t)
	if beforeDuplicate.State != domain.FailoverAttemptRequested {
		close(blocking.release)
		t.Fatalf("attempt state before duplicate = %q, want requested", beforeDuplicate.State)
	}

	duplicate, duplicateErr := m.ContinueFailover(
		context.Background(), id, ContinueFailoverRequest{IncidentID: "inc-1"},
	)
	duringDestroy := st.only(t)
	if !errors.Is(duplicateErr, ErrSwitchOperationInProgress) {
		close(blocking.release)
		t.Fatalf("duplicate Continue error = %v, want ErrSwitchOperationInProgress", duplicateErr)
	}
	if duplicate != (ContinueFailoverResult{}) {
		close(blocking.release)
		t.Fatalf("duplicate returned false success before the owner's failure: %+v", duplicate)
	}
	if duringDestroy.State != domain.FailoverAttemptRequested {
		close(blocking.release)
		t.Fatalf("duplicate changed attempt state to %q, want requested", duringDestroy.State)
	}

	// Make the source definitively survive the first owner's Destroy. This is a
	// pre-stop failure: SwitchWorker rolls back pending and never launches a
	// target, then Continue records the single attempt as failed.
	rt.destroyErr = errors.New("source destroy failed")
	rt.aliveByHandle["rt-1"] = true
	close(blocking.release)
	var first outcome
	select {
	case first = <-firstDone:
	case <-time.After(3 * time.Second):
		t.Fatal("first Continue did not finish after Destroy was released")
	}
	if first.err == nil {
		t.Fatalf("first Continue unexpectedly succeeded: %+v", first.result)
	}

	attempt := st.only(t)
	if attempt.ID != beforeDuplicate.ID || attempt.GenerationID != beforeDuplicate.GenerationID {
		t.Fatalf("failure changed attempt identity: before=%+v after=%+v", beforeDuplicate, attempt)
	}
	if attempt.State != domain.FailoverAttemptFailed {
		t.Fatalf("final attempt state = %q, want failed", attempt.State)
	}
	rec := st.sessions[id]
	if rec.Metadata.Pause == nil || rec.Metadata.SwitchPending != nil {
		t.Fatalf("failed first Continue left untruthful pins: pause=%+v pending=%+v",
			rec.Metadata.Pause, rec.Metadata.SwitchPending)
	}
	if rec.Harness != domain.HarnessClaudeCode || rec.Metadata.RuntimeHandleID != "rt-1" ||
		rec.Metadata.RuntimeLaunchID != "src-gen" {
		t.Fatalf("source identity after rollback = harness %q handle %q generation %q",
			rec.Harness, rec.Metadata.RuntimeHandleID, rec.Metadata.RuntimeLaunchID)
	}
	if rt.destroyed != 1 || rt.created != 0 {
		t.Fatalf("runtime destroy/create = %d/%d, want 1/0", rt.destroyed, rt.created)
	}

	var failoverPhases, switchPhases []domain.LifecycleLedgerPhase
	for _, event := range st.ledger {
		if event.GenerationID != attempt.GenerationID {
			continue
		}
		switch event.Kind {
		case domain.LifecycleKindFailover:
			failoverPhases = append(failoverPhases, event.Phase)
		case domain.LifecycleKindSwitch:
			switchPhases = append(switchPhases, event.Phase)
		}
	}
	wantFailover := []domain.LifecycleLedgerPhase{
		domain.LifecyclePhaseRequested, domain.LifecyclePhaseFailed,
	}
	wantSwitch := []domain.LifecycleLedgerPhase{
		domain.LifecyclePhaseRequested, domain.LifecyclePhasePreStop, domain.LifecyclePhaseFailed,
	}
	if !reflect.DeepEqual(failoverPhases, wantFailover) || !reflect.DeepEqual(switchPhases, wantSwitch) {
		t.Fatalf("ledger phases failover=%v switch=%v, want %v / %v",
			failoverPhases, switchPhases, wantFailover, wantSwitch)
	}
}

// The crash this design previously answered with a lie: the daemon died between
// the attempt's durable write and SwitchWorker establishing ANY pending state.
//
// Reporting reuse here spends the rung, tells the operator the move happened,
// and parks the session paused forever with no runtime — boot is deliberately
// passive, so nothing else would ever drive it. The next Continue must re-drive
// the SAME generation and target, and must not mint a second attempt.
func TestContinueFailover_RequestedCrashRedrivesSameGeneration(t *testing.T) {
	st, rt, m, id := failoverFixture(t)
	st.attempts = append(st.attempts, domain.FailoverAttempt{
		ID: domain.FailoverAttemptID(id, "inc-1", 1), SessionID: id, ProjectID: "mer",
		IncidentID: "inc-1", Seq: 1, RoleID: "implementor", GenerationID: "gen-crashed",
		FromHarness: domain.HarnessClaudeCode, ToHarness: domain.HarnessCodex,
		RungIndex: 0, State: domain.FailoverAttemptRequested,
	})

	res, err := m.ContinueFailover(context.Background(), id, ContinueFailoverRequest{IncidentID: "inc-1"})
	if err != nil {
		t.Fatalf("re-drive after a requested-phase crash: %v", err)
	}
	if rt.created != 1 {
		t.Fatalf("re-drive launched %d runtimes, want exactly 1", rt.created)
	}
	if res.GenerationID != "gen-crashed" {
		t.Fatalf("re-drive minted a new generation %q, want gen-crashed", res.GenerationID)
	}
	if len(st.attempts) != 1 {
		t.Fatalf("re-drive wrote a second attempt row: %d", len(st.attempts))
	}
	if st.attempts[0].State != domain.FailoverAttemptAcked {
		t.Fatalf("attempt state = %q, want acked after a successful re-drive", st.attempts[0].State)
	}
	if !res.Reused {
		t.Fatal("a re-drive adopted an existing attempt; Reused must stay true")
	}
	if st.sessions[id].Metadata.Pause != nil {
		t.Fatal("pause survived a completed re-drive")
	}
}

// A durable target_ack is written BEFORE the session is promoted, so an attempt
// can read `acked` while SwitchPending still holds and the row still names the
// source harness. Clearing the pin on that evidence lifts a human's pause on a
// move that has not landed.
//
// With no incomplete switch to finish, the only honest answer is that a human
// must resolve it — and critically, NOT to fall through and spend a new rung.
func TestContinueFailover_AckedButUnpromotedNeverClearsPauseOrSpendsARung(t *testing.T) {
	st, rt, m, id := failoverFixture(t)
	st.attempts = append(st.attempts, domain.FailoverAttempt{
		ID: domain.FailoverAttemptID(id, "inc-1", 1), SessionID: id, ProjectID: "mer",
		IncidentID: "inc-1", Seq: 1, RoleID: "implementor", GenerationID: "gen-acked",
		FromHarness: domain.HarnessClaudeCode, ToHarness: domain.HarnessCodex,
		RungIndex: 0, State: domain.FailoverAttemptAcked,
	})
	// Promotion never happened: the row still names the source harness, and the
	// runtime generation is still the source's.
	rec := st.sessions[id]
	rec.Harness = domain.HarnessClaudeCode
	rec.Metadata.RuntimeLaunchID = "gen-source"
	st.sessions[id] = rec

	_, err := m.ContinueFailover(context.Background(), id, ContinueFailoverRequest{IncidentID: "inc-1"})
	if !errors.Is(err, ErrFailoverRecoveryRequired) {
		t.Fatalf("err = %v, want ErrFailoverRecoveryRequired", err)
	}
	if st.sessions[id].Metadata.Pause == nil {
		t.Fatal("an unpromoted ack cleared the pause")
	}
	if len(st.attempts) != 1 {
		t.Fatalf("an unpromoted ack spent a second rung: %d attempts", len(st.attempts))
	}
	if rt.created != 0 {
		t.Fatalf("an unpromoted ack launched %d runtimes", rt.created)
	}
}

// A SAME-harness rung with an empty model is a legal, meaningful ladder entry --
// "fall back to this harness's provider default". ValidateRoleMap permits it,
// and NextFailoverRung selects it because an empty model is an exact value and
// not a wildcard.
//
// The saga's resolveTargetModel then rewrites that empty target to the SOURCE
// model on a same-harness move, so the session lands on "sonnet-5" while the
// attempt row still records "". Comparing the session against the attempt's raw
// ToModel therefore never settled, and a crash between the ack and the pin clear
// stuck the session permanently: there is no incomplete switch left to recover,
// so Continue refused with FAILOVER_RECOVERY_REQUIRED forever, with the rung
// already spent and only Resume as an escape.
func TestContinueFailover_SameHarnessEmptyModelRungSettlesAfterPromotion(t *testing.T) {
	st, rt, m, id := failoverFixture(t)
	st.attempts = append(st.attempts, domain.FailoverAttempt{
		ID: domain.FailoverAttemptID(id, "inc-1", 1), SessionID: id, ProjectID: "mer",
		IncidentID: "inc-1", Seq: 1, RoleID: "implementor", GenerationID: "gen-same",
		FromHarness: domain.HarnessClaudeCode, FromModel: "sonnet-5",
		ToHarness: domain.HarnessClaudeCode, ToModel: "",
		RungIndex: 0, State: domain.FailoverAttemptAcked,
	})
	// The move landed: same harness, and the saga kept the source model because
	// the configured target model was empty on a same-harness switch.
	rec := st.sessions[id]
	rec.Harness = domain.HarnessClaudeCode
	rec.Metadata.Role.ResolvedModel = "sonnet-5"
	rec.Metadata.RuntimeLaunchID = "gen-same"
	rec.Metadata.SwitchPending = nil
	st.sessions[id] = rec

	res, err := m.ContinueFailover(context.Background(), id, ContinueFailoverRequest{IncidentID: "inc-1"})
	if err != nil {
		t.Fatalf("converge on a same-harness empty-model rung: %v", err)
	}
	if !res.Reused {
		t.Fatal("convergence reported Reused=false")
	}
	if rt.created != 0 {
		t.Fatalf("convergence launched %d runtimes; the move had already landed", rt.created)
	}
	if len(st.attempts) != 1 {
		t.Fatalf("convergence spent a second rung: %d attempts", len(st.attempts))
	}
	if st.sessions[id].Metadata.Pause != nil {
		t.Fatal("the pin never cleared; the session is stuck on a move that completed")
	}
}

// The ack CAS returns whether it actually wrote. Discarding that answer meant a
// lost transition still appended target_ack and cleared the pin -- the inverse
// of "the pause clears only after target_ack on THIS attempt", and it would let
// a human's pause be lifted on an attempt the store says is not acked.
func TestContinueFailover_LostAckTransitionDoesNotClearThePause(t *testing.T) {
	st, _, m, id := failoverFixture(t)
	st.attempts = append(st.attempts, domain.FailoverAttempt{
		ID: domain.FailoverAttemptID(id, "inc-1", 1), SessionID: id, ProjectID: "mer",
		IncidentID: "inc-1", Seq: 1, RoleID: "implementor", GenerationID: "gen-lost",
		FromHarness: domain.HarnessClaudeCode, ToHarness: domain.HarnessCodex,
		RungIndex: 0, State: domain.FailoverAttemptRequested,
	})
	// Something else moves the row between the read and the CAS, so the
	// transition this call believed it was making is lost.
	st.beforeUpdate = func() {
		for i := range st.attempts {
			st.attempts[i].State = domain.FailoverAttemptFailed
		}
	}

	_, err := m.ContinueFailover(context.Background(), id, ContinueFailoverRequest{IncidentID: "inc-1"})
	if !errors.Is(err, ErrFailoverRecoveryRequired) {
		t.Fatalf("err = %v, want ErrFailoverRecoveryRequired", err)
	}
	if st.sessions[id].Metadata.Pause == nil {
		t.Fatal("a lost ack transition still cleared the pause")
	}
}

// A cross-harness empty model is the OPPOSITE resolution -- provider default,
// never the source model -- so the same comparison must still settle there.
// Pinning both directions keeps the fix tied to resolveTargetModel's rule
// rather than to one branch of it.
func TestContinueFailover_CrossHarnessEmptyModelSettlesOnProviderDefault(t *testing.T) {
	st, _, m, id := failoverFixture(t)
	st.attempts = append(st.attempts, domain.FailoverAttempt{
		ID: domain.FailoverAttemptID(id, "inc-1", 1), SessionID: id, ProjectID: "mer",
		IncidentID: "inc-1", Seq: 1, RoleID: "implementor", GenerationID: "gen-cross",
		FromHarness: domain.HarnessClaudeCode, FromModel: "sonnet-5",
		ToHarness: domain.HarnessCodex, ToModel: "",
		RungIndex: 0, State: domain.FailoverAttemptAcked,
	})
	rec := st.sessions[id]
	rec.Harness = domain.HarnessCodex
	rec.Metadata.Role.ResolvedModel = "" // cross-harness never leaks the source model
	rec.Metadata.RuntimeLaunchID = "gen-cross"
	rec.Metadata.SwitchPending = nil
	st.sessions[id] = rec

	if _, err := m.ContinueFailover(context.Background(), id, ContinueFailoverRequest{IncidentID: "inc-1"}); err != nil {
		t.Fatalf("converge on a cross-harness empty-model rung: %v", err)
	}
	if st.sessions[id].Metadata.Pause != nil {
		t.Fatal("the pin never cleared on a completed cross-harness move")
	}
}

// The settled counterpart: promotion is proven on every axis, so the only thing
// left undone is the pin clear, and a retry does exactly that and no more.
func TestContinueFailover_AckedAndPromotedFinishesOnlyThePinClear(t *testing.T) {
	st, rt, m, id := failoverFixture(t)
	st.attempts = append(st.attempts, domain.FailoverAttempt{
		ID: domain.FailoverAttemptID(id, "inc-1", 1), SessionID: id, ProjectID: "mer",
		IncidentID: "inc-1", Seq: 1, RoleID: "implementor", GenerationID: "gen-done",
		FromHarness: domain.HarnessClaudeCode, ToHarness: domain.HarnessCodex,
		RungIndex: 0, State: domain.FailoverAttemptAcked,
	})
	rec := st.sessions[id]
	rec.Harness = domain.HarnessCodex
	rec.Metadata.Role.ResolvedModel = ""
	rec.Metadata.RuntimeLaunchID = "gen-done"
	rec.Metadata.SwitchPending = nil
	st.sessions[id] = rec

	res, err := m.ContinueFailover(context.Background(), id, ContinueFailoverRequest{IncidentID: "inc-1"})
	if err != nil {
		t.Fatalf("converge after a crash between ack and pin clear: %v", err)
	}
	if !res.Reused {
		t.Fatal("convergence reported Reused=false")
	}
	if rt.created != 0 {
		t.Fatalf("convergence launched %d runtimes; the move had already happened", rt.created)
	}
	if len(st.attempts) != 1 {
		t.Fatalf("convergence spent a second rung: %d attempts", len(st.attempts))
	}
	if st.sessions[id].Metadata.Pause != nil {
		t.Fatal("convergence did not clear the pause it exists to clear")
	}
}

// An incomplete post_stop for a generation this incident's attempt does not
// account for is a genuine ambiguity a human must resolve. Section 6b turned
// this from a guess into an identity check.
func TestContinueFailover_ForeignPostStopRequiresRecovery(t *testing.T) {
	st, rt, m, id := failoverFixture(t)
	st.attempts = append(st.attempts, domain.FailoverAttempt{
		ID: domain.FailoverAttemptID(id, "inc-1", 1), SessionID: id, ProjectID: "mer",
		IncidentID: "inc-1", Seq: 1, GenerationID: "gen-mine",
		ToHarness: domain.HarnessCodex, State: domain.FailoverAttemptRequested,
	})
	rec := st.sessions[id]
	rec.Metadata.SwitchPending = &domain.SwitchPending{
		GenerationID: "gen-someone-else", Kind: domain.LifecycleKindSwitch,
		FromHarness: domain.HarnessClaudeCode, ToHarness: domain.HarnessCodex,
	}
	st.sessions[id] = rec

	_, err := m.ContinueFailover(context.Background(), id, ContinueFailoverRequest{IncidentID: "inc-1"})
	if !errors.Is(err, ErrFailoverRecoveryRequired) {
		t.Fatalf("err = %v, want ErrFailoverRecoveryRequired", err)
	}
	if len(st.attempts) != 1 {
		t.Fatal("an ambiguous recovery state still spent a rung")
	}
	if rt.created != 0 {
		t.Fatal("an ambiguous recovery state launched a runtime")
	}
}

// --- ForceGenerationID: the empty case must be today's behaviour -----------

// Contract section 6b requires proof that the field is inert when unset. The
// two halves: an empty value still mints from newSwitchGeneration and the
// runtime carries it; a set value is used verbatim and mints nothing.
func TestSwitchRequest_ForceGenerationIDEmptyIsUnchangedBehaviour(t *testing.T) {
	run := func(t *testing.T, force string) (SwitchResult, int) {
		t.Helper()
		st := newFakeStore()
		ws := t.TempDir()
		art, sha := pinImplementorTemplate(t, st)
		id := domain.SessionID("mer-1")
		workerSession(st, id, domain.HarnessClaudeCode, ws, art, sha)
		minted := 0
		m := New(Deps{
			Runtime: &fakeRuntime{}, Agents: singleAgent{agent: &recordingAgent{}}, Workspace: &fakeWorkspace{},
			Store: st, Messenger: &fakeMessenger{}, Lifecycle: &fakeLCM{store: st},
			LookPath:    func(string) (string, error) { return "/bin/true", nil },
			NewLaunchID: func() string { minted++; return fmt.Sprintf("minted-%d", minted) },
		})
		m.switchCapsOverride = testSwitchCaps
		res, err := m.SwitchWorker(context.Background(), SwitchRequest{
			SessionID: id, TargetHarness: domain.HarnessCodex, ForceGenerationID: force,
		})
		if err != nil {
			t.Fatalf("switch (force=%q): %v", force, err)
		}
		return res, minted
	}

	t.Run("empty mints exactly as before", func(t *testing.T) {
		res, minted := run(t, "")
		if minted != 1 {
			t.Fatalf("newLaunchID calls = %d, want 1 (unchanged from today)", minted)
		}
		if res.GenerationID != "minted-1" {
			t.Fatalf("generation = %q, want the minted one", res.GenerationID)
		}
		if res.GenerationID != res.Session.Metadata.RuntimeLaunchID {
			t.Fatalf("ledger gen %q != runtime gen %q", res.GenerationID, res.Session.Metadata.RuntimeLaunchID)
		}
	})

	t.Run("set is honoured and mints nothing", func(t *testing.T) {
		res, minted := run(t, "pinned-by-caller")
		if minted != 0 {
			t.Fatalf("newLaunchID calls = %d, want 0: the caller supplied the generation", minted)
		}
		if res.GenerationID != "pinned-by-caller" {
			t.Fatalf("generation = %q, want pinned-by-caller", res.GenerationID)
		}
		if res.Session.Metadata.RuntimeLaunchID != "pinned-by-caller" {
			t.Fatalf("runtime generation = %q, want the pinned one",
				res.Session.Metadata.RuntimeLaunchID)
		}
	})
}

// --- preview ---------------------------------------------------------------

func TestFailoverPreview_ReasonPrecedence(t *testing.T) {
	t.Run("available for a paused role-pinned worker", func(t *testing.T) {
		_, _, m, id := failoverFixture(t)
		got, err := m.FailoverPreview(context.Background(), id)
		if err != nil {
			t.Fatal(err)
		}
		if !got.Available || got.Reason != FailoverReasonNone {
			t.Fatalf("preview = %+v, want available", got)
		}
		if got.NextTarget.Harness != domain.HarnessCodex || got.NextRungIndex != 0 {
			t.Fatalf("next target = %+v idx=%d", got.NextTarget, got.NextRungIndex)
		}
		if got.MaxAttempts != domain.MaxFailoversPerIncident || got.IncidentID != "inc-1" {
			t.Fatalf("preview = %+v", got)
		}
	})

	t.Run("no role pin", func(t *testing.T) {
		st, _, m, id := failoverFixture(t)
		rec := st.sessions[id]
		rec.Metadata.Role.RoleID = ""
		st.sessions[id] = rec
		got, _ := m.FailoverPreview(context.Background(), id)
		if got.Reason != FailoverReasonNoRolePin || got.Available {
			t.Fatalf("preview = %+v", got)
		}
	})

	// no_ladder must outrank not_paused: the service derives contract section
	// 9's null block from (Pause == nil && Reason == no_ladder), so swapping
	// these two silently changes the wire shape.
	t.Run("no ladder outranks not paused", func(t *testing.T) {
		st, _, m, id := failoverFixture(t)
		failoverLadder(st)
		rec := st.sessions[id]
		rec.Metadata.Pause = nil
		st.sessions[id] = rec
		got, _ := m.FailoverPreview(context.Background(), id)
		if got.Reason != FailoverReasonNoLadder {
			t.Fatalf("reason = %q, want no_ladder so the service can emit a null block", got.Reason)
		}
	})

	t.Run("not paused when a ladder exists", func(t *testing.T) {
		st, _, m, id := failoverFixture(t)
		rec := st.sessions[id]
		rec.Metadata.Pause = nil
		st.sessions[id] = rec
		got, _ := m.FailoverPreview(context.Background(), id)
		if got.Reason != FailoverReasonNotPaused {
			t.Fatalf("reason = %q, want not_paused", got.Reason)
		}
	})

	t.Run("limit reached", func(t *testing.T) {
		st, _, m, id := failoverFixture(t)
		for i := 1; i <= domain.MaxFailoversPerIncident; i++ {
			st.attempts = append(st.attempts, domain.FailoverAttempt{
				ID: domain.FailoverAttemptID(id, "inc-1", i), SessionID: id,
				IncidentID: "inc-1", Seq: i, GenerationID: fmt.Sprintf("g%d", i),
				State: domain.FailoverAttemptFailed, ToHarness: domain.HarnessCodex,
			})
		}
		got, _ := m.FailoverPreview(context.Background(), id)
		if got.Reason != FailoverReasonLimitReached || got.Available {
			t.Fatalf("preview = %+v", got)
		}
		if got.AttemptsUsed != domain.MaxFailoversPerIncident {
			t.Fatalf("attemptsUsed = %d", got.AttemptsUsed)
		}
	})

	t.Run("ladder exhausted", func(t *testing.T) {
		st, _, m, id := failoverFixture(t)
		failoverLadder(st, domain.FailoverTarget{Harness: domain.HarnessCodex})
		st.attempts = append(st.attempts, domain.FailoverAttempt{
			ID: domain.FailoverAttemptID(id, "inc-1", 1), SessionID: id,
			IncidentID: "inc-1", Seq: 1, GenerationID: "g1",
			State: domain.FailoverAttemptFailed, ToHarness: domain.HarnessCodex,
		})
		got, _ := m.FailoverPreview(context.Background(), id)
		if got.Reason != FailoverReasonLadderExhausted || got.NextRungIndex != -1 {
			t.Fatalf("preview = %+v", got)
		}
	})

	// Contract section 2: the control set is state-appropriate, and Continue is
	// offered in BOTH paused cells (paused-dead merely adds Restart agent). The
	// preview must therefore read identically for a live and a dead source --
	// which it does because it never consults runtime liveness at all. Asserted
	// rather than assumed: a liveness check added here later would silently
	// disable Continue for exactly the sessions that most need it.
	t.Run("paused-dead reads the same as paused-live", func(t *testing.T) {
		_, rtLive, mLive, idLive := failoverFixture(t)
		rtLive.aliveByHandle = map[string]bool{"rt-1": true}
		live, err := mLive.FailoverPreview(context.Background(), idLive)
		if err != nil {
			t.Fatal(err)
		}

		_, rtDead, mDead, idDead := failoverFixture(t)
		rtDead.aliveByHandle = map[string]bool{} // process gone, handle still recorded
		dead, err := mDead.FailoverPreview(context.Background(), idDead)
		if err != nil {
			t.Fatal(err)
		}

		if !dead.Available {
			t.Fatalf("Continue unavailable on a paused-dead source: %+v", dead)
		}
		if live != dead {
			t.Fatalf("preview differs by liveness:\n live = %+v\n dead = %+v", live, dead)
		}
	})

	t.Run("terminated is switch_unsupported", func(t *testing.T) {
		st, _, m, id := failoverFixture(t)
		rec := st.sessions[id]
		rec.IsTerminated = true
		st.sessions[id] = rec
		got, _ := m.FailoverPreview(context.Background(), id)
		if got.Reason != FailoverReasonSwitchUnsupported {
			t.Fatalf("reason = %q", got.Reason)
		}
	})
}

// --- reconciliation --------------------------------------------------------

func TestReconcileFailoverAttempts_SettlesFromTheLedger(t *testing.T) {
	cases := []struct {
		name  string
		start domain.FailoverAttemptState
		rows  []domain.LifecycleLedgerPhase
		want  domain.FailoverAttemptState
	}{
		{"target_ack closes a requested attempt", domain.FailoverAttemptRequested,
			[]domain.LifecycleLedgerPhase{domain.LifecyclePhasePostStop, domain.LifecyclePhaseTargetAck},
			domain.FailoverAttemptAcked},
		{"target_ack closes a post_stop attempt", domain.FailoverAttemptPostStop,
			[]domain.LifecycleLedgerPhase{domain.LifecyclePhaseTargetAck},
			domain.FailoverAttemptAcked},
		{"post_stop without ack is recorded, not closed", domain.FailoverAttemptRequested,
			[]domain.LifecycleLedgerPhase{domain.LifecyclePhasePostStop},
			domain.FailoverAttemptPostStop},
		{"failed with no post_stop is terminal", domain.FailoverAttemptRequested,
			[]domain.LifecycleLedgerPhase{domain.LifecyclePhaseFailed},
			domain.FailoverAttemptFailed},
		// The uncertain-destroy path writes `failed` and KEEPS its pending fence,
		// so a failed row alongside a post_stop must NOT be read as terminal.
		{"failed alongside post_stop stays recoverable", domain.FailoverAttemptRequested,
			[]domain.LifecycleLedgerPhase{domain.LifecyclePhaseFailed, domain.LifecyclePhasePostStop},
			domain.FailoverAttemptPostStop},
		{"no verdict yet leaves it alone", domain.FailoverAttemptRequested,
			[]domain.LifecycleLedgerPhase{domain.LifecyclePhaseRequested},
			domain.FailoverAttemptRequested},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			st, _, m, id := failoverFixture(t)
			st.attempts = append(st.attempts, domain.FailoverAttempt{
				ID: domain.FailoverAttemptID(id, "inc-1", 1), SessionID: id, ProjectID: "mer",
				IncidentID: "inc-1", Seq: 1, GenerationID: "gen-x",
				ToHarness: domain.HarnessCodex, State: tc.start,
			})
			for _, ph := range tc.rows {
				st.ledger = append(st.ledger, domain.LifecycleLedgerRecord{
					ID: fmt.Sprintf("%s:gen-x:%s", id, ph), SessionID: id, ProjectID: "mer",
					Kind: domain.LifecycleKindSwitch, Phase: ph, GenerationID: "gen-x",
				})
			}
			if err := m.ReconcileFailoverAttempts(context.Background(), id); err != nil {
				t.Fatalf("reconcile: %v", err)
			}
			if got := st.attempts[0].State; got != tc.want {
				t.Fatalf("state = %q, want %q", got, tc.want)
			}
		})
	}
}

// Reconciliation must ignore failover's OWN ledger rows when reading verdicts:
// they share the generation now, and a failover/requested row is not a switch
// phase. isSwitchLedgerKind is what keeps the two apart.
func TestReconcileFailoverAttempts_IgnoresFailoverKindRows(t *testing.T) {
	st, _, m, id := failoverFixture(t)
	st.attempts = append(st.attempts, domain.FailoverAttempt{
		ID: domain.FailoverAttemptID(id, "inc-1", 1), SessionID: id, ProjectID: "mer",
		IncidentID: "inc-1", Seq: 1, GenerationID: "gen-x",
		ToHarness: domain.HarnessCodex, State: domain.FailoverAttemptRequested,
	})
	st.ledger = append(st.ledger, domain.LifecycleLedgerRecord{
		ID: "own-failed-row", SessionID: id, ProjectID: "mer",
		Kind: domain.LifecycleKindFailover, Phase: domain.LifecyclePhaseFailed,
		GenerationID: "gen-x",
	})
	if err := m.ReconcileFailoverAttempts(context.Background(), id); err != nil {
		t.Fatal(err)
	}
	if got := st.attempts[0].State; got != domain.FailoverAttemptRequested {
		t.Fatalf("state = %q, want requested: a failover-kind row was read as a switch verdict", got)
	}
}

// --- wiring ----------------------------------------------------------------

// A store without the failover surface must be told so explicitly. A silent
// degrade here would make every idempotence check answer "no prior attempt",
// which is how one incident spends every rung on the ladder.
func TestContinueFailover_UnwiredStoreIsExplicit(t *testing.T) {
	st := newFakeStore()
	ws := t.TempDir()
	art, sha := pinImplementorTemplate(t, st)
	id := domain.SessionID("mer-1")
	workerSession(st, id, domain.HarnessClaudeCode, ws, art, sha)
	m := New(Deps{
		Runtime: &fakeRuntime{}, Agents: singleAgent{agent: &recordingAgent{}}, Workspace: &fakeWorkspace{},
		Store: st, Messenger: &fakeMessenger{}, Lifecycle: &fakeLCM{store: st},
		LookPath: func(string) (string, error) { return "/bin/true", nil },
	})
	_, err := m.ContinueFailover(context.Background(), id, ContinueFailoverRequest{IncidentID: "inc-1"})
	if !errors.Is(err, ErrFailoverNotWired) {
		t.Fatalf("err = %v, want ErrFailoverNotWired", err)
	}
}
