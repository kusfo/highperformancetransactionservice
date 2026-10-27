# Implementation Tasks

## Tracking and Completion Rules

`SPEC.md` defines behavior; `PLAN.md` defines the implementation direction. Tasks remain unchecked until their acceptance checks pass. Check a box only when its deliverable and acceptance checks are complete; add evidence (test command/result or reviewed artifact) beneath its task ID. Record blockers rather than marking partial work complete.

Each task is one bounded change suitable for its own review. Dependencies are prerequisites, not a requirement to build the whole application: use isolated database/broker fixtures, handler fakes, or frontend API fixtures for validation. Do not substitute mocks for database concurrency checks. Later integration tasks verify the assembled system. Split a task before implementation if it expands beyond one focused change.

Contract tasks resolve outstanding decisions and update SPEC.md/OpenAPI before dependent implementation. They must not silently introduce product behavior. No implementation or deployment is authorized by checking this backlog into the repository.

## Suggested Starting Order

Begin T01–T05; T01 supports the environment/runtime work while contract decisions are captured. Then complete T06–T13 and T17–T27. Broker and frontend foundation work can proceed once their listed prerequisites are ready. Closure and system integration follow the core persistence rules. This is dependency guidance, not an instruction to use multiple agents.

## Contracts and foundations

### T01 — Go module and development commands

- [x] Complete

**Depends on:** —.
**Deliver:** Create module, service/worker entry-point skeletons, formatting and build commands.
**Verify:** Build each binary; run Go tests from a clean checkout.

**Validation (2026-10-13):** Assistant independently ran `make check` in a fresh `golang:1.26` container (Go 1.26.9), using only a source copy of `cmd/`, `go.mod` and `Makefile`, without existing binaries or build cache. Formatting, `go vet ./...`, `go test ./...`, all four builds and all four binary startup checks passed; container exited 0. Packages report `[no test files]`, as expected for these skeletons. `git diff --check` passed. This validates the current uncommitted source in isolation, rather than a committed Git checkout. Docker Desktop could not bind-mount the sandbox temporary directory; source was streamed into the container instead.

### T02 — User and session contract

- [x] Complete

**Depends on:** —.
**Deliver:** Resolve email normalization, profile/password updates, refresh/logout and token representation; document ten-minute expiry and manual administration.
**Verify:** Record decisions in SPEC.md; validate OpenAPI user/session examples; no recovery, verification or superuser endpoints.

**Validation (2026-10-13):** All T02 email, displayName, password-change and session decisions confirmed and recorded in SPEC.md. `api/user-session.openapi.json` and `docs/user-session-contract.md` define the contract and operator-only administration scope. Disposable Python-container validation passed OpenAPI structure, 22 embedded request/response examples, nine invalid-payload cases and Unicode password boundaries; exit 0. No recovery, verification, refresh or superuser endpoints. `git diff --check` passed. Runtime behavior is reserved for later tasks.

### T03 — Account and lifecycle contract

- [ ] Complete

**Depends on:** {deps}.
**Deliver:** Specify metadata validation, pagination and lifecycle request-ID field/header, conflict/replay semantics and account responses.
**Verify:** Validate OpenAPI examples for closure, repeated closure, reactivation and historical retries.

### T04 — Async operation contract

- [ ] Complete

**Depends on:** {deps}.
**Deliver:** Specify submission/status schemas, intake rejection identity, retained declines and overload responses.
**Verify:** Validate pending/success/decline/replay examples; distinguish HTTP lookup success from business outcome.

### T05 — Persistence concurrency design

- [ ] Complete

**Depends on:** {deps}.
**Deliver:** Document shared Account/Transaction persistence, lock order, sequence scheduling and closure-generation model.
**Verify:** Walk through duplicate intake, reordered messages, concurrent closure and reactivation; identify atomic commit boundaries.

### T06 — PostgreSQL test harness and migration runner

- [x] Complete

**Depends on:** T01.
**Deliver:** Add isolated database fixtures, migration execution and explicit test cleanup.
**Verify:** Apply migrations to a fresh database and rerun safely; integration tests leave no shared state.

