// Command user serves registration and login over HTTPS.
package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/kusfo/highperformancetransactionservice/src/httpapi"
	"github.com/kusfo/highperformancetransactionservice/src/login"
	"github.com/kusfo/highperformancetransactionservice/src/userapi"
	"github.com/kusfo/highperformancetransactionservice/src/users"
)

func run() error {
	cfg, err := httpapi.LoadConfig(os.Getenv)
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if os.Getenv("DATABASE_URL") == "" {
		return errors.New("database configuration required")
	}
	poolConfig, err := pgxpool.ParseConfig(os.Getenv("DATABASE_URL"))
	if err != nil {
		return errors.New("invalid database configuration")
	}
	// Keep connection attempts bounded even when a request is cancelled.
	poolConfig.ConnConfig.ConnectTimeout = 5 * time.Second
	pool, err := pgxpool.NewWithConfig(ctx, poolConfig)
	if err != nil {
		return errors.New("database initialization failed")
	}
	defer pool.Close()
	startup, cancel := context.WithTimeout(ctx, 5*time.Second)
	err = pool.Ping(startup)
	cancel()
	if err != nil {
		return errors.New("database unavailable")
	}
	routes := userapi.NewHandlerWithLogin(users.NewRepository(pool), login.NewService(pool, nil))
	runtime, err := httpapi.New(cfg, routes, os.Stdout)
	if err != nil {
		return err
	}
	return runtime.Run(ctx)
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "user: HTTP startup or shutdown failed; check configuration")
		os.Exit(1)
	}
}
