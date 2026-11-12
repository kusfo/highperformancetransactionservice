CREATE SCHEMA financial;

CREATE TABLE financial.accounts (
    account_id uuid PRIMARY KEY,
    account_number text COLLATE "C" NOT NULL CONSTRAINT accounts_number_unique UNIQUE
        CHECK (octet_length(account_number) BETWEEN 1 AND 128),
    owner_id uuid NOT NULL,
    display_name text NOT NULL DEFAULT '' CHECK (char_length(display_name) <= 100),
    description text NOT NULL DEFAULT '' CHECK (char_length(description) <= 1000),
    balance integer NOT NULL DEFAULT 0 CHECK (balance BETWEEN 0 AND 2147483647),
    status text NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'disabled')),
    created_at timestamptz NOT NULL DEFAULT clock_timestamp() CHECK (isfinite(created_at)),
    generation bigint NOT NULL DEFAULT 1 CHECK (generation >= 1),
    next_sequence bigint NOT NULL DEFAULT 1,
    head_sequence bigint NOT NULL DEFAULT 1,
    retry_after timestamptz CHECK (retry_after IS NULL OR isfinite(retry_after)),
    CONSTRAINT accounts_sequence_bounds CHECK (1 <= head_sequence AND head_sequence <= next_sequence),
    CONSTRAINT accounts_disabled_balance CHECK (status <> 'disabled' OR balance = 0)
);

-- Account owners come from authenticated entry points. No identity FK: workers
-- and financial history must remain independent of identity lifecycle writes.
CREATE INDEX accounts_owner_created_number ON financial.accounts
    (owner_id, created_at DESC, account_number DESC);
CREATE INDEX accounts_ready ON financial.accounts
    (retry_after NULLS FIRST, account_id)
    WHERE status = 'active' AND head_sequence < next_sequence;

CREATE FUNCTION financial.guard_account() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF TG_OP = 'DELETE' THEN
        RAISE EXCEPTION 'accounts must be retained' USING ERRCODE = '23514';
    ELSIF TG_OP = 'INSERT' THEN
        IF NEW.balance <> 0 OR NEW.status <> 'active' OR NEW.generation <> 1
           OR NEW.next_sequence <> 1 OR NEW.head_sequence <> 1 OR NEW.retry_after IS NOT NULL THEN
            RAISE EXCEPTION 'invalid initial account state' USING ERRCODE = '23514';
        END IF;
    ELSE
        IF NEW.account_id IS DISTINCT FROM OLD.account_id
           OR NEW.account_number IS DISTINCT FROM OLD.account_number
           OR NEW.owner_id IS DISTINCT FROM OLD.owner_id
           OR NEW.created_at IS DISTINCT FROM OLD.created_at THEN
            RAISE EXCEPTION 'account identity is immutable' USING ERRCODE = '23514';
        END IF;
        IF NEW.generation < OLD.generation OR NEW.next_sequence < OLD.next_sequence
           OR NEW.head_sequence < OLD.head_sequence THEN
            RAISE EXCEPTION 'account counters cannot decrease' USING ERRCODE = '23514';
        END IF;
    END IF;
    RETURN NEW;
END;
$$;
CREATE TRIGGER accounts_guard BEFORE INSERT OR UPDATE OR DELETE ON financial.accounts
    FOR EACH ROW EXECUTE FUNCTION financial.guard_account();
-- T21 adds the latest-closure reference together with its target table/FK.
