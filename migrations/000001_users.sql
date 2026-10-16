CREATE SCHEMA identity;

CREATE TABLE identity.users (
    user_id uuid PRIMARY KEY,
    email text NOT NULL CONSTRAINT users_email_unique UNIQUE,
    password_hash text NOT NULL CHECK (length(password_hash) > 0),
    display_name text NOT NULL DEFAULT '' CHECK (char_length(display_name) <= 100),
    active boolean NOT NULL DEFAULT true,
    created_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT users_email_canonical CHECK (
        char_length(email) BETWEEN 3 AND 254
        AND octet_length(email) = char_length(email)
        AND email = lower(email)
        AND email !~ '[[:space:]]'
    )
);
