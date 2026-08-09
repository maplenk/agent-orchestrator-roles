package claudecode_test

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/limits"
	"github.com/aoagents/agent-orchestrator/backend/internal/roles/capabilities"
)

const (
	claudeRejectedEventFixture = "claude-code-2.1.159-rate-limit-event-rejected.json"
	claudeRejectedEventSource  = "c2014b6fc8be662bb5e852d04d29bfeb9936e493f821ec50caab08345ddbfffb"
	claudeRejectedEventURL     = "https://gist.githubusercontent.com/konard/218091cfcd951e88bb6f1b91a001e26c/raw/694141dfd582be191bcd1d5d391b6eb3ca5de013/solution-draft-log-pr-1780356981419.txt"
	maxClaudeRateLimitEvent    = 4 << 10
	maxClaudeResetUnix         = int64(253402300799) // 9999-12-31T23:59:59Z, the latest JSON-representable time.Time.
)

type claudeRateLimitEvent struct {
	Type          string              `json:"type"`
	RateLimitInfo claudeRateLimitInfo `json:"rate_limit_info"`
	UUID          string              `json:"uuid"`
	SessionID     string              `json:"session_id"`
}

type claudeRateLimitInfo struct {
	Status                string `json:"status"`
	ResetsAt              int64  `json:"resetsAt"`
	RateLimitType         string `json:"rateLimitType"`
	OverageStatus         string `json:"overageStatus"`
	OverageDisabledReason string `json:"overageDisabledReason"`
	IsUsingOverage        bool   `json:"isUsingOverage"`
}

