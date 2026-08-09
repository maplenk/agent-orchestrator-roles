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

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/roles/capabilities"
)

const (
	maxClaudeLimitProjectionBytes = 4 << 10
	maxClaudeRequestIDBytes       = 128
	claudeTranscriptSHA256        = "e392176b2798edd4f255238a2b46a4a87050a08a05fdb7c0bb003255d1d94894"
)

type claudeLimitProjection struct {
	Type              string `json:"type"`
	IsAPIErrorMessage bool   `json:"isApiErrorMessage"`
	APIErrorStatus    int    `json:"apiErrorStatus"`
	Error             string `json:"error"`
	RequestID         string `json:"requestId"`
	Version           string `json:"version"`
	Timestamp         string `json:"timestamp"`
}

type claudeLimitEvidence struct {
	// RequestCorrelation identifies the captured API request only. It is not a
	// durable limit SourceKey: the fixtures carry no quota window/reset identity
	// that could make retries for one incident converge.
	RequestCorrelation string
}

// classifyClaudeLimitProjection is deliberately test-only research code. It
// proves the captured vendor discriminator is exact without wiring a detector,
// registering a harness, or teaching production code to scrape transcript
// prose.
func classifyClaudeLimitProjection(raw []byte) (claudeLimitEvidence, bool, error) {
	if len(raw) == 0 || len(raw) > maxClaudeLimitProjectionBytes {
		return claudeLimitEvidence{}, false, nil
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	var record claudeLimitProjection
	if err := dec.Decode(&record); err != nil {
		return claudeLimitEvidence{}, false, fmt.Errorf("decode Claude limit projection: %w", err)
	}
	var trailing json.RawMessage
	if err := dec.Decode(&trailing); err == nil {
		return claudeLimitEvidence{}, false, fmt.Errorf("decode Claude limit projection: trailing JSON value")
	} else if !errors.Is(err, io.EOF) {
		return claudeLimitEvidence{}, false, fmt.Errorf("decode Claude limit projection trailing bytes: %w", err)
	}
	if record.Type != "assistant" || !record.IsAPIErrorMessage || record.APIErrorStatus != 429 || record.Error != "rate_limit" {
		return claudeLimitEvidence{}, false, nil
	}
	if !validClaudeRequestID(record.RequestID) {
		return claudeLimitEvidence{}, false, nil
	}
	sum := sha256.Sum256([]byte("claude-code|" + record.RequestID))
	return claudeLimitEvidence{RequestCorrelation: "claude-request-" + hex.EncodeToString(sum[:16])}, true, nil
}

func validClaudeRequestID(id string) bool {
	if len(id) <= len("req_") || len(id) > maxClaudeRequestIDBytes || !strings.HasPrefix(id, "req_") {
		return false
	}
	for _, r := range id[len("req_"):] {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_' || r == '-' {
			continue
		}
		return false
	}
	return true
}

func TestClaudeProjectedRealLimitRefusalsClassifyExactly(t *testing.T) {
	paths, err := filepath.Glob(filepath.Join("testdata", "limits", "claude-code-*-rate-limit-refusal-*.json"))
	if err != nil {
		t.Fatalf("glob projected fixtures: %v", err)
	}
	if len(paths) != 2 {
		t.Fatalf("projected fixture count = %d, want 2", len(paths))
	}

	for _, path := range paths {
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		first, ok, err := classifyClaudeLimitProjection(raw)
		if err != nil || !ok {
			t.Fatalf("classify %s: ok=%v err=%v", path, ok, err)
		}
		second, ok, err := classifyClaudeLimitProjection(raw)
		if err != nil || !ok {
			t.Fatalf("replay %s: ok=%v err=%v", path, ok, err)
		}
		if first.RequestCorrelation != second.RequestCorrelation {
			t.Fatalf("replay request correlation changed for %s: %q != %q",
				path, first.RequestCorrelation, second.RequestCorrelation)
		}
	}
}

