# Target B adapter test hygiene — 2026-08-09

## Scope

This slice removes only the three known scheduler-dependent tests that remained
after the accepted MVP and the two durability hardening slices. It does not
change a production timeout, add a retry, serialize the repository test runner,
or promote a capability.

Implementation commit: `66d65e4e` (`test: remove adapter wall-clock dependencies`).

## Load-bearing regressions

- Fake lifecycle coverage no longer measures aggregate wall time. It asserts
  that the generated script contains all six exact accelerated sleeps, while
  the existing launch-command tests continue to pin the speedup calculation,
  default cadence, and invalid-speedup behavior.
- Kilocode tests the local credential sources and interactive-shell exclusion
  directly, with a marker that fails if an interactive shell is invoked. The
  unavailable and zero-credential CLI outputs remain separate classifier tests.
- OpenCode's existing auth-list classifier was extracted unchanged into a pure
  helper. Its command-error case deliberately supplies credential-like output,
  so deleting the `commandErr == nil` authorization guard makes the test fail.

The Kilocode and OpenCode `AuthStatus` command deadlines remain three seconds.
No production response classification changed.

## Review

An independent no-edit review found one P2 test-strength gap: OpenCode's first
command-error row had no credential marker and therefore did not pin the error
guard. The row was changed to `1 credential` plus a command error, then both
focused suites were rerun. The reviewer approved the amended diff with no
remaining findings.

## Gates

From an isolated worktree based on the accepted durability-hardening head:

- focused normal: 105/105 passed across fake, Kilocode, and OpenCode;
- focused race: 105/105 passed across the same packages;
- `go build ./...`: passed;
- `go vet ./...`: passed;
- gofmt and `git diff --check`: clean;
- pinned golangci-lint v2.12.2: zero issues;
- ordinary full backend, first run: 4,724 passed across 132 packages.

The full backend run preceded only the review-requested strengthening of one
pure table-test input; the final focused normal and race reruns each passed
105/105. It was not rerun to manufacture a different repository-wide record.
Frontend typecheck/Vitest, API regeneration, destructive `AO_DATA_DIR`
dogfood, and installed-Electron validation are not applicable to this
backend-only test-hygiene slice: it changes no API, frontend, storage, daemon,
or application behavior.

The first attempted linter command expected a global `golangci-lint` binary and
failed because none was installed. The repository-pinned command
`go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.12.2 run
--path-mode=abs` then completed with zero issues. No test failure was retried.

## Capability state

`limit_detection_supported` remains false for every production harness.
Claude `read_only_enforced` remains false. There is no capability-promotion
commit in this slice.