**Validation (2026-10-13):** `scripts/test-postgres.sh` passed against a disposable PostgreSQL 17 container: formatting, `go vet`, regular Go tests, all five binary builds and race-enabled integration tests. Covered fresh application, rerun, append, whole-batch rollback, changed/missing/duplicate/invalid/out-of-order migrations, concurrent runners and fixture cleanup/isolation. Script verified zero remaining fixture databases, then removed its dedicated containers/network; exit 0. `sh -n scripts/test-postgres.sh` and `git diff --check` passed. Driver and checksums are pinned in go.mod/go.sum; domain schemas remain in later tasks.

### T07 — HTTP runtime and redacted logging

- [x] Complete

**Depends on:** T01.
**Deliver:** Add configuration, bounded HTTP requests, stable errors, structured logging and graceful shutdown.
**Verify:** Handler tests verify malformed requests, limits, cancellation and absence of credentials/tokens in captured logs.


**Validation (2026-10-13):** Fresh Go-container `make check` passed formatting, vet, all package tests and all five binary builds. `go test -race -count=1 -v ./src/httpapi` passed eight tests with configuration/JSON/cancellation subcases, real TLS graceful/forced shutdown and listener cleanup. Captured logs/responses exclude submitted passwords, token-bearing paths/queries/headers/cookies, hashes and panic values. Fixed an empty client-cancellation response discovered on the first run; final validation exited 0. `git diff --check` passed. Settings and runtime limitations documented in docs/http-runtime.md; domain handlers remain later tasks.


## User and authentication

### T08 — User schema and repository

- [x] Complete

**Depends on:** T02, T06.
**Deliver:** Add unique normalized email, password hash and active-state persistence; keep used emails reserved.
**Verify:** Database tests reject concurrent duplicate registration, including emails belonging to inactive users.

**Validation (2026-10-15):** `scripts/test-postgres.sh` passed formatting, vet, regular unit tests, all five builds and race-enabled PostgreSQL 17 integration tests (exit 0). Eight concurrent registration attempts produced one user and duplicate-email sentinels for the others. Verified canonical email lookup/reservation after deactivation, reactivation, retained hashes, display-name/schema constraints, missing users and cancellation. Added an ASCII-before-case-folding regression test and credential serialization/formatting checks. Migration reruns and existing migration tests passed; no fixture databases remained. `git diff --check` passed. HTTP registration, password hashing and session revocation remain later tasks.

### T09 — Password hashing component

- [x] Complete

**Depends on:** T01, T02.
**Deliver:** Choose password-hashing parameters and implement server-generated salts and verification.
**Verify:** Correct passwords verify; wrong passwords fail; equal passwords produce distinct hashes.

**Validation (2026-10-25):** Added `src/passwords` with Argon2id v19 (64 MiB, three passes, four lanes), random 16-byte salts, 32-byte keys and strict PHC parsing before derivation. Correct/wrong passwords, distinct salts, 12/128 Unicode boundaries, invalid UTF-8, whitespace/normalization preservation and malformed/unsupported hashes passed. An independently generated argon2-cffi 25.1.0 vector verifies interoperability. `DOCKER_CONTEXT=desktop-linux make check-all` passed contract/orchestration checks, formatting/vet, normal and race-enabled tests, all five builds and real PostgreSQL integration; no fixture databases remained and resource cleanup exited 0. Password package tests took 1.038s normally and 3.578s with race instrumentation in this run (not a deployment benchmark). Parameters, private-storage format and future endpoint concurrency responsibilities are documented in docs/password-hashing.md. Registration integration remains T10.

### T10 — Registration endpoint

- [x] Complete

**Depends on:** T02, T06, T07, T08, T09.
**Deliver:** Implement registration and safe response serialization.
**Verify:** HTTP/database tests cover valid creation, duplicate email and client-provided hash rejection; responses/logs contain no secrets.

