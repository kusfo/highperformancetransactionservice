# T05 Financial Persistence and Concurrency Design

Status: implementation design for the confirmed T03/T04 contracts. This document
selects storage/locking rules. T17 implements the account table/repository (see
[account persistence](account-persistence.md)); remaining financial tables and
endpoints are future tasks.
SPEC.md and the OpenAPI contracts govern externally visible behavior.

## Ownership and consistency boundary

Account and Transaction use one authoritative PostgreSQL database and a shared
`src/financial` application/persistence component. Account owns metadata and
lifecycle/history HTTP routes; Transaction owns submission and operation lookup.
Both call the shared financial query layer. Route ownership at the gateway
must distinguish GET history from POST submission on the shared transactions path.
The worker is another process in the Transaction domain, not another operation
store. Only this financial component changes balances, outcomes, sequences or
closure state; handlers must not issue ad hoc balance SQL. No network call or
broker publication occurs inside a financial database transaction.

Identity remains separate. HTTP entry points validate sessions/user activity,
then pass a trusted user ID; financial transactions recheck account ownership.
No identity row lock is carried into a financial transaction. Workers use the
accepted user identity without checking later token expiry/revocation/deactivation.
Use separate database roles: identity owns credentials/sessions; financial roles
receive column-level SELECT on identity.sessions (token_hash, user_id, issued_at,
expires_at, revoked_at) and identity.users (user_id, active), matching the existing
session checker. Do not grant table-wide SELECT. Deny password-hash
access and identity writes. Workers need no identity access. Role/grant enforcement
and its negative tests are implemented by T17-A; see [database roles](database-roles.md). Future table grants must extend that tested matrix before deploying their features.

Account owners are immutable UUIDs. Do not introduce a cross-schema user foreign
key solely for authentication: a trusted creation identity supplies existence,
users are retained, and financial history must remain usable independently of
identity lifecycle writes. Document this dependency and deny direct application
owner changes. Operator maintenance must preserve retained identities/history.

## Logical records and constraints

Names below are implementation targets; T17/T21 supply append-only migrations.

| Record | Required state and constraints |
| --- | --- |
| `financial.accounts` | Internal UUID primary key; unique immutable opaque account number; immutable owner UUID; metadata; integer balance checked 0..2147483647; active/disabled status; creation time; generation; next_sequence; head_sequence; retry_after; last closure reference. New accounts: active, balance 0, generation 1, next/head 1. Disabled implies balance 0. |
| `financial.operations` | Global UUID operation primary key; immutable account/owner/amount/source/acceptedAt/generation; user-operation sequence; stored pending/succeeded/declined state and terminal fields. UNIQUE(account_id, sequence) for user operations. Source closure has no acceptance sequence and is inserted terminal. |
| `financial.transactions` | Global UUID transaction primary key; unique operation FK; account, amount, balance_after, source and creation time. Exactly one per successful operation, none for declines. Append-only. |
| `financial.closures` | UNIQUE(account_id, generation); immutable closed_at, first unresolved sequence and acceptance upper bound; frozen ACCOUNT_DISABLED error payload; nullable withdrawal operation/transaction IDs. Zero-balance closure has neither ID. Retain permanently. |
| `financial.lifecycle_results` | Global lifecycle UUID primary key, distinct namespace from operation IDs; immutable owner/account/action plus full response snapshot. Retain successful no-ops as well as state changes. |
| `financial.outbox` | Event UUID primary key; unique accepted user operation FK; immutable operation/account reference; publication state, lease token/deadline, attempts and next retry. No token or password. |

Use UUID-typed keys/parameters, not casts on indexed columns. Use bigint for
sequences/generations and wider Go arithmetic for balance calculations. Detect
sequence/generation exhaustion before mutation; never wrap or reset a lifetime
sequence. Enforce 1 <= head_sequence <= next_sequence and generation >= 1.
Schema checks enforce nonzero symmetric amount range, valid source/state
combinations, pending-without-terminal-fields and terminal field requirements.
Use matching account/operation composite relationships where needed to prevent
history from referencing an operation for another account. Multirow consistency
(balance + history + outcome) is enforced by the shared transaction paths, not
claimed as a property of a simple CHECK constraint.

