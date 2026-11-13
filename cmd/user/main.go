// Command user serves registration, login, profile updates and deactivation over HTTPS.
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/kusfo/highperformancetransactionservice/src/httpapi"
	"github.com/kusfo/highperformancetransactionservice/src/login"
	"github.com/kusfo/highperformancetransactionservice/src/postgres"
	"github.com/kusfo/highperformancetransactionservice/src/profile"
	"github.com/kusfo/highperformancetransactionservice/src/sessions"
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
	pool, err := postgres.OpenRuntimePool(ctx, os.Getenv("DATABASE_URL"), postgres.IdentityRole)
	if err != nil {
		return err
	}
	defer pool.Close()
	routes := userapi.NewHandlerWithLifecycle(users.NewRepository(pool), login.NewService(pool, nil), sessions.NewRepository(pool, nil), profile.NewService(pool))
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
