// Package testdb creates disposable databases for PostgreSQL integration tests.
package testdb

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

// New requires TEST_DATABASE_URL pointing at a disposable server whose role can
// create/drop databases. Cleanup is registered before returning the connection.
func New(t *testing.T) *pgx.Conn {
	t.Helper()
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Fatal("TEST_DATABASE_URL is required for integration tests")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	admin, err := pgx.Connect(ctx, url)
	if err != nil {
		t.Fatal("test database admin connection failed")
	}
	suffix := make([]byte, 12)
	if _, err := rand.Read(suffix); err != nil {
		_ = admin.Close(ctx)
		t.Fatal(err)
	}
	name := "t06_" + hex.EncodeToString(suffix)
	if _, err := admin.Exec(ctx, "CREATE DATABASE "+pgx.Identifier{name}.Sanitize()); err != nil {
		_ = admin.Close(ctx)
		t.Fatal("create isolated database failed")
	}
	var conn *pgx.Conn
	t.Cleanup(func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		if conn != nil {
			if err := conn.Close(cleanup); err != nil {
				t.Error("close isolated connection failed")
			}
		}
		if _, err := admin.Exec(cleanup, "DROP DATABASE "+pgx.Identifier{name}.Sanitize()+" WITH (FORCE)"); err != nil {
			t.Error("drop isolated database failed")
		}
		if err := admin.Close(cleanup); err != nil {
			t.Error("close admin connection failed")
		}
	})
	config, err := pgx.ParseConfig(url)
	if err != nil {
		t.Fatal("invalid test database configuration")
	}
	config.Database = name
	conn, err = pgx.ConnectConfig(ctx, config)
	if err != nil {
		t.Fatal("isolated database connection failed")
	}
	return conn
}
