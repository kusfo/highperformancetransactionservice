# User registration

T10 implements unauthenticated `POST /users` in the User service. Send an
uncompressed `application/json` object containing `email`, `password` and optional
`displayName`. The response is `201` with only `{"userId":"<assigned UUID>"}`.
Registration creates an active user; it does not issue a session.

Emails are trimmed/lowercased using the agreed ASCII policy. Passwords contain
12–128 Unicode characters and are never trimmed or normalized. Display names
contain 1–100 Unicode characters when supplied; omission stores an empty string.
NUL is rejected in display names because PostgreSQL text cannot store it.
Passwords may contain NUL because only an Argon2id hash is stored.

Unknown/case-mismatched fields, duplicate JSON keys, wrong types, explicit nulls,
invalid input and client-provided hashes return `400 INVALID_REQUEST`. Active and
inactive duplicate emails return `409 EMAIL_ALREADY_USED`. Database or hashing
failures return a generic `500 INTERNAL_ERROR`. Other paths return JSON 404;
other methods on `/users` return JSON 405 with `Allow: POST`. Shared HTTP limits
and content-type errors remain in effect. Responses use `Cache-Control: no-store`.

## Running locally

Set `DATABASE_URL` using private environment configuration for an existing
PostgreSQL database. Apply migrations separately before starting the service:

```sh
make build
./bin/migrate
export HTTP_TLS_CERT_FILE=/path/to/local-cert.pem
export HTTP_TLS_KEY_FILE=/path/to/local-key.pem
export HTTP_ADDR=127.0.0.1:8443
./bin/user
```

The certificate must be trusted by your client and valid for the requested host.
The service requires `DATABASE_URL`, checks connectivity with a five-second
startup timeout, and serves HTTPS. It does not automatically migrate the schema.
Connection attempts are capped at five seconds. The pgx pool supports concurrent
requests; pool-size settings can be supplied in `DATABASE_URL`. URLs and driver
errors are never printed. Account/Transaction scaffolds do not yet require a DB.

## Concurrency and cancellation

Each User process allows two active registration/login requests, including
hashing and storage. This bounds active Argon2 memory to approximately 128 MiB plus runtime
overhead; waiting requests use the shared HTTP request deadline. Validate input
before acquiring a slot, check cancellation before hashing and again before
storage, and pass the request context to the database. Argon2 itself cannot be
interrupted. PostgreSQL's unique constraint resolves concurrent duplicate emails.
Tune admission control with measured traffic in future performance tasks.

Registration has no operation-ID retry protocol. If a user is committed but the
HTTP response is lost, retrying returns the duplicate-email conflict. Cancellation
cannot undo a commit that has already completed.

## Validation

`go test -race ./src/userapi` covers validation, safe result/error mapping and
bounded/cancelled requests. `make check-all` also exercises registration over real
TLS and PostgreSQL, checking stored password verification, distinct salts,
concurrent/inactive duplicate emails, omitted/max-length names, client-hash
rejection, database failure redaction and the compiled User executable.
