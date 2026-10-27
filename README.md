# HIGH PERFORMANCE TRANSACTION SERVICE
## INTRODUCTION
My take on experimentic with Agentic Spec Driven Development using Codex from OpenAi.

## Local development

Install Go 1.26 or newer and GNU Make. The PostgreSQL driver is pinned in `go.mod`; dependency checksums are committed
in `go.sum`.

From the repository root:

| Command | Purpose |
| --- | --- |
| `make build` | Build the three HTTP services, worker scaffold and migration CLI into `bin/`. |
| `make fmt` | Apply Go's standard formatting. |
| `make fmt-check` | Detect formatting changes; applies formatting and fails if files changed. |
| `make test` | Run `go test ./...`. |
| `make vet` | Run Go's static checks. |
| `make check` | Run Go formatting, static checks, tests, and builds (no database/contracts). |
| `make check-all` | Run the complete isolated Docker validation suite, including contracts and PostgreSQL. |
| `make clean` | Remove the generated binaries. |

Build one process with `make bin/user` (or `bin/account`, `bin/transaction`,
`bin/transaction-worker`, or `bin/migrate`). Run an HTTP service with `./bin/user`
(or `go run ./cmd/user`) after setting
`HTTP_TLS_CERT_FILE` and `HTTP_TLS_KEY_FILE`. Account and Transaction use the
same HTTP configuration; set different `HTTP_ADDR` ports to run them together.
User implements `POST /users` and `POST /sessions` and also requires `DATABASE_URL` with migrations
applied; see [registration](docs/registration.md). Account/Transaction routes
currently return JSON 404 errors. The worker
still prints its scaffold message and exits. See [HTTP runtime settings](docs/http-runtime.md).
`go test ./...` now includes isolated HTTP runtime tests.

The entry points live under `cmd/`. Application code will live under `src/`,
integration tests under `tests/`, contracts under `api/`, and deployment assets
under `deploy/` as their tasks are implemented. `SPEC.md`, `PLAN.md`, and
`TASKS.md` record requirements, implementation direction, and progress.

To validate a clean checkout, run `make check`, then run the HTTP services with
configured TLS files and the worker scaffold. Go-only checks need no database;
running the User service requires PostgreSQL and applied migrations.


## PostgreSQL migrations and integration tests

`make test-postgres` requires Docker and runs an isolated PostgreSQL 17 server
and Go 1.26 runner, with no published ports or persistent volumes. Sources are
streamed into the runner, so Docker Desktop does not need shared host paths.
The script waits for readiness, runs `make check` and race-enabled integration
tests, verifies no fixture databases remain, and removes its containers/network
on success, failure, or interruption. Test-only trust authentication is confined
to this disposable network; do not use it for a persistent or production server.

With Go installed and an existing **disposable** PostgreSQL server, set
`TEST_DATABASE_URL` in your environment and run `make test-integration`. Its role
must be allowed to create/drop databases. Each test gets a random database and
registers cleanup immediately; test fixtures never use application databases.
Without the integration build tag, normal `make test` does not need PostgreSQL.

`make migrate` applies files from `migrations/` using `DATABASE_URL`. For a custom
folder or timeout, use `./bin/migrate -dir path/to/sql -timeout 2m`. See
[migration conventions](migrations/README.md). The first migration creates user persistence; migration-runner tests also use
independent fixture SQL. Database URLs must not be
committed, printed or copied into issue reports.


## Complete validation

Run `make check-all` before submitting changes. It requires a running Docker
Engine/Desktop, GNU Make, a POSIX shell, tar and mktemp; Go, Python and PostgreSQL
do not need to be installed on the host. For Docker Desktop, select its context
first, or use `DOCKER_CONTEXT=desktop-linux make check-all`.

The command snapshots only the source/test/contract/build inputs, then runs:

1. OpenAPI structure/examples, boundary and shared email cases, plus validation
   orchestration regression tests, in a disposable Python container.
2. Go formatting, vet, regular tests and all binary builds in a Go container.
3. Race-enabled Go tests, including HTTP lifecycle and user repository unit tests.
4. Race-enabled integration tests against a real disposable PostgreSQL server.
5. Verification that test databases were removed, followed by container/network
   and source-archive cleanup. No database ports or persistent volumes are created.

Images, Python requirements, the container's Make package, and Go dependencies
may download on first use; registry/package network access is required. Generated
binaries and formatting changes stay inside the snapshot. Any failed check or
cleanup returns nonzero. Diagnostics retain the failing check's output; do not
interpret the final success message alone as proof that cleanup succeeded.

`make check` remains the faster Go-only command; `make test-postgres` preserves
its previous Go-plus-database scope. For orchestrator development alone, run
`python3 -m unittest discover -s tests/validation -v` (Python and Make required).
Those tests simulate tool/container failures to verify control flow and cleanup;
they do not replace the real validators or PostgreSQL tests in `check-all`.
GitHub Actions runs this command on branch pushes and pull requests. See
[continuous integration](docs/continuous-integration.md) for triggers, the stable
check name and instructions for requiring passing checks before merging.

### Targeted boundary and operational tests

With Go installed, run the real TLS transport checks independently:

```sh
go test -race -count=1 -run '^TestNetwork' ./src/httpapi
```

The following groups require `TEST_DATABASE_URL` pointing to a disposable
PostgreSQL server with database creation/deletion rights. CLI tests build the
executables using Go; service tests use temporary certificates and local ports.

```sh
go test -race -tags=integration -count=1 -run '^TestServiceBinaries$' ./tests/integration
go test -race -tags=integration -count=1 -run 'TestMigrationCLI|TestFixtureCleanupAfterTestFailure' ./tests/integration
go test -race -tags=integration -count=1 -run 'TestUser|TestConcurrentEmail|TestConcurrentRegistration' ./tests/integration
```

`make check-all` includes all these groups without requiring host Go. The Python
orchestration suite also checks SIGTERM cleanup with simulated Docker resources;
a failing-test subprocess verifies actual PostgreSQL fixture cleanup.

Password hashing and verification are documented in [password hashing](docs/password-hashing.md).

Session persistence, token format and lifecycle integration requirements are
documented in [sessions](docs/sessions.md). Login is implemented; see [login](docs/login.md). Bearer middleware and logout remain later tasks.
