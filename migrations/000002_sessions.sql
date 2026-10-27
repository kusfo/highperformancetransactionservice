CREATE TABLE identity.sessions (
    token_hash bytea PRIMARY KEY CHECK (octet_length(token_hash) = 32),
    user_id uuid NOT NULL REFERENCES identity.users(user_id),
    issued_at timestamptz NOT NULL,
    expires_at timestamptz NOT NULL,
    revoked_at timestamptz,
    CONSTRAINT sessions_lifetime CHECK (expires_at = issued_at + interval '10 minutes')
);
CREATE INDEX sessions_user_id ON identity.sessions(user_id);
