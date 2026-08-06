package domain

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// MaxLimitEnvelopeBytes bounds a structured limit envelope. The envelope is
// durable and is copied into the lifecycle ledger, so an unbounded blob would
// be a write amplifier on a path that runs when a harness is already in
// trouble. The real envelopes are a few hundred bytes.
const MaxLimitEnvelopeBytes = 8 << 10

// LimitEnvelopeKind is the closed discriminator set. A limit AO will act on has
// to be one AO has a defined response to; an open string here would be the
// free-text channel MASTER_PLAN §7 rule 1 forbids, one indirection removed.
type LimitEnvelopeKind string

const (
	// LimitEnvelopeUsageLimit is a provider/plan usage limit: the account or
	// key cannot proceed until a window resets.
	LimitEnvelopeUsageLimit LimitEnvelopeKind = "usage_limit"
)

// Valid reports whether k is a known envelope kind.
func (k LimitEnvelopeKind) Valid() bool {
	return k == LimitEnvelopeUsageLimit
}

// LimitEnvelopeV1 is the ONLY shape a structured limit report may take.
//
// It is versioned because it is durable: rows written today are read by later
// binaries, and a decoder that silently accepted an unrecognised shape would
// re-open the free-text hole from the other side. Version is required and
// exact — "absent" is not treated as 1, because an envelope with no version is
// exactly what hand-written prose looks like after someone wraps it in braces.
type LimitEnvelopeV1 struct {
	// Version must be 1.
	Version int `json:"version"`
	// Kind is the discriminator.
	Kind LimitEnvelopeKind `json:"kind"`
	// Harness that reported the limit.
	Harness AgentHarness `json:"harness,omitempty"`
	// Scope names what is exhausted (e.g. "account", "organization", "model"),
	// for the human reading it. Advisory.
	Scope string `json:"scope,omitempty"`
	// ResetsAt is when the provider said the window reopens, if it said.
	// Advisory: nothing schedules against it (see SessionPause).
	ResetsAt *time.Time `json:"resetsAt,omitempty"`
	// Detail is the adapter's short human-readable note. It is a LEAF, never a
	// control input: nothing branches on it. Bounded by the envelope size cap.
	Detail string `json:"detail,omitempty"`
}

// ParseLimitEnvelope decodes and validates a structured limit envelope.
//
// This is the enforcement point for "structured / reviewed envelopes only". A
// non-empty-string check is not enough: `"I hit a limit"` is a perfectly valid
// JSON string, and `["I hit a limit"]` a perfectly valid JSON array, so both
// would sail through any test that only asks "is there something there?". The
// decode therefore requires a JSON OBJECT, rejects unknown fields so prose
// cannot ride along in an extra key, and requires an exact version and a known
// kind.
func ParseLimitEnvelope(raw string) (LimitEnvelopeV1, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return LimitEnvelopeV1{}, fmt.Errorf("limit envelope: empty")
	}
	if len(trimmed) > MaxLimitEnvelopeBytes {
		return LimitEnvelopeV1{}, fmt.Errorf("limit envelope: %d bytes exceeds the %d byte cap",
			len(trimmed), MaxLimitEnvelopeBytes)
	}
	// Must be an object. Checked before decoding because encoding/json will
	// happily unmarshal `null` into a struct and leave every field zero, which
	// would then fail with a confusing "version" error instead of the truth.
	if trimmed[0] != '{' {
		return LimitEnvelopeV1{}, fmt.Errorf("limit envelope: must be a JSON object, got %s", jsonShape(trimmed))
	}

	dec := json.NewDecoder(bytes.NewReader([]byte(trimmed)))
	dec.DisallowUnknownFields()
	var env LimitEnvelopeV1
	if err := dec.Decode(&env); err != nil {
		return LimitEnvelopeV1{}, fmt.Errorf("limit envelope: %w", err)
	}
	// Trailing content after the object would mean the caller sent a stream,
	// not a document; the first value alone must account for the whole input.
	if dec.More() {
		return LimitEnvelopeV1{}, fmt.Errorf("limit envelope: trailing content after the object")
	}

	if env.Version != 1 {
		return LimitEnvelopeV1{}, fmt.Errorf("limit envelope: version must be 1, got %d", env.Version)
	}
	if !env.Kind.Valid() {
		return LimitEnvelopeV1{}, fmt.Errorf("limit envelope: unknown kind %q", env.Kind)
	}
	return env, nil
}

// jsonShape names what the caller actually sent, so the error says "got a
// string" rather than leaving someone to guess why their text was refused.
func jsonShape(trimmed string) string {
	switch trimmed[0] {
	case '"':
		return "a string"
	case '[':
		return "an array"
	case 't', 'f':
		return "a boolean"
	case 'n':
		return "null"
	default:
		return "a number or malformed JSON"
	}
}
