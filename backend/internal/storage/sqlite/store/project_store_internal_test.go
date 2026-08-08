package store

import (
	"database/sql"
	"testing"
)

func TestUnmarshalProjectConfigDegradesGracefully(t *testing.T) {
	// SQL NULL / empty → zero config.
	if got := unmarshalProjectConfig(sql.NullString{}); !got.IsZero() {
		t.Fatalf("NULL config = %#v, want zero", got)
	}

	// Valid JSON decodes.
	if got := unmarshalProjectConfig(sql.NullString{String: `{"defaultBranch":"develop"}`, Valid: true}); got.DefaultBranch != "develop" {
		t.Fatalf("valid config DefaultBranch = %q, want develop", got.DefaultBranch)
	}

	// Persistence predates RoleBinding's strict authoring decoder. A legacy
	// binding with an omitted permission and an old unknown field must not make
	// the valid unrelated config disappear.
	legacy := sql.NullString{String: `{
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
	got := unmarshalProjectConfig(legacy)
	if got.IsZero() || got.DefaultBranch != "develop" || got.Env["KEEP"] != "yes" {
		t.Fatalf("legacy config = %#v, want unrelated fields preserved", got)
	}
	binding, ok := got.RoleMap.Roles["orchestrator"]
	if !ok || binding.Permissions.WorkspaceWrites || !binding.Permissions.CanSpawn {
		t.Fatalf("legacy binding = %#v", binding)
	}

	// Even a roleMap value that cannot be decoded must degrade only that nested
	// field, not the rest of ProjectConfig.
	got = unmarshalProjectConfig(sql.NullString{String: `{"defaultBranch":"release","roleMap":"damaged"}`, Valid: true})
	if got.IsZero() || got.DefaultBranch != "release" || !got.RoleMap.IsZero() {
		t.Fatalf("partially damaged config = %#v, want unrelated fields only", got)
	}

	// Corrupt JSON must NOT error — it degrades to a zero config so the project
	// row (and ListProjects) stay accessible.
	if got := unmarshalProjectConfig(sql.NullString{String: `{not json`, Valid: true}); !got.IsZero() {
		t.Fatalf("corrupt config = %#v, want zero (degraded)", got)
	}
}
