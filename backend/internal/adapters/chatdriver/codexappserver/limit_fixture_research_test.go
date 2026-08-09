package codexappserver

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	rolecaps "github.com/aoagents/agent-orchestrator/backend/internal/roles/capabilities"
)

const codexLimitFixtureDir = "testdata/limits"

type capturedRateLimitWindow struct {
	UsedPercent        int   `json:"usedPercent"`
	WindowDurationMins int   `json:"windowDurationMins"`
	ResetsAt           int64 `json:"resetsAt"`
}

type capturedRateLimit struct {
	LimitID              string                   `json:"limitId"`
	Primary              *capturedRateLimitWindow `json:"primary"`
	SpendControlReached  *bool                    `json:"spendControlReached"`
	PlanType             string                   `json:"planType"`
	RateLimitReachedType *string                  `json:"rateLimitReachedType"`
}

type capturedUpdatedFrame struct {
	Method string `json:"method"`
	Params struct {
		RateLimits capturedRateLimit `json:"rateLimits"`
	} `json:"params"`
	EmittedAtMS int64 `json:"emittedAtMs"`
}

type capturedReadFrame struct {
	ID     int `json:"id"`
	Result struct {
		RateLimits          capturedRateLimit            `json:"rateLimits"`
		RateLimitsByLimitID map[string]capturedRateLimit `json:"rateLimitsByLimitId"`
	} `json:"result"`
}

type limitFixtureProvenance struct {
	SchemaVersion int    `json:"schemaVersion"`
	Vendor        string `json:"vendor"`
	Harness       string `json:"harness"`
	Surface       string `json:"surface"`
	Transport     string `json:"transport"`
	Reason        string `json:"reason"`
	Fixtures      []struct {
		File              string `json:"file"`
		CLIVersion        string `json:"cliVersion"`
		Method            string `json:"method"`
		Direction         string `json:"direction"`
		CapturedAt        string `json:"capturedAt"`
		SHA256            string `json:"sha256"`
		ObservedLimit     bool   `json:"observedLimit"`
		PromotionEligible bool   `json:"promotionEligible"`
		Sanitization      string `json:"sanitization"`
	} `json:"fixtures"`
}

func TestCapturedCodexLimitFixturesAreStructuredNegativeEvidence(t *testing.T) {
	updatedRaw := readCodexLimitFixtureBytes(t, "account_rate_limits_updated_0.146.0_not_reached.json")
	updatedObject := decodeFixtureObject(t, updatedRaw)
	assertExactFixtureKeys(t, updatedObject, "method", "params", "emittedAtMs")
	assertJSONString(t, updatedObject["method"])
	assertJSONInteger(t, updatedObject["emittedAtMs"])
	updatedParams := decodeFixtureObject(t, updatedObject["params"])
	assertExactFixtureKeys(t, updatedParams, "rateLimits")
	assertRateLimitFixtureShape(t, updatedParams["rateLimits"], "null", "object")

	var updated capturedUpdatedFrame
	decodeCodexLimitFixture(t, "account_rate_limits_updated_0.146.0_not_reached.json", updatedRaw, &updated)
	if updated.Method != "account/rateLimits/updated" || updated.EmittedAtMS <= 0 {
		t.Fatalf("updated frame metadata = method %q emittedAtMs %d", updated.Method, updated.EmittedAtMS)
	}
	assertCapturedLimitNotReached(t, updated.Params.RateLimits, "codex")
	events := normalizeNotification(notification{
		Method: updated.Method,
		Params: updatedObject["params"],
	}, time.UnixMilli(updated.EmittedAtMS))
	if len(events) != 1 || events[0].Kind != ports.ChatEventRateLimits || events[0].RateLimits == nil {
		t.Fatalf("captured update normalized to %+v, want one rate-limit state event", events)
	}
	if events[0].RateLimits.PrimaryUsedPercent != float64(updated.Params.RateLimits.Primary.UsedPercent) {
		t.Fatalf("normalized percent = %v, captured = %d",
			events[0].RateLimits.PrimaryUsedPercent, updated.Params.RateLimits.Primary.UsedPercent)
	}

	readRaw := readCodexLimitFixtureBytes(t, "account_rate_limits_read_0.147.0_not_reached.json")
	readObject := decodeFixtureObject(t, readRaw)
	assertExactFixtureKeys(t, readObject, "id", "result")
	assertJSONInteger(t, readObject["id"])
	readResult := decodeFixtureObject(t, readObject["result"])
	assertExactFixtureKeys(t, readResult, "rateLimits", "rateLimitsByLimitId", "rateLimitResetCredits")
	assertRateLimitFixtureShape(t, readResult["rateLimits"], "false", "object")
	byLimit := decodeFixtureObject(t, readResult["rateLimitsByLimitId"])
	assertExactFixtureKeys(t, byLimit, "codex", "codex_bengalfox")
	assertRateLimitFixtureShape(t, byLimit["codex"], "false", "object")
	assertRateLimitFixtureShape(t, byLimit["codex_bengalfox"], "null", "null")
	resetCredits := decodeFixtureObject(t, readResult["rateLimitResetCredits"])
	assertExactFixtureKeys(t, resetCredits, "availableCount", "credits")
	assertJSONInteger(t, resetCredits["availableCount"])
	assertEmptyJSONArray(t, resetCredits["credits"])

	var read capturedReadFrame
	decodeCodexLimitFixture(t, "account_rate_limits_read_0.147.0_not_reached.json", readRaw, &read)
	if read.ID != 2 {
		t.Fatalf("read response id = %d, want captured request id 2", read.ID)
	}
	assertCapturedLimitNotReached(t, read.Result.RateLimits, "codex")
	for limitID, snapshot := range read.Result.RateLimitsByLimitID {
		assertCapturedLimitNotReached(t, snapshot, limitID)
	}
}

