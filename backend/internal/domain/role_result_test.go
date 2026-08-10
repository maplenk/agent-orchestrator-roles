package domain

import (
	"strings"
	"testing"
)

func TestParseRoleResultEnvelope(t *testing.T) {
	valid := `Work finished.

<ao-role-result>{"schemaVersion":1,"state":"completed","summary":"All focused tests passed."}</ao-role-result>`
	got, ok := ParseRoleResultEnvelope(valid)
	if !ok || got.State != RoleResultCompleted || got.Summary != "All focused tests passed." {
		t.Fatalf("ParseRoleResultEnvelope() = %#v, %v", got, ok)
	}

	tests := []string{
		`<ao-role-result>{"schemaVersion":2,"state":"completed","summary":"done"}</ao-role-result>`,
		`<ao-role-result>{"schemaVersion":1,"state":"unknown","summary":"done"}</ao-role-result>`,
		`<ao-role-result>{"schemaVersion":1,"state":"completed","summary":""}</ao-role-result>`,
		`<ao-role-result>{"schemaVersion":1,"state":"completed","summary":"done","extra":true}</ao-role-result>`,
		`<ao-role-result>{bad json}</ao-role-result>`,
		`<ao-role-result>{"schemaVersion":1,"state":"completed","summary":"done"}</ao-role-result> trailing`,
		`ordinary response`,
	}
	for _, input := range tests {
		if report, ok := ParseRoleResultEnvelope(input); ok {
			t.Errorf("ParseRoleResultEnvelope(%q) unexpectedly accepted %#v", input, report)
		}
	}
}

func TestNormalizeRoleResultReportSanitizesAndBoundsSummary(t *testing.T) {
	report, err := NormalizeRoleResultReport(RoleResultReport{
		SchemaVersion: 1,
		State:         RoleResultBlocked,
		Summary:       "  waiting\x00 for approval  ",
	})
	if err != nil {
		t.Fatal(err)
	}
	if report.Summary != "waiting for approval" {
		t.Fatalf("summary = %q", report.Summary)
	}
	_, err = NormalizeRoleResultReport(RoleResultReport{
		SchemaVersion: 1,
		State:         RoleResultFailed,
		Summary:       strings.Repeat("x", MaxRoleResultSummaryBytes+1),
	})
	if err == nil {
		t.Fatal("oversized summary accepted")
	}
}