// classifyClaudeRateLimitEvent is deliberately test-only. It proves that a
// real machine-structured positive event has enough stable vendor identity for
// an incident without claiming that AO's interactive TUI receives this stream.
func classifyClaudeRateLimitEvent(raw []byte) (domain.LimitEnvelopeV1, bool, error) {
	if len(raw) == 0 || len(raw) > maxClaudeRateLimitEvent {
		return domain.LimitEnvelopeV1{}, false, nil
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	var event claudeRateLimitEvent
	if err := dec.Decode(&event); err != nil {
		return domain.LimitEnvelopeV1{}, false, fmt.Errorf("decode Claude rate-limit event: %w", err)
	}
	var trailing json.RawMessage
	if err := dec.Decode(&trailing); err == nil {
		return domain.LimitEnvelopeV1{}, false, errors.New("decode Claude rate-limit event: trailing JSON value")
	} else if !errors.Is(err, io.EOF) {
		return domain.LimitEnvelopeV1{}, false, fmt.Errorf("decode Claude rate-limit event trailing bytes: %w", err)
	}
	if event.Type != "rate_limit_event" || event.RateLimitInfo.Status != "rejected" {
		if event.Type == "rate_limit_event" && event.RateLimitInfo.Status != "allowed" && event.RateLimitInfo.Status != "allowed_warning" {
			return domain.LimitEnvelopeV1{}, false, fmt.Errorf("Claude rate-limit event has unknown status %q", event.RateLimitInfo.Status)
		}
		return domain.LimitEnvelopeV1{}, false, nil
	}
	if !validClaudeRateLimitType(event.RateLimitInfo.RateLimitType) ||
		event.RateLimitInfo.ResetsAt <= 0 || event.RateLimitInfo.ResetsAt > maxClaudeResetUnix {
		return domain.LimitEnvelopeV1{}, false, errors.New("Claude rejected rate-limit event has no stable window identity")
	}

	source := event.RateLimitInfo.RateLimitType + "|" + fmt.Sprint(event.RateLimitInfo.ResetsAt)
	sum := sha256.Sum256([]byte("claude-code|" + source))
	resetsAt := time.Unix(event.RateLimitInfo.ResetsAt, 0).UTC()
	return domain.LimitEnvelopeV1{
		Version:   1,
		Kind:      domain.LimitEnvelopeUsageLimit,
		Harness:   domain.HarnessClaudeCode,
		Scope:     event.RateLimitInfo.RateLimitType,
		SourceKey: "claude-limit-" + hex.EncodeToString(sum[:16]),
		ResetsAt:  &resetsAt,
	}, true, nil
}

func validClaudeRateLimitType(kind string) bool {
	switch kind {
	case "five_hour", "seven_day", "seven_day_opus", "seven_day_sonnet", "seven_day_overage_included", "overage":
		return true
	default:
		return false
	}
}

func TestCapturedClaudeRateLimitEventHasStableIncidentIdentity(t *testing.T) {
	raw := readClaudeRateLimitEventFixture(t)
	first, ok, err := classifyClaudeRateLimitEvent(raw)
	if err != nil || !ok {
		t.Fatalf("classify real fixture: ok=%v err=%v", ok, err)
	}
	if first.Scope != "seven_day" || first.ResetsAt == nil || !first.ResetsAt.Equal(time.Unix(1780398000, 0).UTC()) || first.SourceKey == "" {
		t.Fatalf("classified envelope = %#v", first)
	}
	encoded, err := json.Marshal(first)
	if err != nil {
		t.Fatalf("encode candidate envelope: %v", err)
	}
	parsed, err := domain.ParseLimitEnvelope(string(encoded))
	if err != nil {
		t.Fatalf("parse candidate envelope: %v", err)
	}
	if parsed.SourceKey != first.SourceKey || limits.IncidentID(first) != limits.IncidentID(parsed) {
		t.Fatal("candidate replay changed its source key or incident ID")
	}

	var event claudeRateLimitEvent
	if err := json.Unmarshal(raw, &event); err != nil {
		t.Fatalf("decode real fixture: %v", err)
	}
	if event.Type != "rate_limit_event" || event.RateLimitInfo != (claudeRateLimitInfo{
		Status:                "rejected",
		ResetsAt:              1780398000,
		RateLimitType:         "seven_day",
		OverageStatus:         "rejected",
		OverageDisabledReason: "org_level_disabled",
		IsUsingOverage:        false,
	}) || event.UUID != "00000000-0000-4000-8000-000000000001" || event.SessionID != "00000000-0000-4000-8000-000000000002" {
		t.Fatalf("fixture fields drifted: %#v", event)
	}
	var fixtureObject map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fixtureObject); err != nil {
		t.Fatalf("decode fixture object: %v", err)
	}
	assertClaudeJSONKeys(t, fixtureObject, "type", "rate_limit_info", "uuid", "session_id")
	var infoObject map[string]json.RawMessage
	if err := json.Unmarshal(fixtureObject["rate_limit_info"], &infoObject); err != nil {
		t.Fatalf("decode rate_limit_info: %v", err)
	}
	assertClaudeJSONKeys(t, infoObject, "status", "resetsAt", "rateLimitType", "overageStatus", "overageDisabledReason", "isUsingOverage")
	event.UUID = "00000000-0000-4000-8000-000000000099"
	event.SessionID = "00000000-0000-4000-8000-000000000100"
	mutated, err := json.Marshal(event)
	if err != nil {
		t.Fatalf("encode delivery mutation: %v", err)
	}
	duplicate, ok, err := classifyClaudeRateLimitEvent(mutated)
	if err != nil || !ok || duplicate.SourceKey != first.SourceKey {
		t.Fatalf("delivery identity affected incident: got=%#v ok=%v err=%v", duplicate, ok, err)
	}
}

