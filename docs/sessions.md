# Session persistence and issuance

T11 adds `identity.sessions` through `000002_sessions.sql` and the `src/sessions`
repository. Run migrations explicitly with `make migrate`. Login is implemented by `src/login` and `POST /sessions`; see [login](login.md).
Protected-route bearer middleware is implemented in `src/authn`; see [authentication](authentication.md). Logout remains a later task.

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
object redacts formatting and JSON serialization. Login code uses
`AccessToken()` explicitly to construct the agreed response; never log that value
or token hashes. Storage failures return fixed errors, preserving cancellation.

## Lifecycle integration boundary

`Issue` checks active state but **does not verify passwords**. T12 verifies
credentials before issuance and serializes issuance against password changes
and deactivation: begin a READ COMMITTED transaction, lock the user row, verify
current credential/active state, issue via a repository bound to that transaction,
and commit before publishing the token. Never send a token if commit fails.

T14 password changes take the same user-row lock, replace the hash and call
`RevokeAll` in that transaction; see [profile updates](profile.md). Future
deactivation code must also lock the user row and revoke in its transaction. `users.SetActive` alone currently
does not revoke sessions; it remains a low-level persistence method. Re-enabling
a user must never clear revoked timestamps. Tests explicitly revoke before
re-enablement and verify old sessions remain invalid. T12 tests observed login waits against direct database lifecycle updates.
Password-change integration tests cover atomic rollback and concurrent changes.
HTTP deactivation remains a later task.

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
