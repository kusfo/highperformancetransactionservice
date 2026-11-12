//go:build integration

package integration_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/kusfo/highperformancetransactionservice/src/financial"
)

const accountOwner = "01234567-89ab-cdef-0123-456789abcdef"
const otherAccountOwner = "11234567-89ab-cdef-0123-456789abcdef"

func TestAccountsPersistence(t *testing.T) {
	conn, _, ctx := userFixture(t)
	repo := financial.NewAccounts(conn)
	metadata := financial.Metadata{DisplayName: "  名字  ", Description: strings.Repeat("😀", 1000)}
	a, err := repo.Create(ctx, accountOwner, metadata)
	if err != nil {
		t.Fatal(err)
	}
	if a.Balance != 0 || a.Status != "active" || a.Generation != 1 || a.HeadSequence != 1 || a.NextSequence != 1 || a.RetryAfter != nil || a.CreatedAt.IsZero() || a.CreatedAt.Location().String() != "UTC" {
		t.Fatal("incorrect initial state")
	}
	got, err := repo.ByNumber(ctx, accountOwner, a.AccountNumber)
	if err != nil || got != a || got.DisplayName != metadata.DisplayName || got.Description != metadata.Description {
		t.Fatal("round-trip changed data")
	}
	second, err := repo.Create(ctx, accountOwner, financial.Metadata{})
	if err != nil || second.AccountID == a.AccountID || second.AccountNumber == a.AccountNumber || second.DisplayName != "" || second.Description != "" {
		t.Fatal("multiple accounts/defaults failed")
	}
	for _, owner := range []string{otherAccountOwner, "invalid"} {
		if _, err = repo.ByNumber(ctx, owner, a.AccountNumber); err != financial.ErrNotFound {
			t.Fatal("ownership leaked")
		}
	}
	if _, err = repo.ByNumber(ctx, accountOwner, "missing"); err != financial.ErrNotFound {
		t.Fatal(err)
	}
	// Opaque identifiers retain leading zeros; disabled accounts remain readable.
	_, err = conn.Exec(ctx, `INSERT INTO financial.accounts(account_id,account_number,owner_id) VALUES('21234567-89ab-cdef-0123-456789abcdef','00001234',$1)`, accountOwner)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = conn.Exec(ctx, `UPDATE financial.accounts SET status='disabled' WHERE account_number='00001234'`); err != nil {
		t.Fatal(err)
	}
	leading, err := repo.ByNumber(ctx, accountOwner, "00001234")
	if err != nil || leading.Status != "disabled" {
		t.Fatal("opaque disabled lookup failed")
	}
	if _, err = repo.ByNumber(ctx, accountOwner, "1234"); err != financial.ErrNotFound {
		t.Fatal("number normalized")
	}
	// There is deliberately no identity FK or user activity lookup.
	var owners int
	if err = conn.QueryRow(ctx, `SELECT count(*) FROM identity.users WHERE user_id=$1`, accountOwner).Scan(&owners); err != nil || owners != 0 {
		t.Fatal("unexpected identity dependency")
	}
	tx, err := conn.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	rolled, err := financial.NewAccounts(tx).Create(ctx, accountOwner, financial.Metadata{})
	if err != nil {
		t.Fatal(err)
	}
	if err = tx.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err = repo.ByNumber(ctx, accountOwner, rolled.AccountNumber); err != financial.ErrNotFound {
		t.Fatal("rollback retained account")
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err = repo.Create(cancelled, accountOwner, financial.Metadata{}); err != context.Canceled {
		t.Fatal("cancellation lost")
	}
}

func TestAccountsDatabaseConstraints(t *testing.T) {
	conn, _, ctx := userFixture(t)
	a, err := financial.NewAccounts(conn).Create(ctx, accountOwner, financial.Metadata{})
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ sql, code string }{
		{`UPDATE financial.accounts SET balance=-1`, "23514"},
		{`UPDATE financial.accounts SET balance=2147483648`, "22003"},
		{`UPDATE financial.accounts SET status='invalid'`, "23514"},
		{`UPDATE financial.accounts SET balance=1,status='disabled'`, "23514"},
		{`UPDATE financial.accounts SET head_sequence=0`, "23514"},
		{`UPDATE financial.accounts SET head_sequence=2`, "23514"},
		{`UPDATE financial.accounts SET generation=0`, "23514"},
		{`UPDATE financial.accounts SET next_sequence=0`, "23514"},
		{`UPDATE financial.accounts SET owner_id='11234567-89ab-cdef-0123-456789abcdef'`, "23514"},
		{`UPDATE financial.accounts SET account_id='11234567-89ab-cdef-0123-456789abcdef'`, "23514"},
		{`UPDATE financial.accounts SET account_number='new-number'`, "23514"},
		{`UPDATE financial.accounts SET created_at=created_at+interval '1 second'`, "23514"},
		{`UPDATE financial.accounts SET display_name=repeat('界',101)`, "23514"},
		{`UPDATE financial.accounts SET description=repeat('界',1001)`, "23514"},
		{`UPDATE financial.accounts SET retry_after='infinity'`, "23514"},
		{`DELETE FROM financial.accounts`, "23514"},
		{`INSERT INTO financial.accounts(account_id,account_number,owner_id) SELECT account_id,'other',owner_id FROM financial.accounts`, "23505"},
		{`INSERT INTO financial.accounts(account_id,account_number,owner_id) SELECT '21234567-89ab-cdef-0123-456789abcdef',account_number,owner_id FROM financial.accounts`, "23505"},
	} {
		_, err = conn.Exec(ctx, tc.sql)
		var pgErr *pgconn.PgError
		if !errors.As(err, &pgErr) || pgErr.Code != tc.code {
			t.Fatalf("constraint %s: %v", tc.sql, err)
		}
	}
	for _, initial := range []string{"balance=1", "status='disabled'", "generation=2", "next_sequence=2", "retry_after=now()"} {
		field, value, _ := strings.Cut(initial, "=")
		_, err = conn.Exec(ctx, `INSERT INTO financial.accounts(account_id,account_number,owner_id,`+field+`) VALUES('21234567-89ab-cdef-0123-456789abcdef','initial',$1,`+value+`)`, accountOwner)
		var pgErr *pgconn.PgError
		if !errors.As(err, &pgErr) || pgErr.Code != "23514" {
			t.Fatalf("initial state constraint %s: %v", field, err)
		}
	}
	if _, err = conn.Exec(ctx, `UPDATE financial.accounts SET balance=2147483647,generation=2,next_sequence=3,head_sequence=2`); err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"generation", "next_sequence", "head_sequence"} {
		if _, err = conn.Exec(ctx, `UPDATE financial.accounts SET `+field+`=1`); err == nil {
			t.Fatal("counter decreased")
		}
	}
	if _, err = conn.Exec(ctx, `UPDATE financial.accounts SET balance=0,status='disabled',head_sequence=next_sequence,display_name='edited',description=''`); err != nil {
		t.Fatal(err)
	}
	got, err := financial.NewAccounts(conn).ByNumber(ctx, accountOwner, a.AccountNumber)
	if err != nil || got.Balance != 0 || got.DisplayName != "edited" {
		t.Fatal("valid metadata/state update failed")
	}
}

