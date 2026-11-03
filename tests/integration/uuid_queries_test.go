//go:build integration

package integration_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/kusfo/highperformancetransactionservice/src/passwords"
	"github.com/kusfo/highperformancetransactionservice/src/profile"
	"github.com/kusfo/highperformancetransactionservice/src/sessions"
	"github.com/kusfo/highperformancetransactionservice/src/users"
)

type recordedQuery struct {
	sql  string
	args []any
}
type queryRecorder struct {
	*pgx.Conn
	queries []recordedQuery
}

func (r *queryRecorder) QueryRow(ctx context.Context, sql string, args ...any) pgx.Row {
	r.queries = append(r.queries, recordedQuery{sql, args})
	return r.Conn.QueryRow(ctx, sql, args...)
}
func (r *queryRecorder) Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
	r.queries = append(r.queries, recordedQuery{sql, args})
	return r.Conn.Exec(ctx, sql, args...)
}

type recordingTx struct {
	pgx.Tx
	recorder *queryRecorder
}

func (r *queryRecorder) BeginTx(ctx context.Context, opts pgx.TxOptions) (pgx.Tx, error) {
	tx, err := r.Conn.BeginTx(ctx, opts)
	if err != nil {
		return nil, err
	}
	return &recordingTx{tx, r}, nil
}
func (t *recordingTx) QueryRow(ctx context.Context, sql string, args ...any) pgx.Row {
	t.recorder.queries = append(t.recorder.queries, recordedQuery{sql, args})
	return t.Tx.QueryRow(ctx, sql, args...)
}
func (t *recordingTx) Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
	t.recorder.queries = append(t.recorder.queries, recordedQuery{sql, args})
	return t.Tx.Exec(ctx, sql, args...)
}

func TestUUIDPredicatesUseIndexes(t *testing.T) {
	conn, repo, ctx := userFixture(t)
	hash, err := passwords.Hash("original-password")
	if err != nil {
		t.Fatal(err)
	}
	owner, err := repo.Create(ctx, "indexed@example.com", hash, nil)
	if err != nil {
		t.Fatal(err)
	}
	// Enough unrelated rows for normal planner choices, without disabling seqscan.
	_, err = conn.Exec(ctx, `INSERT INTO identity.users(user_id,email,password_hash)
 SELECT md5(i::text)::uuid,'fixture'||i||'@example.com','fixture-hash' FROM generate_series(1,10000) i;
 INSERT INTO identity.sessions(token_hash,user_id,issued_at,expires_at)
 SELECT decode(md5(user_id::text)||md5(user_id::text),'hex'),user_id,now(),now()+interval '10 minutes' FROM identity.users;
 ANALYZE identity.users; ANALYZE identity.sessions;`)
	if err != nil {
		t.Fatal(err)
	}
	recorder := &queryRecorder{Conn: conn}
	u := users.NewRepository(recorder)
	s := sessions.NewRepository(recorder, nil)
	p := profile.NewService(recorder)
	if _, err = u.ByID(ctx, owner.UserID); err != nil {
		t.Fatal(err)
	}
	if err = u.SetActive(ctx, owner.UserID, true); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Issue(ctx, owner.UserID); err != nil {
		t.Fatal(err)
	}
	if err = s.RevokeAll(ctx, owner.UserID); err != nil {
		t.Fatal(err)
	}
	if _, err = p.Update(ctx, owner.UserID, "Indexed"); err != nil {
		t.Fatal(err)
	}
	if err = p.ChangePassword(ctx, owner.UserID, "original-password", "replacement-password"); err != nil {
		t.Fatal(err)
	}
	if len(recorder.queries) != 8 {
		t.Fatal("missing recorded queries")
	}
	for _, q := range recorder.queries {
		var plan string
		if err := conn.QueryRow(ctx, "EXPLAIN (FORMAT JSON) "+q.sql, q.args...).Scan(&plan); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(plan, `"Index Cond"`) || !strings.Contains(plan, "user_id =") {
			t.Fatalf("UUID predicate did not use index: %s", plan)
		}
	}
}

func TestInvalidUUIDPredicatesPreserveOutcomes(t *testing.T) {
	conn, repo, ctx := userFixture(t)
	owner, err := repo.Create(ctx, "uuid@example.com", "fixture-hash", nil)
	if err != nil {
		t.Fatal(err)
	}
	s := sessions.NewRepository(conn, nil)
	p := profile.NewService(conn)
	token, err := s.Issue(ctx, owner.UserID)
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"missing", strings.ToUpper(owner.UserID), strings.ReplaceAll(owner.UserID, "-", ""), "00000000-0000-0000-0000-000000000000"} {
		// A rejected ID must not poison a caller-owned transaction with a cast error.
		tx, err := conn.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		u := users.NewRepository(tx)
		sessionsTx := sessions.NewRepository(tx, nil)
		if _, err = u.ByID(ctx, id); !errors.Is(err, users.ErrNotFound) {
			t.Fatal("lookup outcome changed")
		}
		if err = u.SetActive(ctx, id, false); !errors.Is(err, users.ErrNotFound) {
			t.Fatal("active-state outcome changed")
		}
		if _, err = sessionsTx.Issue(ctx, id); !errors.Is(err, sessions.ErrInvalidSession) {
			t.Fatal("issuance outcome changed")
		}
		if err = sessionsTx.RevokeAll(ctx, id); err != nil {
			t.Fatal("revocation outcome changed")
		}
		if err = tx.Commit(ctx); err != nil {
			t.Fatal("invalid ID aborted transaction")
		}
		if _, err = p.Update(ctx, id, "Name"); !errors.Is(err, users.ErrNotFound) {
			t.Fatal("profile outcome changed")
		}
		if err = p.ChangePassword(ctx, id, "current-password", "replacement-password"); !errors.Is(err, users.ErrNotFound) {
			t.Fatal("password outcome changed")
		}
	}
	if _, err = s.Authenticate(ctx, token.AccessToken()); err != nil {
		t.Fatal("invalid ID revoked valid session")
	}
}
