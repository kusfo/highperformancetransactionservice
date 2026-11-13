-- Run after migrations as the schema owner with CREATEROLE (or a provisioning
-- administrator). psql -X -v ON_ERROR_STOP=1 -f scripts/database-roles.sql
-- This script contains no passwords and creates NOLOGIN capability roles only.
BEGIN;
SELECT pg_advisory_xact_lock(718604207);
DO $$
DECLARE role_name text;
BEGIN
    FOREACH role_name IN ARRAY ARRAY['hpts_identity','hpts_financial_http','hpts_worker','hpts_relay'] LOOP
        IF NOT EXISTS (SELECT FROM pg_roles WHERE rolname=role_name) THEN
            EXECUTE format('CREATE ROLE %I NOLOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOREPLICATION NOBYPASSRLS', role_name);
        END IF;
        IF EXISTS (SELECT FROM pg_roles WHERE rolname=role_name AND
            (rolcanlogin OR rolsuper OR rolcreatedb OR rolcreaterole OR rolreplication OR rolbypassrls))
           OR EXISTS (SELECT FROM pg_auth_members m JOIN pg_roles r ON r.oid=m.member WHERE r.rolname=role_name) THEN
            RAISE EXCEPTION 'unsafe existing capability role';
        END IF;
    END LOOP;
END $$;

REVOKE CREATE ON SCHEMA public FROM PUBLIC;
REVOKE ALL ON SCHEMA identity, financial FROM PUBLIC, hpts_identity, hpts_financial_http, hpts_worker, hpts_relay;
REVOKE ALL ON ALL TABLES IN SCHEMA identity, financial FROM PUBLIC, hpts_identity, hpts_financial_http, hpts_worker, hpts_relay;
-- Table-level revocation does not remove pre-existing column grants.
DO $$
DECLARE entry record;
BEGIN
    FOR entry IN SELECT n.nspname,c.relname,string_agg(quote_ident(a.attname),',') AS columns
        FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace
        JOIN pg_attribute a ON a.attrelid=c.oid AND a.attnum>0 AND NOT a.attisdropped
        WHERE n.nspname IN ('identity','financial') AND c.relkind IN ('r','p')
        GROUP BY n.nspname,c.relname LOOP
        EXECUTE format('REVOKE ALL (%s) ON %I.%I FROM PUBLIC,hpts_identity,hpts_financial_http,hpts_worker,hpts_relay',entry.columns,entry.nspname,entry.relname);
    END LOOP;
END $$;
REVOKE ALL ON ALL FUNCTIONS IN SCHEMA identity, financial FROM PUBLIC, hpts_identity, hpts_financial_http, hpts_worker, hpts_relay;
-- Applies to objects subsequently created by the provisioning/migration owner.
ALTER DEFAULT PRIVILEGES IN SCHEMA identity, financial REVOKE EXECUTE ON FUNCTIONS FROM PUBLIC;

GRANT USAGE ON SCHEMA identity TO hpts_identity, hpts_financial_http;
GRANT SELECT, INSERT ON identity.users, identity.sessions TO hpts_identity;
GRANT UPDATE (display_name,password_hash,active) ON identity.users TO hpts_identity;
GRANT UPDATE (revoked_at) ON identity.sessions TO hpts_identity;

GRANT SELECT (user_id,active) ON identity.users TO hpts_financial_http;
GRANT SELECT (token_hash,user_id,issued_at,expires_at,revoked_at) ON identity.sessions TO hpts_financial_http;

GRANT USAGE ON SCHEMA financial TO hpts_financial_http, hpts_worker, hpts_relay;
GRANT SELECT ON financial.accounts TO hpts_financial_http, hpts_worker;
GRANT INSERT (account_id,account_number,owner_id,display_name,description) ON financial.accounts TO hpts_financial_http;
GRANT UPDATE (display_name,description,balance,status,generation,next_sequence,head_sequence,retry_after)
    ON financial.accounts TO hpts_financial_http;
GRANT UPDATE (balance,head_sequence,retry_after) ON financial.accounts TO hpts_worker;
-- Relay deliberately has no table privileges until the T21 outbox exists.
-- No blanket default table grants: every new table must declare its role grants.
COMMIT;