func TestCapturedClaudeRateLimitEventMutationsFailClosed(t *testing.T) {
	raw := readClaudeRateLimitEventFixture(t)
	var base claudeRateLimitEvent
	if err := json.Unmarshal(raw, &base); err != nil {
		t.Fatalf("decode fixture: %v", err)
	}
	tests := []struct {
		name    string
		mutate  func(*claudeRateLimitEvent)
		wantErr bool
	}{
		{"allowed status", func(event *claudeRateLimitEvent) { event.RateLimitInfo.Status = "allowed" }, false},
		{"warning status", func(event *claudeRateLimitEvent) { event.RateLimitInfo.Status = "allowed_warning" }, false},
		{"unknown status", func(event *claudeRateLimitEvent) { event.RateLimitInfo.Status = "future_status" }, true},
		{"wrong event type", func(event *claudeRateLimitEvent) { event.Type = "assistant" }, false},
		{"missing limit type", func(event *claudeRateLimitEvent) { event.RateLimitInfo.RateLimitType = "" }, true},
		{"unknown limit type", func(event *claudeRateLimitEvent) { event.RateLimitInfo.RateLimitType = "future_window" }, true},
		{"missing reset", func(event *claudeRateLimitEvent) { event.RateLimitInfo.ResetsAt = 0 }, true},
		{"unrepresentable reset", func(event *claudeRateLimitEvent) { event.RateLimitInfo.ResetsAt = maxClaudeResetUnix + 1 }, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			mutated := base
			tc.mutate(&mutated)
			encoded, err := json.Marshal(mutated)
			if err != nil {
				t.Fatalf("encode mutation: %v", err)
			}
			_, ok, err := classifyClaudeRateLimitEvent(encoded)
			if ok || (err != nil) != tc.wantErr {
				t.Fatalf("mutation result: ok=%v err=%v wantErr=%v", ok, err, tc.wantErr)
			}
		})
	}

	unknown := append(bytes.TrimSpace(raw), []byte(`{"unexpected":true}`)...)
	if _, ok, err := classifyClaudeRateLimitEvent(unknown); err == nil || ok {
		t.Fatalf("trailing JSON accepted: ok=%v err=%v", ok, err)
	}
	oversized := bytes.Repeat([]byte("x"), maxClaudeRateLimitEvent+1)
	if _, ok, err := classifyClaudeRateLimitEvent(oversized); err != nil || ok {
		t.Fatalf("oversized payload result: ok=%v err=%v", ok, err)
	}
}