func TestCapturedCodexLimitFixtureProvenanceIsBoundToBytes(t *testing.T) {
	raw := readCodexLimitFixtureBytes(t, "provenance.json")
	object := decodeFixtureObject(t, raw)
	assertExactFixtureKeys(t, object,
		"schemaVersion", "vendor", "harness", "surface", "transport", "fixtures", "reason")
	assertJSONInteger(t, object["schemaVersion"])
	for _, key := range []string{"vendor", "harness", "surface", "transport", "reason"} {
		assertJSONString(t, object[key])
	}
	var fixtureRecords []json.RawMessage
	if err := json.Unmarshal(object["fixtures"], &fixtureRecords); err != nil || len(fixtureRecords) != 2 {
		t.Fatalf("decode provenance fixture records: count=%d err=%v", len(fixtureRecords), err)
	}
	for _, rawRecord := range fixtureRecords {
		record := decodeFixtureObject(t, rawRecord)
		assertExactFixtureKeys(t, record, "file", "cliVersion", "method", "direction", "capturedAt",
			"sha256", "observedLimit", "promotionEligible", "sanitization")
		for _, key := range []string{"file", "cliVersion", "method", "direction", "capturedAt", "sha256", "sanitization"} {
			assertJSONString(t, record[key])
		}
		assertJSONRaw(t, record["observedLimit"], "false")
		assertJSONRaw(t, record["promotionEligible"], "false")
	}

	var provenance limitFixtureProvenance
	decodeCodexLimitFixture(t, "provenance.json", raw, &provenance)
	if provenance.SchemaVersion != 1 || provenance.Vendor != "codex" || provenance.Harness != "codex" ||
		provenance.Surface != "chat-app-server" || provenance.Transport != "JSON-RPC over stdio" ||
		strings.TrimSpace(provenance.Reason) == "" {
		t.Fatalf("fixture provenance = %#v", provenance)
	}
	if len(provenance.Fixtures) != 2 {
		t.Fatalf("fixture provenance has %d records, want 2", len(provenance.Fixtures))
	}
	expectedMetadata := map[string]struct {
		version, method, direction, capturedAt, sha256 string
	}{
		"account_rate_limits_updated_0.146.0_not_reached.json": {
			version: "0.146.0", method: "account/rateLimits/updated", direction: "server notification",
			capturedAt: "2026-08-02T11:18:23Z",
			sha256:     "a0190ee2a85e225c210a023da0a305a34acb1016f7a65d2298e9cbc781c688ea",
		},
		"account_rate_limits_read_0.147.0_not_reached.json": {
			version: "0.147.0", method: "account/rateLimits/read", direction: "server response",
			capturedAt: "2026-08-09T06:23:03Z",
			sha256:     "84f8b803a2f15fae11c9955009ba31ed6a4de808457707011dd825b1814ccc3a",
		},
	}
	seen := map[string]bool{}
	for i, fixture := range provenance.Fixtures {
		expected, expectedOK := expectedMetadata[fixture.File]
		if !expectedOK || seen[fixture.File] || strings.TrimSpace(fixture.Sanitization) == "" {
			t.Fatalf("incomplete or duplicate fixture provenance: %#v", fixture)
		}
		seen[fixture.File] = true
		delete(expectedMetadata, fixture.File)
		if fixture.CLIVersion != expected.version || fixture.Method != expected.method ||
			fixture.Direction != expected.direction || fixture.CapturedAt != expected.capturedAt ||
			fixture.SHA256 != expected.sha256 {
			t.Fatalf("fixture metadata = %#v, expected = %#v", fixture, expected)
		}
		capturedAt, err := time.Parse(time.RFC3339, fixture.CapturedAt)
		if err != nil || capturedAt.Location() != time.UTC || !strings.HasSuffix(fixture.CapturedAt, "Z") {
			t.Fatalf("fixture capturedAt = %q, want RFC3339 UTC", fixture.CapturedAt)
		}
		if fixture.ObservedLimit || fixture.PromotionEligible {
			t.Fatalf("negative fixture marked promotable: %#v", fixture)
		}
		fixtureRaw := readCodexLimitFixtureBytes(t, fixture.File)
		sum := sha256.Sum256(fixtureRaw)
		if got := hex.EncodeToString(sum[:]); got != fixture.SHA256 {
			t.Fatalf("fixture %d (%s) sha256 = %s, provenance = %s",
				i, fixture.File, got, fixture.SHA256)
		}
	}
	if len(expectedMetadata) != 0 {
		t.Fatalf("missing fixture metadata: %v", expectedMetadata)
	}
	if rolecaps.For(domain.HarnessCodex).LimitDetectionSupported {
		t.Fatal("negative research fixtures promoted Codex limit detection")
	}
}

