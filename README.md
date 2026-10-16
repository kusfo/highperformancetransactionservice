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
| `make check` | Run formatting, static checks, tests, and builds. |
| `make clean` | Remove the generated binaries. |

Build one process with `make bin/user` (or `bin/account`, `bin/transaction`,
`bin/transaction-worker`, or `bin/migrate`). Run an HTTP service with `./bin/user`
(or `go run ./cmd/user`) after setting
`HTTP_TLS_CERT_FILE` and `HTTP_TLS_KEY_FILE`. Account and Transaction use the
same configuration; set different `HTTP_ADDR` ports to run them together. Domain
endpoints are later tasks; current routes return JSON 404 errors. The worker
still prints its scaffold message and exits. See [HTTP runtime settings](docs/http-runtime.md).
`go test ./...` now includes isolated HTTP runtime tests.

The entry points live under `cmd/`. Application code will live under `src/`,
integration tests under `tests/`, contracts under `api/`, and deployment assets
under `deploy/` as their tasks are implemented. `SPEC.md`, `PLAN.md`, and
`TASKS.md` record requirements, implementation direction, and progress.

To validate a clean checkout, run `make check`, then run the HTTP services with
configured TLS files and the worker scaffold. With Go installed, these checks
do not need PostgreSQL, RabbitMQ, or Docker.


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
