//go:build integration

package integration_test

import (
	"context"
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
func TestConcurrentRunners(t *testing.T) {
	conn := testdb.New(t)
	ctx := testContext(t)
	second, err := pgx.ConnectConfig(ctx, conn.Config().Copy())
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close(context.Background())
	files := sqlFiles("CREATE TABLE fixture (id integer); INSERT INTO fixture VALUES (1);")
	done := make(chan error, 2)
	for _, c := range []*pgx.Conn{conn, second} {
		go func(c *pgx.Conn) { done <- postgres.Migrate(ctx, c, files) }(c)
	}
	for i := 0; i < 2; i++ {
		if err := <-done; err != nil {
			t.Fatal(err)
		}
	}
	var count int
	if err := conn.QueryRow(ctx, "SELECT count(*) FROM fixture").Scan(&count); err != nil || count != 1 {
		t.Fatalf("duplicate application: %d, %v", count, err)
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