**Validation (2026-10-26):** Implemented `POST /users` with strict input decoding, server-side Argon2id hashing, pooled PostgreSQL storage, bounded registration concurrency and userId-only responses. User service now requires `DATABASE_URL`; migrations remain explicit. Confirmed NUL display-name rejection is reflected in SPEC/OpenAPI. `DOCKER_CONTEXT=desktop-linux make check-all` passed contracts/examples, orchestration tests, formatting/vet, all five builds, unit/race tests and real PostgreSQL integration. HTTPS tests verified canonical email, stored hash verification/salt uniqueness, omitted/100-character names, active/inactive and concurrent duplicate conflicts, client-hash rejection, safe storage errors and log redaction. Unit tests cover invalid/null/duplicate/unknown fields, NUL names, error mapping and cancellation while waiting/hashing. The compiled User executable registered successfully and exited cleanly on SIGTERM; missing/malformed database configuration failed safely. No fixture databases remained and resource cleanup exited 0. An unused integration-test import found in the first run was removed before the successful rerun. See docs/registration.md for setup and runtime limits. Login/session issuance remains T11/T12.

### T11 — Session persistence and issuance

- [x] Complete

**Depends on:** T02, T06, T08.
**Deliver:** Implement selected token model, ten-minute expiry and revocation persistence.
**Verify:** Clock-controlled tests cover expiry boundary, token authenticity and durable revocation.

**Validation (2026-10-27):** Added append-only session migration and `src/sessions`: random 256-bit opaque tokens, SHA-256 digest-only storage, ten-minute expiry, active-user checks and durable single/all-user revocation. `DOCKER_CONTEXT=desktop-linux make check-all` passed contracts, orchestration regressions, formatting/vet, all five builds, unit/race tests and real PostgreSQL integration; no fixture databases remained and cleanup exited 0. Injected-clock tests verify issuance, just-before/exact/after expiry and pre-issuance rejection. Altered/unknown/malformed tokens fail; revocation persists across connections; explicitly revoked sessions remain invalid after re-enablement. Transaction rollback covers issuance/revocation; storage failures expose fixed errors and no issued token; credential formatting/JSON are redacted. `git diff --check` passed. See docs/sessions.md for format, clock requirements and transaction boundaries. Login/middleware and atomic lifecycle integration remain later tasks; no HTTP session endpoint is added here.

### T12 — Login endpoint

- [x] Complete

**Depends on:** T02, T07, T08, T09, T11.
**Deliver:** Issue sessions only for valid credentials of active users.
**Verify:** Test valid login, inactive users and generic failure responses for unknown email/wrong password.

**Validation (2026-10-27):** Implemented `POST /sessions` and `src/login` with canonical email lookup, exact password verification, shared bounded registration/login admission, a READ COMMITTED user-row lock and session commit before token publication. Unknown/wrong/inactive credentials share a fixed 401 response; unknown/malformed stored credentials incur dummy Argon2 work. The login contract's 1–128-character password range is preserved (short attempts return 401, empty/overlong input returns 400). `DOCKER_CONTEXT=desktop-linux make check-all` passed contracts, orchestration, formatting/vet, all five builds, unit/race tests and real PostgreSQL integration. HTTPS checks verified response fields, ten-minute expiry, distinct durable tokens, generic failures, no sessions on rejected attempts and credential/token log redaction. A deferred-constraint failure proved no token is returned on failed commit. Observed database lock waits proved login rechecks committed deactivation/password changes. Compiled User service login and SIGTERM passed. No fixture databases remained; resource cleanup exited 0. `git diff --check` passed. See docs/login.md. Bearer middleware, logout and HTTP lifecycle endpoints remain later tasks.

### T13 — Entry-point authentication middleware

- [ ] Complete

**Depends on:** {deps}.
**Deliver:** Validate bearer credentials, expiry, revocation and active user; expose validated identity.
**Verify:** Test missing, malformed, forged, expired/revoked credentials and inactive users using isolated fixtures.

### T14 — Profile update endpoint

- [ ] Complete