func TestClaudeProjectedLimitRefusalMutationsDoNotClassify(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("testdata", "limits", "claude-code-2.1.224-rate-limit-refusal-01.json"))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	var base claudeLimitProjection
	if err := json.Unmarshal(raw, &base); err != nil {
		t.Fatalf("decode fixture: %v", err)
	}

	tooLongRequestID := "req_" + strings.Repeat("a", maxClaudeRequestIDBytes)
	tests := []struct {
		name string
		mut  func(*claudeLimitProjection)
	}{
		{"record type", func(r *claudeLimitProjection) { r.Type = "user" }},
		{"API error marker", func(r *claudeLimitProjection) { r.IsAPIErrorMessage = false }},
		{"status", func(r *claudeLimitProjection) { r.APIErrorStatus = 500 }},
		{"category", func(r *claudeLimitProjection) { r.Error = "overloaded" }},
		{"empty request ID", func(r *claudeLimitProjection) { r.RequestID = "" }},
		{"wrong request ID prefix", func(r *claudeLimitProjection) { r.RequestID = "request_0001" }},
		{"invalid request ID character", func(r *claudeLimitProjection) { r.RequestID = "req_bad/value" }},
		{"oversized request ID", func(r *claudeLimitProjection) { r.RequestID = tooLongRequestID }},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			mutated := base
			tc.mut(&mutated)
			encoded, err := json.Marshal(mutated)
			if err != nil {
				t.Fatalf("encode mutation: %v", err)
			}
			if evidence, ok, err := classifyClaudeLimitProjection(encoded); err != nil || ok {
				t.Fatalf("mutation classified: evidence=%+v ok=%v err=%v", evidence, ok, err)
			}
		})
	}
}

