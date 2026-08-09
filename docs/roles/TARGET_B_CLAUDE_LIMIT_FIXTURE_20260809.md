# Target B Claude positive limit-fixture research — 2026-08-09

## Scope and current result

This is a fixture-only research slice after the accepted roles MVP. It records
one real, positive, machine-structured Claude Code limit event and exercises it
through an adapter-local, test-only classifier. It does **not** add a production
detector, register a detector, connect an event stream to the Router, or promote
any capability.

**Fixture/test commit:** source `899c7bc`; roles-trunk integration `14ce328d`

The captured event proves that Claude Code 2.1.159 emitted a structured
`rate_limit_event` with `status=rejected`, `rateLimitType=seven_day`, and
`resetsAt=1780398000`. It was observed on the print/Agent SDK stream, not on
AO's accepted interactive Claude TUI surface.

## Immutable public source and sanitized fixture

The source is a byte-complete public third-party execution log:

- immutable raw URL:
  `https://gist.githubusercontent.com/konard/218091cfcd951e88bb6f1b91a001e26c/raw/694141dfd582be191bcd1d5d391b6eb3ca5de013/solution-draft-log-pr-1780356981419.txt`;
- complete-source SHA-256:
  `c2014b6fc8be662bb5e852d04d29bfeb9936e493f821ec50caab08345ddbfffb`;
- complete-source size: 178,074 bytes and 915 lines;
- selected event lines: 692-704;
- observed timestamp: `2026-06-01T15:09:21.346Z`; and
- retrieval timestamp recorded by the provenance:
  `2026-08-09T17:20:53Z`.

The committed projection is
`claude-code-2.1.159-rate-limit-event-rejected.json`, with SHA-256
`1663ef80778318ffe6a4eea8f6fb5531cec205a8e557f8a50ed625a959877d0b`.
It preserves only the event type, the six `rate_limit_info` fields, `uuid`, and
`session_id`. The two real identifiers are replaced with distinct,
deterministic UUID placeholders. No account-identifying field, credential,
prompt or tool content, or filesystem path is retained. The retained
`org_level_disabled` value is non-identifying limit telemetry.

The provenance file binds the exact immutable URL, full-source hash, event line
range, CLI version, surface, direction, timestamps, selected fields, redaction
descriptions, fixture hash, classifier inputs, schema corroboration, privacy
claims, and the no-promotion blocker. The schema corroboration from a locally
installed Claude Code 2.1.226 binary is explicitly marked as a type-schema
inspection, not a wire capture.

## Test-only classifier contract

The classifier exists only in `claudecode_test`; no production package calls
or exports it. Its reviewed contract is deliberately closed:

- payloads are bounded to 4 KiB, decoded with unknown fields disallowed, and
  reject trailing JSON values;
- only `type=rate_limit_event` and `status=rejected` produce an envelope;
  `allowed` and `allowed_warning` are negative results, while an unknown status
  fails loudly;
- `rateLimitType` must be one of the six corroborated vendor enum values;
- `resetsAt` must be positive and no later than Unix `253402300799`, the final
  UTC second whose Go `time.Time` is JSON-representable;
- the candidate envelope is `usage_limit` for `claude-code`, with the vendor
  rate-limit type as its advisory scope and the vendor reset instant as
  `ResetsAt`; and
- its test-only `SourceKey` is derived from
  `sha256("claude-code|" + rateLimitType + "|" + decimalResetsAt)`, truncated
  to 16 bytes and hex encoded with the `claude-limit-` prefix.

`rateLimitType + resetsAt` is the observed quota-window identity available in
the event. Synthetic mutation of the delivery `uuid` and `session_id` proves
those delivery fields do not change the candidate incident identity. The
evidence does not claim that duplicate delivery was empirically observed.

