# Entry-point authentication

T13 provides `src/authn.Require(checker, next)` for protected HTTP handlers. Use
`sessions.NewRepository(pool, nil)` as the checker. Wrap routes inside the shared
HTTPS runtime so request deadlines, size limits and redacted logs remain active.
Registration and login are public; no existing domain route needs protection yet.
Profile/account/operation endpoints will apply this wrapper when implemented.

```go
protected := authn.Require(sessions.NewRepository(pool, nil), handler)
// Inside handler:
identity, ok := authn.FromContext(r.Context())
// Reject missing identity; enforce resource ownership using identity.UserID.
```

## Accepted credentials and identity

Accept exactly one `Authorization: Bearer <accessToken>` header. The scheme is
case-insensitive and one or more ASCII spaces separate it from the token.
Missing/empty credentials, other schemes, ambiguous repeated headers, comma lists
and embedded whitespace fail. The session repository validates canonical token
encoding before querying PostgreSQL. Cookies, query parameters, request bodies
and caller-supplied user-ID headers are never authentication sources.

Each protected request performs a fresh session lookup for token authenticity,
absolute expiry, revocation and current user active state. There is no identity or
session cache. A private context key holds an `Identity` containing only `UserID`;
tokens and stored digests are not copied into identity. Middleware does not mutate
the original request context. Downstream code must still avoid logging headers.

Authentication is not resource authorization: handlers must scope their reads and
writes to the validated user. Never trust a user ID from request headers or URL
parameters without ownership checks. Authenticate when accepting or looking up an
operation; do not rerun this middleware inside workers. Later expiry, revocation
or deactivation must not cancel already accepted financial operations.

## Failure behavior

Missing, malformed, unknown, expired, revoked and inactive-user credentials all
return `401` with `UNAUTHORIZED`, a fixed message, `WWW-Authenticate: Bearer` and
`Cache-Control: no-store`. Protected handlers are not invoked on failure.
Database failures fail closed with a generic `500 INTERNAL_ERROR`, rather than
misreporting credentials as invalid. Cancellation returns the shared fixed 503
response; the outer runtime retains responsibility for request timeout responses.
A successful lookup followed by detected cancellation does not dispatch.

Revocation/deactivation committed before the lookup is visible to that request.
A request already authenticated may continue; this is an entry check, not a
continuous authorization lease. Later lifecycle endpoints must atomically revoke
sessions when changing passwords or deactivating users, as documented in
[sessions](sessions.md).

## Validation

`make check-all` includes unit tests for parsing, context isolation, error
redaction and cancellation, plus a real TLS/PostgreSQL protected fixture route.
The fixture checks expiry boundaries, forged/revoked tokens, inactive users,
revocation followed by re-enablement, multiple Authorization headers, untrusted
identity sources, database outage and metadata-only logging. The fixture endpoint
is test-only; no placeholder protected endpoint is published by the service.

```sh
go test -race ./src/authn
go test -race -tags=integration -run '^TestBearer' ./tests/integration
```
