# Implemented-Task Test Review — 2026-10-16

Scope: T01, T02, T06, T07 and T08. This review adds documentation only;
additional reproduction tests ran in a disposable source copy, not the repository.

## Validation Results

- `scripts/test-postgres.sh`: passed formatting, vet, normal tests, all five builds,
  race-enabled migration/user integration tests and fixture database cleanup.
- Existing OpenAPI validator: passed structure, 22 examples, nine negative payloads
  and Unicode password boundaries.
- Extra HTTP review probe: failed as expected, reproducing the panic bug below.
- Extra contract probe: a registration email containing `é` passes schema
  validation despite the documented ASCII-only input policy.

These results distinguish passing current tests from sufficient coverage.
No login, hashing, token revocation or financial operation implementation exists
yet, so their runtime tests belong to subsequent tasks.

## Prioritized Findings

### High — Panic after a buffered success produces a success plus invalid JSON

`src/httpapi/runtime.go:34` catches a handler panic by writing another response.
If that handler already called `WriteJSON(w, 201, ...)`, the timeout wrapper has
buffered its success header and body. Recovery appends a JSON error rather than
discarding that response. The probe returned `201` and two JSON documents:

```text
{"created":true}
{"code":"INTERNAL_ERROR","message":"An internal error occurred."}
```

The existing panic test panics before writing anything. Add a regression for
headers/body written before panic and change recovery to discard buffered output
and return one fixed 500 JSON error. Also verify secret-bearing response headers
and partial output do not survive recovery.

### Medium — Contract tests do not enforce the confirmed ASCII email policy

The email schemas use `format: email`, maxLength and prose, without an ASCII
constraint. The configured validator accepts `é@example.com`; the repository
correctly rejects it. Add explicit input/output schema constraints and matching
positive/negative fixtures, preserving accepted trimming/case-normalization
behavior. Compare contract examples with NormalizeEmail so they cannot drift.

### Medium — Graceful-drain test does not prove a request stays alive during drain

`TestTLSGracefulShutdown` cancels the runtime and immediately releases its handler.
The handler can finish before shutdown starts, so a passing result does not prove
that shutdown waits for in-flight requests. Synchronize on shutdown/listener
closure while holding the handler; assert Serve has not returned and its request
context is not cancelled; release it and verify successful completion.

### Medium — Migration concurrency test needs deterministic overlap

`TestConcurrentRunners` starts two goroutines but does not establish that the
second attempts migration while the first holds its transaction lock. Scheduling
can turn it into sequential reruns. Add database-visible synchronization and a
lock-wait/cancellation scenario; verify the waiting runner leaves history intact
and can retry successfully. Preserve the existing rollback/integrity tests.

### Medium — No single check covers all delivered artifacts

`make check` excludes PostgreSQL integration and Python contract checks.
`make test-postgres` adds database integration but still excludes contract checks
and race-enabled HTTP/unit tests. Document an aggregate command, or add one,
that runs all these groups without claiming mocks replace real database tests.
CI remains T54, but contributors need a complete local validation path now.

### Lower Priority — Additional boundary and operational cases

- T01/T07: start each actual service binary with test certificates, verify HTTPS
  404 responses and SIGTERM exit; test missing TLS configuration exit status.
- T06: exercise the migration CLI itself (missing URL, invalid SQL, rerun, safe
  diagnostics), failure/interruption cleanup and empty migration directories.
- T07: exact body-size boundary, compressed content rejection, real chunked
  transport, header/read deadlines and plaintext rejection on the TLS listener.
- T08: 100-character name acceptance, 254/255-byte email boundaries, mixed-case
  concurrent submissions, concurrent attempts against an inactive user's email,
  and database-outage error redaction. Assert PostgreSQL SQLSTATE/constraint names
  in direct schema tests rather than accepting any error as proof of a constraint.

## Existing Coverage Worth Preserving

Global email uniqueness already has a genuine eight-connection concurrency test.
Migration reruns, append, rollback, history tampering and cleanup use PostgreSQL.
HTTP tests cover malformed/oversized JSON, early panic redaction, cancellation,
TLS setup and forced shutdown. Credentials have serialization/formatting checks.
These provide a useful foundation; the findings above are focused additions,
not a recommendation to rewrite the suite or test unimplemented features.

Recommended order: fix the panic bug with its regression; align email schemas;
strengthen lifecycle/concurrency tests; then add the complete validation command.

## Remediation Update

The first two findings have been addressed in the working tree:

- Panic recovery now surrounds the timeout wrapper, which discards uncommitted
  handler output on panic. Regression cases cover headers only, explicit success
  status, and a success JSON body, and check secret exclusion and logged status.
- OpenAPI input email constraints now explicitly reject non-ASCII while allowing
  surrounding ASCII whitespace before normalization. Canonical profile output
  must be lowercase ASCII without whitespace. Shared JSON fixtures exercise both
  schema validation and Go NormalizeEmail, including normalized length boundaries.

The other three gaps remain open as R01 (deterministic overlap), R02 (aggregate
validation), and R03 (boundary/operational coverage) in TASKS.md. No work on those
follow-ups is included in this fix.

Fix validation: fresh Go-container `make check` passed formatting, vet, package
tests and all five builds. Race-enabled HTTP/user tests passed, including the new
panic-response and shared email regressions. OpenAPI validation passed structure,
22 examples, nine existing invalid cases, Unicode password boundaries and all
14 shared email input/output cases. `git diff --check` passed. PostgreSQL
integration was not rerun after these fixes because no database code or SQL
changed; its successful review run is recorded above.

## R01 Progress — 2026-10-18

Shutdown tests now wait for listener closure and the server shutdown callback
before asserting that the blocked request remains active and Serve has not
returned. Releasing the handler must then yield a complete success response.
Three race-enabled HTTP runs passed. Replacing Shutdown with immediate Close in
a temporary source copy caused the test to fail, proving regression detection.

Migration concurrency tests now hold the first runner inside a migration using
a separate advisory gate and inspect pg_blocking_pids/pg_locks to confirm that
the second runner waits on the first's advisory lock. Cases cover ordinary
serialization and cancellation followed by retry on a fresh connection, with
single application/history assertions. Database execution and the no-lock
mutation check await Docker availability; R01 is not yet marked complete.

Full `make check` passed with the temporary Go 1.26.9 toolchain, and integration-tagged tests compiled. Docker Desktop remained unreachable after the socket appeared, so actual database validation is still outstanding.

## R01 Completion — 2026-10-18

Docker validation is now complete. Full repository checks and PostgreSQL tests
passed; both synchronization tests passed three additional race-enabled runs.
Temporary source mutations (immediate Close instead of Shutdown, and removal of
the migration advisory lock) each failed the intended test. The latter failed on
an observed non-advisory wait, demonstrating that incidental table/transaction
locking cannot satisfy the new test. Cancelled migration waiters left no committed
history and fresh-connection retries applied no duplicate work. Fixture cleanup
left zero test databases; validation exited 0. R01 is complete; R02/R03 remain open.

Repeat normal validation with `make test-postgres`; targeted repetition uses
`go test -race -count=3 -run TestTLSGracefulShutdown ./src/httpapi` and, against a
disposable database, `go test -race -tags=integration -count=3 -run TestConcurrentRunners ./tests/integration`.
Mutation checks were run only in temporary copies, never on production sources.
