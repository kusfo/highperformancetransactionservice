// Command user runs the shared HTTP runtime; domain endpoints follow later.
package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"github.com/kusfo/highperformancetransactionservice/src/httpapi"
)

func run() error {
	cfg, err := httpapi.LoadConfig(os.Getenv)
	if err != nil {
		return err
	}
	routes := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		httpapi.WriteError(w, 404, "NOT_FOUND", "Resource not found.")
	})
	runtime, err := httpapi.New(cfg, routes, os.Stdout)
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return runtime.Run(ctx)
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "user: HTTP startup or shutdown failed; check configuration")
		os.Exit(1)
	}
}