func TestCapturedClaudeRateLimitEventProvenanceAndNonPromotion(t *testing.T) {
	type provenance struct {
		SchemaVersion int    `json:"schemaVersion"`
		Vendor        string `json:"vendor"`
		Product       string `json:"product"`
		CLIVersion    string `json:"cliVersion"`
		Surface       string `json:"surface"`
		Transport     string `json:"transport"`
		Direction     string `json:"direction"`
		ObservedAt    string `json:"observedAt"`
		RetrievedAt   string `json:"retrievedAt"`
		Source        struct {
			Kind       string `json:"kind"`
			URL        string `json:"url"`
			SHA256     string `json:"sha256"`
			EventLines string `json:"eventLines"`
		} `json:"source"`
		Fixture struct {
			File           string            `json:"file"`
			SHA256         string            `json:"sha256"`
			SelectedFields []string          `json:"selectedFields"`
			RedactedFields map[string]string `json:"redactedFields"`
		} `json:"fixture"`
		Classification struct {
			ObservedLimit               bool     `json:"observedLimit"`
			StableOccurrenceKeyObserved bool     `json:"stableOccurrenceKeyObserved"`
			SourceKeyFields             []string `json:"sourceKeyFields"`
			DuplicateDeliveryObserved   bool     `json:"duplicateDeliveryEmpiricallyObserved"`
			PromotionEligible           bool     `json:"promotionEligible"`
			Blocker                     string   `json:"blocker"`
		} `json:"classification"`
		SchemaCorroboration struct {
			InstalledCLIVersion string   `json:"installedCliVersion"`
			Surface             string   `json:"surface"`
			StatusValues        []string `json:"statusValues"`
			RateLimitTypes      []string `json:"rateLimitTypes"`
			WireCapture         bool     `json:"wireCapture"`
		} `json:"schemaCorroboration"`
		Privacy struct {
			AccountIdentifyingFieldsRetained bool `json:"accountIdentifyingFieldsRetained"`
			CredentialsRetained              bool `json:"credentialsRetained"`
			PromptsOrToolContentRetained     bool `json:"promptsOrToolContentRetained"`
			PathsRetained                    bool `json:"pathsRetained"`
			NonIdentifyingLimitTelemetryKept bool `json:"nonIdentifyingLimitTelemetryRetained"`
		} `json:"privacy"`
	}

	path := filepath.Join("testdata", "limits", strings.TrimSuffix(claudeRejectedEventFixture, ".json")+".provenance.json")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read provenance: %v", err)
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	var got provenance
	if err := dec.Decode(&got); err != nil {
		t.Fatalf("decode provenance: %v", err)
	}
	var provenanceObject map[string]json.RawMessage
	if err := json.Unmarshal(raw, &provenanceObject); err != nil {
		t.Fatalf("decode provenance object: %v", err)
	}
	assertClaudeJSONKeys(t, provenanceObject, "schemaVersion", "vendor", "product", "cliVersion", "surface", "transport", "direction", "observedAt", "retrievedAt", "source", "fixture", "classification", "schemaCorroboration", "privacy")
	if got.SchemaVersion != 1 || got.Vendor != "Anthropic" || got.Product != "Claude Code" || got.CLIVersion != "2.1.159" ||
		got.Surface != "print-stream-json" || got.Transport != "JSONL over stdout" || got.Direction != "vendor-to-client" ||
		got.ObservedAt != "2026-06-01T15:09:21.346Z" || got.RetrievedAt != "2026-08-09T17:20:53Z" {
		t.Fatalf("historical provenance = %#v", got)
	}
	if got.Source.Kind != "public-third-party-execution-log" || got.Source.URL != claudeRejectedEventURL ||
		got.Source.SHA256 != claudeRejectedEventSource || got.Source.EventLines != "692-704" {
		t.Fatalf("source provenance = %#v", got.Source)
	}
	fixtureRaw := readClaudeRateLimitEventFixture(t)
	fixtureSum := sha256.Sum256(fixtureRaw)
	if got.Fixture.File != claudeRejectedEventFixture || got.Fixture.SHA256 != hex.EncodeToString(fixtureSum[:]) {
		t.Fatalf("fixture provenance = %#v", got.Fixture)
	}
	wantSelected := []string{"type", "rate_limit_info.status", "rate_limit_info.resetsAt", "rate_limit_info.rateLimitType", "rate_limit_info.overageStatus", "rate_limit_info.overageDisabledReason", "rate_limit_info.isUsingOverage", "uuid", "session_id"}
	if !reflect.DeepEqual(got.Fixture.SelectedFields, wantSelected) || got.Fixture.RedactedFields["uuid"] == "" || got.Fixture.RedactedFields["session_id"] == "" {
		t.Fatalf("fixture selection/redaction = %#v", got.Fixture)
	}
	wantSourceKeyFields := []string{"rate_limit_info.rateLimitType", "rate_limit_info.resetsAt"}
	if !got.Classification.ObservedLimit || !got.Classification.StableOccurrenceKeyObserved || got.Classification.DuplicateDeliveryObserved || got.Classification.PromotionEligible ||
		!reflect.DeepEqual(got.Classification.SourceKeyFields, wantSourceKeyFields) ||
		!strings.Contains(got.Classification.Blocker, "interactive Claude TUI") || !strings.Contains(got.Classification.Blocker, "AO runtime generation") {
		t.Fatalf("classification provenance = %#v", got.Classification)
	}
	if got.SchemaCorroboration.InstalledCLIVersion != "2.1.226" || got.SchemaCorroboration.Surface != "local installed binary type schema" ||
		!reflect.DeepEqual(got.SchemaCorroboration.StatusValues, []string{"allowed", "allowed_warning", "rejected"}) ||
		!reflect.DeepEqual(got.SchemaCorroboration.RateLimitTypes, []string{"five_hour", "seven_day", "seven_day_opus", "seven_day_sonnet", "seven_day_overage_included", "overage"}) ||
		got.SchemaCorroboration.WireCapture {
		t.Fatalf("schema corroboration = %#v", got.SchemaCorroboration)
	}
	if got.Privacy.AccountIdentifyingFieldsRetained || got.Privacy.CredentialsRetained || got.Privacy.PromptsOrToolContentRetained ||
		got.Privacy.PathsRetained || !got.Privacy.NonIdentifyingLimitTelemetryKept {
		t.Fatalf("privacy provenance = %#v", got.Privacy)
	}
	for _, forbidden := range []string{"req_", "authorization", "cookie", "/home/", "tool_use", "organization-id"} {
		if bytes.Contains(bytes.ToLower(fixtureRaw), []byte(forbidden)) {
			t.Fatalf("fixture retained forbidden field/value marker %q", forbidden)
		}
	}
	if capabilities.For(domain.HarnessClaudeCode).LimitDetectionSupported {
		t.Fatal("Claude limit detection was promoted by fixture research")
	}
}

func readClaudeRateLimitEventFixture(t *testing.T) []byte {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", "limits", claudeRejectedEventFixture))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	return raw
}
