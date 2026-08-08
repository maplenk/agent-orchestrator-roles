package sessionmanager

import (
	"context"
	"errors"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

func TestSessionOutputReadsOnlyTheRequestedLiveTUIRuntime(t *testing.T) {
	st := newFakeStore()
	id := domain.SessionID("mer-worker")
	st.sessions[id] = domain.SessionRecord{
		ID: id, ProjectID: "mer", Mode: domain.SessionModeTUI,
		Metadata: domain.SessionMetadata{RuntimeHandleID: "tmux-mer-worker"},
	}
	rt := &fakeRuntime{outputs: []string{"verification complete\nPASS"}}
	m := New(Deps{Store: st, Runtime: rt})

	got, err := m.SessionOutput(context.Background(), id, 37)
	if err != nil {
		t.Fatal(err)
	}
	if got != "verification complete\nPASS" {
		t.Fatalf("output = %q", got)
	}
	if rt.outputCalls != 1 || rt.lastOutputHandle.ID != "tmux-mer-worker" || rt.lastOutputLines != 37 {
		t.Fatalf("runtime read = calls:%d handle:%q lines:%d", rt.outputCalls, rt.lastOutputHandle.ID, rt.lastOutputLines)
	}
}

func TestSessionOutputRefusesNonTerminalOrInvalidReadsBeforeRuntime(t *testing.T) {
	tests := []struct {
		name  string
		rec   domain.SessionRecord
		lines int
		want  error
	}{
		{"chat", domain.SessionRecord{ID: "s", Mode: domain.SessionModeChat, Metadata: domain.SessionMetadata{RuntimeHandleID: "h"}}, 20, ErrSessionOutputUnavailable},
		{"terminated", domain.SessionRecord{ID: "s", IsTerminated: true, Metadata: domain.SessionMetadata{RuntimeHandleID: "h"}}, 20, ErrTerminated},
		{"missing handle", domain.SessionRecord{ID: "s"}, 20, ErrIncompleteHandle},
		{"zero lines", domain.SessionRecord{ID: "s", Metadata: domain.SessionMetadata{RuntimeHandleID: "h"}}, 0, ErrSessionOutputLinesInvalid},
		{"too many lines", domain.SessionRecord{ID: "s", Metadata: domain.SessionMetadata{RuntimeHandleID: "h"}}, 1001, ErrSessionOutputLinesInvalid},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			st := newFakeStore()
			st.sessions["s"] = tt.rec
			rt := &fakeRuntime{outputs: []string{"must not leak"}}
			m := New(Deps{Store: st, Runtime: rt})
			if _, err := m.SessionOutput(context.Background(), "s", tt.lines); !errors.Is(err, tt.want) {
				t.Fatalf("err = %v, want %v", err, tt.want)
			}
			if rt.outputCalls != 0 {
				t.Fatalf("runtime output calls = %d, want 0", rt.outputCalls)
			}
		})
	}
}