func assertCapturedLimitNotReached(t *testing.T, got capturedRateLimit, wantLimitID string) {
	t.Helper()
	if got.LimitID != wantLimitID || got.Primary == nil {
		t.Fatalf("captured rate limit = %#v, want %s with a primary window", got, wantLimitID)
	}
	if got.Primary.UsedPercent < 0 || got.Primary.UsedPercent > 100 {
		t.Fatalf("captured used percent = %d, want 0..100", got.Primary.UsedPercent)
	}
	if got.Primary.WindowDurationMins <= 0 || got.Primary.ResetsAt <= 0 {
		t.Fatalf("captured primary window = %#v, want positive duration/reset", got.Primary)
	}
	if got.SpendControlReached != nil && *got.SpendControlReached {
		t.Fatal("negative fixture claims spend control was reached")
	}
	if got.RateLimitReachedType != nil {
		t.Fatalf("negative fixture claims reached type %q", *got.RateLimitReachedType)
	}
}

func assertRateLimitFixtureShape(t *testing.T, raw json.RawMessage, wantSpend, wantCredits string) {
	t.Helper()
	object := decodeFixtureObject(t, raw)
	assertExactFixtureKeys(t, object, "limitId", "limitName", "primary", "secondary", "credits",
		"individualLimit", "spendControlReached", "planType", "rateLimitReachedType")
	assertJSONString(t, object["limitId"])
	assertJSONNullOrString(t, object["limitName"])
	primary := decodeFixtureObject(t, object["primary"])
	assertExactFixtureKeys(t, primary, "usedPercent", "windowDurationMins", "resetsAt")
	assertJSONInteger(t, primary["usedPercent"])
	assertJSONInteger(t, primary["windowDurationMins"])
	assertJSONInteger(t, primary["resetsAt"])
	assertJSONRaw(t, object["secondary"], "null")
	assertJSONRaw(t, object["individualLimit"], "null")
	assertJSONRaw(t, object["spendControlReached"], wantSpend)
	assertJSONString(t, object["planType"])
	assertJSONRaw(t, object["rateLimitReachedType"], "null")
	if wantCredits == "null" {
		assertJSONRaw(t, object["credits"], "null")
		return
	}
	credits := decodeFixtureObject(t, object["credits"])
	assertExactFixtureKeys(t, credits, "hasCredits", "unlimited", "balance")
	assertJSONBool(t, credits["hasCredits"])
	assertJSONBool(t, credits["unlimited"])
	assertJSONString(t, credits["balance"])
}

