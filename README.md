# HIGH PERFORMANCE TRANSACTION SERVICE
## INTRODUCTION
My take on experimentic with Agentic Spec Driven Development using Codex from OpenAi.

## Local development

Install Go 1.26 or newer and GNU Make. This module currently uses only the Go
standard library, so no `go.sum` is needed yet. Third-party dependencies will
introduce a committed `go.sum` alongside `go.mod`.

From the repository root:

| Command | Purpose |
| --- | --- |
| `make build` | Build all four processes into `bin/`. |
| `make fmt` | Apply Go's standard formatting. |
| `make fmt-check` | Detect formatting changes; applies formatting and fails if files changed. |
| `make test` | Run `go test ./...`. |
| `make vet` | Run Go's static checks. |
| `make check` | Run formatting, static checks, tests, and builds. |
| `make clean` | Remove the four generated binaries. |

Build one process with `make bin/user` (or `bin/account`, `bin/transaction`,
`bin/transaction-worker`). Run a scaffold with `./bin/user`, or use
`go run ./cmd/user` directly. Each scaffold prints its name and exits; HTTP
servers and queue consumers are later tasks. There are no test cases yet;
`go test ./...` currently checks that packages compile.

The entry points live under `cmd/`. Application code will live under `src/`,
integration tests under `tests/`, contracts under `api/`, and deployment assets
under `deploy/` as their tasks are implemented. `SPEC.md`, `PLAN.md`, and
`TASKS.md` record requirements, implementation direction, and progress.

To validate a clean checkout, run `make check`, then execute each binary in
`bin/`. PostgreSQL, RabbitMQ, and Docker are not needed for these skeletons.