The positive replay also JSON-encodes and reparses through the production
domain envelope parser and preserves both `SourceKey` and `IncidentID`.
Mutation coverage fails closed for unknown status, absent or future limit type,
absent reset, a reset beyond the JSON-representable boundary, trailing JSON,
and oversized input. Exact raw object keys, every captured telemetry value,
the two distinct UUID placeholders, source/fixture hashes, provenance fields,
privacy claims, and the unchanged Claude capability flag are load-bearing.

## Honest fixed failures and review corrections

The first focused test run exposed a compile failure in the draft research
test: it used the wrong production-domain constant names and did not match the
string-accepting `ParseLimitEnvelope` signature. The test was corrected to use
the current domain constants and parser contract.

The next focused run exposed a provenance filename construction error: the
test looked for the fixture name with an extra `.json` before
`.provenance.json`. Filename derivation was corrected to select the committed
`claude-code-2.1.159-rate-limit-event-rejected.provenance.json` file.

Independent review then found two evidence-hardening gaps. The provenance test
matched only a fragment of the source URL, and the candidate classifier
accepted reset epochs beyond Go's JSON time range. Both were fixed: the test
now asserts the exact immutable URL, and the classifier has the exact maximum
reset bound plus a load-bearing boundary mutation. The final focused re-review
reported no remaining P1, P2, or P3 finding.

These failures and corrections are retained here; they are not erased by the
later passing focused runs.

## Verification

From the isolated limit-detector worktree:

| Gate | Exact result |
|------|---------------------|
| Claude adapter focused normal | **21/21 passed** |
| Claude adapter focused race | **21/21 passed** |
| Claude adapter + limits + capabilities normal | **151/151 passed** |
| Claude adapter + limits + capabilities race | **151/151 passed** |
| Full backend normal (`go test ./...`, from the root lint script) | **Passed across all packages** |
| Full backend race | **Not run for this testdata-only slice; focused race is recorded above** |
| Backend build | **Passed: `go build ./...`** |
| Backend vet | **Passed: `go vet ./...`** |
| Formatting/diff checks | **Passed** |
| Pinned golangci-lint v2.12.2 | **0 issues with an isolated cache** |
| Frontend typecheck | **Passed** |
| Full frontend Vitest | **153/153 files; 2,060/2,060 tests passed** |

The first root lint invocation completed the full backend normal tests, then
failed during golangci-lint because its shared cache contained absolute paths
from the already-deleted `/tmp/ao-targetb-pause-generation-binding` worktree.
The pinned linter was rerun once with a new isolated cache and reported zero
issues. The stale-cache failure is retained here rather than overwritten.

The first frontend typecheck attempt failed before compilation because the
isolated worktree had no installed `tsc`. After exact `npm ci --ignore-scripts`
installation from the frontend lockfile, typecheck passed. The first full
Vitest run then passed 2,055/2,060 and failed only the five landing Markdown
tests because the nested `frontend/src/landing` dependency tree was not yet
installed and `cheerio` could not be resolved. Installing that nested lockfile
once made the authoritative full run pass 2,060/2,060. These setup failures and
their exact corrections are part of this record.

API and sqlc regeneration, destructive isolated-data dogfood, and installed
native Electron validation are not applicable: the implementation commit
changes only backend `_test.go` research code and sanitized testdata. There is
no migration, API, frontend, packaged-app, prompt, or host-instruction change.

## Promotion blocker and boundary

This positive event is not production ingress. It comes from Claude Code's
print/Agent SDK stream, while AO runs the accepted interactive Claude TUI and
has no reviewed supported channel that delivers this event together with the
observation-time AO runtime generation. Existing `StopFailure` hooks do not
carry `rateLimitType` or `resetsAt`, so they cannot safely reconstruct this
identity.

Accordingly, the production detector registry remains empty,
`limit_detection_supported` remains `false` for Claude and every production
harness, and Claude `read_only_enforced` remains `false`. No automatic-failover
product trigger is made reachable. Any production ingress, detector
implementation, capability promotion, review, and live acceptance remain
separate future work.
