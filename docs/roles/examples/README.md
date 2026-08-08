# Role map examples

`role-map.strict.example.json` is a **literally pasteable** strict role map, kept
valid against the runtime capability registry by
`TestValidateRoleMap_ShippedStrictExampleValidates`
(`backend/internal/roles/capabilities/capabilities_test.go`).

## Why there are no comments in the JSON

`PUT /api/v1/projects/{id}/config` decodes with `DisallowUnknownFields`
(`decodeJSONStrict`, `backend/internal/httpd/controllers/projects.go`). Any key
outside the schema — including a `_comment` — makes the whole request fail with
`400 INVALID_JSON`, **before** capability validation ever runs. Annotations
therefore live in this file, not in the JSON.

## How to send it — the two paths take different envelopes

Sending the CLI form to the HTTP endpoint returns `400 INVALID_JSON`.

**HTTP.** `PUT /api/v1/projects/{id}/config` decodes `SetConfigInput`, whose only
top-level field is `config` (`backend/internal/service/project/dto.go:40-42`), so the
role map nests **two** levels deep:

```json
{ "config": { "roleMap": { "role_map_schema_version": 1, "…": "…" } } }
```

**CLI.** `--config-json` unmarshals straight into the project-config mirror
(`backend/internal/cli/project.go`), so the role map nests **one** level:

```bash
ao project set-config --project <id> --config-json '{"roleMap": { … }}'
```

The two also differ in strictness: the HTTP path uses `DisallowUnknownFields` and
rejects unknown keys outright, while `--config-json` drops them silently. A file that
"works" via the CLI can still 400 over HTTP — which is why this example carries no
annotation keys.

## Why each binding looks the way it does

Capability cells today (`backend/internal/roles/capabilities/capabilities.go` is
the source of truth; `../CAPABILITY_MATRIX.md` mirrors it):

| Harness | spawn | switch | read-only enforced |
|---------|-------|--------|--------------------|
| codex | yes | yes | **yes** |
| claude-code | yes | yes | no |
| pi | yes | no | no |
| everything else | yes | no | no |

- **`orchestrator` is Claude Code with `workspaceWrites: true`.** Strict mode
  still pins its role, requires `canSpawn`, rejects caller routing overrides,
  and injects the coordination-only delegation contract. The instruction not
  to implement is model-enforced, not a filesystem sandbox claim. Its Codex
  rung enables the in-place strict orchestrator switch in either direction.
- **`reviewer` and `verifier` remain Codex read-only roles.** Their explicit
  `workspaceWrites: false` requires `read_only_enforced`; binding Claude or Pi
  to either is rejected at config-save.
- **`ui` is Pi with no failover ladder.** Pi is spawn-capable but not
  switch-capable, so it is fine as a primary — but the primary of a role with a
  ladder is the switch *source*, so giving `ui` a ladder would be rejected. Add
  one only after Pi is switch-promoted.
- **`orchestrator` and `implementor` have Codex rungs.** Every rung *and*
  the owning primary must advertise `switch_supported`; today that is
  `claude-code` and `codex` only. `pi` and `grok` rungs are rejected at
  config-save until those cells are promoted.

## Not yet supported

`MASTER_PLAN.md` §4.5 shows a `limits` block (`onUsageLimit`,
`maxFailoversPerIncident`). That is **Phase 3A design intent and is not on
`domain.RoleMap` yet** — sending it today fails strict decode. It is deliberately
absent from this example.
