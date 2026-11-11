# T04 Asynchronous Operation Contract

Status: confirmed. `api/operation.openapi.json` defines submission and lookup.
`api/account.openapi.json` owns transaction-history GET on the shared account
transactions path. These artifacts describe behavior; financial endpoints and
workers are not yet implemented.

## Submission and authorization

POST `/accounts/{accountNumber}/transactions` accepts exactly `operationId` (UUID)
and `amount` (nonzero signed integer cents, -2147483647 through 2147483647).
Reject missing/null/unknown/duplicate fields, malformed Unicode and noninteger
JSON tokens, including decimal and exponent notation. Normalize valid UUID
spelling to its UUID value. Treat account numbers as opaque strings. There is no
caller-supplied owner, currency, source, sequence, transactionId or status.
Reject an Idempotency-Key header here: the body operationId is the sole identity.
Lifecycle IDs are a separate namespace.

Authenticate and validate inputs, authorize the target account, then look up the
global operation ID. Missing/inaccessible targets share 404 NOT_FOUND. An ID
bound to a different account, amount or owner returns generic 409
OPERATION_ID_CONFLICT without revealing stored payloads or outcomes. A caller
owning two accounts still cannot reuse one operation ID across both accounts.

An identical accepted operation replays before current disabled-state or intake
quota/backlog checks. Pending replays return 202; terminal replays return 200.
Authorization still applies, including expired/revoked tokens and inactive users.
A new admission returns 202 only after durable recording; Location identifies
`/operations/{operationId}`. Both 202 and terminal replay 200 include Location.
The initial response is a pending snapshot even if processing completes before
the client receives it. Acceptance neither reserves funds nor guarantees success.

## Rejections, overload and ambiguous outcomes

A new request on a disabled account returns 409 ACCOUNT_DISABLED. Caller-quota
rejection returns 429 RATE_LIMITED; global backlog/capacity rejection returns
503 SERVICE_UNAVAILABLE. Overload responses include Retry-After as positive
integer seconds. Exact thresholds and maximum backoff are deferred to T32.

These explicit preacceptance rejections reserve no ID. Retrying the same ID and
payload after reactivation or capacity recovery can create a new acceptance at
that later position. A new user interaction generates a fresh ID. Invalid and
unauthorized requests also reserve nothing. Conflicts never modify the record
that already owns the ID. Rejections contain only code and message, not an
operation resource or transaction identity.

Existing accepted IDs bypass new-work admission controls while storage is
available. This is not an availability guarantee: connection failures, unavailable
storage or a lost response can prevent replay/lookup. A timeout or generic 5xx
never proves that acceptance failed; retry the same ID and payload. A 503 emitted
by an intermediary can also be ambiguous. Do not create a new ID to recover from
an uncertain response. Client retries should use bounded exponential backoff
with jitter and honor Retry-After; a client timeout does not cancel accepted work.
Broker outages after acceptance leave the operation pending and recoverable.

## Operation resources

All outcomes include operationId, accountNumber, signed amount, source
(`user`/`closure`), acceptedAt (UTC timestamp) and status. The source is assigned
by the service. acceptedAt is informational; per-account durable acceptance order
is authoritative, not timestamp comparison. No internal scheduling sequence or
lifecycle generation is exposed.

- `pending`: no transactionId, balanceAfter, completedAt or error.
- `succeeded`: transactionId, original balanceAfter and completedAt; no error.
- `declined`: completedAt and error `{code,message,httpStatus}`; no transactionId
  or balanceAfter. INSUFFICIENT_FUNDS and BALANCE_OVERFLOW carry metadata 422;
  ACCOUNT_DISABLED after closure carries metadata 409.

GET `/operations/{operationId}` returns HTTP 200 for any authorized existing
state, including declines. Unknown/inaccessible IDs share 404. Malformed UUIDs
return 400. Responses use Cache-Control: no-store. Polling has no completion SLA;
clients poll conservatively with backoff. Pending GET does not return HTTP 202.
Terminal outcomes, timestamps and IDs are immutable and retained indefinitely.
Their balanceAfter is historical, not the account's current balance.

A positive-balance closure creates an internally generated succeeded operation,
queryable through the same owner-authorized endpoint. Its amount is negative and
balanceAfter zero. It has no externally observable pending state because closure
is atomic. Zero-balance closure creates no operation. An owner replaying a
matching closure operation ID/account/amount receives the same terminal resource,
without converting it into a user operation or creating new work.

## Ordering and lifecycle examples

Accepted user operations reach outcomes in account-local acceptance order.
Infrastructure failures leave work pending and block later work on that account;
they are not terminal financial declines. A terminal business decline permits
advancement. Dead-lettering must not discard accepted work or invent a business
outcome. No ordering across accounts is promised.

Closure overrides unresolved user-operation ordering. Operations pending at the
closure boundary become durably declined ACCOUNT_DISABLED, even if physical
materialization happens later; a post-closure lookup must expose the decline.
completedAt for that decline is the closure boundary time, not a later worker
retry time. Reactivation must never revive it. T05 defines the persistence design.
Previously completed outcomes replay unchanged after closure/reactivation.
Workers do not recheck tokens or user active state after acceptance.

The contract tests compare pending retries, success replay after balance changes,
insufficient-funds replay after a deposit, closure decline after reactivation,
and internal closure results. They also check distinct intake HTTP errors versus
HTTP-200 business outcomes. These are schema/example checks, not runtime proofs
of ordering, idempotency, atomicity or authorization; later implementation tasks
must exercise those properties against real persistence and workers.
