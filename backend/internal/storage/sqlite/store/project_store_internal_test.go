package store

import (
	"database/sql"
	"reflect"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

func TestUnmarshalProjectConfigFailsClosed(t *testing.T) {
	// SQL NULL / empty → zero config.
	if got, err := unmarshalProjectConfig(sql.NullString{}); err != nil || !got.IsZero() {
		t.Fatalf("NULL config = %#v, err=%v; want zero", got, err)
	}

	// Valid JSON decodes.
	if got, err := unmarshalProjectConfig(sql.NullString{String: `{"defaultBranch":"develop"}`, Valid: true}); err != nil || got.DefaultBranch != "develop" {
		t.Fatalf("valid config = %#v, err=%v; want develop", got, err)
	}

	// A binding that is malformed under the authoring contract cannot be
	// sanitized at persistence read time.
	malformed := sql.NullString{String: `{
		"defaultBranch":"develop",
		"env":{"KEEP":"yes"},
		"roleMap":{
			"role_map_schema_version":1,
			"roles":{"orchestrator":{
				"template":"orchestrator",
				"harness":"codex",
				"permissions":{"canSpawn":true},
				"legacyField":"ignored at the persistence boundary"
			}}
		}
	}`, Valid: true}
	if got, err := unmarshalProjectConfig(malformed); err == nil || !got.IsZero() {
		t.Fatalf("malformed config = %#v, err=%v; want explicit error", got, err)
	}

	if got, err := unmarshalProjectConfig(sql.NullString{String: `{"defaultBranch":"release","roleMap":"damaged"}`, Valid: true}); err == nil || !got.IsZero() {
		t.Fatalf("damaged role map = %#v, err=%v; want explicit error", got, err)
	}

	if got, err := unmarshalProjectConfig(sql.NullString{String: `{not json`, Valid: true}); err == nil || !got.IsZero() {
		t.Fatalf("corrupt config = %#v, err=%v; want explicit error", got, err)
	}
}

func TestProjectConfigValidRoleMapDecodePreservesBytesSemanticsAndHash(t *testing.T) {
	cfg := domain.ProjectConfig{
		DefaultBranch: "develop",
		Env:           map[string]string{"KEEP": "yes"},
		RoleMap: domain.RoleMap{
			SchemaVersion:    domain.RoleMapSchemaVersion,
			StrictDelegation: true,
			OrchestratorRole: "orchestrator",
			Roles: map[string]domain.RoleBinding{
				"orchestrator": {
					Template: "orchestrator",
					Harness:  domain.HarnessCodex,
					Permissions: domain.RoleExecutionPolicy{
						WorkspaceWrites: true,
						CanSpawn:        true,
					},
				},
				"reviewer": {
					Template: "reviewer",
					Harness:  domain.HarnessClaudeCode,
					Permissions: domain.RoleExecutionPolicy{
						WorkspaceWrites: true,
						CanSpawn:        false,
					},
				},
			},
		},
	}
	wantHash, err := cfg.RoleMap.SHA256()
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := marshalProjectConfig(cfg)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := unmarshalProjectConfig(encoded)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(decoded, cfg) {
		t.Fatalf("decoded config = %#v, want %#v", decoded, cfg)
	}
	gotHash, err := decoded.RoleMap.SHA256()
	if err != nil {
		t.Fatal(err)
	}
	if gotHash != wantHash {
		t.Fatalf("role map hash = %s, want %s", gotHash, wantHash)
	}
	reencoded, err := marshalProjectConfig(decoded)
	if err != nil {
		t.Fatal(err)
	}
	if reencoded != encoded {
		t.Fatalf("valid persisted bytes changed:\n got: %s\nwant: %s", reencoded.String, encoded.String)
	}
}
