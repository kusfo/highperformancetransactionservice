// Command account serves authenticated account creation over HTTPS.
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/kusfo/highperformancetransactionservice/src/accountapi"
	"github.com/kusfo/highperformancetransactionservice/src/financial"
	"github.com/kusfo/highperformancetransactionservice/src/httpapi"
	"github.com/kusfo/highperformancetransactionservice/src/postgres"
	"github.com/kusfo/highperformancetransactionservice/src/sessions"
)

func run() error {
	cfg, err := httpapi.LoadConfig(os.Getenv)
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	pool, err := postgres.OpenRuntimePool(ctx, os.Getenv("DATABASE_URL"), postgres.FinancialHTTPRole)
	if err != nil {
		return err
	}
	defer pool.Close()
	routes := accountapi.NewHandler(financial.NewAccounts(pool), sessions.NewRepository(pool, nil))
	runtime, err := httpapi.New(cfg, routes, os.Stdout)
	if err != nil {
		return err
	}
	return runtime.Run(ctx)
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "account: HTTP startup or shutdown failed; check configuration")
		os.Exit(1)
	}
}