func readCodexLimitFixtureBytes(t *testing.T, name string) []byte {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(codexLimitFixtureDir, name)) //nolint:gosec // fixed testdata path
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func decodeCodexLimitFixture(t *testing.T, name string, raw []byte, dst any) {
	t.Helper()
	if err := json.Unmarshal(raw, dst); err != nil {
		t.Fatalf("decode %s: %v", name, err)
	}
}

func decodeFixtureObject(t *testing.T, raw []byte) map[string]json.RawMessage {
	t.Helper()
	var object map[string]json.RawMessage
	if err := json.Unmarshal(raw, &object); err != nil || object == nil {
		t.Fatalf("decode fixture object: %v", err)
	}
	return object
}

func assertExactFixtureKeys(t *testing.T, object map[string]json.RawMessage, want ...string) {
	t.Helper()
	if len(object) != len(want) {
		t.Fatalf("fixture keys = %v, want exactly %v", fixtureKeys(object), want)
	}
	for _, key := range want {
		if _, ok := object[key]; !ok {
			t.Fatalf("fixture keys = %v, missing %q", fixtureKeys(object), key)
		}
	}
}

func fixtureKeys(object map[string]json.RawMessage) []string {
	keys := make([]string, 0, len(object))
	for key := range object {
		keys = append(keys, key)
	}
	return keys
}

func assertJSONRaw(t *testing.T, raw json.RawMessage, want string) {
	t.Helper()
	if got := strings.TrimSpace(string(raw)); got != want {
		t.Fatalf("fixture value = %s, want exact %s", got, want)
	}
}

func assertJSONInteger(t *testing.T, raw json.RawMessage) {
	t.Helper()
	text := strings.TrimSpace(string(raw))
	if strings.ContainsAny(text, ".eE") {
		t.Fatalf("fixture number = %s, want integer JSON representation", text)
	}
	if _, err := strconv.ParseInt(text, 10, 64); err != nil {
		t.Fatalf("fixture number = %s, want integer: %v", text, err)
	}
}

func assertJSONString(t *testing.T, raw json.RawMessage) {
	t.Helper()
	var value string
	if err := json.Unmarshal(raw, &value); err != nil {
		t.Fatalf("fixture value = %s, want JSON string: %v", raw, err)
	}
}

func assertJSONNullOrString(t *testing.T, raw json.RawMessage) {
	t.Helper()
	if strings.TrimSpace(string(raw)) == "null" {
		return
	}
	assertJSONString(t, raw)
}

func assertJSONBool(t *testing.T, raw json.RawMessage) {
	t.Helper()
	var value bool
	if err := json.Unmarshal(raw, &value); err != nil {
		t.Fatalf("fixture value = %s, want JSON boolean: %v", raw, err)
	}
}

func assertEmptyJSONArray(t *testing.T, raw json.RawMessage) {
	t.Helper()
	var values []json.RawMessage
	if err := json.Unmarshal(raw, &values); err != nil || values == nil || len(values) != 0 {
		t.Fatalf("fixture value = %s, want empty JSON array", raw)
	}
}