**Depends on:** {deps}.
**Deliver:** Implement only agreed profile/credential changes and owner authorization.
**Verify:** Test allowed fields, duplicate email if applicable, identity isolation and forbidden fields.

### T15 — User deactivation endpoint

- [ ] Complete

**Depends on:** {deps}.
**Deliver:** Deactivate without deleting accounts or transaction history; reject subsequent authenticated requests.
**Verify:** Repository/handler tests verify preserved records, repeated deactivation semantics and blocked new requests.

### T16 — Manual revocation and user re-enablement

- [ ] Complete

**Depends on:** {deps}.
**Deliver:** Provide documented operator-only commands/procedures without superuser APIs/UI.
**Verify:** Fixture tests show revocation persists and re-enabled users can log in; commands do not print secrets.


## Account and operation persistence

### T17 — Account schema and repository

- [ ] Complete

**Depends on:** {deps}.
**Deliver:** Add unique assigned numbers, single owner, bounded balance, metadata, status, sequence and lifecycle generation.
**Verify:** Database tests enforce zero creation, balance bounds, uniqueness and immutable owner/number.

### T18 — Account create endpoint

- [ ] Complete

**Depends on:** {deps}.
**Deliver:** Assign account number/owner server-side and accept agreed metadata only.
**Verify:** Test multiple accounts per user, zero initial balance and rejected balance/owner overrides.

### T19 — Account read and list endpoints

- [ ] Complete

**Depends on:** {deps}.
**Deliver:** Implement owner-scoped reads and pagination including disabled accounts.
**Verify:** Test cross-user isolation, missing/inaccessible responses and stable pagination.

### T20 — Account metadata update endpoint

- [ ] Complete

**Depends on:** {deps}.
**Deliver:** Allow displayName/description changes only.
**Verify:** Test field validation and rejection of balance, number and ownership changes.

### T21 — Operation, history and outbox schema

- [ ] Complete

**Depends on:** {deps}.
**Deliver:** Add global operation/transaction IDs, immutable payload/outcomes, per-account sequence, outbox and lifecycle results.
**Verify:** Database tests verify uniqueness, required relationships and rollback; no automatic outcome expiry.

### T22 — Money validation component

- [ ] Complete

**Depends on:** {deps}.
**Deliver:** Implement nonzero symmetric amount limits, bounded balances and wider intermediate arithmetic.
**Verify:** Boundary tests include zero, ±2147483647, -2147483648, underflow and overflow without wraparound.

### T23 — Durable operation acceptance

- [ ] Complete

**Depends on:** {deps}.
**Deliver:** Atomically check account/owner/status, assign sequence and store pending operation plus outbox; handle global duplicates.
**Verify:** Real database concurrency tests prove one operation/outbox/sequence for duplicates, payload conflicts and no partial acceptance.

### T24 — Transaction submission endpoint

- [ ] Complete

**Depends on:** {deps}.
**Deliver:** Expose durable acceptance, Location header, pending and terminal replays, and intake errors.
**Verify:** Handler tests with repository fixtures verify 202 only after commit, conflict isolation and lost-response retry identity.

### T25 — Operation status endpoint

- [ ] Complete

**Depends on:** {deps}.
**Deliver:** Return authorized pending or immutable terminal results with original balance-after.
**Verify:** Test all states, 200 lookup for declines, cross-user isolation and original results after later account changes.


## Ordered execution and messaging

### T26 — Atomic balance processor

- [ ] Complete

**Depends on:** {deps}.
**Deliver:** Process stored operations without bearer tokens; atomically commit balance/history/outcome or retained business decline.
**Verify:** Invoke directly against PostgreSQL: verify success, insufficient funds, overflow, duplicates and injected rollback.

### T27 — Per-account ordered scheduler

- [ ] Complete

**Depends on:** {deps}.
**Deliver:** Select earliest unresolved operation, serialize same-account execution and recover notification gaps.
**Verify:** Database tests force out-of-order triggers, competing workers and transient failures; later debit cannot overtake earlier deposit.

