package codex

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// The config directory is part of a native-session identity. Sequential
// probes in one daemon process must not leak either the ambient profile or a
// prior invocation's explicitly selected profile into the next invocation.
func TestContinuationBoundaryKeepsCodexHomesInvocationScoped(t *testing.T) {
	p := &Plugin{}
	sessionID := "019f9f7c-53c0-7f10-8d56-a8a979dd7001"
	firstRoot := t.TempDir()
	secondRoot := t.TempDir()
	t.Setenv(codexHomeEnv, t.TempDir())

	transcript := filepath.Join(firstRoot, "sessions", "2026", "08", "09", "rollout-2026-08-09T00-00-00-"+sessionID+".jsonl")
	if err := os.MkdirAll(filepath.Dir(transcript), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(transcript, []byte("{\"type\":\"session_meta\"}\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	firstDir, err := p.NativeSessionConfigDir(context.Background(), map[string]string{codexHomeEnv: firstRoot})
	if err != nil {
		t.Fatal(err)
	}
	secondDir, err := p.NativeSessionConfigDir(context.Background(), map[string]string{codexHomeEnv: secondRoot})
	if err != nil {
		t.Fatal(err)
	}
	if firstDir != firstRoot || secondDir != secondRoot {
		t.Fatalf("resolved config roots = (%q, %q), want (%q, %q)", firstDir, secondDir, firstRoot, secondRoot)
	}

	first := ports.NativeSessionRef{NativeSessionID: sessionID, ConfigDir: firstDir}
	second := ports.NativeSessionRef{NativeSessionID: sessionID, ConfigDir: secondDir}
	if got, err := p.ProbeNativeSession(context.Background(), first); err != nil || got != ports.NativeSessionAvailabilityAvailable {
		t.Fatalf("first CODEX_HOME probe = (%q, %v), want available", got, err)
	}
	if got, err := p.ProbeNativeSession(context.Background(), second); err != nil || got != ports.NativeSessionAvailabilityUnavailable {
		t.Fatalf("second CODEX_HOME probe = (%q, %v), want unavailable", got, err)
	}
	if path, ok, err := p.LocateTranscript(context.Background(), second); err != nil || ok || path != "" {
		t.Fatalf("second CODEX_HOME transcript = (%q, %v, %v), want no cross-profile result", path, ok, err)
	}
}
