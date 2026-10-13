// Command migrate applies append-only PostgreSQL migrations.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/kusfo/highperformancetransactionservice/src/postgres"
)

func run() error {
	dir := flag.String("dir", "migrations", "directory containing migration SQL files")
	timeout := flag.Duration("timeout", time.Minute, "connection and migration timeout")
	flag.Parse()
	url := os.Getenv("DATABASE_URL")
	if url == "" {
		return fmt.Errorf("DATABASE_URL is required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()
	conn, err := pgx.Connect(ctx, url)
	if err != nil {
		return fmt.Errorf("database connection failed")
	}
	defer func() {
		closeCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = conn.Close(closeCtx)
	}()
	return postgres.Migrate(ctx, conn, os.DirFS(*dir))
}

func main() {
	if err := run(); err != nil {
		// SQL/driver errors can contain connection details or application values.
		// Do not print them; detailed migration errors are available to code/tests.
		fmt.Fprintln(os.Stderr, "migration failed; check configuration and migration files")
		os.Exit(1)
	}
	fmt.Println("migrations applied")
}