Index account number and (owner_id, created_at, account_number) for reads;
(account_id, sequence) for head selection; (account_id, generation) for boundaries;
(account_id, created_at, transaction_id) for history; and due active account /
outbox work predicates for recovery. Define a predictable bytewise ordering for
opaque account-number tie-breakers across SQL and cursor code. EXPLAIN on populated
fixtures is part of the schema/repository tasks. Do not add a global financial
mutex or a globally locked backlog counter; T32 must use bounded admission that
does not reverse this lock order, and must specify whether limits are exact.

## Lock order and isolation

Use READ COMMITTED, explicit row locks and short bounded transactions. A plain
read before a lock is advisory only: re-read protected state after acquiring the
lock. A single mutating transaction targets one account; transfers are out of
scope. Never retain a lock across a network wait, hashing, sleep or broker call.

Acquire locks in this order:

1. Transaction-scoped advisory locks for all request identity keys needed by the
   transaction, sorted by their final signed 64-bit lock value and deduplicated.
2. Target account row `FOR UPDATE`.
3. Existing operation rows in sequence order, then boundary/history/lifecycle
   inserts and finally outbox inserts/updates needed by that mutation.

Derive an advisory value deterministically from SHA-256 of a namespace tag plus
canonical UUID bytes (fixed, versioned encoding; use the first 8 bytes as signed
big-endian int64). Reserve the encoding in one shared module for every process.
Hash collisions cause extra serialization, never shared identity: primary-key
constraints and full UUID/payload comparisons remain authoritative. Use only
`pg_advisory_xact_lock`, never session-level locks. Acquire the whole sorted key
set before account locking; if additional keys are discovered, roll back and
restart with the full set. Do not acquire request-key locks in worker processing.

Intake locks its operation ID. Reactivation locks its lifecycle ID. Disable
preallocates a candidate withdrawal operation UUID and locks both its lifecycle
key and candidate operation key, even if the balance later proves zero or the
request replays. A used candidate UUID causes rollback and bounded regeneration,
not reuse of another operation. Transaction UUID collisions likewise roll back
and retry. Request advisory locks serialize duplicates before account locking;
they do not replace database uniqueness or atomically commit external state.

All processing/materialization paths take the account lock before operation locks.
They never acquire request-key locks afterward. Metadata PATCH also takes the
account lock. Read-only lookup needs no mutation locks. Outbox-only relay leases
are separate transactions that never lock accounts or operations. Do not join and
lock outbox + account in one relay claim. Future batch tools must follow the same
order or process one account per transaction.

## Durable acceptance

1. At HTTP entry authenticate, parse/validate input and check target ownership
   without locking. This preliminary check prevents leaking global ID conflicts.
2. Begin transaction, acquire operation advisory key, lock target account and
   recheck ownership. Read the operation ID with a fresh statement snapshot.
3. Existing matching account/owner/amount: resolve its effective outcome and
   return a replay, before checking disabled state or new-work admission. Other
   bindings: generic OPERATION_ID_CONFLICT; never expose stored payloads.
4. For a new ID, check active state and admission. Rejections roll back and reserve
   nothing. Allocate the current next_sequence and increment it transactionally.
5. Insert pending operation (generation and trusted submitting owner included)
   and one outbox event. Commit all changes, then return 202 and Location.

A rolled-back admission consumes neither ID nor sequence. Account locking makes
same-account committed acceptances a total order: a second transaction cannot
allocate until the first commits or rolls back. acceptedAt is captured after
locking but is not used to derive ordering. Cross-account same-ID attempts share
the request-key lock: the loser sees the winning committed binding or proceeds
if it rolled back. Every path still handles unique-constraint collisions safely.

## Ordered application and recovery

The authoritative scheduler is persisted account state: an active account has
ready work when head_sequence < next_sequence and retry_after is due. A message
is a wake-up hint; its amount or delivery position is never authoritative.

