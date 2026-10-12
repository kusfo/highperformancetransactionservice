# Implementation Plan

## Status and Goals

Go is the selected implementation language. Deliver User, Account, and Transaction services that can be tested locally and deployed to cloud infrastructure. `SPEC.md` remains the behavioral source of truth. This plan proposes technical choices; Docker Compose, Kubernetes, PostgreSQL, and the data ownership model below are recommendations, not yet confirmed selections. No application implementation has started.

## Deployment Recommendation

Package each service as an OCI container image using multi-stage Docker builds. Use Docker Compose for the local environment and provide Kubernetes manifests for portable cloud deployment. Docker packages and runs containers; Kubernetes orchestrates container workloads, so they are complementary.

Local Compose should run the three services, PostgreSQL, migrations, and an HTTPS gateway with locally trusted development certificates. Credentials must use HTTPS locally as well. Document certificate setup, persistence, reset procedures, and sample requests using fictitious data.

Kubernetes deliverables should include Deployments, Services, probes, resource requests/limits, configuration and secret references, and a one-shot migration Job. Keep provider-specific ingress, TLS, storage, secret integration, and database provisioning in documented overlays. Validate the manifests on a local cluster. Images alone do not provide turnkey deployment to every cloud: each environment still needs networking, certificates, database access, and secrets configured.

Prefer managed PostgreSQL in production where available. Do not treat the single-node local database as a production high-availability configuration. Document backups, recovery objectives, connection limits, and restore verification. Running PostgreSQL inside Kubernetes is a separate operational choice.

Sources: [Docker Compose](https://docs.docker.com/compose/) and [Kubernetes overview](https://kubernetes.io/docs/concepts/overview/).

## Database Recommendation and Alternatives

| Database | Strength | Fit for this repository |
| --- | --- | --- |
| PostgreSQL — recommended | Relational constraints, transactions, row locking, and straightforward local operation. | Natural fit for committing account state, global operation identity, and transaction history together. |
| DynamoDB | Managed serverless operation and ACID transactional APIs. | Strong alternative if AWS becomes the target; creates AWS dependency and requires careful access-pattern, contention, and transaction-cost design. |
| MongoDB | Flexible documents, single-document atomic updates, and multi-document transactions. | Viable portable alternative; global operation records and separate history require transactional modeling and a replica set for local transaction testing. |
| Cassandra | Distributed, partition-oriented storage and single-partition lightweight compare-and-set transactions. | Better candidate for separately derived large-scale history workloads than the initial authoritative balance store. Global operation uniqueness and account-local atomic updates do not naturally share a partition. |

This is a workload-specific recommendation, not a claim that one database is universally faster. No throughput or latency target is specified yet. Benchmark realistic account contention before considering a different store.

Sources: [PostgreSQL locking](https://www.postgresql.org/docs/current/explicit-locking.html), [DynamoDB transactions](https://docs.aws.amazon.com/amazondynamodb/latest/developerguide/transactions.html), [MongoDB transactions](https://www.mongodb.com/docs/manual/core/transactions/), and [Cassandra architecture](https://cassandra.apache.org/doc/latest/cassandra/architecture/overview.html).

## Service and Data Boundaries

Use one Go module initially, with separate binaries under `cmd/user/`, `cmd/account/`, and `cmd/transaction/`. Organize application code under `src/`, integration tests under `tests/`, SQL migrations under `migrations/`, contracts under `api/`, and deployment assets under `deploy/`.

Recommend one authoritative PostgreSQL database for Account and Transaction data, with a shared, narrowly scoped persistence component for balance and lifecycle mutations. Both service entry points use the same database transaction rules. This intentionally couples those services' persistence to preserve the confirmed atomic closure requirement; independent databases would require reconsidering consistency guarantees. Keep user credentials in a separate logical schema with restricted access.

Model users, accounts, operations, transactions, and lifecycle request outcomes. Enforce global operation uniqueness with a database unique constraint. Store original account/amount, outcome, transaction identity, and balance-after for replay. Keep transaction history separate from mutable account state. Use integer cents, database balance constraints, and wider Go arithmetic for intermediate calculations.

## Implementation Stages

### 1. Resolve Contracts and Bootstrap

Resolve the outstanding decisions listed below before implementing affected behavior. Write OpenAPI contracts for agreed endpoints, errors, replay responses, and pagination. Add Go tooling, formatting, dependency locking, migrations, and documented development commands.

### 2. User Service

Implement registration, login, and agreed profile lifecycle behavior. Salt and hash passwords server-side. Validate bearer-token authenticity and expiry in each service. Keep credentials and tokens out of logs and error output. Select password hashing parameters, token format, signing-key handling, and revocation policy before completing authentication.

### 3. Account and Transaction Core

Implement account creation at zero, owner-scoped CRUD, history reads, and signed balance operations. Within a database transaction, coordinate global operation reservation with account locking, ownership/status checks, range checks, balance updates, and outcome/history recording. Commit insufficient-funds declines as outcomes without a balance change. Replays read the original outcome only after authorization. Use consistent lock ordering and bounded retries for database transaction failures; do not rely on process-local locks.

Implement atomic disabling and closure withdrawal, persistent closure results, and balance-neutral reactivation through the same mutation rules. Lifecycle requests need explicit replay identity so an old disable retry cannot close a reactivated account. External payout integration remains outside scope.

### 4. Correctness and Failure Testing

Run unit tests, real PostgreSQL integration tests, and HTTP end-to-end tests. Include concurrent same-ID requests, conflicting global IDs across accounts/users, distinct competing debits, overflow, retained declines, lost-response retries, ownership failures, and restart recovery. Verify closure races with credits/debits, concurrent closure, zero-balance closure, history access while disabled, reactivation, and old lifecycle retries.

Planned commands include `go test ./...`, `go test -race ./...`, and `docker compose up --build`; these are deliverables, not currently available commands. CI should build all binaries/images, apply migrations to a clean database, and run the meaningful checks.

### 5. Deployment and Performance Validation

Add readiness/liveness behavior, graceful shutdown, structured redacted logs, and metrics for latency, errors, database contention, and replay rates. Build immutable images, validate Kubernetes configuration, and exercise deploy/migrate/smoke-test procedures locally. Document production TLS, secrets, database backup/restore, and rollout compatibility.

Benchmark distributed-account and hot-account workloads separately, recording throughput and percentile latency. Limit connection pools and request concurrency to database capacity. Scale stateless service replicas only after measuring database contention; extra replicas cannot eliminate serialization required on one account.

## Decisions Needed Before Affected Implementation

- Operation/outcome retention and behavior after expiry; whether invalid, unauthorized, overflow, and disabled-account declines reserve IDs.
- Lifecycle request identity and replay contracts across repeated disable/reactivate cycles.
- Signed 32-bit amount interpretation, successful transaction responses/IDs, pending duplicate behavior, and operation lookup.
- Account metadata, shared ownership, pagination, email normalization/uniqueness, and user deletion effects.
- Token expiry, refresh/revocation, credential changes, and password recovery.
- Initial performance targets, cloud target for the first deployment, and backup/recovery objectives.

These decisions do not prevent preparing the repository and local environment, but must not be silently invented in implementation.
