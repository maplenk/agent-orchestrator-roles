package store

import (
	"strings"
	"testing"
)

func TestDecodeSwitchPending(t *testing.T) {
	p, err := decodeSwitchPending("")
	if err != nil || p != nil {
		t.Fatalf("empty: %v %v", p, err)
	}
	p, err = decodeSwitchPending(`{"generationId":"g1","toHarness":"codex"}`)
	if err != nil || p == nil || p.GenerationID != "g1" {
		t.Fatalf("valid: %+v err=%v", p, err)
	}
	_, err = decodeSwitchPending("{not-json")
	if err == nil || !strings.Contains(err.Error(), "corrupt") {
		t.Fatalf("malformed: %v", err)
	}
	_, err = decodeSwitchPending(`{"toHarness":"codex"}`)
	if err == nil || !strings.Contains(err.Error(), "generationId") {
		t.Fatalf("missing gen: %v", err)
	}
}
