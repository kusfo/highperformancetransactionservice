package postgres

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	IdentityRole      = "hpts_identity"
	FinancialHTTPRole = "hpts_financial_http"
	WorkerRole        = "hpts_worker"
	RelayRole         = "hpts_relay"
)

var ErrRuntimeRole = errors.New("unsafe or unprovisioned runtime database role")

type roleQuerier interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}

// CheckRuntimeRole requires a dedicated login with only the intended capability
// membership. Owners, elevated roles and SET ROLE/connection-option disguises
// fail closed. Provisioning must also control direct grants and role changes.
func CheckRuntimeRole(ctx context.Context, db roleQuerier, role string) error {
	switch role {
	case IdentityRole, FinancialHTTPRole, WorkerRole, RelayRole:
	default:
		return ErrRuntimeRole
	}
	var safe bool
	err := db.QueryRow(ctx, `SELECT current_user=session_user AND current_user<>$1
 AND pg_has_role(session_user,$1,'USAGE')
 AND NOT EXISTS (
 SELECT FROM pg_roles r WHERE pg_has_role(session_user,r.oid,'MEMBER') AND (
 r.rolsuper OR r.rolcreatedb OR r.rolcreaterole OR r.rolreplication OR r.rolbypassrls
 OR r.rolname NOT IN (session_user,$1)
 OR EXISTS (SELECT FROM pg_database d WHERE d.datname=current_database() AND d.datdba=r.oid)
 OR EXISTS (SELECT FROM pg_namespace n WHERE n.nspname IN ('identity','financial','public') AND n.nspowner=r.oid)
 OR EXISTS (SELECT FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace
 WHERE n.nspname IN ('identity','financial','public') AND c.relowner=r.oid)))
 AND NOT has_schema_privilege(session_user,'public','CREATE')
 AND NOT has_schema_privilege(session_user,'identity','CREATE')
 AND NOT has_schema_privilege(session_user,'financial','CREATE')`, role).Scan(&safe)
	if err != nil || !safe {
		return ErrRuntimeRole
	}
	// Explicit identity boundaries also detect accidental direct/PUBLIC grants.
	err = db.QueryRow(ctx, `SELECT CASE $1::text
 WHEN 'hpts_identity' THEN NOT has_schema_privilege(session_user,'financial','USAGE')
 WHEN 'hpts_financial_http' THEN
 NOT has_table_privilege(session_user,'identity.users','SELECT')
 AND NOT has_table_privilege(session_user,'identity.sessions','SELECT')
 AND
 NOT has_column_privilege(session_user,'identity.users','password_hash','SELECT')
 AND NOT has_any_column_privilege(session_user,'identity.users','INSERT,UPDATE,REFERENCES')
 AND NOT has_any_column_privilege(session_user,'identity.sessions','INSERT,UPDATE,REFERENCES')
 AND NOT has_table_privilege(session_user,'identity.users','DELETE,TRUNCATE,TRIGGER')
 AND NOT has_table_privilege(session_user,'identity.sessions','DELETE,TRUNCATE,TRIGGER')
 ELSE NOT has_schema_privilege(session_user,'identity','USAGE') END`, role).Scan(&safe)
	if err != nil || !safe {
		return ErrRuntimeRole
	}
	return nil
}

// OpenRuntimePool validates every new connection, including later pool growth.
// Migrations and operator tooling intentionally use separate connection paths.
func OpenRuntimePool(ctx context.Context, url, role string) (*pgxpool.Pool, error) {
	if url == "" {
		return nil, errors.New("database configuration required")
	}
	cfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		return nil, errors.New("invalid database configuration")
	}
	cfg.ConnConfig.ConnectTimeout = 5 * time.Second
	cfg.AfterConnect = func(ctx context.Context, c *pgx.Conn) error { return CheckRuntimeRole(ctx, c, role) }
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, errors.New("database initialization failed")
	}
	startup, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err = pool.Ping(startup); err != nil {
		pool.Close()
		return nil, errors.New("database unavailable or runtime role rejected")
	}
	return pool, nil
}