### T28 — RabbitMQ development fixture

- [ ] Complete

**Depends on:** {deps}.
**Deliver:** Add isolated broker fixture, durable topology and documented local versus production durability settings.
**Verify:** Publish/consume smoke test; topology declaration is repeatable; fixture cleanup is explicit.

### T29 — Transactional outbox relay

- [ ] Complete

**Depends on:** {deps}.
**Deliver:** Publish committed operation notifications with confirms and durable publication tracking.
**Verify:** Broker/database tests inject broker outage and crash after publish; no event is lost and duplicates are allowed.

### T30 — Worker message adapter

- [ ] Complete

**Depends on:** {deps}.
**Deliver:** Dispatch operation notifications into ordered scheduling and acknowledge only committed outcomes.
**Verify:** Test duplicate delivery and commit-before-ack crash; one balance effect and one history record.

### T31 — Worker recovery and dead-letter handling

- [ ] Complete

**Depends on:** {deps}.
**Deliver:** Add bounded backoff, unresolved-work reconciliation and safe replay procedures.
**Verify:** Simulate poison message, relay outage and stranded operation; accepted work is recoverable without duplicate effects.

### T32 — Admission control

- [ ] Complete

**Depends on:** {deps}.
**Deliver:** Add configurable intake limits and bounded worker concurrency/prefetch.
**Verify:** Overload tests reject new work before acceptance while existing-ID retries remain readable; no orphan pending operations.


## Closure, reactivation and history

### T33 — Atomic closure persistence

- [ ] Complete

**Depends on:** {deps}.
**Deliver:** Atomically withdraw remaining balance, disable account, preserve result and invalidate pending generation durably.
**Verify:** Direct database tests cover zero/nonzero balance, concurrent closure and closure-versus-processing; rollback leaves no partial state.

### T34 — Reactivation and lifecycle replay persistence

- [ ] Complete

**Depends on:** {deps}.
**Deliver:** Use lifecycle request IDs; reactivate without money changes and replay old outcomes without repeating actions.
**Verify:** Test disable/reactivate cycles, conflicting IDs and old disable retry; pending old-generation work stays declined.

### T35 — Disable and reactivate endpoints

- [ ] Complete

**Depends on:** {deps}.
**Deliver:** Expose owner-only lifecycle handlers and recorded closure results.
**Verify:** Handler tests verify status, withdrawal IDs, no zero-balance transaction and no effects from repeated requests.

### T36 — Pending-on-closure status integration

- [ ] Complete

**Depends on:** {deps}.
**Deliver:** Make closure-invalidated pending operations appear as retained ACCOUNT_DISABLED declines before/after worker reconciliation.
**Verify:** Close then immediately query/reactivate before worker runs; status is declined and no old operation executes.

### T37 — Transaction history endpoint

- [ ] Complete

**Depends on:** {deps}.
**Deliver:** Add owner-scoped paginated history including closure withdrawals and disabled accounts.
**Verify:** Seed history directly: verify ordering/pagination, isolation and readability after disabling.

### T38 — Authentication-change queue tests

- [ ] Complete

**Depends on:** {deps}.
**Deliver:** Verify expiry, manual revocation and user deactivation do not cancel durably accepted work.
**Verify:** Gate worker execution, change authentication state, resume and verify original work completes while new HTTP requests fail.


## Manual-testing frontend

### T39 — Frontend foundation and session design

- [ ] Complete

**Depends on:** {deps}.
**Deliver:** Choose framework and browser session handling; build API client and fictitious fixtures.
**Verify:** Frontend build and isolated client tests pass; document token handling, no secret logging and exact decimal-to-cents conversion.

### T40 — Registration and login screens

- [ ] Complete

**Depends on:** {deps}.
**Deliver:** Implement registration/login, expiry feedback and error states against API fixtures.
**Verify:** Browser tests use stubbed responses for success/failure; credentials never appear in logs or URLs.

### T41 — Account list and creation UI

- [ ] Complete