func TestClaudeLimitFixtureProvenanceAndNonPromotion(t *testing.T) {
	type provenance struct {
		SchemaVersion int `json:"schemaVersion"`
		Source        struct {
			Product          string   `json:"product"`
			Channel          string   `json:"channel"`
			TranscriptSHA256 string   `json:"transcriptSha256"`
			RecordCLIVersion string   `json:"recordCliVersion"`
			RecordTimestamps []string `json:"recordTimestamps"`
			AbsolutePathKept bool     `json:"absolutePathRetained"`
		} `json:"source"`
		Projection struct {
			SelectedFields []string          `json:"selectedFields"`
			DroppedFields  []string          `json:"droppedFields"`
			RedactedFields map[string]string `json:"redactedFields"`
		} `json:"projection"`
		Fixtures []struct {
			File                 string `json:"file"`
			SHA256               string `json:"sha256"`
			Version              string `json:"version"`
			Timestamp            string `json:"timestamp"`
			PlaceholderRequestID string `json:"placeholderRequestId"`
			ObservedLimit        bool   `json:"observedLimit"`
			PromotionEligible    bool   `json:"promotionEligible"`
		} `json:"fixtures"`
		StableOccurrenceKeyObserved bool   `json:"stableOccurrenceKeyObserved"`
		PromotionBlocker            string `json:"promotionBlocker"`
		Corroboration               struct {
			MatchingLocalRecords      int      `json:"matchingLocalRecords"`
			MatchingLocalFiles        int      `json:"matchingLocalFiles"`
			ObservedRecordCLIVersions []string `json:"observedRecordCliVersions"`
			Predicate                 string   `json:"predicate"`
		} `json:"corroboration"`
	}
	raw, err := os.ReadFile(filepath.Join("testdata", "limits", "provenance.json"))
	if err != nil {
		t.Fatalf("read provenance: %v", err)
	}
	var got provenance
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&got); err != nil {
		t.Fatalf("decode provenance: %v", err)
	}
	var rawObject map[string]json.RawMessage
	if err := json.Unmarshal(raw, &rawObject); err != nil {
		t.Fatalf("decode provenance object: %v", err)
	}
	assertClaudeJSONKeys(t, rawObject, "schemaVersion", "source", "projection", "fixtures",
		"stableOccurrenceKeyObserved", "promotionBlocker", "corroboration")
	if strings.TrimSpace(string(rawObject["stableOccurrenceKeyObserved"])) != "false" {
		t.Fatal("stableOccurrenceKeyObserved must be present and false")
	}
	if got.SchemaVersion != 1 || got.Source.Product != "Claude Code" || got.Source.Channel != "durable project transcript JSONL" ||
		got.Source.TranscriptSHA256 != claudeTranscriptSHA256 || got.Source.RecordCLIVersion != "2.1.224" || got.Source.AbsolutePathKept {
		t.Fatalf("source provenance = %+v", got.Source)
	}
	if bytes.Contains(raw, []byte("/Users/")) || bytes.Contains(raw, []byte("tagtaste")) {
		t.Fatal("provenance retained an absolute source path or local username")
	}
	wantTimestamps := []string{"2026-08-07T09:48:12.097Z", "2026-08-07T09:49:07.952Z"}
	if !reflect.DeepEqual(got.Source.RecordTimestamps, wantTimestamps) {
		t.Fatalf("record timestamps = %#v, want %#v", got.Source.RecordTimestamps, wantTimestamps)
	}
	wantSelected := []string{"type", "isApiErrorMessage", "apiErrorStatus", "error", "requestId", "version", "timestamp"}
	if !reflect.DeepEqual(got.Projection.SelectedFields, wantSelected) {
		t.Fatalf("selected fields = %#v, want %#v", got.Projection.SelectedFields, wantSelected)
	}
	wantDropped := []string{"cwd", "entrypoint", "gitBranch", "isSidechain", "message", "parentUuid", "sessionId", "slug", "userType", "uuid"}
	if !reflect.DeepEqual(got.Projection.DroppedFields, wantDropped) || got.Projection.RedactedFields["requestId"] == "" {
		t.Fatalf("projection provenance is incomplete: %+v", got.Projection)
	}
	if len(got.Fixtures) != 2 {
		t.Fatalf("projection fixture count = %d, want 2", len(got.Fixtures))
	}
	expectedFixtures := map[string]struct {
		sha256    string
		version   string
		timestamp string
		requestID string
	}{
		"claude-code-2.1.224-rate-limit-refusal-01.json": {
			sha256:  "c477060f9b7b10982c6e0143671d5c4c637dce30a28ef1a32280c1cb27de9d37",
			version: "2.1.224", timestamp: "2026-08-07T09:48:12.097Z",
			requestID: "req_000000000000000000000001",
		},
		"claude-code-2.1.224-rate-limit-refusal-02.json": {
			sha256:  "44c5bac156cc1a3e372a188478687aa3f2a16534d653d0b82386b0ac92f9c037",
			version: "2.1.224", timestamp: "2026-08-07T09:49:07.952Z",
			requestID: "req_000000000000000000000002",
		},
	}
	for _, fixture := range got.Fixtures {
		expected, ok := expectedFixtures[fixture.File]
		if !ok {
			t.Fatalf("unexpected projection fixture %q", fixture.File)
		}
		delete(expectedFixtures, fixture.File)
		if fixture.SHA256 != expected.sha256 || fixture.Version != expected.version ||
			fixture.Timestamp != expected.timestamp || fixture.PlaceholderRequestID != expected.requestID ||
			!fixture.ObservedLimit || fixture.PromotionEligible {
			t.Fatalf("projection fixture provenance = %#v, expected = %#v", fixture, expected)
		}
		fixtureRaw, err := os.ReadFile(filepath.Join("testdata", "limits", fixture.File))
		if err != nil {
			t.Fatalf("read projection fixture %s: %v", fixture.File, err)
		}
		sum := sha256.Sum256(fixtureRaw)
		if hex.EncodeToString(sum[:]) != fixture.SHA256 {
			t.Fatalf("projection fixture %s digest does not match provenance", fixture.File)
		}
		var projection claudeLimitProjection
		if err := json.Unmarshal(fixtureRaw, &projection); err != nil {
			t.Fatalf("decode projection fixture %s: %v", fixture.File, err)
		}
		if projection.Version != fixture.Version || projection.Timestamp != fixture.Timestamp ||
			projection.RequestID != fixture.PlaceholderRequestID {
			t.Fatalf("projection fixture %s does not match provenance: %#v", fixture.File, projection)
		}
	}
	if len(expectedFixtures) != 0 {
		t.Fatalf("missing projected fixtures: %v", expectedFixtures)
	}
	if got.StableOccurrenceKeyObserved || !strings.Contains(got.PromotionBlocker, "requestId identifies an API request") ||
		!strings.Contains(got.PromotionBlocker, "No reviewed limit window") {
		t.Fatalf("promotion blocker is incomplete: stable=%v blocker=%q",
			got.StableOccurrenceKeyObserved, got.PromotionBlocker)
	}
	if got.Corroboration.MatchingLocalRecords != 16 || got.Corroboration.MatchingLocalFiles != 10 ||
		!reflect.DeepEqual(got.Corroboration.ObservedRecordCLIVersions, []string{"2.1.212", "2.1.221", "2.1.224"}) ||
		got.Corroboration.Predicate != "type=assistant, isApiErrorMessage=true, apiErrorStatus=429, error=rate_limit, requestId=req_..." {
		t.Fatalf("corroboration provenance = %#v", got.Corroboration)
	}

	if capabilities.For(domain.HarnessClaudeCode).LimitDetectionSupported {
		t.Fatal("Claude limit detection was promoted by a fixture-only research slice")
	}
}

func assertClaudeJSONKeys(t *testing.T, object map[string]json.RawMessage, want ...string) {
	t.Helper()
	if len(object) != len(want) {
		t.Fatalf("JSON keys = %v, want exactly %v", reflect.ValueOf(object).MapKeys(), want)
	}
	for _, key := range want {
		if _, ok := object[key]; !ok {
			t.Fatalf("JSON object missing key %q", key)
		}
	}
}
