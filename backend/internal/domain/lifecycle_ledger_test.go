package domain

import "testing"

func TestLifecycleLedgerKind_Valid(t *testing.T) {
	for _, k := range []LifecycleLedgerKind{
		LifecycleKindSwitch, LifecycleKindPause, LifecycleKindResume,
		LifecycleKindFailover, LifecycleKindFreshConversation,
	} {
		if !k.Valid() {
			t.Fatalf("%q should be valid", k)
		}
	}
	if (LifecycleLedgerKind("chat")).Valid() {
		t.Fatal("chat must not be a ledger kind")
	}
}