**Depends on:** {deps}.
**Deliver:** Display owned accounts and create zero-balance accounts with metadata.
**Verify:** Browser fixture tests cover empty/loading/error states, balances and active/disabled display.

### T42 — Deposit and withdrawal forms

- [ ] Complete

**Depends on:** {deps}.
**Deliver:** Validate exact cents, generate a fresh operationId per interaction and reuse it on automatic retries.
**Verify:** Browser tests verify amount boundaries, double-submit protection, retry payload identity and new-interaction IDs.

### T43 — Pending outcome polling UI

- [ ] Complete

**Depends on:** {deps}.
**Deliver:** Poll status, preserve pending IDs across reloads and refresh current balance/history after completion.
**Verify:** Fixture tests cover delayed success/decline, auth expiry and reload recovery; historical balance-after never overwrites newer balance.

### T44 — Closure, reactivation and history UI

- [ ] Complete

**Depends on:** {deps}.
**Deliver:** Show lifecycle results, editable metadata and transaction history for disabled accounts.
**Verify:** Browser fixture tests cover closure withdrawal, zero closure, reactivation and reused lifecycle retry IDs.


## Packaging, deployment and system verification

### T45 — Container build targets

- [ ] Complete

**Depends on:** {deps}.
**Deliver:** Add non-root multi-stage images for all service processes, relay and frontend.
**Verify:** Build each image independently and smoke-test configuration/startup; no embedded secrets.

### T46 — Local HTTPS gateway

- [ ] Complete

**Depends on:** {deps}.
**Deliver:** Configure same-origin API routing and local certificate workflow.
**Verify:** Run with stub backends; verify trusted HTTPS and blocked/plain-HTTP credential submission.

### T47 — Full local Compose environment

- [ ] Complete

**Depends on:** {deps}.
**Deliver:** Wire services, worker, relay, frontend, PostgreSQL, RabbitMQ and migrations with persistence.
**Verify:** Fresh-start smoke test and documented reset; restart preserves balances and operation outcomes.

### T48 — HTTP end-to-end correctness suite

- [ ] Complete

**Depends on:** {deps}.
**Deliver:** Exercise agreed API behavior across real services/storage/broker.
**Verify:** Verify FIFO, global conflicts, closure races, retained declines, restart/lost-response replay and authorization isolation.

### T49 — Browser end-to-end manual walkthrough

- [ ] Complete

**Depends on:** {deps}.
**Deliver:** Automate and document registration through deposit/withdrawal, closure/history and reactivation.
**Verify:** Run against the local HTTPS environment with fictitious accounts; confirm UI outcomes agree with persisted balances.

### T50 — Health checks and shutdown

- [ ] Complete

**Depends on:** {deps}.
**Deliver:** Add dependency-aware readiness, liveness and bounded graceful process shutdown.
**Verify:** Fault tests distinguish unavailable dependencies from unhealthy process; worker shutdown preserves unacked work.

### T51 — Operational metrics

- [ ] Complete

**Depends on:** {deps}.
**Deliver:** Expose request/processing latency, pending age, contention, replay and broker recovery metrics.
**Verify:** Deterministic tests verify metric changes and avoid user/account/token high-cardinality labels.

### T52 — Kubernetes application manifests

- [ ] Complete

**Depends on:** {deps}.
**Deliver:** Add deployments/services, probes, resources, secret/config references and migration Job.
**Verify:** Render/validate manifests locally with placeholder secrets; verify labels, routing and migration sequencing.

### T53 — Kubernetes local deployment smoke test

- [ ] Complete

**Depends on:** {deps}.
**Deliver:** Deploy on a local cluster with documented database/broker/TLS overlays.
**Verify:** Run API smoke tests and worker restart replay; document provider-specific prerequisites without assuming a cloud account.

### T54 — GitHub Actions continuous integration

- [x] Complete

