//go:build integration

package integration_test

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/kusfo/highperformancetransactionservice/src/postgres"
	"github.com/kusfo/highperformancetransactionservice/tests/testdb"
)

func sqlFiles(sql string) fstest.MapFS {
	return fstest.MapFS{"000001_fixture.sql": {Data: []byte(sql)}}
}
func testContext(t *testing.T) context.Context {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)
	return ctx
}
func TestFreshRerunAndAppend(t *testing.T) {
	t.Parallel()
	conn := testdb.New(t)
	ctx := testContext(t)
	files := sqlFiles("CREATE TABLE fixture (id integer PRIMARY KEY); INSERT INTO fixture VALUES (1);")
	for i := 0; i < 2; i++ {
		if err := postgres.Migrate(ctx, conn, files); err != nil {
			t.Fatal(err)
		}
	}
	files["000002_append.sql"] = &fstest.MapFile{Data: []byte("INSERT INTO fixture VALUES (2);")}
	if err := postgres.Migrate(ctx, conn, files); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := conn.QueryRow(ctx, "SELECT count(*) FROM fixture").Scan(&count); err != nil || count != 2 {
		t.Fatalf("row count %d, error %v", count, err)
	}
	if err := conn.QueryRow(ctx, "SELECT count(*) FROM schema_migrations").Scan(&count); err != nil || count != 2 {
		t.Fatalf("history count %d, error %v", count, err)
	}
}
func TestFailureRollsBackEntireBatch(t *testing.T) {
	t.Parallel()
	conn := testdb.New(t)
	ctx := testContext(t)
	files := sqlFiles("CREATE TABLE fixture (id integer);")
	files["000002_failure.sql"] = &fstest.MapFile{Data: []byte("SELECT * FROM nonexistent_table;")}
	if err := postgres.Migrate(ctx, conn, files); err == nil {
		t.Fatal("expected failure")
	}
	var exists bool
	if err := conn.QueryRow(ctx, "SELECT to_regclass('public.fixture') IS NOT NULL OR to_regclass('public.schema_migrations') IS NOT NULL").Scan(&exists); err != nil || exists {
		t.Fatalf("partial changes survived: %v, %v", exists, err)
	}
	delete(files, "000002_failure.sql")
	if err := postgres.Migrate(ctx, conn, files); err != nil {
		t.Fatal(err)
	}
}
func TestHistoryIntegrity(t *testing.T) {
	for _, mode := range []string{"changed", "missing", "duplicate", "invalid", "older"} {
		t.Run(mode, func(t *testing.T) {
			t.Parallel()
			conn := testdb.New(t)
			ctx := testContext(t)
			files := fstest.MapFS{"000002_fixture.sql": {Data: []byte("CREATE TABLE fixture (id integer);")}}
			if err := postgres.Migrate(ctx, conn, files); err != nil {
				t.Fatal(err)
			}
			switch mode {
			case "changed":
				files["000002_fixture.sql"].Data = []byte("SELECT 1;")
			case "missing":
				delete(files, "000002_fixture.sql")
			case "duplicate":
				files["000002_duplicate.sql"] = &fstest.MapFile{Data: []byte("SELECT 1;")}
			case "invalid":
				files["bad.sql"] = &fstest.MapFile{Data: []byte("SELECT 1;")}
			case "older":
				files["000001_older.sql"] = &fstest.MapFile{Data: []byte("SELECT 1;")}
			}
			if err := postgres.Migrate(ctx, conn, files); err == nil {
				t.Fatal("expected rejection")
			}
		})
	}
}

// waitForBlocker observes PostgreSQL's real wait graph. Polling is bounded;
// elapsed time is not used as evidence that two runners overlap.
func waitForBlocker(t *testing.T, ctx context.Context, observer *pgx.Conn, waiter, blocker uint32) {
	t.Helper()
	ticker := time.NewTicker(5 * time.Millisecond)
	defer ticker.Stop()
	for {
		var blocked, advisory bool
		err := observer.QueryRow(ctx, `SELECT $1::integer = ANY(pg_blocking_pids($2::integer)), EXISTS(SELECT 1 FROM pg_locks WHERE pid=$2::integer AND locktype='advisory' AND NOT granted)`, int64(blocker), int64(waiter)).Scan(&blocked, &advisory)
		if err != nil {
			t.Fatal(err)
		}
		if blocked {
			if !advisory {
				t.Fatal("runner blocked on a different lock, not the migration advisory lock")
			}
			return
		}
		select {
		case <-ctx.Done():
			t.Fatal("expected database lock wait was not observed")
		case <-ticker.C:
		}
	}
}