func TestConcurrentAccountCreation(t *testing.T) {
	conn, _, ctx := userFixture(t)
	const count = 8
	start := make(chan struct{})
	results := make(chan financial.Account, count)
	failures := make(chan error, count)
	for range count {
		c, err := pgx.ConnectConfig(ctx, conn.Config().Copy())
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = c.Close(context.Background()) })
		go func() {
			<-start
			a, err := financial.NewAccounts(c).Create(ctx, accountOwner, financial.Metadata{})
			results <- a
			failures <- err
		}()
	}
	close(start)
	numbers := map[string]bool{}
	ids := map[string]bool{}
	for range count {
		a := <-results
		if err := <-failures; err != nil {
			t.Fatal(err)
		}
		if numbers[a.AccountNumber] || ids[a.AccountID] {
			t.Fatal("duplicate assigned identity")
		}
		numbers[a.AccountNumber] = true
		ids[a.AccountID] = true
	}
	var persisted int
	if err := conn.QueryRow(ctx, `SELECT count(*) FROM financial.accounts WHERE owner_id=$1`, accountOwner).Scan(&persisted); err != nil || persisted != count {
		t.Fatal("concurrent creations lost")
	}
}

func TestAccountIndexes(t *testing.T) {
	conn, _, ctx := userFixture(t)
	_, err := conn.Exec(ctx, `INSERT INTO financial.accounts(account_id,account_number,owner_id)
 SELECT md5(i::text)::uuid,lpad(i::text,32,'0'),md5(i::text)::uuid FROM generate_series(1,10000) i;
 UPDATE financial.accounts SET next_sequence=2 WHERE account_number=lpad('5000',32,'0');
 ANALYZE financial.accounts`)
	if err != nil {
		t.Fatal(err)
	}
	var owner string
	if err = conn.QueryRow(ctx, `SELECT md5('5000')::uuid::text`).Scan(&owner); err != nil {
		t.Fatal(err)
	}
	recorder := &queryRecorder{Conn: conn}
	if _, err = financial.NewAccounts(recorder).ByNumber(ctx, owner, strings.Repeat("0", 28)+"5000"); err != nil {
		t.Fatal(err)
	}
	queries := append(recorder.queries, recordedQuery{`SELECT account_number FROM financial.accounts WHERE owner_id=$1 ORDER BY created_at DESC,account_number DESC LIMIT 50`, []any{owner}}, recordedQuery{`SELECT account_id FROM financial.accounts WHERE status='active' AND head_sequence<next_sequence AND (retry_after IS NULL OR retry_after<=now()) ORDER BY retry_after NULLS FIRST,account_id LIMIT 50`, nil})
	for _, q := range queries {
		rows, err := conn.Query(ctx, "EXPLAIN "+q.sql, q.args...)
		if err != nil {
			t.Fatal(err)
		}
		var plan strings.Builder
		for rows.Next() {
			var line string
			if err = rows.Scan(&line); err != nil {
				t.Fatal(err)
			}
			plan.WriteString(line)
		}
		rows.Close()
		if err = rows.Err(); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(plan.String(), "Index") || strings.Contains(plan.String(), "Seq Scan") {
			t.Fatalf("unindexed plan: %s", plan.String())
		}
	}
}
