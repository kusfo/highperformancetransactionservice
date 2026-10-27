# PostgreSQL migrations

Add append-only `000001_description.sql` files here as schema tasks are implemented.
Versions must be unique, positive six-digit numbers. Never edit, rename, remove,
or insert a migration before already applied history. Non-SQL files are ignored.

The runner uses `public.schema_migrations` with SHA-256 checksums and a
transaction-scoped PostgreSQL advisory lock. All pending files and their history
commit together; any error rolls back the whole batch. Concurrent runs serialize.
SQL files must not include transaction-control statements or nontransactional DDL
(such as `CREATE INDEX CONCURRENTLY`). Migration `000001_users.sql` introduces the identity schema and users table.
T17/T21 will add account/transaction schemas. Empty runs initialize migration history.

Run `DATABASE_URL=... make migrate` against the intended database with appropriate
DDL rights; the CLI never prints the URL or underlying database error payloads.
Keep credentials in environment configuration rather than shell history or logs.

`000002_sessions.sql` adds hashed opaque sessions with exact ten-minute expiry,
user foreign keys and durable revocation. Raw access tokens are never stored.
