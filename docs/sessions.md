# Session persistence and issuance

T11 adds `identity.sessions` through `000002_sessions.sql` and the `src/sessions`
repository. Run migrations explicitly with `make migrate`. Login, logout HTTP
handlers and bearer middleware remain T12/T13 and subsequent lifecycle tasks.

## Credential format and lifetime

Issue 32 random bytes using `crypto/rand`, encoded as 43 characters of unpadded
URL-safe base64. Store only SHA-256 of the decoded bytes, with the user UUID,
issuance/expiry timestamps and nullable revocation timestamp. The digest is the
primary key; a collision fails safely without returning a credential. No JWT,
signing key or refresh token is involved. A high-entropy random token permits a
fast one-way digest rather than password hashing; see the
[OWASP session guidance](https://cheatsheetseries.owasp.org/cheatsheets/Session_Management_Cheat_Sheet.html#server-side-session-token-storage).

Issue uses UTC timestamps at PostgreSQL microsecond precision, with expiry exactly
ten minutes later. Authentication accepts `issued_at <= now < expires_at`, an
active user and no revocation. It rejects malformed/noncanonical tokens before
querying PostgreSQL; well-formed unknown, altered, revoked and expired tokens
share `ErrInvalidSession`. There is no application-side session cache.

The constructor accepts an injected clock for deterministic tests; nil uses
`time.Now`. Production service clocks must be synchronized. The issued credential
object redacts formatting and JSON serialization. Future login code must use
`AccessToken()` explicitly to construct the agreed response; never log that value
or token hashes. Storage failures return fixed errors, preserving cancellation.

## Lifecycle integration boundary

`Issue` checks active state but **does not verify passwords**. T12 must verify
credentials before issuance. It must serialize issuance against password changes
and deactivation: begin a READ COMMITTED transaction, lock the user row, verify
current credential/active state, issue via a repository bound to that transaction,
and commit before publishing the token. Never send a token if commit fails.

Password-change/deactivation tasks must take the same user-row lock, modify user
state and call `RevokeAll` in that transaction. `users.SetActive` alone currently
does not revoke sessions; it remains a low-level persistence method. Re-enabling
a user must never clear revoked timestamps. Tests explicitly revoke before
re-enablement and verify old sessions remain invalid. HTTP lifecycle integration
and deterministic competing login/lifecycle tests belong to those later tasks.

`Revoke` is idempotent for validly encoded tokens, including unknown and expired
ones. It preserves an existing revocation timestamp. `RevokeAll` affects only the
specified user's unrevoked sessions. These are trusted internal methods; future
HTTP callers must enforce ownership. T16 will supply operator procedures.
No automatic session deletion or change to queued financial operations is added.

## Validation

`make check-all` includes serialization/parser tests and real PostgreSQL session
tests. The latter use an injected clock for exact expiry, a second connection for
durable revocation, and transactions for rollback of issuance/revocation.
Targeted execution against a disposable server:

```sh
go test -race ./src/sessions
go test -race -tags=integration -run '^TestSession' ./tests/integration
```
