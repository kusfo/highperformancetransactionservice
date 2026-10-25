# Implementation Plan

## Status and Goals

Go is the selected implementation language. Deliver User, Account, and Transaction services plus a manual-testing frontend that can be tested locally and deployed to cloud infrastructure. `SPEC.md` remains the behavioral source of truth. Confirmed stack: Go, PostgreSQL, RabbitMQ, Docker images, Docker Compose locally, and Kubernetes deployment assets. Detailed implementation choices, including the shared Account/Transaction persistence design, follow the recommendations below. No application implementation has started.

## Deployment Strategy

Package each service as an OCI container image using multi-stage Docker builds. Use Docker Compose for the local environment and provide Kubernetes manifests for portable cloud deployment. Docker packages and runs containers; Kubernetes orchestrates container workloads, so they are complementary.

Local Compose should run the three services, PostgreSQL, migrations, and an HTTPS gateway with locally trusted development certificates. Credentials must use HTTPS locally as well. Document certificate setup, persistence, reset procedures, and sample requests using fictitious data.

Kubernetes deliverables should include Deployments, Services, probes, resource requests/limits, configuration and secret references, and a one-shot migration Job. Keep provider-specific ingress, TLS, storage, secret integration, and database provisioning in documented overlays. Validate the manifests on a local cluster. Images alone do not provide turnkey deployment to every cloud: each environment still needs networking, certificates, database access, and secrets configured.

Prefer managed PostgreSQL in production where available. Do not treat the single-node local database as a production high-availability configuration. Document backups, recovery objectives, connection limits, and restore verification. Running PostgreSQL inside Kubernetes is a separate operational choice.

