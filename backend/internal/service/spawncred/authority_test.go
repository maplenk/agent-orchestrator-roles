package spawncred_test

import (
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/service/spawncred"
)

func TestIssue_ValidAndNonMintable(t *testing.T) {
	a, ha, err := spawncred.Issue()
	if err != nil {
		t.Fatal(err)
	}
	b, hb, err := spawncred.Issue()
	if err != nil {
		t.Fatal(err)
	}
	if a == b || ha == hb {
		t.Fatal("tokens/hashes must be unique")
	}
	if !spawncred.ValidToken(a, ha) {
		t.Fatal("a should validate against ha")
	}
	if spawncred.ValidToken(a, hb) {
		t.Fatal("a must not validate against other session hash")
	}
	if spawncred.ValidToken("", ha) || spawncred.ValidToken(a, "") {
		t.Fatal("empty must fail")
	}
}

func TestOperator_Valid(t *testing.T) {
	op, tok, err := spawncred.NewOperator()
	if err != nil {
		t.Fatal(err)
	}
	if !op.Valid(tok) {
		t.Fatal("operator token should validate")
	}
	if op.Valid(tok + "x") {
		t.Fatal("tampered operator token must fail")
	}
	if op.Valid("") {
		t.Fatal("empty must fail")
	}
}
