# User Persistence

`000001_users.sql` creates `identity.users` with an assigned UUID, canonical
unique email, opaque server-generated password hash, optional display name,
active flag and creation timestamp. The email constraint applies to active and
inactive rows; deactivation never releases an email for registration.

`src/users.Repository` accepts a pgx connection, transaction or pool. Use separate
connections or a pool for concurrent calls. Create generates a UUID and starts
users as active. ByID returns only public profile fields. CredentialsByEmail is
restricted to authentication code and returns active state plus the stored hash;
authentication must reject inactive users in later tasks. Credential objects
omit their fields from JSON and redact fmt output; never log their hash accessor.

Email normalization trims surrounding ASCII whitespace, lowercases ASCII and
rejects non-ASCII or malformed addresses. Omitted display names default to empty;
supplied names contain 1–100 Unicode characters. Hashes must be nonempty; hashing
and password verification belong to T09, not this repository.

SetActive retains the row and hash, including repeated state changes. Session
revocation/HTTP authorization are later tasks and must accompany user deactivation
at the service layer. No profile, password-change or registration endpoint is
introduced here. Repository errors are fixed sentinels and do not expose driver
error details, submitted values or hashes; context cancellation is preserved.

Run `make test-postgres` for disposable PostgreSQL tests, including concurrent
registration, migration rerun, retained inactive identities and schema checks.
Normal Go tests also verify email normalization and credential serialization.
