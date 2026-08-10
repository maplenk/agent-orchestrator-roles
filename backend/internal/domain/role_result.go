package domain

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"time"
)

const (
	// RoleResultSchemaVersion is the host-owned worker result envelope version.
	RoleResultSchemaVersion = 1
	// MaxRoleResultSummaryBytes bounds the durable, display-safe result summary.
	MaxRoleResultSummaryBytes = 4096
)

const (
	roleResultOpenTag  = "<ao-role-result>"
	roleResultCloseTag = "</ao-role-result>"
)

// RoleResultState is an agent-reported semantic outcome for one assigned turn.
// It is a claim, not host verification of the work or its evidence.
type RoleResultState string

const (
	// RoleResultCompleted means the assigned role turn reached its reported end.
	RoleResultCompleted RoleResultState = "completed"
	// RoleResultBlocked means the turn needs input or an external dependency.
	RoleResultBlocked RoleResultState = "blocked"
	// RoleResultFailed means execution could not complete the assigned role turn.
	RoleResultFailed RoleResultState = "failed"
)

// RoleResultReport is the provider-neutral payload carried from a final
// assistant response into the generation-fenced lifecycle reducer.
type RoleResultReport struct {
	SchemaVersion int             `json:"schemaVersion"`
	State         RoleResultState `json:"state"`
	Summary       string          `json:"summary"`
}

// SessionRoleResult is the durable last result exposed by session reads.
// GenerationID is internal ownership provenance; Current is the public answer
// to whether this result still belongs to the session's accepted task turn.
type SessionRoleResult struct {
	RoleID       string          `json:"roleId"`
	State        RoleResultState `json:"state"`
	Summary      string          `json:"summary"`
	ReportedAt   time.Time       `json:"reportedAt"`
	Current      bool            `json:"current"`
	GenerationID string          `json:"-"`
}

// NormalizeRoleResultReport validates and sanitizes a report received across
// a host boundary. Unknown versions/states fail closed instead of being guessed.
func NormalizeRoleResultReport(report RoleResultReport) (RoleResultReport, error) {
	report.Summary = strings.TrimSpace(SanitizeControlChars(report.Summary))
	if report.SchemaVersion != RoleResultSchemaVersion {
		return RoleResultReport{}, errors.New("unsupported role result schema version")
	}
	switch report.State {
	case RoleResultCompleted, RoleResultBlocked, RoleResultFailed:
	default:
		return RoleResultReport{}, errors.New("unknown role result state")
	}
	if report.Summary == "" {
		return RoleResultReport{}, errors.New("role result summary is required")
	}
	if len(report.Summary) > MaxRoleResultSummaryBytes {
		return RoleResultReport{}, errors.New("role result summary is too long")
	}
	return report, nil
}

// ParseRoleResultEnvelope extracts one strict trailing result block. Text after
// the closing tag, malformed JSON, extra JSON fields, and embedded/quoted blocks
// are not completion evidence.
func ParseRoleResultEnvelope(text string) (RoleResultReport, bool) {
	trimmed := strings.TrimSpace(text)
	if !strings.HasSuffix(trimmed, roleResultCloseTag) {
		return RoleResultReport{}, false
	}
	start := strings.LastIndex(trimmed, roleResultOpenTag)
	if start < 0 {
		return RoleResultReport{}, false
	}
	bodyStart := start + len(roleResultOpenTag)
	bodyEnd := len(trimmed) - len(roleResultCloseTag)
	if bodyStart >= bodyEnd {
		return RoleResultReport{}, false
	}
	var report RoleResultReport
	dec := json.NewDecoder(bytes.NewBufferString(trimmed[bodyStart:bodyEnd]))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&report); err != nil {
		return RoleResultReport{}, false
	}
	if err := dec.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return RoleResultReport{}, false
	}
	report, err := NormalizeRoleResultReport(report)
	return report, err == nil
}