Discover candidate accounts without operation locks. A worker locks one account
(`FOR UPDATE SKIP LOCKED` may skip busy accounts), then fetches exactly its head
operation. Never skip an operation within an account. A missing head row in an
active generation is an invariant violation: alert and stop that account rather
than jumping over it. Validate ownership binding and immutable payload consistency,
not current user/token activity. An already terminal head may advance safely.

For pending active-generation work, calculate with int64 against locked balance.
Within one transaction either update balance + insert transaction + store success,
or store INSUFFICIENT_FUNDS/BALANCE_OVERFLOW with no balance/history change.
Advance head and clear retry state in the same commit. Process bounded batches
(one operation per transaction initially) for fairness. No other worker can apply
a competing operation for that account during this transaction.

Transient failures roll back the entire processing transaction. Persist bounded
backoff only in a new account-first transaction that confirms the same head and
generation are still pending; a stale failure must not delay newer work after
closure/reactivation. If persisting retry state fails, broker backoff and the
periodic scanner retry; safety does not depend on the retry timestamp.

Periodic recovery scans the persisted ready-account predicate independently of
RabbitMQ, including after restarts. Thus lost/out-of-order notifications cannot
strand a head. If a message names a later operation, process the head first and
reschedule/nack the still-pending message with bounded delay; never apply its
payload out of order. Ack a message only after its operation is durably terminal
(including an effective closure decline). Commit-before-ack crashes cause harmless
redelivery. Infrastructure or poison-message handling does not invent a terminal
business decline; alert/quarantine the notification and recover by database scan.

## Closure boundary and effective outcomes

Choose generation-boundary records rather than an unbounded UPDATE of every
pending operation during close. Generation starts at 1; disabling closes the
current generation. Only disabled-to-active reactivation increments generation;
already-active reactivation is a no-op. Lifetime sequences never reset.

Under the request locks and account lock, replay an existing lifecycle key first.
For a new disable on an active account, capture one closed_at timestamp and the
range [head_sequence, next_sequence). Insert the immutable closure boundary.
If balance > 0, insert a succeeded closure operation and its transaction for
minus the locked balance, with balanceAfter 0; these have no user acceptance
sequence and no processing outbox event. Set balance 0 and status disabled,
set head_sequence = next_sequence, clear retry state and set last closure.
Insert the complete lifecycle snapshot and commit everything together. New-key
close while already disabled stores a no-op snapshot referencing the same latest
withdrawal; zero-balance closures never manufacture an operation.

Resolve public outcomes from one SQL statement snapshot using operation plus its
matching closure boundary:

- Stored terminal state wins and is immutable.
- Stored pending in a closed generation and within the boundary range is
  effectively declined using the boundary's frozen ACCOUNT_DISABLED code/message/
  httpStatus payload; completedAt is boundary.closed_at.
- Otherwise stored pending remains pending.

A pending closed-generation row outside the recorded range is a corruption signal,
not permission to execute. The same resolver is mandatory for GET, POST replay,
worker duplicate handling and recovery; a raw operations-table status is not a
public API. A read overlapping closure may linearize before its commit. A read
whose statement begins after closure commits must expose the effective decline,
even if closure is followed by reactivation before that read.

A background materializer can persist these derived declines in small batches,
account-first, using exactly the same completedAt/code/message. It must never
change an effective response, even after an application message-text update. Old generations must not block the new active head.
Outbox records for those operations may still publish: their messages resolve
terminal and are acknowledged without balance effects. Backlog metrics/admission
must count effective active work, not every physical pending row. Boundary,
operation, result and transaction retention is indefinite.

Reactivation locks its lifecycle key then account, replays known outcomes, and
otherwise changes disabled to active while incrementing generation. Balance stays
zero; head equals next. Already-active no-ops preserve balance and generation.
Both store complete response snapshots. Reusing a key for another action/account
conflicts; an old disable/reactivate retry returns its historical snapshot without
mutating state. Metadata edits do not rewrite previous snapshots.

## Outbox delivery and failures

