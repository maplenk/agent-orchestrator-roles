package spawncred_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/spawncred"
)

func TestAuthority_TokenStableAndValid(t *testing.T) {
	dir := t.TempDir()
	a, err := spawncred.LoadAuthority(dir)
	if err != nil {
		t.Fatal(err)
	}
	tok := a.Token("mer-1")
	if tok == "" {
		t.Fatal("empty token")
	}
	if !a.Valid("mer-1", tok) {
		t.Fatal("expected valid")
	}
	if a.Valid("mer-2", tok) {
		t.Fatal("token must not validate for other session")
	}
	if a.Valid("mer-1", tok+"x") {
		t.Fatal("tampered token must fail")
	}
	if a.Valid("mer-1", "") {
		t.Fatal("empty token must fail")
	}

	// Survives reload from same data dir.
	a2, err := spawncred.LoadAuthority(dir)
	if err != nil {
		t.Fatal(err)
	}
	if a2.Token(domain.SessionID("mer-1")) != tok {
		t.Fatal("token must be stable across reload")
	}
	if _, err := os.Stat(filepath.Join(dir, "spawn-capability.key")); err != nil {
		t.Fatalf("key file: %v", err)
	}
}

func TestAuthority_DistinctFromBrowserShape(t *testing.T) {
	// Different sessions get different tokens; same session is deterministic.
	dir := t.TempDir()
	a, err := spawncred.LoadAuthority(dir)
	if err != nil {
		t.Fatal(err)
	}
	if a.Token("a") == a.Token("b") {
		t.Fatal("tokens must differ by session")
	}
}
