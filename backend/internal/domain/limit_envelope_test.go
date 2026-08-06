package domain

import (
	"strings"
	"testing"
)

const goodEnvelope = `{"version":1,"kind":"usage_limit","harness":"codex","scope":"account"}`

// The forgeries are the point. Every one of these is something a non-empty
// string check accepts, and each is a way free text could have become a
// "structured" limit report — the exact failure MASTER_PLAN §7 rule 1 names.
func TestParseLimitEnvelopeRejectsForgeries(t *testing.T) {
	for _, tc := range []struct {
		name string
		raw  string
		want string
	}{
		{"bare prose", `I hit a limit`, "JSON object"},
		{"prose as a JSON string", `"I hit a limit"`, "a string"},
		{"prose in an array", `["I hit a limit"]`, "an array"},
		{"a number", `429`, "a number"},
		{"null", `null`, "null"},
		{"boolean", `true`, "a boolean"},
		{"empty", ``, "empty"},
		{"whitespace only", `   `, "empty"},
		{"malformed object", `{"version":1,`, "limit envelope"},
		// A missing version is what hand-written prose looks like once someone
		// wraps it in braces, so absent must not be read as 1.
		{"missing version", `{"kind":"usage_limit"}`, "version must be 1"},
		{"wrong version", `{"version":2,"kind":"usage_limit"}`, "version must be 1"},
		{"missing kind", `{"version":1}`, "unknown kind"},
		{"unknown kind", `{"version":1,"kind":"vibes"}`, "unknown kind"},
		// Prose riding along in an extra key would otherwise be preserved
		// verbatim in durable state and in the ledger.
		{"unknown field smuggling prose", `{"version":1,"kind":"usage_limit","note":"I hit a limit"}`, "unknown field"},
		{"trailing content", `{"version":1,"kind":"usage_limit"} {"more":1}`, "trailing content"},
		// Decoder.More() is NOT an end-of-document check: it asks "another
		// element in the current array or object?", so at top level it returns
		// false for a closing delimiter. Both of these decoded cleanly with
		// More()==false and were accepted as well-formed envelopes. Only a
		// second decode returning io.EOF proves the input ended.
		{"trailing array delimiter", `{"version":1,"kind":"usage_limit"}]`, "malformed input after"},
		{"trailing object delimiter", `{"version":1,"kind":"usage_limit"}}`, "malformed input after"},
		{"fragment of a larger array", `{"version":1,"kind":"usage_limit"},{"version":1,"kind":"usage_limit"}]`, "malformed input after"},
		{"trailing garbage", `{"version":1,"kind":"usage_limit"}garbage`, "after the object"},
		{"oversize", `{"version":1,"kind":"usage_limit","detail":"` + strings.Repeat("x", MaxLimitEnvelopeBytes) + `"}`, "cap"},
		// The cap must bind the RAW bytes: the raw string is what is persisted
		// into pause_json and copied verbatim into the ledger, so measuring the
		// trimmed substring would let unbounded whitespace through.
		{"whitespace padding over the cap", strings.Repeat(" ", MaxLimitEnvelopeBytes) + `{"version":1,"kind":"usage_limit"}`, "cap"},
		{"leading whitespace over the cap", strings.Repeat("\n", MaxLimitEnvelopeBytes+1) + `{"version":1,"kind":"usage_limit"}`, "cap"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ParseLimitEnvelope(tc.raw)
			if err == nil {
				t.Fatalf("accepted %q as a structured limit envelope", tc.raw)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error %q does not mention %q", err, tc.want)
			}
		})
	}
}

func TestParseLimitEnvelopeAcceptsRealOnes(t *testing.T) {
	for _, raw := range []string{
		`{"version":1,"kind":"usage_limit"}`,
		goodEnvelope,
		`{"version":1,"kind":"usage_limit","resetsAt":"2026-08-06T18:00:00Z","detail":"5h window"}`,
		"  " + goodEnvelope + "  ",
	} {
		env, err := ParseLimitEnvelope(raw)
		if err != nil {
			t.Fatalf("rejected a legitimate envelope %q: %v", raw, err)
		}
		if env.Kind != LimitEnvelopeUsageLimit || env.Version != 1 {
			t.Fatalf("parsed to %+v", env)
		}
	}
}

// The size cap is a real boundary, not an approximation: an envelope one byte
// under it must still parse, or the cap is silently stricter than documented.
func TestParseLimitEnvelopeSizeBoundary(t *testing.T) {
	prefix := `{"version":1,"kind":"usage_limit","detail":"`
	suffix := `"}`
	fill := MaxLimitEnvelopeBytes - len(prefix) - len(suffix)
	atCap := prefix + strings.Repeat("x", fill) + suffix
	if len(atCap) != MaxLimitEnvelopeBytes {
		t.Fatalf("test built a %d byte envelope, want exactly %d", len(atCap), MaxLimitEnvelopeBytes)
	}
	if _, err := ParseLimitEnvelope(atCap); err != nil {
		t.Fatalf("an envelope exactly at the cap was rejected: %v", err)
	}
	if _, err := ParseLimitEnvelope(prefix + strings.Repeat("x", fill+1) + suffix); err == nil {
		t.Fatal("an envelope one byte over the cap was accepted")
	}
}

func TestLimitEnvelopeKindIsClosed(t *testing.T) {
	if !LimitEnvelopeUsageLimit.Valid() {
		t.Fatal("usage_limit is not valid")
	}
	for _, k := range []LimitEnvelopeKind{"", "usage-limit", "USAGE_LIMIT", "rate_limit"} {
		if k.Valid() {
			t.Errorf("kind %q is accepted; the discriminator set must stay closed", k)
		}
	}
}