Sources: [Docker Compose](https://docs.docker.com/compose/) and [Kubernetes overview](https://kubernetes.io/docs/concepts/overview/).

## Database Recommendation and Alternatives

| Database | Strength | Fit for this repository |
| --- | --- | --- |
| PostgreSQL — selected | Relational constraints, transactions, row locking, and straightforward local operation. | Natural fit for committing account state, global operation identity, and transaction history together. |
| DynamoDB | Managed serverless operation and ACID transactional APIs. | Strong alternative if AWS becomes the target; creates AWS dependency and requires careful access-pattern, contention, and transaction-cost design. |
| MongoDB | Flexible documents, single-document atomic updates, and multi-document transactions. | Viable portable alternative; global operation records and separate history require transactional modeling and a replica set for local transaction testing. |
| Cassandra | Distributed, partition-oriented storage and single-partition lightweight compare-and-set transactions. | Better candidate for separately derived large-scale history workloads than the initial authoritative balance store. Global operation uniqueness and account-local atomic updates do not naturally share a partition. |

This is a workload-specific recommendation, not a claim that one database is universally faster. No throughput or latency target is specified yet. Benchmark realistic account contention before considering a different store.

Sources: [PostgreSQL locking](https://www.postgresql.org/docs/current/explicit-locking.html), [DynamoDB transactions](https://docs.aws.amazon.com/amazondynamodb/latest/developerguide/transactions.html), [MongoDB transactions](https://www.mongodb.com/docs/manual/core/transactions/), and [Cassandra architecture](https://cassandra.apache.org/doc/latest/cassandra/architecture/overview.html).

## Service and Data Boundaries

Use one Go module initially, with separate binaries under `cmd/user/`, `cmd/account/`, and `cmd/transaction/`. Organize application code under `src/`, integration tests under `tests/`, SQL migrations under `migrations/`, contracts under `api/`, and deployment assets under `deploy/`.

Recommend one authoritative PostgreSQL database for Account and Transaction data, with a shared, narrowly scoped persistence component for balance and lifecycle mutations. Both service entry points use the same database transaction rules. This intentionally couples those services' persistence to preserve the confirmed atomic closure requirement; independent databases would require reconsidering consistency guarantees. Keep user credentials in a separate logical schema with restricted access.

Model users, accounts, operations, transactions, and lifecycle request outcomes. Enforce global operation uniqueness with a database unique constraint. Store original account/amount, outcome, transaction identity, and balance-after for replay. Keep transaction history separate from mutable account state. Use integer cents, database balance constraints, and wider Go arithmetic for intermediate calculations.

## Asynchronous Transaction Processing

Separate HTTP intake and queue processing into independently deployable Go processes within the Transaction service, sharing its domain rules and authoritative operation store. Add `cmd/transaction-worker/`; do not create an independent idempotency database for the worker. The asynchronous API is confirmed.

### Durable Acceptance and Result Retrieval

1. Intake authenticates, validates the payload, checks account ownership/status, and atomically inserts a globally unique operation record with status `pending` plus an outbox event in PostgreSQL. It does not reserve funds or decide sufficient funds at acceptance.
2. Return `202 Accepted` only after that database commit, with `operationId`, `status`, and `Location: /operations/{operationId}`. This means durable acceptance, not a successful transaction or confirmed publication to the broker. No `transactionId` exists yet.
3. An identical duplicate returns the same operation's current state without creating more work; conflicting account/amount reuse returns `409`. Authorization applies to every replay and status lookup.
4. `GET /operations/{operationId}` returns `200` with `pending`, `succeeded`, or `declined`. Success includes the original transaction ID and balance-after; decline includes the stable business error code. A successful GET is not evidence of a successful balance operation. Polling is the initial recommendation; notifications can be added later.

The confirmed asynchronous submission contract replaces the earlier immediate `201` success contract; it does not silently change existing synchronous error mappings. Invalid/authentication/conflict failures still occur at intake, while insufficient-funds/overflow declines after acceptance are represented in the operation resource.

### Queue-Safe Idempotency

Use RabbitMQ as the selected portable broker, locally and in cloud deployments. The database remains authoritative; broker deduplication is not the correctness mechanism.

An outbox relay publishes committed events with publisher confirms, then records publication. A crash between publication and recording can republish an event. Use durable replicated queues and persistent messages in production, with appropriate local configuration. Each message identifies the operation; workers read its immutable payload from the database rather than trusting queued amounts or passing access tokens through the broker.

Workers lock/coordinate the operation and account rows using consistent lock ordering. They atomically commit the account change, transaction record, and terminal operation outcome, or commit a business decline without changing the balance. A terminal operation is never executed again. Acknowledge the message only after commit: if the worker dies after commit but before acknowledgment, redelivery reads the terminal result and causes no further debit. Infrastructure failures trigger bounded backoff/redelivery, not an insufficient-funds or overflow decline. Dead-letter handling must retain the unresolved operation and support safe recovery; it must not silently drop accepted work. Monitor pending age and reconcile stranded operations/outbox entries.

This delivers at-most-once balance effects over at-least-once messaging; it does not promise exactly-once message delivery. Retain accepted operation IDs and terminal outcomes indefinitely for the initial version, including every business decline. Invalid or unauthorized requests do not reserve IDs.

### Ordering, Account Lifecycle, and Capacity

Confirmed: execute balance operations in durable acceptance order within each account. This ensures a previously accepted deposit is processed before a later withdrawal that depends on those funds. No global ordering across accounts is required. Recommend assigning a monotonically increasing per-account sequence during the durable acceptance transaction, serialized against other intake and account lifecycle mutations. Acceptance order is the committed database order, not client timestamps or HTTP arrival time; concurrent submissions are ordered by that serialization. Duplicate submissions retain the original sequence.

Workers must process the earliest unresolved operation for an account before later operations, regardless of broker delivery order, redelivery, or worker scheduling. A transient infrastructure failure blocks later operations on that account until resolved; a terminal business decline permits advancement. Durable scheduling/reconciliation must recover gaps and out-of-order notifications. Account locks alone guarantee safe balances, not acceptance order. Funds are evaluated against the balance after earlier operations have reached their terminal outcomes. More workers help across accounts but cannot remove contention on one account.

Confirmed: pending operations on a closed account must decline, rather than being drained before closure. Closure is a lifecycle boundary that overrides pending balance-operation ordering. Processing and closure use the same account lock, so an applied operation either commits before the closure withdrawal or never changes the balance.

Recommend recording an account lifecycle generation with accepted operations and retaining a durable closure boundary. Operations still pending at that boundary receive a stable `ACCOUNT_DISABLED` decline and must never execute after reactivation, even if workers record the decline later. The closure commit must make that rejection durable; status lookup must not present affected work as executable after closure. Completed operations retain their original outcomes. New operations after reactivation belong to the new generation and begin from the zero balance. The exact persistence mechanism is a recommendation, not a newly confirmed implementation choice.

Persist the authenticated submitting identity at acceptance. Validate authentication, token expiry/revocation, user active status, and ownership at HTTP entry points, including retries and status lookups. Workers do not revalidate tokens or user active status: token expiry, manual revocation, and user deactivation must not cancel accepted work. Workers still enforce balance, ordering, and account lifecycle rules. Never pass bearer tokens through the broker.

Set bounded worker concurrency/prefetch and queue/backlog admission limits. Return `429` or `503` for new work rejected before durable acceptance; retries of already accepted IDs still retrieve their state. Queues absorb bursts and decouple latency but do not increase sustainable database write capacity. Measure backlog growth, processing latency, oldest pending age, broker redeliveries, and account contention.

Add tests for intake/relay/worker crashes at each commit boundary, broker downtime, duplicate delivery, pending retries, poison messages, overload, closure while queued, and reactivation with old pending work. Verify deposit-before-withdrawal acceptance order, out-of-order delivery, same-account worker races, sequence gaps, and earlier-operation failures blocking later work. Include broker and worker in Compose and Kubernetes assets as part of the confirmed asynchronous design.

Sources: [HTTP 202 semantics](https://www.rfc-editor.org/rfc/rfc9110.html#section-15.3.3), [transactional outbox](https://docs.aws.amazon.com/prescriptive-guidance/latest/cloud-design-patterns/transactional-outbox.html), and [RabbitMQ reliability](https://www.rabbitmq.com/docs/reliability).

## Implementation Stages

### 1. Resolve Contracts and Bootstrap

Resolve the outstanding decisions listed below before implementing affected behavior. Write OpenAPI contracts for agreed endpoints, errors, replay responses, and pagination. Add Go tooling, formatting, dependency locking, migrations, and documented development commands.

### 2. User Service

Implement registration, login, and agreed profile lifecycle behavior. Salt and hash passwords server-side. Validate bearer-token authenticity and expiry in each service. Keep credentials and tokens out of logs and error output. Use unique emails, user deactivation with retained transaction history, 10-minute access tokens, and documented manual revocation/re-enablement. Superuser UI/API, recovery, and email verification are outside scope. T09 uses Argon2id version 19 with 64 MiB memory, three iterations, four lanes, 16-byte random salts and 32-byte derived keys; see `docs/password-hashing.md`. T02 selects opaque tokens with stored hashes; complete the remaining authentication integration in the session tasks.

### 3. Account and Transaction Core

Implement account creation at zero, owner-scoped CRUD, history reads, and signed balance operations. Within a database transaction, coordinate global operation reservation with account locking, ownership/status checks, range checks, balance updates, and outcome/history recording. Commit insufficient-funds declines as outcomes without a balance change. Replays read the original outcome only after authorization. Use consistent lock ordering and bounded retries for database transaction failures; do not rely on process-local locks.

Implement atomic disabling and closure withdrawal, persistent closure results, and balance-neutral reactivation through the same mutation rules. Require client-generated lifecycle request IDs and retained outcomes so an old disable retry cannot close a reactivated account. Accounts have one owner and editable `displayName`/`description` metadata. Use the symmetric nonzero amount range ±2,147,483,647 cents. External payout integration remains outside scope.

### 4. Correctness and Failure Testing

Run unit tests, real PostgreSQL integration tests, and HTTP end-to-end tests. Verify that queued operations continue after token expiry, manual revocation, and user deactivation, while new HTTP requests are rejected. Include concurrent same-ID requests, conflicting global IDs across accounts/users, distinct competing debits, overflow, retained declines, lost-response retries, ownership failures, and restart recovery. Verify closure races with credits/debits, concurrent closure, zero-balance closure, history access while disabled, reactivation, and old lifecycle retries.

Planned commands include `go test ./...`, `go test -race ./...`, and `docker compose up --build`; these are deliverables, not currently available commands. CI should build all binaries/images, apply migrations to a clean database, and run the meaningful checks.

### 5. Manual-Testing Frontend

Deliver a browser UI for exercising the services manually. The frontend is in scope; framework selection remains open. Keep the UI small and focused on service behavior.

- Register and log in a user; show actionable validation and authentication errors.
- List only the logged-in user's accounts, with account number, current balance formatted in virtual currency units, and active/disabled status.
- Create an account with zero balance, close an account, and show the automatic closure withdrawal and resulting disabled state. Include reactivation without a balance change.
- Deposit or withdraw a positive amount entered in whole units/cents, converting it exactly to signed integer cents for the API. Reject zero, invalid precision, and out-of-range input without floating-point rounding.
- Generate a new `operationId` for each explicit user submission. Automatic retries reuse that ID and the same payload. Prevent accidental duplicate submissions while preserving visibility of pending operations.
- Show pending, successful, and declined operation outcomes, poll the authorized status endpoint, and refresh the current balance/history after terminal outcomes. Preserve pending operation IDs across page reloads without storing credentials in them. A recorded balance-after belongs to that operation and must not replace a newer current balance.
- List account transactions, including closure withdrawals and history for disabled accounts. Show insufficient-funds, overflow, and disabled-account errors clearly.

Serve the frontend through the HTTPS gateway locally and in cloud deployments, preferably on the same origin as routed API calls. Add its build/container assets to Compose and Kubernetes deployment assets. Decide browser token storage/session handling explicitly; never log passwords or tokens, and use fictitious users and funds in manual tests.

Document a manual walkthrough: register/login, create an account, deposit, withdraw, inspect declines and pending outcomes, close with a remaining balance, inspect history, and reactivate. Add browser end-to-end coverage for the principal flows once the UI exists, including authorization isolation and automatic retry identity. No frontend code is introduced by this plan update.

### 6. Deployment and Performance Validation

Add readiness/liveness behavior, graceful shutdown, structured redacted logs, and metrics for latency, errors, database contention, and replay rates. Build immutable images, validate Kubernetes configuration, and exercise deploy/migrate/smoke-test procedures locally. Document production TLS, secrets, database backup/restore, and rollout compatibility.

Benchmark distributed-account and hot-account workloads separately, recording throughput and percentile latency. Limit connection pools and request concurrency to database capacity. Scale stateless service replicas only after measuring database contention; extra replicas cannot eliminate serialization required on one account.

## Remaining Implementation Decisions

- Lifecycle request-ID field/header, pagination, optional individual transaction lookup, and nonaccepted disabled-account attempt identity.
- Frontend framework, browser token/session handling, and manual revocation/re-enablement procedures. Use opaque tokens with stored hashes, no refresh, current-session logout, and permanent session revocation on password change/deactivation.
- Overload thresholds, first cloud environment, and backup/recovery objectives.

Performance targets will be refined once expected traffic is established. Build repeatable benchmarks and report measured baselines without claiming an unagreed throughput target. Task creation can proceed with these remaining details captured as contract/design tasks before their dependent implementation.
