package project_test

import (
	"bytes"
	"encoding/json"
	"os"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/roles/capabilities"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/project"
)

// shippedRoleMapExample is the canonical pasteable role map documented in
// docs/roles/examples/README.md.
const shippedRoleMapExample = "../../../../docs/roles/examples/role-map.strict.example.json"

// TestSetConfigEnvelope_DocumentedHTTPFormDecodes pins the HTTP envelope that
// docs/roles/examples/README.md tells operators to send. The two supported
// paths nest the role map at different depths — HTTP wraps it in "config"
// (SetConfigInput), while CLI --config-json unmarshals the project config
// directly — and PUT /projects/{id}/config decodes with DisallowUnknownFields,
// so getting the depth wrong is a 400 rather than a silently ignored key.
// Without this test the README's HTTP snippet is an unverified claim.
func TestSetConfigEnvelope_DocumentedHTTPFormDecodes(t *testing.T) {
	roleMap, err := os.ReadFile(shippedRoleMapExample)
	if err != nil {
		t.Fatalf("canonical example unreadable (%s): %v", shippedRoleMapExample, err)
	}

	body := []byte(`{"config":{"roleMap":` + string(roleMap) + `}}`)

	// Mirrors decodeJSONStrict in httpd/controllers/projects.go.
	var in project.SetConfigInput
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&in); err != nil {
		t.Fatalf("documented HTTP envelope fails strict decode: %v", err)
	}

	if in.Config.RoleMap.IsZero() {
		t.Fatal("role map decoded empty: the documented envelope nests it at the wrong depth")
	}
	if _, ok := in.Config.RoleMap.Roles["implementor"]; !ok {
		t.Fatalf("expected implementor role to survive decode, got roles %v", in.Config.RoleMap.Roles)
	}
	if err := in.Config.Validate(); err != nil {
		t.Fatalf("documented envelope fails config validation: %v", err)
	}
	if err := capabilities.ValidateRoleMap(in.Config.RoleMap); err != nil {
		t.Fatalf("documented envelope fails capability validation: %v", err)
	}
}

// TestSetConfigEnvelope_CLIFormIsNotTheHTTPForm guards the distinction the
// README calls out: the CLI-shaped body (role map at the top level, no "config"
// wrapper) must NOT be accepted by the HTTP decoder. If this ever starts
// passing, the two documented forms have converged and the README is stale.
func TestSetConfigEnvelope_CLIFormIsNotTheHTTPForm(t *testing.T) {
	roleMap, err := os.ReadFile(shippedRoleMapExample)
	if err != nil {
		t.Fatalf("canonical example unreadable (%s): %v", shippedRoleMapExample, err)
	}

	body := []byte(`{"roleMap":` + string(roleMap) + `}`)

	var in project.SetConfigInput
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&in); err == nil {
		t.Fatal("CLI-shaped body was accepted by the strict HTTP decoder; " +
			"README documents these as distinct envelopes")
	}
}
