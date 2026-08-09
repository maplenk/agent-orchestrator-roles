# Target B vendor-limit fixture research — 2026-08-09

## Scope and result

Implementation commit: `11d12414` (`test: capture vendor limit fixtures`).

This is a capture-and-replay research slice only. It adds adapter-local
testdata and test-only classifiers/normalizers; it adds no production detector,
registry wiring, scheduler, parser, API, or capability change.

The evidence result is deliberately asymmetric:

- **Claude Code:** two real 2.1.224 durable-transcript records prove a
  machine-structured provider refusal: `type=assistant`, the API-error marker,
  HTTP 429, exact `error=rate_limit`, and a provider-shaped request ID. Only
  seven selected fields are committed. Request IDs are replaced with
  deterministic `req_` placeholders; prompts, messages, paths, usernames,
  session/record IDs, branch metadata, and the other transcript fields are not
  copied.
- **Codex:** a real 0.146.0 `account/rateLimits/updated` notification and a
  live 0.147.0 `account/rateLimits/read` response prove machine-structured
  quota-state channels. Both explicitly have `rateLimitReachedType: null` and
  therefore are negative detector evidence, not refusal evidence.

The live Codex 0.147.0 read used an authenticated local app-server initialize
and `account/rateLimits/read`; it did not send a model turn. Claude evidence was
projected read-only from existing durable records; no attempt was made to force
or spend into a limit.

## Why nothing is promoted

Claude's `requestId` identifies one failed API request. The two captured
refusals occurred close together and do not carry a reviewed quota window,
reset cycle, or stable provider occurrence ID. Treating each request as a
`SourceKey` would split retries for one real incident and could incorrectly
reset the per-incident failover bound. The test-only classifier therefore calls
its derived value `RequestCorrelation` and explicitly says it is not a durable
incident key.

Codex exposes the right typed state fields, including a reached discriminator,
but neither real fixture observed a reached/refused value. Generated enum
declarations and a synthetic 100% mutation are not live evidence.

The current neutral `limits.Event` also has no typed field for a stable vendor
occurrence key/window; its `Detail` field is deliberately a non-branching leaf.
That production contract gap must be designed only after qualifying evidence
shows the needed stable fields. It is not worked around by parsing prose or
closing over fixture-specific state.

Accordingly:

- `limit_detection_supported=false` remains true for Claude, Codex, and every
  other production harness;
- the production detector registry remains empty;
- no detector is implemented or composed;
- any future implementation and capability promotion remain separate commits,
  reviews, and live acceptance.

## Load-bearing tests

Claude coverage requires the complete structured refusal predicate and rejects
mutations to record type, API-error marker, HTTP status, error category, request
ID prefix/characters, empty IDs, and oversized IDs. Replaying one projected
record preserves its request correlation. Fixture SHA-256, CLI version,
timestamp, exact deterministic placeholder, selected/dropped fields, source
transcript hash, corroboration counts/versions/predicate, positive-observation
flag, negative-promotion flag, and missing-stable-occurrence blocker are all
asserted.

Codex coverage replays the exact captured notification `params` bytes through
the production quota normalizer. It allowlists every reviewed top-level and
nested key, requires integer numeric representations, distinguishes explicit
`null` from absent and `false`, checks all per-limit snapshots and reset-credit
shape, binds both fixture byte hashes, and pins exact method/version/direction/
capture metadata. Adding identity/credential fields or changing a negative
reached discriminator fails the shape tests.

Independent review initially found that Codex's typed projection conflated a
missing reached field with explicit null and did not bind provenance to bytes;
both were corrected. A second review found that Claude incorrectly named a
per-request hash `SourceKey`; it was changed to non-incident request
correlation, and provenance now records the stable-occurrence blocker. Final
independent safety and privacy/provenance reviews approved both vendor portions
with no P1/P2/P3 findings.

## Gates

From an isolated worktree based on the accepted test-hygiene head:

- fixture-focused normal: 302/302 passed across Claude, Codex app-server,
  `internal/limits`, and role capabilities;
- fixture-focused race: 302/302 passed across the same four packages;
- `go build ./...`: passed;
- `go vet ./...`: passed;
- gofmt and staged `git diff --check`: clean;
- pinned golangci-lint v2.12.2: zero issues;
- ordinary full backend, first final-diff run: 4,737 passed across 132 packages.

The first multi-package focused attempt had one compile failure because the
test imported the role-capabilities package with a name already declared in the
Codex app-server package. The import was aliased, after which the focused gates
passed. This is retained as historical evidence rather than omitted or
reclassified as a test failure.

Frontend typecheck/Vitest, API regeneration, destructive `AO_DATA_DIR`
dogfood, and installed-Electron validation are not applicable: the slice adds
only backend `_test.go` files and testdata, and changes no product behavior,
storage, API, frontend, or packaged application surface.