Claim due outbox rows using short transactions and expiring leases; increment a
lease token on each claim. Publish outside SQL transactions, wait for publisher
confirmation, then mark sent only if the lease token still matches. Failed or
expired claims become retryable. A stale publisher cannot finalize a newer lease;
a crash after publish can still duplicate delivery, which processing tolerates.
Outbox publication never grants acceptance: acceptance is the earlier SQL commit.
Broker unavailability leaves accepted work durable and replayable.

Use bounded deadlines and retry deadlocks/serialization failures only after a
complete rollback, with the original client identity/payload. A retry exhausted
before acceptance returns a safe error; a connection loss during COMMIT is
ambiguous. Reconcile acceptance/lifecycle mutations by their request IDs and
processing by its effective outcome on a fresh connection. Never repeat a balance
write merely because acknowledgment or commit response was lost. Consistency
requires durable PostgreSQL commits, not asynchronous-commit success reporting;
production replication/failover durability is an operational configuration gate.

## Concurrency walkthrough and later proof obligations

| Interleaving | Required result / commit boundary | Later proof |
| --- | --- | --- |
| Same operation ID, same account, two intake requests | Request-key wait; one operation/outbox/sequence; second reads pending or terminal after winner commits. Rollback lets the waiter accept. | T23: observe wait and inject rollback/commit-response loss. |
| Same ID, different owned accounts or users | Global request-key wait and uniqueness; one binding wins, other returns generic conflict without original details. | T23/T24: separate connections and cross-owner probes. |
| Deposit seq 7, debit seq 8; message 8 arrives first | Worker reads head 7, commits its outcome, then evaluates 8. Transient failure of 7 cannot advance 8. | T26/T27/T30: reversed delivery and failed head. |
| Competing workers / duplicate message | Account lock serializes evaluation; outcome/history/balance/head commit together. Redelivery sees terminal. | T26/T30: observed waits and crash before/after commit. |
| Intake vs close | Intake first: operation belongs to closed generation and declines if unapplied. Close first: new intake rejects without reserving ID. | T23/T33: both observed lock orders. |
| Worker vs close | Worker commit first affects withdrawal. Close commit first derives decline and prevents later application. | T33/T36: credit/debit races, rollback and overflow boundaries. |
| Two closes, same or distinct keys | One boundary/withdrawal; same-key replay or distinct-key no-op records reuse original closure IDs. | T33: zero and positive balances, concurrent callers. |
| Close then reactivate, delayed worker | Boundary remains durable; old pending work declines, new generation accepts at lifetime tail. No fund revival. | T34/T36: leave physical rows pending across reactivation. |
| Reactivate then fresh close; replay old lifecycle keys | Lock order decides fresh requests; old keys only replay historical snapshots. | T34/T35: reused/conflicting keys and old active/disabled snapshots. |
| Profile metadata edits while disabled | Current metadata changes; stored lifecycle snapshots and withdrawal IDs do not. | T20/T35: independent reads and replay comparison. |
| Relay crash / stale lease / missing notification | Duplicate publication is safe; persisted head scanner restores progress even without broker notification. | T29–T31: lease fencing, recovery and dead-letter cases. |
| Token expiry/deactivation after acceptance | HTTP retries may reject; worker still processes unless closure/balance rules decline. | T38: accepted identity survives auth lifecycle changes. |

These walkthroughs establish the intended serialization points, not an empirical
proof of runtime correctness. No financial SQL migration or production worker is
added by T05. Before each later task is complete, use real PostgreSQL fixtures,
observed lock overlap, fault injection and post-commit reads for its listed proof.

## PostgreSQL references

The READ COMMITTED statement-snapshot model requires re-reading after lock waits;
transaction advisory locks release automatically and consistent lock ordering
reduces deadlock risk. SKIP LOCKED is suitable for discovering queue work, not for
claiming ordered operation results without the account head check. References:
[transaction isolation](https://www.postgresql.org/docs/17/transaction-iso.html),
[explicit/advisory locking](https://www.postgresql.org/docs/17/explicit-locking.html),
[SELECT and SKIP LOCKED](https://www.postgresql.org/docs/17/sql-select.html).