func TestConcurrentRunners(t *testing.T) {
	for _, cancelWaiter := range []bool{false, true} {
		name := "serialized"
		if cancelWaiter {
			name = "cancel_and_retry"
		}
		t.Run(name, func(t *testing.T) {
			observer := testdb.New(t)
			ctx := testContext(t)
			first, err := pgx.ConnectConfig(ctx, observer.Config().Copy())
			if err != nil {
				t.Fatal(err)
			}
			second, err := pgx.ConnectConfig(ctx, observer.Config().Copy())
			if err != nil {
				_ = first.Close(ctx)
				t.Fatal(err)
			}
			defer func() {
				closeCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				_ = first.Close(closeCtx)
				_ = second.Close(closeCtx)
			}()
			runCtx, cancelRuns := context.WithCancel(ctx)
			defer cancelRuns()
			// A separate gate holds the first runner INSIDE its migration, after it
			// acquired the production migration lock. The observer owns that gate.
			const gate = 918006
			if _, err := observer.Exec(ctx, `SELECT pg_advisory_lock($1)`, gate); err != nil {
				t.Fatal(err)
			}
			defer func() {
				cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				_, _ = observer.Exec(cleanup, `SELECT pg_advisory_unlock($1)`, gate)
			}()
			files := sqlFiles("SELECT pg_advisory_xact_lock(918006); CREATE TABLE fixture (id integer); INSERT INTO fixture VALUES (1);")
			firstDone := make(chan error, 1)
			go func() { firstDone <- postgres.Migrate(runCtx, first, files) }()
			waitForBlocker(t, ctx, observer, first.PgConn().PID(), observer.PgConn().PID())
			waiterCtx, cancelWait := context.WithCancel(runCtx)
			defer cancelWait()
			secondDone := make(chan error, 1)
			go func() { secondDone <- postgres.Migrate(waiterCtx, second, files) }()
			waitForBlocker(t, ctx, observer, second.PgConn().PID(), first.PgConn().PID())
			if cancelWaiter {
				cancelWait()
				select {
				case err := <-secondDone:
					if !errors.Is(err, context.Canceled) {
						t.Fatalf("expected cancelled lock wait: %v", err)
					}
				case <-ctx.Done():
					t.Fatal("cancelled runner did not return")
				}
				var exists bool
				if err := observer.QueryRow(ctx, `SELECT to_regclass('public.schema_migrations') IS NOT NULL`).Scan(&exists); err != nil || exists {
					t.Fatalf("waiting runner committed history: %v %v", exists, err)
				}
			}
			if _, err := observer.Exec(ctx, `SELECT pg_advisory_unlock($1)`, gate); err != nil {
				t.Fatal(err)
			}
			select {
			case err := <-firstDone:
				if err != nil {
					t.Fatal(err)
				}
			case <-ctx.Done():
				t.Fatal("first runner did not finish")
			}
			if cancelWaiter {
				// Cancellation may close a pgx connection: retry on a fresh connection,
				// as a restarted migration command would do.
				retry, err := pgx.ConnectConfig(ctx, observer.Config().Copy())
				if err != nil {
					t.Fatal(err)
				}
				defer retry.Close(context.Background())
				if err := postgres.Migrate(ctx, retry, files); err != nil {
					t.Fatal(err)
				}
			} else {
				select {
				case err := <-secondDone:
					if err != nil {
						t.Fatal(err)
					}
				case <-ctx.Done():
					t.Fatal("second runner did not finish")
				}
			}
			var count int
			if err := observer.QueryRow(ctx, "SELECT count(*) FROM fixture").Scan(&count); err != nil || count != 1 {
				t.Fatalf("duplicate application: %d %v", count, err)
			}
			if err := observer.QueryRow(ctx, "SELECT count(*) FROM schema_migrations").Scan(&count); err != nil || count != 1 {
				t.Fatalf("incorrect migration history: %d %v", count, err)
			}
		})
	}
}

func TestFixtureCleanupAndIsolation(t *testing.T) {
	ctx := testContext(t)
	var name string
	t.Run("fixture", func(t *testing.T) {
		conn := testdb.New(t)
		if err := conn.QueryRow(ctx, "SELECT current_database()").Scan(&name); err != nil {
			t.Fatal(err)
		}
		if _, err := conn.Exec(ctx, "CREATE TABLE isolated_marker(id integer)"); err != nil {
			t.Fatal(err)
		}
	})
	admin, err := pgx.Connect(ctx, os.Getenv("TEST_DATABASE_URL"))
	if err != nil {
		t.Fatal("admin connection failed")
	}
	defer admin.Close(ctx)
	var exists bool
	if err := admin.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM pg_database WHERE datname=$1)", name).Scan(&exists); err != nil || exists {
		t.Fatalf("fixture database remains: %v, %v", exists, err)
	}
	other := testdb.New(t)
	if err := other.QueryRow(ctx, "SELECT to_regclass('public.isolated_marker') IS NOT NULL").Scan(&exists); err != nil || exists {
		t.Fatalf("shared fixture state: %v, %v", exists, err)
	}
	if !strings.HasPrefix(name, "t06_") {
		t.Fatal("unexpected fixture name")
	}
}