**Priority:** Next task, before T10; no deployment environment is required.
**Depends on:** R02.
**Deliver:** Add a GitHub Actions workflow for every branch push, pull requests and manual dispatch. Run the existing `make check-all` on a GitHub-hosted Ubuntu VM with Docker: contracts/examples, orchestration tests, formatting/vet, Go builds, race tests and real PostgreSQL integration. Use read-only repository permissions, pinned action revisions, a job timeout and cancellation of superseded runs for the same ref/event. Preserve cleanup and expose check logs/status. No deployment or cloud credentials are required.
**Verify:** Validate workflow syntax and the existing command locally; observe successful hosted push and pull-request runs. In a disposable branch, introduce a deliberate test failure and verify the check fails. Document the stable check name and how to make it required through repository rules; a workflow alone does not block merges. Leave incomplete until hosted-run evidence is recorded.

**Validation (2026-10-25):** actionlint 1.7.7 and local `make check-all` passed, including race-enabled tests, PostgreSQL integration and fixture/resource cleanup. GitHub-hosted [push run](https://github.com/kusfo/highperformancetransactionservice/actions/runs/40239902511) and [PR run](https://github.com/kusfo/highperformancetransactionservice/actions/runs/40240024178) passed for workflow commit `3ffe8f8`. The separate disposable failure-probe branch produced an [expected failed run](https://github.com/kusfo/highperformancetransactionservice/actions/runs/40240098031), with `TestCIFailureProbe` and exit code 2 confirmed in logs. Its [superseded run](https://github.com/kusfo/highperformancetransactionservice/actions/runs/40240082449) was cancelled. The intentional failing test is absent from the implementation branch. See `docs/continuous-integration.md` for check requirements. Implementation is delivered in PR #1; activation on main and repository merge rules require merging/configuration, not further workflow code.

### T54-B — Extend CI for later components

- [ ] Complete

**Depends on:** T54 and the tasks delivering broker integration, frontend and container builds.
**Deliver:** Add broker integration tests, frontend checks and application image builds as those components become available. Keep publishing/deployment separate until environments and release policy are defined.
**Verify:** Each added check passes on GitHub and propagates failures; builds do not require deployment credentials.

### T55 — Repeatable load benchmark

- [ ] Complete

**Depends on:** {deps}.
**Deliver:** Create distributed-account/hot-account workloads and backlog measurements; record hardware/configuration.
**Verify:** Report throughput and latency baselines plus invariant checks; do not invent unconfirmed traffic targets.

### T56 — Database backup and restore runbook

- [ ] Complete

**Depends on:** {deps}.
**Deliver:** Document backup/restore and validate recovery of balances, history and idempotency outcomes.
**Verify:** Restore an isolated fixture database and verify retries do not reapply completed operations.

### T57 — Deployment and operator documentation

- [ ] Complete

**Depends on:** {deps}.
**Deliver:** Document local usage, cloud overlays, TLS/secrets, rollouts, safe queue replay and manual administration.
**Verify:** Follow runbooks in disposable environments; explicitly list unresolved cloud/recovery sizing decisions.

## Test Review Follow-ups

These follow-ups cover the three deferred gaps from the 2026-10-16 review.
The panic recovery and email contract fixes are recorded in the review document.

### R01 — Prove lifecycle and migration concurrency overlap

- [x] Complete

**Depends on:** T06, T07.
**Deliver:** Synchronize graceful shutdown tests so the handler remains blocked after listener closure; synchronize migration tests so a second runner demonstrably waits on the first, including cancellation/retry.
**Verify:** Tests fail if shutdown closes active requests immediately or migration serialization is removed; avoid relying on scheduler timing alone.

**Validation (2026-10-18):** Completed after Docker Desktop became available. Full formatting/vet/unit/build checks and race-enabled PostgreSQL integration passed. Shutdown and migration-overlap tests each passed three additional race-enabled repetitions, including cancelled waiter/retry and single-application/history assertions. In disposable source copies, replacing graceful Shutdown with immediate Close failed the shutdown test, and removing the migration advisory lock failed both concurrency cases on the observed lock type. Both mutation failures were expected and checked by the validation script. No fixture databases remained; dedicated containers/network were removed; validation exit 0. Production code is unchanged.

### R02 — Add one complete local validation command

- [x] Complete

**Depends on:** T02, T06, T07, T08.
**Deliver:** Provide a documented command for OpenAPI/examples, formatting, vet, builds, race-enabled HTTP/user tests and real PostgreSQL integration; preserve container/fixture cleanup on failure.
**Verify:** Run from an isolated source copy; failure in any check group produces nonzero status and no leftover test resources. CI wiring remains T54.

**Validation (2026-10-21):** Finalized `make check-all` passed from isolated snapshots: OpenAPI/examples/email fixtures, orchestration regressions, formatting/vet/unit tests, all five builds, race-enabled Go tests and PostgreSQL integration. Four orchestration tests cover success, failure propagation/cleanup for every group (including orchestration itself), cleanup-error status, and preserved postgres-only mode. A real isolated source copy with an intentionally failing Go test returned nonzero; Docker inspection confirmed its three containers and network were removed. Successful-run fixture count was zero and cleanup exited 0. `sh -n` and `git diff --check` passed. Earlier in-development wrapper run was invalidated by editing it during execution; only the finalized unchanged run is completion evidence. CI wiring remains T54.

### R03 — Extend boundary and operational test coverage

- [x] Complete

**Depends on:** T01, T06, T07, T08.
**Deliver:** Add the following independently runnable regression cases in small changes:

- Service binary HTTPS startup/SIGTERM and missing-TLS configuration failure.
- Migration CLI missing URL, invalid SQL, rerun and safe diagnostics; interrupted/failing fixture cleanup and empty migration directory.
- Exact request-body limit, compressed-body rejection, real chunked requests, network header/read deadlines and plaintext rejection.
- User name upper boundary, mixed-case concurrent attempts against active/inactive emails, storage-outage redaction, and explicit SQLSTATE/constraint assertions. Shared email length fixtures now cover the 254/255-character boundary.

**Verify:** Each case reproduces its targeted failure independently, uses disposable resources, and passes with the documented behavior. Split into separately tracked subtasks when work begins; do not add tests for unimplemented domain endpoints.

### R03-A — Service executable lifecycle

- [x] Complete

**Verify:** `TestServiceBinaries` builds each HTTP service, checks missing TLS rejection, serves HTTPS and exits cleanly on SIGTERM.

### R03-B — Migration CLI and cleanup

- [x] Complete

**Verify:** `TestMigrationCLI` covers configuration, redaction, rollback, timeout, rerun and empty directories; `TestFixtureCleanupAfterTestFailure` checks a failing subprocess drops its database. The orchestration SIGTERM test verifies interrupted wrapper cleanup using simulated Docker resources.

### R03-C — HTTP transport boundaries

- [x] Complete

**Verify:** `TestNetwork*` covers exact/oversized known-length and chunked bodies, unsupported encoding, oversized headers, plaintext rejection and independent header/body read deadlines over real sockets.

### R03-D — User persistence boundaries

- [x] Complete

**Verify:** Integration tests cover 100/101-character Unicode names, mixed-case concurrent active/inactive email registration, closed-connection error redaction and named PostgreSQL constraints/SQLSTATEs.

**Validation (2026-10-22):** `DOCKER_CONTEXT=desktop-linux make check-all` passed contracts/examples, all five orchestration tests, formatting/vet, normal and race-enabled Go tests, all five builds, and real PostgreSQL integration (including compiled CLI/service subprocesses). No fixture databases remained; container/network cleanup completed with exit 0. The first run caught a test URL error: pgx's original connection string referenced the admin database, so CLI tests now explicitly select their isolated fixture database. SIGTERM wrapper cleanup is covered with simulated Docker resources; failing-test fixture cleanup uses real PostgreSQL. Production code is unchanged.

## Deferred Scope

External payout integration, superuser UI/API, email verification/recovery, shared ownership and ownership transfers are excluded. Performance targets will follow expected-traffic clarification. First-cloud provisioning and recovery sizing remain environment decisions, not implicit completion criteria for the portable deployment assets.
